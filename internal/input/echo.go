package input

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// Нажатия «от имени» физического устройства (contracts.DeviceOutput, FR-DSL-2, FR-DEV-3).
//
// Макрос {Sega.Start} или {UnKey.001} с кнопкой, которой нет у виртуальных клавиатуры и мыши mKey
// (кнопка джойстика, особая кнопка геймпада), нажимается на копии устройства:
//   - устройство захвачено (FR-HK-2) — в его passthrough-копию, через которую и так идут его события;
//   - не захвачено — в «копию для нажатий» (echo): mKey создаёт её при первом нажатии (те же кнопки,
//     оси и VID:PID, имя «mKey passthrough: …» — mKey её не читает, петли нет), ждёт settle_ms, пока
//     её подхватит система, и держит, пока устройство подключено.
//
// Защиты: при отключении устройства, экстренной остановке и остановке модуля копия для нажатий
// отпускает всё, что на ней зажато, и уничтожается (при экстренной остановке — создастся заново
// при следующем нажатии).

// DeviceOutput возвращает устройство вывода физического устройства path (contracts.DeviceOutput).
func (m *Module) DeviceOutput(path string) (contracts.VirtualDevice, error) {
	m.mu.RLock()
	d, ok := m.devices[path]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", contracts.ErrDeviceGone, path)
	}
	return &echoDevice{m: m, path: path, name: d.desc.Info.Name, held: map[uint16]bool{}}, nil
}

// echoWrite отправляет пакет событий «от имени» устройства path: в passthrough-копию, если оно
// захвачено, иначе — в копию для нажатий (создаётся при первом обращении).
func (m *Module) echoWrite(ctx context.Context, path string, events []ev.Event) error {
	// Устройство должно быть подключено.
	m.mu.RLock()
	d, ok := m.devices[path]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", contracts.ErrDeviceGone, path)
	}
	if len(events) == 0 {
		return nil
	}
	if !events[len(events)-1].IsSync() {
		events = append(events, ev.Sync())
	}

	// Захвачено — в passthrough-копию.
	d.cloneMu.Lock()
	if d.grabbed.Load() && d.clone != nil {
		defer d.cloneMu.Unlock()
		return d.clone.write(events)
	}

	// Иначе — копия для нажатий; новой копии даём время появиться в системе (события в первые
	// мгновения теряются — композитор ещё не открыл устройство).
	if d.echo == nil {
		w, err := m.createClone(cloneSetup(d.desc.Info))
		if err != nil {
			d.cloneMu.Unlock()
			return fmt.Errorf("%w: copy of %s: %w", contracts.ErrOutputUnavailable, d.desc.Info.Name, err)
		}
		d.echo = &passthrough{w: w, held: map[uint16]bool{}}
		d.echoReady = m.clk.Now().Add(time.Duration(m.cfg.SettleMS) * time.Millisecond)
		m.log.Info("device copy for presses created", "path", path, "name", d.desc.Info.Name)
	}
	wait := d.echoReady.Sub(m.clk.Now())
	d.cloneMu.Unlock()
	if wait > 0 {
		if err := m.clk.Sleep(ctx, wait); err != nil {
			return err
		}
	}

	// Пишем (копию могли уничтожить, пока ждали: устройство отключили или экстренная остановка).
	d.cloneMu.Lock()
	defer d.cloneMu.Unlock()
	if d.echo == nil {
		return fmt.Errorf("%w: %s", contracts.ErrDeviceGone, path)
	}
	return d.echo.write(events)
}

// dropEcho отпускает всё, что зажато на копии для нажатий устройства d, и уничтожает её.
func (m *Module) dropEcho(d *openDevice) {
	d.cloneMu.Lock()
	defer d.cloneMu.Unlock()
	if d.echo == nil {
		return
	}
	if err := d.echo.close(); err != nil {
		m.log.Warn("close device copy", "err", err)
	}
	d.echo = nil
}

// echoDevice — устройство вывода «от имени» физического устройства (contracts.VirtualDevice).
// Помнит, что зажал через него макрос, чтобы ReleaseAll отпустил это и только это.
type echoDevice struct {
	m    *Module
	path string
	name string
	// mu защищает held — коды, зажатые через это устройство.
	mu   sync.Mutex
	held map[uint16]bool
}

// Name возвращает имя устройства (как у физического).
func (e *echoDevice) Name() string { return e.name }

// key отправляет нажатие или отпускание кнопки code и запоминает её состояние.
func (e *echoDevice) key(ctx context.Context, code uint16, value int32) error {
	if err := e.m.echoWrite(ctx, e.path, []ev.Event{{Type: ev.EvKey, Code: code, Value: value}}); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if value == ev.ValueUp {
		delete(e.held, code)
	} else {
		e.held[code] = true
	}
	return nil
}

// Press зажимает кнопку code.
func (e *echoDevice) Press(ctx context.Context, code uint16) error {
	return e.key(ctx, code, ev.ValueDown)
}

// Release отпускает кнопку code.
func (e *echoDevice) Release(ctx context.Context, code uint16) error {
	return e.key(ctx, code, ev.ValueUp)
}

// Tap нажимает code, держит hold и отпускает; отпускает и при отмене ctx.
func (e *echoDevice) Tap(ctx context.Context, code uint16, hold time.Duration) error {
	if err := e.Press(ctx, code); err != nil {
		return err
	}
	err := e.m.clk.Sleep(ctx, hold)
	return errors.Join(err, e.Release(context.WithoutCancel(ctx), code))
}

// Emit отправляет произвольные события (оси, кнопки) одним пакетом.
func (e *echoDevice) Emit(ctx context.Context, events ...ev.Event) error {
	return e.m.echoWrite(ctx, e.path, events)
}

// Held возвращает отсортированные коды кнопок, зажатых через это устройство.
func (e *echoDevice) Held() []uint16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	codes := make([]uint16, 0, len(e.held))
	for c := range e.held {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}

// ReleaseAll отпускает всё, что зажато через это устройство (без контекста отмены: это очистка).
func (e *echoDevice) ReleaseAll() error {
	var errs []error
	for _, c := range e.Held() {
		errs = append(errs, e.Release(context.Background(), c))
	}
	return errors.Join(errs...)
}

var (
	_ contracts.DeviceOutput  = (*Module)(nil)
	_ contracts.VirtualDevice = (*echoDevice)(nil)
)
