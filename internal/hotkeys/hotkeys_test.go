package hotkeys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// kbPath — путь фейковой клавиатуры.
const kbPath = "/dev/input/event3"

// padPath и mousePath — фейковые геймпад и мышь (источники привязок осей).
const (
	padPath   = "/dev/input/event9"
	mousePath = "/dev/input/event10"
)

// fakeInput — источник событий: список устройств и запись вызовов захвата и Inject.
type fakeInput struct {
	mu        sync.Mutex
	policy    func(contracts.InputDevice) bool
	injected  []ev.Event
	suspended bool
}

func (f *fakeInput) Devices() []contracts.InputDevice {
	return []contracts.InputDevice{
		{Info: ev.Info{Path: kbPath, Name: "USB Keyboard", Caps: ev.Capabilities{Codes: map[uint16][]uint16{
			ev.EvKey: {ev.KeyA, ev.KeyF8, ev.KeyH, ev.KeyCapslock, ev.KeyLeftctrl, ev.KeyCalc},
		}}}},
		// Геймпад: стик 0…255 (центр 128) и курок 0…255 (как у xpad — с кнопкой South).
		{Info: ev.Info{Path: padPath, Name: "Pad", Caps: ev.Capabilities{
			Codes: map[uint16][]uint16{ev.EvKey: {ev.BtnSouth}, ev.EvAbs: {ev.AbsX, ev.AbsZ}},
			Abs:   map[uint16]ev.AbsInfo{ev.AbsX: {Minimum: 0, Maximum: 256}, ev.AbsZ: {Minimum: 0, Maximum: 255}},
		}}},
		// Мышь.
		{Info: ev.Info{Path: mousePath, Name: "Mouse", Caps: ev.Capabilities{Codes: map[uint16][]uint16{
			ev.EvKey: {ev.BtnLeft}, ev.EvRel: {ev.RelX, ev.RelY, ev.RelWheel},
		}}}},
	}
}
func (f *fakeInput) Status() contracts.InputStatus                       { return contracts.InputStatus{} }
func (f *fakeInput) Subscribe(int) (<-chan contracts.InputEvent, func()) { return nil, func() {} }
func (f *fakeInput) SetHandler(contracts.InputHandler)                   {}
func (f *fakeInput) GrabSuspended() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suspended
}
func (f *fakeInput) ResumeGrab() {}

// EmergencyStop не нужен тестам горячих клавиш.
func (f *fakeInput) EmergencyStop(string) {}

// EmergencyCombo и SetEmergencyCombo не нужны тестам горячих клавиш.
func (f *fakeInput) EmergencyCombo() string              { return "" }
func (f *fakeInput) SetEmergencyCombo(string) error      { return nil }
func (f *fakeInput) OwnDevices() []contracts.InputDevice { return nil }
func (f *fakeInput) SetGrabPolicy(p func(contracts.InputDevice) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policy = p
}
func (f *fakeInput) Inject(_ string, events ...ev.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.injected = append(f.injected, events...)
	return nil
}

// grabs сообщает, захватила бы политика фейковую клавиатуру.
func (f *fakeInput) grabs() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy != nil && f.policy(f.Devices()[0])
}

// fakeProjects — хранилище с одним проектом (для переназначений); остальные методы не используются.
type fakeProjects struct {
	contracts.Projects
	p project.Project
}

func (f fakeProjects) Dir() string { return "" }
func (f fakeProjects) List() []contracts.ProjectState {
	return []contracts.ProjectState{{Project: f.p}}
}
func (f fakeProjects) Get(string) (contracts.ProjectState, bool) {
	return contracts.ProjectState{}, false
}
func (f fakeProjects) SetEnabled(string, bool) error              { return nil }
func (f fakeProjects) SetEventEnabled(string, string, bool) error { return nil }
func (f fakeProjects) Import(string, []byte) (string, error)      { return "", nil }

// newTestModule создаёт модуль с фейковым вводом (без менеджера — только нужные поля).
func newTestModule() (*Module, *fakeInput) {
	in := &fakeInput{}
	m := New()
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.input = in
	m.layout.Store("us")
	return m, in
}

// recorder копит срабатывания.
type recorder struct {
	mu    sync.Mutex
	fires []contracts.Fire
}

func (r *recorder) fire(f contracts.Fire) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fires = append(r.fires, f)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.fires)
}

// arm регистрирует триггер вида typ с параметрами params.
func arm(t testing.TB, tt contracts.TriggerType, params map[string]any) *recorder {
	t.Helper()
	rec := &recorder{}
	if _, err := tt.Arm(context.Background(), contracts.EventRef{}, project.Trigger{Type: tt.Meta().ID, Params: params}, rec.fire); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	return rec
}

// key подаёт событие клавиши и возвращает решение обработчика.
func key(m *Module, code uint16, value int32) bool {
	e := ev.Event{Type: ev.EvKey, Code: code, Value: value}
	return m.HandleInput(kbPath, &e, true)
}

// TestChordConsume проверяет сочетание с consume: основная клавиша «съедается», модификаторы — нет.
func TestChordConsume(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	rec := arm(t, hotkeyType{m}, map[string]any{"keys": "^{Ctrl}^{Alt}{H}", "consume": true})

	// Захват включён для устройства с клавишей H.
	if !in.grabs() {
		t.Fatal("device with the hotkey must be grabbed")
	}

	// Ctrl и Alt проходят, H съедается (и её повтор, и отпускание), срабатывание одно.
	if key(m, ev.KeyLeftctrl, 1) || key(m, ev.KeyRightalt, 1) {
		t.Fatal("modifiers must pass through")
	}
	if !key(m, ev.KeyH, 1) || !key(m, ev.KeyH, 2) || !key(m, ev.KeyH, 0) {
		t.Fatal("main key must be consumed (down, repeat, up)")
	}
	if rec.count() != 1 {
		t.Fatalf("fires = %d", rec.count())
	}

	// Лишний модификатор (Shift) — не срабатывает, H проходит в систему.
	key(m, ev.KeyLeftshift, 1)
	if key(m, ev.KeyH, 1) || rec.count() != 1 {
		t.Fatal("extra modifier must prevent the hotkey")
	}
}

// TestToggleDoubleRelease проверяет режимы toggle, double и release.
func TestToggleDoubleRelease(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	toggle := arm(t, hotkeyType{m}, map[string]any{"keys": "{F8}", "on": "toggle"})
	double := arm(t, hotkeyType{m}, map[string]any{"keys": "{A}", "on": "double"})
	release := arm(t, hotkeyType{m}, map[string]any{"keys": "{H}", "on": "release"})

	// Переключатель: включено, выключено.
	for range 2 {
		key(m, ev.KeyF8, 1)
		key(m, ev.KeyF8, 0)
	}
	if toggle.count() != 2 || !*toggle.fires[0].Toggle || *toggle.fires[1].Toggle {
		t.Fatalf("toggle fires = %+v", toggle.fires)
	}

	// Двойное нажатие: одно срабатывание на два быстрых нажатия.
	for range 2 {
		key(m, ev.KeyA, 1)
		key(m, ev.KeyA, 0)
	}
	if double.count() != 1 {
		t.Fatalf("double fires = %d", double.count())
	}

	// Отпускание: срабатывает на отпускании, не на нажатии.
	key(m, ev.KeyH, 1)
	if release.count() != 0 {
		t.Fatal("release must not fire on press")
	}
	key(m, ev.KeyH, 0)
	if release.count() != 1 {
		t.Fatal("release must fire on key up")
	}
}

// TestHold проверяет удержание: срабатывает, только если клавишу продержали нужное время.
func TestHold(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	rec := arm(t, hotkeyType{m}, map[string]any{"keys": "{H}", "on": "hold", "hold_ms": 30})

	// Отпустили раньше — нет срабатывания.
	key(m, ev.KeyH, 1)
	time.Sleep(5 * time.Millisecond)
	key(m, ev.KeyH, 0)
	time.Sleep(50 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatal("short press must not fire")
	}

	// Продержали — срабатывание, и Held сообщает, что клавиша ещё зажата.
	key(m, ev.KeyH, 1)
	time.Sleep(60 * time.Millisecond)
	if rec.count() != 1 || !rec.fires[0].Held() {
		t.Fatalf("hold fires = %d", rec.count())
	}
	key(m, ev.KeyH, 0)
	if rec.fires[0].Held() {
		t.Fatal("Held must be false after release")
	}
}

// TestNoGrabWithoutConsume проверяет, что без consume и переназначений захвата нет.
func TestNoGrabWithoutConsume(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	arm(t, hotkeyType{m}, map[string]any{"keys": "{F8}"})
	if in.grabs() {
		t.Fatal("no grab without consume")
	}
}

// TestModifiersReleaseRestore проверяет отпускание модификаторов для приложений и их возврат (FR-HK-3).
func TestModifiersReleaseRestore(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	rec := arm(t, hotkeyType{m}, map[string]any{"keys": "^{Ctrl}{H}", "consume": true})
	key(m, ev.KeyLeftctrl, 1)
	key(m, ev.KeyH, 1)

	// Release отпускает Ctrl, Restore возвращает (Ctrl всё ещё зажат пользователем).
	mc := rec.fires[0].Modifiers
	mc.Release()
	mc.Restore()
	if len(in.injected) != 2 || in.injected[0].Value != 0 || in.injected[1].Value != 1 || in.injected[0].Code != ev.KeyLeftctrl {
		t.Fatalf("injected = %v", in.injected)
	}

	// Если пользователь отпустил Ctrl во время макроса — возвращать нечего.
	mc2 := rec.fires[0].Modifiers
	mc2.Release()
	key(m, ev.KeyLeftctrl, 0)
	mc2.Restore()
	if len(in.injected) != 3 {
		t.Fatalf("restore after physical release must not press Ctrl: %v", in.injected)
	}
}

// TestSequence проверяет последовательность {G}{G}.
func TestSequence(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	rec := arm(t, sequenceType{m}, map[string]any{"keys": "{G}{G}", "within_ms": 500})
	for _, c := range []uint16{ev.KeyG, ev.KeyA, ev.KeyG, ev.KeyG} {
		key(m, c, 1)
		key(m, c, 0)
	}
	if rec.count() != 1 {
		t.Fatalf("sequence fires = %d", rec.count())
	}
}

// TestHotstring проверяет распознавание слова и макрос замены.
func TestHotstring(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	rec := arm(t, hotstringType{m}, map[string]any{"text": "btw", "replace": "by the way"})

	// "xbtw " — не отдельное слово, срабатывания нет; " btw " — есть.
	for _, c := range []uint16{ev.KeyX, ev.KeyB, ev.KeyT, ev.KeyW, ev.KeySpace, ev.KeyB, ev.KeyT, ev.KeyW, ev.KeySpace} {
		key(m, c, 1)
		key(m, c, 0)
	}
	if rec.count() != 1 {
		t.Fatalf("hotstring fires = %d", rec.count())
	}
	if got := rec.fires[0].Vars["replace_dsl"]; got != `{Backspace*4}{"by the way "}` {
		t.Fatalf("replace_dsl = %v", got)
	}

	// Backspace исправляет опечатку: "btx⌫w " срабатывает.
	for _, c := range []uint16{ev.KeyB, ev.KeyT, ev.KeyX, ev.KeyBackspace, ev.KeyW, ev.KeySpace} {
		key(m, c, 1)
		key(m, c, 0)
	}
	if rec.count() != 2 {
		t.Fatalf("hotstring after backspace fires = %d", rec.count())
	}
}

// TestRemap проверяет переназначение CapsLock → Esc и включение захвата.
func TestRemap(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	m.projects = fakeProjects{p: project.Project{ID: "p", Remaps: []project.Remap{{From: "{CapsLock}", To: "Esc"}}}}
	m.reloadRemaps()
	if !in.grabs() {
		t.Fatal("remap must grab the device")
	}
	e := ev.Event{Type: ev.EvKey, Code: ev.KeyCapslock, Value: 1}
	if m.HandleInput(kbPath, &e, true) || e.Code != ev.KeyEsc {
		t.Fatalf("remapped event = %v", e)
	}
}

// TestWaitKeyAndIsDown проверяет ожидание нажатия и состояние клавиш.
func TestWaitKeyAndIsDown(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	f8, _ := m.ParseKey("F8")
	done := make(chan error, 1)
	go func() { done <- m.WaitKey(context.Background(), f8) }()
	time.Sleep(10 * time.Millisecond)
	key(m, ev.KeyF8, 1)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitKey did not return")
	}
	if !m.IsDown(f8) {
		t.Fatal("F8 must be down")
	}
	ctrl, _ := m.ParseKey("{Ctrl}")
	key(m, ev.KeyRightctrl, 1)
	if !m.IsDown(ctrl) {
		t.Fatal("Ctrl (any side) must be down")
	}
}

// TestArmErrors проверяет ошибки параметров.
func TestArmErrors(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	bad := []struct {
		tt     contracts.TriggerType
		params map[string]any
	}{
		{hotkeyType{m}, map[string]any{"keys": "{Mous0}"}},
		{hotkeyType{m}, map[string]any{"keys": "{A}", "on": "sometimes"}},
		{hotkeyType{m}, map[string]any{"keys": "{A*3}"}},
		{hotkeyType{m}, map[string]any{"keys": "{A}", "typo": 1}},
		{sequenceType{m}, map[string]any{"keys": "{A}"}},
		{hotstringType{m}, map[string]any{"text": " "}},
	}
	for _, b := range bad {
		if _, err := b.tt.Arm(context.Background(), contracts.EventRef{}, project.Trigger{Params: b.params}, func(contracts.Fire) {}); err == nil {
			t.Errorf("%s %v: expected error", b.tt.Meta().ID, b.params)
		}
	}
}

// TestSuspendedNoTriggers проверяет, что после экстренной остановки горячие клавиши не срабатывают
// и ничего не «съедают», а состояние клавиш продолжает отслеживаться.
func TestSuspendedNoTriggers(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	rec := arm(t, hotkeyType{m}, map[string]any{"keys": "{F8}", "on": "toggle", "consume": true})
	in.mu.Lock()
	in.suspended = true
	in.mu.Unlock()

	// F8 проходит в систему и не срабатывает.
	if key(m, ev.KeyF8, 1) || key(m, ev.KeyF8, 0) || rec.count() != 0 {
		t.Fatalf("suspended: drop or fire (fires=%d)", rec.count())
	}

	// Состояние клавиш ведётся.
	key(m, ev.KeyA, 1)
	a, _ := m.ParseKey("A")
	if !m.IsDown(a) {
		t.Fatal("key state must be tracked while suspended")
	}

	// После возобновления снова срабатывает.
	in.mu.Lock()
	in.suspended = false
	in.mu.Unlock()
	key(m, ev.KeyF8, 1)
	if rec.count() != 1 {
		t.Fatal("must fire after resume")
	}
}

// fakeInspector — авто-ID: фейковая клавиатура (kbPath) — устройство UnKey, его кнопка 001 — KEY_CALC.
type fakeInspector struct{ contracts.Inspector }

// ResolveKey знает UnKey.001 и стандартные имена на UnKey.
func (fakeInspector) ResolveKey(device, button string) (contracts.DeviceKey, error) {
	if !strings.EqualFold(device, "UnKey") {
		return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownDevice, "device", device)
	}
	if button == "001" {
		return contracts.DeviceKey{Key: keys.Key{Name: "UnKey001", Type: ev.EvKey, Code: ev.KeyCalc}, Device: "UnKey"}, nil
	}
	if k, ok := keys.Lookup(button); ok {
		return contracts.DeviceKey{Key: k, Device: "UnKey"}, nil
	}
	return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", device, "button", button)
}

// DeviceOf — авто-ID только у фейковой клавиатуры.
func (fakeInspector) DeviceOf(path string) string {
	if path == kbPath {
		return "UnKey"
	}
	return ""
}

// TestDeviceKeys проверяет кнопки устройств с авто-ID (FR-DEV-2): горячая клавиша срабатывает
// только от своего устройства, перехват — только его, последовательности, состояние, ошибки имён.
func TestDeviceKeys(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	m.inspect = fakeInspector{}
	other := "/dev/input/event99"
	press := func(dev string, code uint16) {
		for _, v := range []int32{1, 0} {
			e := ev.Event{Type: ev.EvKey, Code: code, Value: v}
			m.HandleInput(dev, &e, true)
		}
	}

	// {UnKey001}: с другого устройства тот же код не срабатывает, со своего — да; перехват — своего устройства.
	rec := arm(t, hotkeyType{m}, map[string]any{"keys": "{UnKey001}", "consume": true})
	press(other, ev.KeyCalc)
	if rec.count() != 0 {
		t.Fatal("fired from another device")
	}
	press(kbPath, ev.KeyCalc)
	if rec.count() != 1 || !in.grabs() {
		t.Fatalf("own device: fires=%d grab=%v", rec.count(), in.grabs())
	}

	// Сочетание с обычным модификатором и кнопкой устройства; стандартное имя на устройстве ({UnKey.A}).
	chord := arm(t, hotkeyType{m}, map[string]any{"keys": "^{Ctrl}{UnKey.A}"})
	e := ev.Event{Type: ev.EvKey, Code: ev.KeyLeftctrl, Value: 1}
	m.HandleInput(other, &e, true)
	press(other, ev.KeyA)
	press(kbPath, ev.KeyA)
	e.Value = 0
	m.HandleInput(other, &e, true)
	if chord.count() != 1 {
		t.Errorf("chord fires = %d, want 1 (only A from UnKey)", chord.count())
	}

	// Последовательность из кнопок устройства.
	seq := arm(t, sequenceType{m}, map[string]any{"keys": "{UnKey001}{UnKey001}"})
	press(kbPath, ev.KeyCalc)
	press(other, ev.KeyCalc)
	if seq.count() != 0 {
		t.Error("sequence fired with a press from another device")
	}
	press(kbPath, ev.KeyCalc)
	press(kbPath, ev.KeyCalc)
	if seq.count() != 1 {
		t.Errorf("sequence fires = %d", seq.count())
	}

	// Состояние клавиши устройства.
	k, err := m.ParseKey("{UnKey001}")
	if err != nil || k.Device != "UnKey" {
		t.Fatalf("ParseKey: %+v %v", k, err)
	}
	down := ev.Event{Type: ev.EvKey, Code: ev.KeyCalc, Value: 1}
	m.HandleInput(other, &down, true)
	if m.IsDown(k) {
		t.Error("IsDown: pressed on another device")
	}
	m.HandleInput(kbPath, &down, true)
	if !m.IsDown(k) {
		t.Error("IsDown: pressed on own device")
	}

	// Ошибки имён — коды языка макросов; без инспектора устройства неизвестны.
	for _, c := range []struct {
		keys, code string
		insp       contracts.Inspector
	}{
		{"{UnKey9.001}", dsl.ErrUnknownDevice, fakeInspector{}},
		{"{UnKey099}", dsl.ErrUnknownButton, fakeInspector{}},
		{"{UnKey001}", dsl.ErrUnknownDevice, nil},
	} {
		mm, _ := newTestModule()
		mm.inspect = c.insp
		err := hotkeyType{mm}.Validate(project.Trigger{Type: "hotkey", Params: map[string]any{"keys": c.keys}})
		var de *dsl.Error
		if !errors.As(err, &de) || de.Code != c.code {
			t.Errorf("%s: err = %v", c.keys, err)
		}
	}
}

// fakeOut — устройство-цель привязок: записывает нажатия и положения осей.
type fakeOut struct {
	mu  sync.Mutex
	log []string
}

func (f *fakeOut) add(s string) { f.mu.Lock(); f.log = append(f.log, s); f.mu.Unlock() }
func (f *fakeOut) got() string  { f.mu.Lock(); defer f.mu.Unlock(); return strings.Join(f.log, " ") }

// fakeOutDev — устройство-цель с именем (клавиатура mKey или pad2).
type fakeOutDev struct {
	contracts.VirtualDevice
	name string
	out  *fakeOut
}

func (d fakeOutDev) Press(_ context.Context, c uint16) error {
	d.out.add(fmt.Sprintf("%s+%s", d.name, ev.CodeName(ev.EvKey, c)))
	return nil
}
func (d fakeOutDev) Release(_ context.Context, c uint16) error {
	d.out.add(fmt.Sprintf("%s-%s", d.name, ev.CodeName(ev.EvKey, c)))
	return nil
}
func (d fakeOutDev) SetAxis(_ context.Context, c uint16, v float64) error {
	d.out.add(fmt.Sprintf("%s:%s=%v", d.name, ev.CodeName(ev.EvAbs, c), v))
	return nil
}

// fakeOutputs — клавиатура mKey и виртуальные устройства проектов (только pad2 — Xbox).
type fakeOutputs struct {
	contracts.VirtualDevices
	contracts.VirtualDeviceManager
	out *fakeOut
}

func (f fakeOutputs) Keyboard() (contracts.VirtualDevice, error) {
	return fakeOutDev{name: "kbd", out: f.out}, nil
}
func (f fakeOutputs) Device(name string) (contracts.VirtualDevice, error) {
	return fakeOutDev{name: name, out: f.out}, nil
}
func (f fakeOutputs) ResolveIn(_ project.VirtualDevice, control string) (uint16, bool, error) {
	switch control {
	case "DPadUp":
		return ev.BtnDpadUp, false, nil
	case "LX":
		return ev.AbsX, true, nil
	case "LY":
		return ev.AbsY, true, nil
	case "RX":
		return ev.AbsRx, true, nil
	}
	return 0, false, contracts.ErrUnknownControl
}

// TestBindings проверяет привязки (FR-VD-3): кнопка → кнопка виртуального устройства, две
// клавиши → ось (последняя нажатая побеждает, отпустили все — покой), кнопка → клавиша mKey,
// «спрятать» нажатие, сброс при перезагрузке проектов и отпускание после экстренной остановки.
func TestBindings(t *testing.T) {
	t.Parallel()
	m, in := newTestModule()
	out := &fakeOut{}
	outs := fakeOutputs{out: out}
	m.devs, m.vdm = outs, outs
	m.projects = fakeProjects{p: project.Project{ID: "p",
		VirtualDevices: []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}},
		Bindings: []project.Binding{
			{From: "{H}", To: "{pad2.DPadUp}"},
			{From: "{A}", To: "{pad2.LX}", Value: -1},
			{From: "{F8}", To: "{pad2.LX}", Value: 1, Hide: true},
			{From: "{CapsLock}", To: "{Space}"},
			{From: "{Nope}", To: "{pad2.South}"}, // ошибочная — пропускается
		}}}
	m.reloadRemaps()
	m.wg.Add(1)
	go m.bindWorker()
	defer func() { _ = m.Stop(context.Background()) }()
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for out.got() != want {
			if time.Now().After(deadline) {
				t.Fatalf("outputs:\n%s\nwant:\n%s", out.got(), want)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Кнопка → кнопка: нажатие и отпускание.
	key(m, ev.KeyH, 1)
	key(m, ev.KeyH, 0)
	want := "pad2+BTN_DPAD_UP pad2-BTN_DPAD_UP"
	wait(want)

	// Две кнопки на одну ось: A — влево, F8 — вправо, отпустили F8 — снова влево, A — центр.
	// F8 спрятана от системы (устройство захватывается ради неё).
	key(m, ev.KeyA, 1)
	if !key(m, ev.KeyF8, 1) || !in.grabs() {
		t.Error("hidden binding press must be dropped and the device grabbed")
	}
	key(m, ev.KeyF8, 0)
	key(m, ev.KeyA, 0)
	want += " pad2:ABS_X=-1 pad2:ABS_X=1 pad2:ABS_X=-1 pad2:ABS_X=0"
	wait(want)

	// Кнопка → клавиша mKey.
	key(m, ev.KeyCapslock, 1)
	key(m, ev.KeyCapslock, 0)
	want += " kbd+KEY_SPACE kbd-KEY_SPACE"
	wait(want)

	// Перезагрузка проектов при нажатой привязке — нажатое отпускается.
	key(m, ev.KeyH, 1)
	m.reloadRemaps()
	want += " pad2+BTN_DPAD_UP pad2-BTN_DPAD_UP"
	wait(want)

	// После экстренной остановки нажатие не передаётся, а отпускание — всегда.
	key(m, ev.KeyH, 1)
	in.mu.Lock()
	in.suspended = true
	in.mu.Unlock()
	key(m, ev.KeyA, 1)
	key(m, ev.KeyH, 0)
	want += " pad2+BTN_DPAD_UP pad2-BTN_DPAD_UP"
	wait(want)
}

// axis отправляет движение оси (EV_ABS или EV_REL) устройства path; true — событие спрятано.
func axis(m *Module, path string, typ, code uint16, value int32) bool {
	e := ev.Event{Type: typ, Code: code, Value: value}
	return m.HandleInput(path, &e, true)
}

// TestAxisBindings проверяет привязки осей (FR-VD-3): ось → ось с переворотом и мёртвой зоной,
// курок → кнопка с порогом, мышь → стик (возврат в центр), колесо → клавиша, плавный наклон клавишей.
func TestAxisBindings(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	out := &fakeOut{}
	outs := fakeOutputs{out: out}
	m.devs, m.vdm = outs, outs
	m.projects = fakeProjects{p: project.Project{ID: "p",
		VirtualDevices: []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}},
		Bindings: []project.Binding{
			{From: "{LX}", To: "{pad2.LX}", Invert: true, Deadzone: 0.5, Hide: true},
			{From: "{LT}", To: "{Space}", Threshold: 0.5},
			{From: "{MouseX}", To: "{pad2.RX}", Sensitivity: 2},
			{From: "{MouseWheel}", To: "{H}", Threshold: 1},
			{From: "{A}", To: "{pad2.LY}", Value: 1, RampMS: 50},
		}}}
	m.reloadRemaps()
	if len(m.bindings) != 5 {
		t.Fatalf("bindings = %d", len(m.bindings))
	}
	m.wg.Add(1)
	go m.bindWorker()
	defer func() { _ = m.Stop(context.Background()) }()
	wait := func(pred func(string) bool, what string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !pred(out.got()) {
			if time.Now().After(deadline) {
				t.Fatalf("%s; outputs:\n%s", what, out.got())
			}
			time.Sleep(time.Millisecond)
		}
	}
	has := func(s string) func(string) bool { return func(got string) bool { return strings.Contains(got, s) } }

	// Ось → ось: стик вправо до упора — цель влево до упора (переворот); малое отклонение — центр
	// (мёртвая зона 0.5); исходное движение спрятано.
	if !axis(m, padPath, ev.EvAbs, ev.AbsX, 256) {
		t.Error("hidden axis must be dropped")
	}
	wait(has("pad2:ABS_X=-1"), "inverted full deflection")
	axis(m, padPath, ev.EvAbs, ev.AbsX, 160)
	wait(has("pad2:ABS_X=0"), "deadzone")

	// Курок → Пробел: за порогом нажат, чуть ниже порога ещё нажат (гистерезис), отпущен — отпущен.
	axis(m, padPath, ev.EvAbs, ev.AbsZ, 200)
	wait(has("kbd+KEY_SPACE"), "trigger press")
	axis(m, padPath, ev.EvAbs, ev.AbsZ, 120)
	axis(m, padPath, ev.EvAbs, ev.AbsZ, 0)
	wait(has("kbd-KEY_SPACE"), "trigger release")
	if strings.Count(out.got(), "+KEY_SPACE") != 1 {
		t.Errorf("trigger bounced: %s", out.got())
	}

	// Мышь → стик: быстрое движение — наклон до упора, мышь остановилась — стик в центре.
	axis(m, mousePath, ev.EvRel, ev.RelX, 40)
	wait(has("pad2:ABS_RX=1"), "mouse to stick")
	wait(has("pad2:ABS_RX=0"), "stick back to center")

	// Колесо вверх → короткое нажатие H; вниз — ничего.
	axis(m, mousePath, ev.EvRel, ev.RelWheel, -1)
	axis(m, mousePath, ev.EvRel, ev.RelWheel, 1)
	wait(has("kbd-KEY_H"), "wheel tap")
	if strings.Count(out.got(), "+KEY_H") != 1 {
		t.Errorf("wheel: %s", out.got())
	}

	// Клавиша → ось плавно: промежуточные положения, затем 1; отпустили — плавно к 0.
	key(m, ev.KeyA, 1)
	wait(has("pad2:ABS_Y=1"), "ramp up")
	key(m, ev.KeyA, 0)
	wait(func(got string) bool { return strings.HasSuffix(got, "pad2:ABS_Y=0") }, "ramp down")
	if n := strings.Count(out.got(), "pad2:ABS_Y="); n < 4 {
		t.Errorf("ramp is not smooth (%d steps): %s", n, out.got())
	}
}

// TestSteerAndCurve проверяет «мышь как руль» (steer: угол копится и держится; recenter_ms —
// возврат в центр, когда мышь стоит) и кривую отклика у «ось → ось» (ADR-0040).
func TestSteerAndCurve(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	out := &fakeOut{}
	outs := fakeOutputs{out: out}
	m.devs, m.vdm = outs, outs
	m.projects = fakeProjects{p: project.Project{ID: "p",
		VirtualDevices: []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}},
		Bindings: []project.Binding{
			{From: "{MouseX}", To: "{pad2.RX}", Steer: true},
			{From: "{MouseY}", To: "{pad2.LY}", Steer: true, RecenterMS: 100},
			{From: "{LX}", To: "{pad2.LX}", Curve: 2},
		}}}
	m.reloadRemaps()
	if len(m.bindings) != 3 {
		t.Fatalf("bindings = %d", len(m.bindings))
	}
	m.wg.Add(1)
	go m.bindWorker()
	defer func() { _ = m.Stop(context.Background()) }()
	wait := func(pred func(string) bool, what string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !pred(out.got()) {
			if time.Now().After(deadline) {
				t.Fatalf("%s; outputs:\n%s", what, out.got())
			}
			time.Sleep(time.Millisecond)
		}
	}
	has := func(s string) func(string) bool { return func(got string) bool { return strings.Contains(got, s) } }

	// Руль: 500 единиц — половина поворота, ещё 250 — три четверти; мышь стоит — угол держится.
	axis(m, mousePath, ev.EvRel, ev.RelX, 500)
	wait(has("pad2:ABS_RX=0.5"), "steer half")
	axis(m, mousePath, ev.EvRel, ev.RelX, 250)
	wait(has("pad2:ABS_RX=0.75"), "steer accumulates")
	time.Sleep(60 * time.Millisecond)
	if got := out.got(); !strings.HasSuffix(got, "pad2:ABS_RX=0.75") {
		t.Fatalf("steer must hold its angle: %s", got)
	}

	// С возвратом: упор, затем сам в центр (не сразу — через промежуточные положения).
	axis(m, mousePath, ev.EvRel, ev.RelY, 2000)
	wait(has("pad2:ABS_Y=1"), "steer to the stop")
	wait(func(got string) bool {
		return strings.HasSuffix(got, "pad2:ABS_Y=0") || strings.Contains(got, "pad2:ABS_Y=0 ")
	}, "recenter")
	if n := strings.Count(out.got(), "pad2:ABS_Y="); n < 4 {
		t.Errorf("recenter is not smooth (%d steps): %s", n, out.got())
	}

	// Кривая 2: стик наполовину — цель на четверть.
	axis(m, padPath, ev.EvAbs, ev.AbsX, 192)
	wait(has("pad2:ABS_X=0.25"), "curve")
}

// TestLatch проверяет «рычаг» (latch): кнопка плавно двигает ось, после отпускания ось остаётся,
// где её застало отпускание; кнопка с value 0 ставит рычаг в ноль (ADR-0040).
func TestLatch(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	out := &fakeOut{}
	outs := fakeOutputs{out: out}
	m.devs, m.vdm = outs, outs
	m.projects = fakeProjects{p: project.Project{ID: "p",
		VirtualDevices: []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}},
		Bindings: []project.Binding{
			{From: "{A}", To: "{pad2.LY}", Value: 1, RampMS: 300, Latch: true},
			{From: "{B}", To: "{pad2.LY}", Latch: true},
		}}}
	m.reloadRemaps()
	if len(m.bindings) != 2 {
		t.Fatalf("bindings = %d", len(m.bindings))
	}
	m.wg.Add(1)
	go m.bindWorker()
	defer func() { _ = m.Stop(context.Background()) }()

	// A держим ~100 мс из 300: ось на полпути; отпустили — больше не двигается.
	key(m, ev.KeyA, 1)
	time.Sleep(100 * time.Millisecond)
	key(m, ev.KeyA, 0)
	time.Sleep(30 * time.Millisecond)
	before := out.got()
	time.Sleep(100 * time.Millisecond)
	if after := out.got(); after != before {
		t.Fatalf("latched axis kept moving:\n%s\n%s", before, after)
	}
	if strings.Contains(before, "pad2:ABS_Y=1") || !strings.Contains(before, "pad2:ABS_Y=0.") {
		t.Fatalf("latch must stop halfway: %s", before)
	}

	// B — рычаг в ноль.
	key(m, ev.KeyB, 1)
	key(m, ev.KeyB, 0)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.HasSuffix(out.got(), "pad2:ABS_Y=0") {
		if time.Now().After(deadline) {
			t.Fatalf("lever to zero: %s", out.got())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestBindingOptions проверяет правила настроек привязок (contracts.CompileBinding).
func TestBindingOptions(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule()
	outs := fakeOutputs{out: &fakeOut{}}
	m.devs, m.vdm = outs, outs
	pads := []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}}
	for _, c := range []struct {
		b    project.Binding
		code string
	}{
		{project.Binding{From: "{LX}", To: "{pad2.LX}"}, ""},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Value: 1}, dsl.ErrBindingOption},
		{project.Binding{From: "{LX}", To: "{Space}"}, dsl.ErrBindingThreshold},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Deadzone: 0.95}, dsl.ErrBindingRange},
		{project.Binding{From: "{A}", To: "{Space}", Invert: true}, dsl.ErrBindingOption},
		{project.Binding{From: "{A}", To: "{pad2.LX}", RampMS: 9000, Value: 1}, dsl.ErrBindingRange},
		{project.Binding{From: "{MouseX}", To: "{pad2.RX}", Deadzone: 0.1}, dsl.ErrBindingOption},
		{project.Binding{From: "{A}", To: "{Space}", Value: 1}, dsl.ErrAxisExpected},
		{project.Binding{From: "{Nope}", To: "{Space}"}, dsl.ErrUnknownKey},
		{project.Binding{From: "{MouseX}", To: "{pad2.LX}", Steer: true, RecenterMS: 500}, ""},
		{project.Binding{From: "{MouseX}", To: "{pad2.LX}", RecenterMS: 500}, dsl.ErrBindingOption},
		{project.Binding{From: "{MouseX}", To: "{pad2.LX}", Steer: true, RecenterMS: 20000}, dsl.ErrBindingRange},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Steer: true}, dsl.ErrBindingOption},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Curve: 2}, ""},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Curve: 0.1}, dsl.ErrBindingRange},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Curve: 9}, dsl.ErrBindingRange},
		{project.Binding{From: "{MouseX}", To: "{pad2.LX}", Curve: 2}, dsl.ErrBindingOption},
		{project.Binding{From: "{A}", To: "{pad2.LX}", Latch: true}, ""},
		{project.Binding{From: "{A}", To: "{pad2.LX}"}, dsl.ErrBindingValue},
		{project.Binding{From: "{LX}", To: "{pad2.LX}", Latch: true}, dsl.ErrBindingOption},
	} {
		src, err := m.ParseBindingSource(c.b.From)
		if err == nil {
			_, err = contracts.CompileBinding(c.b, src, pads, outs)
		}
		var de *dsl.Error
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%+v: %v", c.b, err)
		case c.code != "" && (!errors.As(err, &de) || de.Code != c.code):
			t.Errorf("%+v: %v, want %s", c.b, err, c.code)
		}
	}
}

// BenchmarkHandleInput измеряет обработку одного события в потоке ввода (NFR-1: медиана < 1 мс)
// при включённых горячих клавишах, переназначении и привязках.
func BenchmarkHandleInput(b *testing.B) {
	m, _ := newTestModule()
	out := &fakeOut{}
	outs := fakeOutputs{out: out}
	m.devs, m.vdm = outs, outs
	m.projects = fakeProjects{p: project.Project{ID: "p",
		VirtualDevices: []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}},
		Remaps:         []project.Remap{{From: "{CapsLock}", To: "{Esc}"}},
		Bindings: []project.Binding{
			{From: "{H}", To: "{pad2.DPadUp}"},
			{From: "{LX}", To: "{pad2.LX}", Deadzone: 0.1},
		}}}
	m.reloadRemaps()
	for range 20 {
		arm(b, hotkeyType{m}, map[string]any{"keys": "^{Ctrl}^{Alt}{F8}"})
	}
	m.wg.Add(1)
	go m.bindWorker()
	defer func() { _ = m.Stop(context.Background()) }()
	b.ResetTimer()
	for i := range b.N {
		code := []uint16{ev.KeyA, ev.KeyH, ev.KeyCapslock}[i%3]
		key(m, code, 1)
		key(m, code, 0)
	}
}
