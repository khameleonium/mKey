package hotkeys

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// kbPath — путь фейковой клавиатуры.
const kbPath = "/dev/input/event3"

// fakeInput — источник событий: список устройств и запись вызовов захвата и Inject.
type fakeInput struct {
	mu        sync.Mutex
	policy    func(contracts.InputDevice) bool
	injected  []ev.Event
	suspended bool
}

func (f *fakeInput) Devices() []contracts.InputDevice {
	return []contracts.InputDevice{{Info: ev.Info{Path: kbPath, Name: "USB Keyboard", Caps: ev.Capabilities{Codes: map[uint16][]uint16{
		ev.EvKey: {ev.KeyA, ev.KeyF8, ev.KeyH, ev.KeyCapslock, ev.KeyLeftctrl},
	}}}}}
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
func (f *fakeInput) EmergencyCombo() string         { return "" }
func (f *fakeInput) SetEmergencyCombo(string) error { return nil }
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
func arm(t *testing.T, tt contracts.TriggerType, params map[string]any) *recorder {
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
	f8, _ := keys.Lookup("F8")
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
	ctrl, _ := keys.Lookup("Ctrl")
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
	a, _ := keys.Lookup("A")
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
