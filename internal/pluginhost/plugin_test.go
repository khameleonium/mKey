package pluginhost

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/jsonrpc"
	"mkey/internal/lib/project"
	"mkey/internal/registry"
)

// Тестовый плагин — сама тестовая программа, запущенная с MKEY_TEST_PLUGIN=1 (TestMain).
// Он знает действия hello (нажать params.text через mkey.send), crash (упасть), forbidden
// (попросить то, на что нет разрешения), условие is_true и триггер tick (срабатывает сразу).

// TestMain запускает тестовый плагин вместо тестов, если так задано окружением.
func TestMain(m *testing.M) {
	if os.Getenv("MKEY_TEST_PLUGIN") == "1" {
		runTestPlugin()
		return
	}
	os.Exit(m.Run())
}

// runTestPlugin — тестовый плагин: отвечает по протоколу через stdin/stdout.
func runTestPlugin() {
	var conn *jsonrpc.Conn
	done := make(chan struct{})
	var once sync.Once
	handler := func(ctx context.Context, method string, params json.RawMessage, _ bool) (any, error) {
		var p struct {
			Type   string         `json:"type"`
			Params map[string]any `json:"params"`
			Handle string         `json:"handle"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "initialize":
			return map[string]any{
				"actions": []map[string]any{
					{"id": "hello", "name": map[string]string{"ru": "Привет", "en": "Hello"}, "params_schema": map[string]any{"type": "object"}},
					{"id": "crash"}, {"id": "forbidden"},
				},
				"conditions": []map[string]any{{"id": "is_true"}},
				"triggers":   []map[string]any{{"id": "tick"}},
			}, nil
		case "ping":
			return nil, nil
		case "shutdown":
			once.Do(func() { close(done) })
			return nil, nil
		case "action.validate":
			if p.Params["text"] == "bad" {
				return nil, errors.New("плохой текст")
			}
			return nil, nil
		case "action.run":
			switch p.Type {
			case "hello":
				return nil, conn.Call(ctx, "mkey.send", map[string]any{"macro": p.Params["text"]}, nil)
			case "crash":
				os.Exit(3)
			case "forbidden":
				return nil, conn.Call(ctx, "mkey.vars.set", map[string]any{"project": "p", "name": "x", "value": 1}, nil)
			}
		case "condition.check":
			return map[string]any{"result": p.Params["value"] == true}, nil
		case "trigger.arm":
			go func() {
				_ = conn.Notify("trigger.fire", map[string]any{"handle": p.Handle, "vars": map[string]any{"n": 1}})
			}()
			return nil, nil
		case "trigger.disarm":
			return nil, nil
		}
		return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "no %s", method)
	}
	conn = jsonrpc.New(os.Stdin, os.Stdout, handler)
	select {
	case <-done:
	case <-conn.Done():
	}
	time.Sleep(10 * time.Millisecond)
}

// writeTestPlugin создаёт папку плагина: манифест и скрипт запуска тестовой программы как плагина.
func writeTestPlugin(t *testing.T, dir, id string, perms ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pdir := filepath.Join(dir, id)
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nMKEY_TEST_PLUGIN=1 exec " + exe + " -test.run=^$\n"
	if err := os.WriteFile(filepath.Join(pdir, "run.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	perm := "[]"
	if len(perms) > 0 {
		perm = "[" + strings.Join(perms, ", ") + "]"
	}
	man := "id: " + id + "\nname: { ru: Тест, en: Test }\nversion: 0.1.0\nplugin_api: 1\nkind: process\nentry: ./run.sh\npermissions: " + perm + "\n"
	if err := os.WriteFile(filepath.Join(pdir, ManifestFile), []byte(man), 0o600); err != nil {
		t.Fatal(err)
	}
	return pdir
}

// fakeRunner запоминает макросы, отправленные плагином.
type fakeRunner struct {
	contracts.SequenceRunner
	mu   sync.Mutex
	sent []string
}

// Run запоминает макрос.
func (f *fakeRunner) Run(_ context.Context, src string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, src)
	return nil
}

// fakeRC — контекст выполнения события для действий и условий.
type fakeRC struct{ contracts.RunContext }

// Event возвращает событие p/e.
func (fakeRC) Event() contracts.EventRef { return contracts.EventRef{Project: "p", Event: "e"} }

// Fire возвращает пустое срабатывание.
func (fakeRC) Fire() contracts.Fire { return contracts.Fire{} }

// newTestHost создаёт модуль с временными папками, реестром, шиной и фейковым выводом.
func newTestHost(t *testing.T) (*Module, *fakeRunner) {
	t.Helper()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := New()
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.tr, m.bus, m.ext = i18n.New(cat, "ru"), bus.New(16), registry.NewExtensions()
	m.cfg = Config{Dir: filepath.Join(dir, "plugins"), SystemDir: filepath.Join(dir, "system"), LogDir: filepath.Join(dir, "logs"), Active: []string{}}
	r := &fakeRunner{}
	m.runner = r
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, r
}

// waitState ждёт состояния плагина.
func waitState(t *testing.T, m *Module, id, state string) contracts.PluginInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, p := range m.List() {
			if p.ID == id && p.State == state {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin %s did not reach %s: %+v", id, state, m.List())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPluginLifecycle проверяет полный путь: установка (выключен), включение, виды в реестре,
// действие с запросом к mKey, проверка параметров, условие, триггер, нет разрешения, падение
// с перезапуском и повторным взводом триггера, выключение (виды убраны), удаление.
func TestPluginLifecycle(t *testing.T) {
	t.Parallel()
	m, runner := newTestHost(t)
	src := writeTestPlugin(t, t.TempDir(), "io.test.hello", "output.send")
	ctx := context.Background()

	// Установка: плагин выключен, видов нет.
	info, err := m.Install(src)
	if err != nil || info.Active || info.State != contracts.PluginOff {
		t.Fatalf("install: %+v %v", info, err)
	}
	if _, err := m.Install(src); !errors.Is(err, contracts.ErrPluginExists) {
		t.Fatalf("second install: %v", err)
	}

	// Включение: работает, виды зарегистрированы с названиями на языках.
	if err := m.SetActive("io.test.hello", true); err != nil {
		t.Fatal(err)
	}
	info = waitState(t, m, "io.test.hello", contracts.PluginRunning)
	if strings.Join(info.Actions, ",") != "hello,crash,forbidden" || m.Active()[0] != "io.test.hello" {
		t.Fatalf("info = %+v", info)
	}
	ext, ok := m.ext.Get(contracts.PointAction, "hello")
	if !ok || ext.Meta().Names["ru"] != "Привет" || ext.Meta().Provider != "io.test.hello" {
		t.Fatalf("registry: %v %+v", ok, ext)
	}
	act := ext.(contracts.ActionType)

	// Действие: плагин нажимает текст через mKey; неверные параметры — текст ошибки плагина.
	if err := act.Run(ctx, fakeRC{}, project.Action{Type: "hello", Value: map[string]any{"text": "{A}"}}); err != nil {
		t.Fatal(err)
	}
	if len(runner.sent) != 1 || runner.sent[0] != "{A}" {
		t.Fatalf("sent = %v", runner.sent)
	}
	if err := act.Validate(project.Action{Value: map[string]any{"text": "bad"}}); err == nil || err.Error() != "плохой текст" {
		t.Fatalf("validate: %v", err)
	}

	// Условие и разрешения: vars.write не объявлено — ошибка -32001.
	cond, _ := m.ext.Get(contracts.PointCondition, "is_true")
	if ok, err := cond.(contracts.ConditionType).Check(ctx, fakeRC{}, project.Condition{Params: map[string]any{"value": true}}); !ok || err != nil {
		t.Fatalf("condition: %v %v", ok, err)
	}
	forb, _ := m.ext.Get(contracts.PointAction, "forbidden")
	var rpcErr *jsonrpc.Error
	if err := forb.(contracts.ActionType).Run(ctx, fakeRC{}, project.Action{}); !errors.As(err, &rpcErr) || !strings.Contains(err.Error(), "vars.write") {
		t.Fatalf("forbidden: %v", err)
	}

	// Триггер: срабатывает сразу после взвода.
	fires := make(chan contracts.Fire, 4)
	trig, _ := m.ext.Get(contracts.PointTrigger, "tick")
	disarm, err := trig.(contracts.TriggerType).Arm(ctx, contracts.EventRef{Project: "p", Event: "e"}, project.Trigger{Type: "tick"}, func(f contracts.Fire) { fires <- f })
	if err != nil {
		t.Fatal(err)
	}
	if f := <-fires; f.Vars["n"] != float64(1) {
		t.Fatalf("fire vars = %v", f.Vars)
	}

	// Падение: плагин перезапускается и взводит триггер заново.
	crash, _ := m.ext.Get(contracts.PointAction, "crash")
	_ = crash.(contracts.ActionType).Run(ctx, fakeRC{}, project.Action{})
	waitState(t, m, "io.test.hello", contracts.PluginRunning)
	select {
	case <-fires:
	case <-time.After(10 * time.Second):
		t.Fatal("trigger was not re-armed after restart")
	}
	disarm()

	// Выключение: виды убраны; удаление — папки нет.
	if err := m.SetActive("io.test.hello", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.ext.Get(contracts.PointAction, "hello"); ok {
		t.Fatal("type still registered after disable")
	}
	if err := m.Remove("io.test.hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.cfg.Dir, "io.test.hello")); !os.IsNotExist(err) {
		t.Fatalf("folder left: %v", err)
	}
}

// TestManifestErrors проверяет понятные ошибки манифеста и опасный архив.
func TestManifestErrors(t *testing.T) {
	t.Parallel()
	m, _ := newTestHost(t)
	dir := t.TempDir()
	for name, man := range map[string]string{
		"api":   "id: a\nplugin_api: 2\nkind: data\n",
		"kind":  "id: a\nplugin_api: 1\nkind: dll\n",
		"entry": "id: a\nplugin_api: 1\nkind: process\nentry: ../evil\n",
		"perm":  "id: a\nplugin_api: 1\nkind: data\npermissions: [root]\n",
		"typo":  "id: a\nplugin_api: 1\nkind: data\nentyr: x\n",
	} {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(p, 0o700)
		_ = os.WriteFile(filepath.Join(p, ManifestFile), []byte(man), 0o600)
		if _, err := m.Install(p); !errors.Is(err, contracts.ErrBadPlugin) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Архив с путём наружу не распаковывается.
	zpath := filepath.Join(dir, "evil.zip")
	f, _ := os.Create(zpath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../escape.txt")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	_ = f.Close()
	if _, err := m.Install(zpath); !errors.Is(err, contracts.ErrBadPlugin) {
		t.Fatalf("zip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.cfg.Dir, "..", "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped the plugin folder")
	}
}

// TestCrashLoop проверяет отключение после трёх падений за минуту.
func TestCrashLoop(t *testing.T) {
	t.Parallel()
	m, _ := newTestHost(t)
	pdir := filepath.Join(m.cfg.Dir, "io.test.crash")
	_ = os.MkdirAll(pdir, 0o700)
	_ = os.WriteFile(filepath.Join(pdir, "run.sh"), []byte("#!/bin/sh\necho oops >&2\nexit 1\n"), 0o700)
	_ = os.WriteFile(filepath.Join(pdir, ManifestFile), []byte("id: io.test.crash\nplugin_api: 1\nkind: process\nentry: run.sh\n"), 0o600)
	if err := m.SetActive("io.test.crash", true); err != nil {
		t.Fatal(err)
	}
	info := waitState(t, m, "io.test.crash", contracts.PluginFailed)
	if info.Error == "" {
		t.Fatal("no error")
	}
	if lines, _ := m.Log("io.test.crash", 10); len(lines) == 0 || lines[0] != "oops" {
		t.Fatalf("log = %v", lines)
	}
}

// fakeLua — загрузчик Lua-плагинов: плагин с одним действием tick.
type fakeLua struct{ loaded chan string }

// LoadPlugin «загружает» плагин и сообщает путь.
func (f fakeLua) LoadPlugin(_, path string, _ []string) (contracts.LuaPlugin, error) {
	f.loaded <- path
	return fakeLuaPlugin{}, nil
}

// fakeLuaPlugin — Lua-плагин с действием tick и триггером ping (срабатывает сразу при взведении).
type fakeLuaPlugin struct{ contracts.LuaPlugin }

// Actions возвращает действие tick.
func (fakeLuaPlugin) Actions() []contracts.PluginType {
	return []contracts.PluginType{{ID: "tick", Names: map[string]string{"ru": "Тик"}}}
}

// Conditions — условий нет.
func (fakeLuaPlugin) Conditions() []contracts.PluginType { return nil }

// Triggers возвращает триггер ping.
func (fakeLuaPlugin) Triggers() []contracts.PluginType {
	return []contracts.PluginType{{ID: "ping", Names: map[string]string{"ru": "Пинг"}}}
}

// Validate принимает любые параметры.
func (fakeLuaPlugin) Validate(string, any) error { return nil }

// ArmTrigger срабатывает сразу со значением from = событие.
func (fakeLuaPlugin) ArmTrigger(_ context.Context, ev contracts.EventRef, _ string, _ any, fire func(map[string]any)) (func(), error) {
	fire(map[string]any{"from": ev.Event})
	return func() {}, nil
}

// Close ничего не делает.
func (fakeLuaPlugin) Close() {}

// TestLuaAndDataPlugins проверяет Lua-плагин (виды из загрузчика) и плагин-данные (шаблон проекта).
func TestLuaAndDataPlugins(t *testing.T) {
	t.Parallel()
	m, _ := newTestHost(t)
	loaded := make(chan string, 1)
	m.lua = fakeLua{loaded: loaded}

	// Lua-плагин: main.lua загружен, действие зарегистрировано с названием плагина.
	ldir := filepath.Join(m.cfg.Dir, "io.test.lua")
	_ = os.MkdirAll(ldir, 0o700)
	_ = os.WriteFile(filepath.Join(ldir, "main.lua"), []byte("-- test"), 0o600)
	_ = os.WriteFile(filepath.Join(ldir, ManifestFile), []byte("id: io.test.lua\nplugin_api: 1\nkind: lua\nentry: main.lua\n"), 0o600)
	if err := m.SetActive("io.test.lua", true); err != nil {
		t.Fatal(err)
	}
	if got := <-loaded; got != filepath.Join(ldir, "main.lua") {
		t.Fatalf("loaded %s", got)
	}
	if e, ok := m.ext.Get(contracts.PointAction, "tick"); !ok || e.Meta().Names["ru"] != "Тик" {
		t.Fatalf("tick: %v", ok)
	}

	// Триггер Lua-плагина: значения опроса доходят до срабатывания.
	e, ok := m.ext.Get(contracts.PointTrigger, "ping")
	if !ok || e.Meta().Names["ru"] != "Пинг" {
		t.Fatalf("ping: %v", ok)
	}
	var got contracts.Fire
	disarm, err := e.(contracts.TriggerType).Arm(context.Background(), contracts.EventRef{Event: "e1"}, project.Trigger{Type: "ping"}, func(f contracts.Fire) { got = f })
	if err != nil || got.Vars["from"] != "e1" {
		t.Fatalf("arm: %v, fire = %+v", err, got)
	}
	disarm()

	// Плагин-данные: шаблон проекта из templates/.
	ddir := filepath.Join(m.cfg.Dir, "io.test.data")
	_ = os.MkdirAll(filepath.Join(ddir, "templates"), 0o700)
	_ = os.WriteFile(filepath.Join(ddir, "templates", "games.mkey.yaml"), []byte("version: 1\nname: Игры\nevents: []\n"), 0o600)
	_ = os.WriteFile(filepath.Join(ddir, ManifestFile), []byte("id: io.test.data\nplugin_api: 1\nkind: data\n"), 0o600)
	if err := m.SetActive("io.test.data", true); err != nil {
		t.Fatal(err)
	}
	e, ok = m.ext.Get(contracts.PointProjectTemplate, "games")
	if !ok || e.(contracts.ProjectTemplate).Template().Names["en"] != "Игры" {
		t.Fatalf("template: %v", ok)
	}
	info := waitState(t, m, "io.test.data", contracts.PluginRunning)
	if len(info.Templates) != 1 {
		t.Fatalf("info = %+v", info)
	}

	// Выключение убирает виды.
	_ = m.SetActive("io.test.lua", false)
	if _, ok := m.ext.Get(contracts.PointAction, "tick"); ok {
		t.Fatal("tick still registered")
	}
}
