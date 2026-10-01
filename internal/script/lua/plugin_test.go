package lua

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginSrc — Lua-плагин для проверки: действие count со своим счётчиком, действие press
// (нажатие — нужно разрешение output.send), условие big и проверка параметров.
const pluginSrc = `
local count = 0
mkey.register_action{
  id = "count", name = { ru = "Посчитать", en = "Count" },
  params = { type = "object", properties = { step = { type = "integer", default = 1 } } },
  validate = function(p) if p.step and p.step < 0 then return "шаг не может быть меньше нуля" end end,
  run = function(p, event)
    count = count + (p.step or 1)
    mkey.var.count = count
  end,
}
mkey.register_action{ id = "press", run = function() mkey.tap("A") end }
mkey.register_action{ id = "files", run = function() return os.time() end }
mkey.register_condition{ id = "big", check = function(p) return count >= (p.min or 10) end }
`

// loadTestPlugin пишет main.lua и загружает его с разрешениями perms.
func loadTestPlugin(t *testing.T, perms []string) *luaPlugin {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.lua")
	if err := os.WriteFile(path, []byte(pluginSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Module{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, err := m.LoadPlugin("io.test.counter", path, perms)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p.(*luaPlugin)
}

// TestLuaPlugin проверяет Lua-плагин: виды со схемой и названиями, состояние между вызовами,
// проверку параметров, условие, разрешения и отсутствие доступа к os.
func TestLuaPlugin(t *testing.T) {
	t.Parallel()
	p := loadTestPlugin(t, []string{"vars.write"})
	ctx := context.Background()
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{}}}

	// Виды: названия и схема параметров.
	if len(p.Actions()) != 3 || len(p.Conditions()) != 1 {
		t.Fatalf("types: %+v %+v", p.Actions(), p.Conditions())
	}
	a := p.Actions()[0]
	if a.Names["ru"] != "Посчитать" || !strings.Contains(string(a.ParamsSchema), `"step"`) {
		t.Fatalf("action = %+v (%s)", a, a.ParamsSchema)
	}

	// Счётчик живёт между вызовами; переменная записана (разрешение vars.write есть).
	for range 3 {
		if err := p.RunAction(ctx, rc, "count", map[string]any{"step": 4}); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := rc.vars.Get("count"); v != 12.0 {
		t.Fatalf("count = %v", v)
	}
	if ok, err := p.CheckCondition(ctx, rc, "big", map[string]any{"min": 10}); !ok || err != nil {
		t.Fatalf("big = %v %v", ok, err)
	}

	// Проверка параметров — текст плагина.
	if err := p.Validate("count", map[string]any{"step": -1}); err == nil || err.Error() != "шаг не может быть меньше нуля" {
		t.Fatalf("validate = %v", err)
	}

	// Без разрешения output.send нажатие — ошибка; os недоступен.
	if err := p.RunAction(ctx, rc, "press", nil); err == nil || !strings.Contains(err.Error(), "output.send") {
		t.Fatalf("press = %v", err)
	}
	if len(rc.sends) != 0 {
		t.Fatalf("sent without permission: %v", rc.sends)
	}
	if err := p.RunAction(ctx, rc, "files", nil); err == nil {
		t.Fatal("os must be unavailable to plugins")
	}
}

// TestCounterExample проверяет пример examples/plugins/lua-counter: загрузка, счёт, условие.
func TestCounterExample(t *testing.T) {
	t.Parallel()
	m := &Module{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, err := m.LoadPlugin("io.mkey.example.counter", "../../../examples/plugins/lua-counter/main.lua", []string{"notify"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx := context.Background()
	rc := &fakeRC{vars: &fakeVars{m: map[string]any{}}}
	for range 2 {
		if err := p.RunAction(ctx, rc, "counter_add", map[string]any{"step": 5, "goal": 10}); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := p.CheckCondition(ctx, rc, "counter_reached", map[string]any{"value": 10}); !ok {
		t.Fatal("counter did not reach 10")
	}
	if err := p.Validate("counter_add", map[string]any{"step": 0}); err == nil {
		t.Fatal("step 0 accepted")
	}
}
