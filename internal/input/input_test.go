package input

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/registry"
)

// fakeDevice — фейковое устройство: события приходят из канала, Close прерывает чтение.
type fakeDevice struct {
	info   ev.Info
	events chan ev.Event
	once   sync.Once
	done   chan struct{}
	// gone — устройство «отключено»: чтение вернёт ошибку, как при ENODEV.
	gone chan struct{}
	// mu защищает grabbed и pressed.
	mu      sync.Mutex
	grabbed bool
	pressed []uint16
}

func newFakeDevice(path, name, phys string) *fakeDevice {
	return &fakeDevice{
		info: ev.Info{Path: path, Name: name, Phys: phys, Caps: ev.Capabilities{Codes: map[uint16][]uint16{
			ev.EvKey: {ev.KeyA, ev.KeyZ, ev.KeySpace},
		}}},
		events: make(chan ev.Event, 16),
		done:   make(chan struct{}),
		gone:   make(chan struct{}),
	}
}

func (f *fakeDevice) Info() ev.Info { return f.info }

func (f *fakeDevice) ReadEvents(buf []ev.Event) ([]ev.Event, error) {
	select {
	case e := <-f.events:
		buf[0] = e
		return buf[:1], nil
	case <-f.done:
		return nil, os.ErrClosed
	case <-f.gone:
		return nil, io.ErrUnexpectedEOF
	}
}

func (f *fakeDevice) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

func (f *fakeDevice) Grab() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grabbed = true
	return nil
}

func (f *fakeDevice) Ungrab() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grabbed = false
	return nil
}

func (f *fakeDevice) PressedKeys() ([]uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.pressed...), nil
}

func (f *fakeDevice) isGrabbed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.grabbed
}

// fakeFS — набор фейковых устройств и отказов в доступе по пути.
type fakeFS struct {
	mu      sync.Mutex
	devices map[string]*fakeDevice
	denied  map[string]bool
}

func (f *fakeFS) open(path string) (deviceReader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denied[path] {
		return nil, os.ErrPermission
	}
	d, ok := f.devices[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return d, nil
}

// setup создаёт каталог с файлами event*, фейковую ФС и запущенный модуль.
func setup(t *testing.T, fs *fakeFS) (*Module, *registry.Manager, string) {
	t.Helper()

	// Каталог с пустыми файлами eventN (их имена видит Glob).
	dir := t.TempDir()
	for p := range fs.devices {
		touch(t, filepath.Join(dir, filepath.Base(p)))
	}
	for p := range fs.denied {
		touch(t, filepath.Join(dir, filepath.Base(p)))
	}

	// Пути фейков должны совпадать с путями в каталоге.
	fixed := &fakeFS{devices: map[string]*fakeDevice{}, denied: map[string]bool{}}
	for p, d := range fs.devices {
		np := filepath.Join(dir, filepath.Base(p))
		d.info.Path = np
		fixed.devices[np] = d
	}
	for p := range fs.denied {
		fixed.denied[filepath.Join(dir, filepath.Base(p))] = true
	}
	fs.devices, fs.denied = fixed.devices, fixed.denied

	// Запускаем модуль в менеджере.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mod := newModule(dir, fs.open, clock.NewFake(time.Unix(0, 0)))
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })
	return mod, mgr, dir
}

// touch создаёт пустой файл.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanAndFilterOwn проверяет открытие устройств, исключение своих и учёт недоступных.
func TestScanAndFilterOwn(t *testing.T) {
	t.Parallel()

	// Обычная клавиатура, собственное устройство mKey и недоступное устройство.
	fs := &fakeFS{
		devices: map[string]*fakeDevice{
			"event0": newFakeDevice("", "USB Keyboard", "usb-1/input0"),
			"event1": newFakeDevice("", "mKey Keyboard", "mkey/keyboard"),
		},
		denied: map[string]bool{"event2": true},
	}
	mod, mgr, dir := setup(t, fs)

	// Открыта только физическая клавиатура, классифицированная как клавиатура.
	devs := mod.Devices()
	if len(devs) != 1 || devs[0].Info.Name != "USB Keyboard" || devs[0].Kinds[0] != ev.KindKeyboard {
		t.Fatalf("Devices = %+v", devs)
	}

	// Недоступное устройство видно в статусе.
	if st := mod.Status(); st.Open != 1 || len(st.Denied) != 1 || st.Denied[0] != filepath.Join(dir, "event2") {
		t.Fatalf("Status = %+v", st)
	}

	// Сервис опубликован.
	if _, err := contracts.LookupService[contracts.InputSource](mgr.Services()); err != nil {
		t.Fatal(err)
	}
}

// TestSubscribeAndRemove проверяет доставку событий подписчику и отключение устройства.
func TestSubscribeAndRemove(t *testing.T) {
	t.Parallel()
	kb := newFakeDevice("", "USB Keyboard", "usb-1/input0")
	fs := &fakeFS{devices: map[string]*fakeDevice{"event0": kb}}
	mod, mgr, _ := setup(t, fs)

	// Подписываемся на события устройств и на отключения через шину.
	ch, cancel := mod.Subscribe(8)
	defer cancel()
	removed, unsub := busOf(t, mgr).Subscribe(contracts.TopicInputDeviceRemoved)
	defer unsub()

	// Событие устройства доходит до подписчика с путём устройства.
	kb.events <- ev.Event{Type: ev.EvKey, Code: ev.KeyA, Value: 1}
	select {
	case ie := <-ch:
		if ie.Event.Code != ev.KeyA || ie.Device != kb.info.Path {
			t.Fatalf("event = %+v", ie)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not delivered")
	}

	// Отключаем устройство: оно пропадает из списка, на шину приходит уведомление.
	close(kb.gone)
	select {
	case <-removed:
	case <-time.After(2 * time.Second):
		t.Fatal("device_removed not published")
	}
	if len(mod.Devices()) != 0 {
		t.Fatalf("Devices after removal = %+v", mod.Devices())
	}
}

// TestAccessGranted проверяет, что устройство открывается после выдачи прав (смена атрибутов файла).
func TestAccessGranted(t *testing.T) {
	t.Parallel()
	fs := &fakeFS{devices: map[string]*fakeDevice{}, denied: map[string]bool{"event5": true}}
	mod, _, dir := setup(t, fs)
	path := filepath.Join(dir, "event5")

	// Выдаём права: устройство появляется в фейковой ФС, модуль получает Chmod.
	fs.mu.Lock()
	delete(fs.denied, path)
	fs.devices[path] = newFakeDevice(path, "Gamepad", "usb-2/input0")
	fs.mu.Unlock()
	mod.handleFSEvent(fsnotify.Event{Name: path, Op: fsnotify.Chmod})

	// Устройство открыто, недоступных больше нет.
	if st := mod.Status(); st.Open != 1 || len(st.Denied) != 0 {
		t.Fatalf("Status = %+v", st)
	}
}

// TestStopClosesSubscribers проверяет, что остановка закрывает каналы подписчиков.
func TestStopClosesSubscribers(t *testing.T) {
	t.Parallel()
	fs := &fakeFS{devices: map[string]*fakeDevice{"event0": newFakeDevice("", "Mouse", "usb-3")}}
	mod, mgr, _ := setup(t, fs)
	ch, cancel := mod.Subscribe(1)

	// После остановки канал закрыт, повторная отписка безопасна.
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel must be closed")
	}
	cancel()
}

// busOf возвращает шину, общую для модулей менеджера (через сервис-хост недоступна напрямую).
func busOf(t *testing.T, mgr *registry.Manager) contracts.Bus {
	t.Helper()
	return mgr.Bus()
}
