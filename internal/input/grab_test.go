package input

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/registry"
)

// fakeClone — passthrough-копия, записывающая события.
type fakeClone struct {
	mu     sync.Mutex
	events []ev.Event
	closed bool
}

func (c *fakeClone) Write(events ...ev.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
	return nil
}

func (c *fakeClone) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeClone) keys() []ev.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ev.Event
	for _, e := range c.events {
		if e.Type == ev.EvKey {
			out = append(out, e)
		}
	}
	return out
}

func (c *fakeClone) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// dropF8 — обработчик, «съедающий» F8 (как горячая клавиша с consume).
type dropF8 struct{}

func (dropF8) HandleInput(_ string, e *ev.Event, _ bool) bool {
	return e.Type == ev.EvKey && e.Code == ev.KeyF8
}

// grabRig — модуль с одной фейковой клавиатурой и фабрикой копий.
type grabRig struct {
	mod    *Module
	kb     *fakeDevice
	path   string
	clones []*fakeClone
	mu     sync.Mutex
	bus    *bus.Bus
}

// newGrabRig запускает модуль с клавиатурой и возвращает стенд.
func newGrabRig(t *testing.T) *grabRig {
	t.Helper()
	rig := &grabRig{kb: newFakeDevice("", "USB Keyboard", "usb-1/input0")}
	fs := &fakeFS{devices: map[string]*fakeDevice{"event0": rig.kb}}

	// Модуль стартуем вручную, чтобы подменить фабрику копий до Start.
	dir := t.TempDir()
	touch(t, dir+"/event0")
	rig.path = dir + "/event0"
	rig.kb.info.Path = rig.path
	fs.devices = map[string]*fakeDevice{rig.path: rig.kb}
	// Часы начинаются с реалистичного времени: у watchdog «ноль» означает «не занят».
	mod := newModule(dir, fs.open, clock.NewFake(time.Unix(1_700_000_000, 0)))
	mod.createClone = func(ev.Setup) (cloneWriter, error) {
		c := &fakeClone{}
		rig.mu.Lock()
		rig.clones = append(rig.clones, c)
		rig.mu.Unlock()
		return c, nil
	}
	cat, _ := i18n.LoadCatalog()
	rig.bus = bus.New(0)
	mgr, err := registry.NewManager(registry.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Translator: i18n.New(cat, "en"), Bus: rig.bus,
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })
	rig.mod = mod
	return rig
}

// clone возвращает последнюю созданную копию.
func (r *grabRig) clone() *fakeClone {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.clones) == 0 {
		return nil
	}
	return r.clones[len(r.clones)-1]
}

// eventually ждёт выполнения условия до 2 секунд.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// key отправляет в фейковую клавиатуру нажатие или отпускание.
func (r *grabRig) key(code uint16, value int32) {
	r.kb.events <- ev.Event{Type: ev.EvKey, Code: code, Value: value}
}

// grabKeyboards включает захват клавиатур с обработчиком dropF8 и ждёт захвата.
func (r *grabRig) grabKeyboards(t *testing.T) {
	t.Helper()
	r.mod.SetHandler(dropF8{})
	r.mod.SetGrabPolicy(func(d contracts.InputDevice) bool { return strings.Contains(d.Info.Name, "Keyboard") })
	eventually(t, "grab", func() bool { return r.kb.isGrabbed() && r.clone() != nil })
}

// TestGrabPassthrough проверяет захват: горячая клавиша «съедается», остальное проходит в копию.
func TestGrabPassthrough(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)
	rig.grabKeyboards(t)

	// F8 съедается, A проходит.
	rig.key(ev.KeyF8, 1)
	rig.key(ev.KeyF8, 0)
	rig.key(ev.KeyA, 1)
	rig.key(ev.KeyA, 0)
	c := rig.clone()
	eventually(t, "A forwarded", func() bool { return len(c.keys()) == 2 })
	for _, e := range c.keys() {
		if e.Code != ev.KeyA {
			t.Fatalf("clone got %v", e)
		}
	}

	// Inject пишет в копию (например, отпустить модификатор для приложений).
	if err := rig.mod.Inject(rig.path, ev.Event{Type: ev.EvKey, Code: ev.KeyLeftctrl, Value: 0}); err != nil {
		t.Fatal(err)
	}

	// Снятие политики: захват снят, зажатая на копии клавиша отпущена, копия уничтожена.
	rig.key(ev.KeyB, 1)
	eventually(t, "B down forwarded", func() bool { return len(c.keys()) == 4 })
	rig.mod.SetGrabPolicy(nil)
	eventually(t, "ungrab", func() bool { return !rig.kb.isGrabbed() && c.isClosed() })
	last := c.keys()[len(c.keys())-1]
	if last.Code != ev.KeyB || last.Value != ev.ValueUp {
		t.Fatalf("held key not released on ungrab: %v", c.keys())
	}
	if err := rig.mod.Inject(rig.path, ev.Event{Type: ev.EvKey, Code: ev.KeyA}); !errors.Is(err, contracts.ErrNotGrabbed) {
		t.Fatalf("Inject after ungrab = %v", err)
	}
}

// TestGrabWaitsForRelease проверяет, что захват не включается, пока зажата клавиша.
func TestGrabWaitsForRelease(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)

	// Пользователь держит Ctrl: захвата нет.
	rig.kb.mu.Lock()
	rig.kb.pressed = []uint16{ev.KeyLeftctrl}
	rig.kb.mu.Unlock()
	rig.mod.SetGrabPolicy(func(contracts.InputDevice) bool { return true })
	time.Sleep(50 * time.Millisecond)
	if rig.kb.isGrabbed() {
		t.Fatal("must not grab while a key is held")
	}

	// Отпустил — захват включился.
	rig.kb.mu.Lock()
	rig.kb.pressed = nil
	rig.kb.mu.Unlock()
	eventually(t, "grab after release", rig.kb.isGrabbed)
}

// TestEmergencyStop проверяет экстренную остановку: захват снят и отключён до ResumeGrab (SEC-1).
func TestEmergencyStop(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)
	emergency, cancel := rig.bus.Subscribe(contracts.TopicEmergency)
	defer cancel()
	rig.grabKeyboards(t)

	// Esc + Backspace + Enter одновременно.
	rig.key(ev.KeyEsc, 1)
	rig.key(ev.KeyBackspace, 1)
	rig.key(ev.KeyEnter, 1)
	select {
	case <-emergency:
	case <-time.After(2 * time.Second):
		t.Fatal("emergency not published")
	}
	eventually(t, "ungrab", func() bool { return !rig.kb.isGrabbed() })
	if !rig.mod.GrabSuspended() {
		t.Fatal("grab must be suspended")
	}

	// Политика не может снова захватить устройство, пока перехват отключён.
	rig.mod.SetGrabPolicy(func(contracts.InputDevice) bool { return true })
	time.Sleep(50 * time.Millisecond)
	if rig.kb.isGrabbed() {
		t.Fatal("must not grab while suspended")
	}

	// После отпускания клавиш и ResumeGrab захват возвращается.
	rig.key(ev.KeyEsc, 0)
	rig.key(ev.KeyBackspace, 0)
	rig.key(ev.KeyEnter, 0)
	rig.mod.ResumeGrab()
	eventually(t, "regrab", rig.kb.isGrabbed)
}

// TestWatchdog проверяет снятие захвата при зависшей обработке (SEC-3).
func TestWatchdog(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)
	rig.grabKeyboards(t)

	// Имитируем зависание: обработка «началась» давно.
	rig.mod.mu.RLock()
	d := rig.mod.devices[rig.path]
	rig.mod.mu.RUnlock()
	d.busySince.Store(rig.mod.clk.Now().Add(-time.Second).UnixNano())
	rig.mod.checkWatchdog()
	eventually(t, "watchdog ungrab", func() bool { return !rig.kb.isGrabbed() })
	if !rig.mod.GrabSuspended() {
		t.Fatal("watchdog must suspend grabbing")
	}
}

// TestCloneSetup проверяет описание passthrough-копии: префикс mKey и обрезку имени по символам.
func TestCloneSetup(t *testing.T) {
	t.Parallel()
	info := ev.Info{Name: strings.Repeat("Клавиатура", 10), ID: ev.ID{Vendor: 1, Product: 2}, Caps: ev.Capabilities{Codes: map[uint16][]uint16{ev.EvKey: {ev.KeyA}}}}
	s := cloneSetup(info)
	if !strings.HasPrefix(s.Name, contracts.VirtualNamePrefix) || len(s.Name) > 79 || !strings.HasPrefix(s.Phys, contracts.VirtualPhysPrefix) {
		t.Fatalf("setup = %q / %q (%d bytes)", s.Name, s.Phys, len(s.Name))
	}
	if !utf8.ValidString(s.Name) {
		t.Fatalf("name is not valid UTF-8: %q", s.Name)
	}
	if s.ID != info.ID || len(s.Keys) != 1 {
		t.Fatalf("setup = %+v", s)
	}
}

// TestEmergencyCodes проверяет разбор клавиш экстренной остановки.
func TestEmergencyCodes(t *testing.T) {
	t.Parallel()
	if c, err := emergencyCodes([]string{"Esc", "backspace", "ENTER"}); err != nil || len(c) != 3 {
		t.Fatalf("codes = %v, %v", c, err)
	}
	if _, err := emergencyCodes([]string{"Esc"}); err == nil {
		t.Fatal("one key must be rejected")
	}
	if _, err := emergencyCodes([]string{"Esc", "Mous0"}); err == nil {
		t.Fatal("unknown key must be rejected")
	}
}
