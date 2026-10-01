package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/mkrec"
)

// Пределы скорости воспроизведения (FR-REC-4).
const (
	minSpeed = 0.1
	maxSpeed = 10.0
)

// Коды кнопок мыши (BTN_LEFT…BTN_TASK) и начало кодов кнопок геймпадов и прочих «BTN_»,
// которые не относятся к клавиатуре (linux/input-event-codes.h).
const (
	btnMouseFirst = 0x110
	btnMouseLast  = 0x117
	btnMiscFirst  = 0x100
	keyOK         = 0x160 // после блока BTN_* снова идут клавиши (KEY_OK и далее)
)

// errNoOutput — модуль вывода отключён: воспроизводить некуда.
var errNoOutput = errors.New("virtual input is unavailable")

// Play воспроизводит запись (contracts.Player).
func (m *Module) Play(ctx context.Context, name string, opts contracts.PlayOptions) error {
	// Запись и параметры.
	if m.devs == nil {
		return fmt.Errorf("play: %w", errNoOutput)
	}
	path, err := m.recordingPath(name)
	if err != nil {
		return err
	}
	rec, err := readRecording(path)
	if err != nil {
		return err
	}
	speed := opts.Speed
	if speed == 0 {
		speed = 1
	}
	if speed < minSpeed || speed > maxSpeed {
		return fmt.Errorf("play: speed must be between %g and %g", minSpeed, maxSpeed)
	}
	frames := rec.Frames
	if ms := m.RecordSettings().CoalesceMS; ms > 0 {
		frames = mkrec.CoalesceMoves(frames, time.Duration(ms)*time.Millisecond)
	}

	// Виртуальные клавиатура и мышь.
	kb, err := m.devs.Keyboard()
	if err != nil {
		return err
	}
	mouse, err := m.devs.Mouse()
	if err != nil {
		return err
	}

	// Регистрируем воспроизведение, чтобы его можно было остановить (mkey stop, экстренная остановка).
	ctx, cancel := context.WithCancel(ctx)
	key := new(int)
	m.mu.Lock()
	m.plays[key] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.plays, key)
		m.mu.Unlock()
		cancel()
	}()

	// Воспроизводим нужное число раз; нажатые клавиши отпускаются после каждого прохода и при остановке.
	p := &playback{kb: kb, mouse: mouse, held: map[contracts.VirtualDevice]map[uint16]bool{}, skipMoves: opts.SkipMoves}
	if rec.Header.Centered && !opts.SkipMoves {
		p.center = m.devs.CenterPointer
	}
	defer p.releaseAll()
	m.log.Info("playback started", "name", name, "speed", speed, "repeat", opts.Repeat)
	for pass := 0; opts.Repeat < 0 || pass < max(1, opts.Repeat); pass++ {
		if err := p.run(ctx, m, frames, rec.Duration, speed); err != nil {
			m.log.Info("playback stopped", "name", name)
			return err
		}
		p.releaseAll()
	}
	m.log.Info("playback finished", "name", name)
	return nil
}

// playback — одно воспроизведение: куда отправлять события и что сейчас нажато.
type playback struct {
	kb, mouse contracts.VirtualDevice
	// center ставит указатель в центр экрана перед каждым проходом (nil — запись без калибровки).
	center func(ctx context.Context) error
	// held — нажатые воспроизведением клавиши по устройствам.
	held map[contracts.VirtualDevice]map[uint16]bool
	// skipMoves — не повторять перемещения мыши.
	skipMoves bool
}

// run проигрывает пакеты один раз в темпе записи (с учётом скорости). Проход длится ровно
// duration/speed: пауза в конце записи тоже сохраняется, чтобы при повторах не сбивался ритм.
func (p *playback) run(ctx context.Context, m *Module, frames []mkrec.Frame, duration time.Duration, speed float64) error {
	// Указатель — в центр экрана, как в начале записи (FR-REC-5): дальше перемещения идут от этой точки.
	// Ошибка не прерывает воспроизведение: без калибровки оно всё равно идёт.
	if p.center != nil {
		_ = p.center(ctx)
	}

	base := m.clk.Now()
	// waitUntil ждёт момента t записи: отсчёт от начала прохода, поэтому паузы не накапливают ошибку.
	waitUntil := func(t time.Duration) error {
		if wait := base.Add(time.Duration(float64(t) / speed)).Sub(m.clk.Now()); wait > 0 {
			if err := m.clk.Sleep(ctx, wait); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	for _, f := range frames {
		if err := waitUntil(f.T); err != nil {
			return err
		}

		// События пакета — на клавиатуру и мышь; пакет на каждое устройство отправляется разом.
		out := map[contracts.VirtualDevice][]ev.Event{}
		for _, e := range f.Events {
			if dev := p.route(e); dev != nil && p.track(dev, e) {
				out[dev] = append(out[dev], e)
			}
		}
		for _, dev := range []contracts.VirtualDevice{p.kb, p.mouse} {
			if evs := out[dev]; len(evs) > 0 {
				if err := dev.Emit(ctx, evs...); err != nil {
					return err
				}
			}
		}
	}

	// Пауза до конца записи.
	return waitUntil(duration)
}

// route выбирает виртуальное устройство для события (nil — такое пока не воспроизводится:
// геймпады, тач, оси).
func (p *playback) route(e ev.Event) contracts.VirtualDevice {
	switch {
	case e.Type == ev.EvRel:
		if p.skipMoves && (e.Code == ev.RelX || e.Code == ev.RelY) {
			return nil
		}
		return p.mouse
	case e.Type != ev.EvKey:
		return nil
	case e.Code >= btnMouseFirst && e.Code <= btnMouseLast:
		return p.mouse
	case e.Code < btnMiscFirst || e.Code >= keyOK:
		return p.kb
	}
	return nil
}

// track учитывает нажатия; false — событие не нужно отправлять (отпускание того, что не нажимали).
func (p *playback) track(dev contracts.VirtualDevice, e ev.Event) bool {
	if e.Type != ev.EvKey {
		return true
	}
	held := p.held[dev]
	if held == nil {
		held = map[uint16]bool{}
		p.held[dev] = held
	}
	switch e.Value {
	case ev.ValueDown:
		held[e.Code] = true
	case ev.ValueUp:
		if !held[e.Code] {
			return false
		}
		delete(held, e.Code)
	}
	return true
}

// releaseAll отпускает всё, что нажато воспроизведением (без контекста: отпускание обязательно, SEC-2).
func (p *playback) releaseAll() {
	for dev, held := range p.held {
		for code := range held {
			_ = dev.Release(context.Background(), code)
		}
		clear(held)
	}
}

// StopPlayback останавливает все воспроизведения (contracts.Player).
func (m *Module) StopPlayback() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.plays {
		cancel()
	}
	return len(m.plays)
}

// Playing возвращает число идущих воспроизведений (contracts.Player).
func (m *Module) Playing() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.plays)
}

// readRecording читает файл записи целиком.
func readRecording(path string) (*mkrec.Recording, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return mkrec.Read(f)
}
