package lua

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// fakeRC — контекст выполнения события, записывающий макросы.
type fakeRC struct {
	mu    sync.Mutex
	sends []string
	vars  *fakeVars
}

func (f *fakeRC) Event() contracts.EventRef {
	return contracts.EventRef{Project: "p", Event: "e", Name: "Test"}
}
func (f *fakeRC) Send(_ context.Context, src string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, src)
	return nil
}
func (f *fakeRC) RunActions(context.Context, []project.Action) error       { return nil }
func (f *fakeRC) Check(context.Context, []project.Condition) (bool, error) { return true, nil }
func (f *fakeRC) Toggled() bool                                            { return true }
func (f *fakeRC) Held() bool                                               { return false }
func (f *fakeRC) Fire() contracts.Fire                                     { return contracts.Fire{} }
func (f *fakeRC) Vars() contracts.VarStore                                 { return f.vars }
func (f *fakeRC) Logger() *slog.Logger                                     { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeVars — переменные в памяти.
type fakeVars struct {
	mu sync.Mutex
	m  map[string]any
}

func (v *fakeVars) Get(n string) (any, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	x, ok := v.m[n]
	return x, ok
}
func (v *fakeVars) Set(n string, x any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[n] = x
	return nil
}
func (v *fakeVars) Add(string, float64) error { return nil }
func (v *fakeVars) All() map[string]any       { v.mu.Lock(); defer v.mu.Unlock(); return maps.Clone(v.m) }

// fakeKeys — состояние клавиш: зажата только F8.
type fakeKeys struct{}

func (fakeKeys) IsDown(k contracts.DeviceKey) bool                  { return k.Name == "F8" }
func (fakeKeys) WaitKey(context.Context, contracts.DeviceKey) error { return nil }
func (fakeKeys) Resume()                                            {}
func (fakeKeys) Suspended() bool                                    { return false }

// ParseKey знает стандартные имена и одну кнопку устройства — UnKey001 (как F8 устройства UnKey).
// ParseBindingSource — как ParseKey (осей фейк не знает).
func (f fakeKeys) ParseBindingSource(name string) (contracts.DeviceKey, error) {
	return f.ParseKey(name)
}

func (fakeKeys) ParseKey(name string) (contracts.DeviceKey, error) {
	name = strings.Trim(name, "{}")
	if name == "UnKey001" {
		return contracts.DeviceKey{Key: keys.Key{Name: "F8", Type: 1, Code: 66}, Device: "UnKey"}, nil
	}
	k, ok := keys.Lookup(name)
	if !ok {
		return contracts.DeviceKey{}, errors.New("unknown key")
	}
	return contracts.DeviceKey{Key: k}, nil
}

// newAction создаёт действие lua с фейковым состоянием клавиш.
func newAction() luaAction {
	return luaAction{m: &Module{keyState: fakeKeys{}}}
}

// TestScriptAPI проверяет функции mkey: нажатия, переменные, состояние клавиш, сведения о событии.
func TestScriptAPI(t *testing.T) {
	t.Parallel()
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{"count": 1.0}}}
	code := `
mkey.tap("A")
mkey.down("Shift")
mkey.up("{Shift}")
mkey.hold("Space", 50)
mkey.type("Привет")
mkey.move_rel(10, -5)
mkey.click("Right")
mkey.var.count = mkey.var.count + 1
if mkey.is_down("F8") and not mkey.is_down("F9") and mkey.is_down("UnKey001") then mkey.send("{B}") end
mkey.var.who = mkey.event.id
local w = mkey.window()
local p, err = mkey.pixel(1, 2)
if w == nil and p == nil and err ~= nil then mkey.send("{C}") end
`
	if err := newAction().Run(context.Background(), rc, project.Action{Type: "lua", Value: code}); err != nil {
		t.Fatal(err)
	}
	want := []string{"{A}", "^{Shift}", "~{Shift}", "{Space 50}", `{"Привет"}`, "{Move +10 -5}", "{Click Right}", "{B}", "{C}"}
	if strings.Join(rc.sends, "|") != strings.Join(want, "|") {
		t.Fatalf("sends = %v", rc.sends)
	}
	if v, _ := rc.vars.Get("count"); v != 2.0 {
		t.Fatalf("count = %v", v)
	}
	if v, _ := rc.vars.Get("who"); v != "e" {
		t.Fatalf("who = %v", v)
	}
}

// TestCancelInfiniteLoop проверяет, что остановка прерывает бесконечный цикл и вызывает on_stop.
func TestCancelInfiniteLoop(t *testing.T) {
	t.Parallel()
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{}}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	code := `mkey.on_stop(function() mkey.var.cleaned = true end)
while true do end`

	start := time.Now()
	err := newAction().Run(ctx, rc, project.Action{Type: "lua", Value: code})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if v, _ := rc.vars.Get("cleaned"); v != true {
		t.Fatalf("on_stop not called: %v", v)
	}
}

// TestSleepInterrupted проверяет, что mkey.sleep прерывается остановкой.
func TestSleepInterrupted(t *testing.T) {
	t.Parallel()
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{}}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	err := newAction().Run(ctx, rc, project.Action{Type: "lua", Value: `mkey.sleep(60000)`})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("sleep not interrupted: %v after %v", err, time.Since(start))
	}
}

// TestValidate проверяет проверку синтаксиса и параметров.
func TestValidate(t *testing.T) {
	t.Parallel()
	a := newAction()
	if err := a.Validate(project.Action{Value: `mkey.tap("A")`}); err != nil {
		t.Fatalf("valid script: %v", err)
	}
	for _, bad := range []any{`if then`, map[string]any{"code": "x", "file": "y"}, map[string]any{}} {
		if err := a.Validate(project.Action{Value: bad}); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}

	// Ошибка во время выполнения — понятная ошибка, а не паника.
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{}}}
	if err := a.Run(context.Background(), rc, project.Action{Value: `error("boom")`}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runtime error = %v", err)
	}
}
