package output

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
)

// eventWriter — то, во что устройство пишет события: настоящий uinput или фейк в тестах.
type eventWriter interface {
	// Write отправляет события одним пакетом.
	Write(events ...ev.Event) error
	// Close уничтожает устройство.
	Close() error
}

// device — реализация contracts.VirtualDevice поверх eventWriter.
type device struct {
	// name — имя устройства в системе.
	name string
	// w — приёмник событий.
	w eventWriter
	// clk — часы для пауз и ожидания прогрева.
	clk clock.Clock
	// readyAt — момент, после которого композитор уже подхватил устройство.
	readyAt time.Time
	// mu защищает запись в устройство и набор зажатых клавиш.
	mu sync.Mutex
	// held — зажатые сейчас клавиши.
	held map[uint16]bool
	// limit — наибольшее число пакетов событий (нажатие, движение…) в секунду (SEC-4); 0 — без ограничения.
	limit int
	// windowStart и windowCount — текущее окно ограничителя скорости.
	windowStart time.Time
	windowCount int
}

// newDevice оборачивает writer в виртуальное устройство. settle — время прогрева после создания,
// limit — наибольшее число пакетов событий в секунду (0 — без ограничения).
func newDevice(name string, w eventWriter, clk clock.Clock, settle time.Duration, limit int) *device {
	return &device{name: name, w: w, clk: clk, readyAt: clk.Now().Add(settle), held: map[uint16]bool{}, limit: limit}
}

// Name возвращает имя устройства.
func (d *device) Name() string { return d.name }

// Press зажимает клавишу code.
func (d *device) Press(ctx context.Context, code uint16) error {
	return d.Emit(ctx, ev.Event{Type: ev.EvKey, Code: code, Value: ev.ValueDown})
}

// Release отпускает клавишу code.
func (d *device) Release(ctx context.Context, code uint16) error {
	return d.Emit(ctx, ev.Event{Type: ev.EvKey, Code: code, Value: ev.ValueUp})
}

// Tap нажимает клавишу, держит hold и отпускает. Если ctx отменён во время удержания,
// клавиша всё равно отпускается, чтобы не «залипнуть».
func (d *device) Tap(ctx context.Context, code uint16, hold time.Duration) error {
	// Нажимаем.
	if err := d.Press(ctx, code); err != nil {
		return err
	}

	// Держим; при отмене отпускаем без контекста и возвращаем причину отмены.
	if err := d.clk.Sleep(ctx, hold); err != nil {
		_ = d.Emit(context.WithoutCancel(ctx), ev.Event{Type: ev.EvKey, Code: code, Value: ev.ValueUp})
		return err
	}

	// Отпускаем.
	return d.Release(ctx, code)
}

// Emit отправляет события, добавляя SYN_REPORT в конце, и обновляет набор зажатых клавиш.
// Пока устройство «прогревается», сначала ждёт окончания прогрева.
func (d *device) Emit(ctx context.Context, events ...ev.Event) error {
	// Пустой вызов — ничего не делаем.
	if len(events) == 0 {
		return nil
	}

	// Ждём, пока композитор подхватит устройство: события в первые мгновения теряются.
	if wait := d.readyAt.Sub(d.clk.Now()); wait > 0 {
		if err := d.clk.Sleep(ctx, wait); err != nil {
			return err
		}
	}

	// Завершаем пакет SYN_REPORT, если вызывающий этого не сделал.
	if !events[len(events)-1].IsSync() {
		events = append(events, ev.Sync())
	}

	// Ограничитель скорости: слишком частые события замедляются, а не отбрасываются —
	// потерянное «отпускание» оставило бы клавишу зажатой (SEC-4). Считается пакет целиком
	// (одно нажатие или одно движение мыши по двум осям), а не его части: иначе мышь с частотой
	// 1000 Гц упиралась бы в предел и при повторе записи её движения приходилось бы склеивать,
	// а склеенные движения система разгоняет сильнее — путь курсора искажается.
	if err := d.throttle(ctx); err != nil {
		return err
	}

	// Отправляем пакет и обновляем набор зажатых клавиш только при успешной записи.
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.w.Write(events...); err != nil {
		return fmt.Errorf("%s: write: %w", d.name, err)
	}
	for _, e := range events {
		if e.Type != ev.EvKey {
			continue
		}
		switch e.Value {
		case ev.ValueDown:
			d.held[e.Code] = true
		case ev.ValueUp:
			delete(d.held, e.Code)
		}
	}
	return nil
}

// throttle ждёт, если за текущую секунду уже отправлено больше limit пакетов событий.
func (d *device) throttle(ctx context.Context) error {
	if d.limit <= 0 {
		return nil
	}
	d.mu.Lock()
	now := d.clk.Now()
	if now.Sub(d.windowStart) >= time.Second {
		d.windowStart, d.windowCount = now, 0
	}
	d.windowCount++
	wait := time.Duration(0)
	if d.windowCount > d.limit {
		wait = d.windowStart.Add(time.Second).Sub(now)
		d.windowStart, d.windowCount = d.windowStart.Add(time.Second), 1
	}
	d.mu.Unlock()
	return d.clk.Sleep(ctx, wait)
}

// Held возвращает отсортированные коды зажатых клавиш.
func (d *device) Held() []uint16 {
	d.mu.Lock()
	defer d.mu.Unlock()
	codes := make([]uint16, 0, len(d.held))
	for c := range d.held {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}

// ReleaseAll отпускает все зажатые клавиши одним пакетом, без ожидания прогрева.
func (d *device) ReleaseAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Нечего отпускать.
	if len(d.held) == 0 {
		return nil
	}

	// Собираем события отпускания для всех зажатых клавиш и завершаем пакет.
	events := make([]ev.Event, 0, len(d.held)+1)
	for c := range d.held {
		events = append(events, ev.Event{Type: ev.EvKey, Code: c, Value: ev.ValueUp})
	}
	events = append(events, ev.Sync())

	// Отправляем; набор очищаем только при успехе, чтобы можно было повторить.
	if err := d.w.Write(events...); err != nil {
		return fmt.Errorf("%s: release all: %w", d.name, err)
	}
	clear(d.held)
	return nil
}

// close отпускает всё зажатое и уничтожает устройство.
func (d *device) close() error {
	relErr := d.ReleaseAll()
	closeErr := d.w.Close()
	if relErr != nil {
		return relErr
	}
	return closeErr
}

// Проверка на этапе компиляции, что device реализует контракт.
var _ contracts.VirtualDevice = (*device)(nil)
