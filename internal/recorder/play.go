package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/mkrec"
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

// maxFixedPauseMS — предел «фиксированной паузы» (минута между действиями — уже не повтор).
const maxFixedPauseMS = 60000

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
	if opts.FixedPauseMS < 0 || opts.FixedPauseMS > maxFixedPauseMS {
		return fmt.Errorf("play: fixed pause must be between 0 and %d ms", maxFixedPauseMS)
	}
	frames := rec.Frames
	if ms := m.RecordSettings().CoalesceMS; ms > 0 {
		frames = mkrec.CoalesceMoves(frames, time.Duration(ms)*time.Millisecond)
	}
	duration := rec.Duration
	if opts.FixedPauseMS > 0 {
		frames, duration = fixedPauses(frames, time.Duration(opts.FixedPauseMS)*time.Millisecond)
		speed = 1
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

	// Копии записанных геймпадов, джойстиков и сенсорных экранов (T12.2); уничтожаются в конце.
	clones, closeClones := m.cloneDevices(rec, frames)
	defer closeClones()

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

	// Воспроизводим нужное число раз; нажатое отпускается после каждого прохода и при остановке.
	p := &playback{kb: kb, mouse: mouse, clones: clones, held: map[contracts.VirtualDevice]map[uint16]bool{}, skipMoves: opts.SkipMoves}
	if rec.Header.Centered && !opts.SkipMoves {
		p.center = m.devs.CenterPointer
	}
	defer p.releaseAll()
	m.log.Info("playback started", "name", name, "speed", speed, "repeat", opts.Repeat, "fixed_pause_ms", opts.FixedPauseMS, "clones", len(clones))
	for pass := 0; opts.Repeat < 0 || pass < max(1, opts.Repeat); pass++ {
		if err := p.run(ctx, m, frames, duration, speed); err != nil {
			m.log.Info("playback stopped", "name", name)
			return err
		}
		p.releaseAll()
	}
	m.log.Info("playback finished", "name", name)
	return nil
}

// fixedPauses расставляет действия через равные промежутки pause (первое — сразу) и возвращает
// их вместе с длительностью прохода.
func fixedPauses(frames []mkrec.Frame, pause time.Duration) ([]mkrec.Frame, time.Duration) {
	out := make([]mkrec.Frame, len(frames))
	for i, f := range frames {
		f.T = time.Duration(i) * pause
		out[i] = f
	}
	return out, time.Duration(len(frames)) * pause
}

// cloneDevices создаёт копии записанных устройств, у которых есть возможности (caps) и действия
// в записи. Копию создать не удалось или у записи нет caps (сделана до T12.2) — действия этого
// устройства пропускаются с предупреждением в журнале. close уничтожает копии.
func (m *Module) cloneDevices(rec *mkrec.Recording, frames []mkrec.Frame) (map[int]contracts.VirtualDevice, func()) {
	// Устройства, у которых есть действия.
	used := map[int]bool{}
	for _, f := range frames {
		used[f.Device] = true
	}

	// Копии — для всех, кроме клавиатур и мышей (их повторяют клавиатура и мышь mKey).
	clones := map[int]contracts.VirtualDevice{}
	var closers []func() error
	for _, d := range rec.Header.Devices {
		if !used[d.ID] || plainDevice(d.Kinds) {
			continue
		}
		if d.Caps == nil || m.vdm == nil {
			m.log.Warn("recorded device cannot be replayed", "device", d.Name, "reason", "no caps in the recording or virtual devices unavailable")
			continue
		}
		c := d.Caps
		setup := ev.Setup{ID: c.ID, Keys: c.Keys, Rels: c.Rels, Abs: c.Abs, Props: c.Props}
		dev, closeFn, err := m.vdm.Clone(d.Name, setup)
		if err != nil {
			m.log.Warn("recorded device not cloned", "device", d.Name, "err", err)
			continue
		}
		clones[d.ID] = dev
		closers = append(closers, closeFn)
	}
	return clones, func() {
		for _, c := range closers {
			if err := c(); err != nil {
				m.log.Warn("clone not removed", "err", err)
			}
		}
	}
}

// plainDevice сообщает, что устройство — только клавиатура и/или мышь.
func plainDevice(kinds []string) bool {
	if len(kinds) == 0 {
		return false
	}
	for _, k := range kinds {
		if k != string(ev.KindKeyboard) && k != string(ev.KindMouse) {
			return false
		}
	}
	return true
}

// playback — одно воспроизведение: куда отправлять события и что сейчас нажато.
type playback struct {
	kb, mouse contracts.VirtualDevice
	// clones — копии записанных устройств по их номерам в записи.
	clones map[int]contracts.VirtualDevice
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

		// События пакета — на копию записанного устройства или на клавиатуру и мышь mKey;
		// пакет на каждое устройство отправляется разом (в постоянном порядке).
		out := map[contracts.VirtualDevice][]ev.Event{}
		var order []contracts.VirtualDevice
		for _, e := range f.Events {
			dev := p.route(f.Device, e)
			if dev == nil || !p.track(dev, e) {
				continue
			}
			if _, ok := out[dev]; !ok {
				order = append(order, dev)
			}
			out[dev] = append(out[dev], e)
		}
		for _, dev := range order {
			if err := dev.Emit(ctx, out[dev]...); err != nil {
				return err
			}
		}
	}

	// Пауза до конца записи.
	return waitUntil(duration)
}

// route выбирает виртуальное устройство для события устройства записи device (nil — событие
// не воспроизводится). Копии записанных геймпадов и экранов получают кнопки и оси как есть.
func (p *playback) route(device int, e ev.Event) contracts.VirtualDevice {
	if clone, ok := p.clones[device]; ok {
		if e.Type == ev.EvKey || e.Type == ev.EvAbs || e.Type == ev.EvRel {
			return clone
		}
		return nil
	}
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

// releaseAll отпускает всё, что нажато воспроизведением, а копии устройств возвращает в покой
// (оси, палец сенсорного экрана) — без контекста: отпускание обязательно (SEC-2).
func (p *playback) releaseAll() {
	for dev, held := range p.held {
		for code := range held {
			_ = dev.Release(context.Background(), code)
		}
		clear(held)
	}
	for _, c := range p.clones {
		_ = c.ReleaseAll()
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
