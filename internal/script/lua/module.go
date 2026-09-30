package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	glua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/paths"
	"mkey/internal/lib/project"
)

// ModuleID — идентификатор модуля.
const ModuleID = "lua"

// Config — настройки модуля из секции modules.lua.
type Config struct {
	// ScriptsDir — каталог файлов скриптов (по умолчанию ~/.config/mkey/scripts).
	ScriptsDir string `json:"scripts_dir"`
}

// Module — модуль скриптов Lua.
type Module struct {
	cfg Config
	// Необязательные сервисы для функций mkey.is_down, mkey.notify, mkey.run.
	keyState contracts.KeyState
	notifier contracts.Notifier
	events   contracts.Events
}

// New создаёт модуль.
func New() *Module { return &Module{} }

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, получает сервисы и регистрирует действие lua.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.ScriptsDir == "" {
		m.cfg.ScriptsDir = filepath.Join(paths.Config(os.Getenv), "scripts")
	}
	m.keyState, _ = contracts.LookupService[contracts.KeyState](host.Services())
	m.notifier, _ = contracts.LookupService[contracts.Notifier](host.Services())
	m.events, _ = contracts.LookupService[contracts.Events](host.Services())
	return host.Extensions().Register(contracts.PointAction, luaAction{m})
}

// Start ничего не делает.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает: выполнения прерываются движком через контекст.
func (m *Module) Stop(context.Context) error { return nil }

// luaParams — параметры действия lua: строка с кодом или {file} / {code}.
type luaParams struct {
	Code string `json:"code"`
	File string `json:"file"`
}

// luaAction — вид действия lua.
type luaAction struct{ m *Module }

// Meta возвращает метаданные действия.
func (luaAction) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "lua", NameKey: "action.lua", DescriptionKey: "action.lua.description", Category: "script", Icon: "code",
		Provider:     ModuleID,
		ParamsSchema: []byte(`{"oneOf":[{"type":"string","x-widget":"code","x-lang":"lua"},{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}]}`),
	}
}

// source возвращает код скрипта и его имя для сообщений об ошибках.
func (a luaAction) source(v any) (string, string, error) {
	// Строка — код прямо в проекте.
	if s, ok := v.(string); ok {
		return s, "script", nil
	}

	// Карта — код или файл из каталога скриптов (путь не может выйти за его пределы).
	var p luaParams
	if err := project.Decode(v, &p); err != nil {
		return "", "", err
	}
	switch {
	case p.Code != "" && p.File == "":
		return p.Code, "script", nil
	case p.File != "" && p.Code == "":
		path := filepath.Join(a.m.cfg.ScriptsDir, filepath.Clean("/"+p.File))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", err
		}
		return string(data), p.File, nil
	}
	return "", "", errors.New(`lua: set either code or file, e.g. lua: "mkey.tap('A')"`)
}

// Validate проверяет синтаксис скрипта (файл проверяется при выполнении — его могут поправить позже).
func (a luaAction) Validate(pa project.Action) error {
	if m, ok := pa.Value.(map[string]any); ok && m["file"] != nil {
		_, _, err := a.source(pa.Value)
		return err
	}
	code, name, err := a.source(pa.Value)
	if err != nil {
		return err
	}
	_, err = parse.Parse(strings.NewReader(code), name)
	return err
}

// Run выполняет скрипт в собственном состоянии Lua с модулем mkey.
func (a luaAction) Run(ctx context.Context, rc contracts.RunContext, pa project.Action) error {
	code, name, err := a.source(pa.Value)
	if err != nil {
		return err
	}

	// Новое состояние Lua; контекст прерывает выполнение при остановке.
	ls := glua.NewState()
	defer ls.Close()
	ls.SetContext(ctx)
	api := &luaAPI{m: a.m, rc: rc, ctx: ctx}
	ls.SetGlobal("mkey", api.table(ls))

	// Выполнение; после отмены вызываются обработчики mkey.on_stop.
	err = ls.DoString(code)
	if ctx.Err() != nil {
		api.runOnStop(ls)
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("lua (%s): %w", name, err)
	}
	return nil
}

// luaAPI — функции модуля mkey для одного выполнения скрипта.
type luaAPI struct {
	m      *Module
	rc     contracts.RunContext
	ctx    context.Context
	onStop []*glua.LFunction
}

// table собирает таблицу mkey (docs/lua-api.md).
func (api *luaAPI) table(ls *glua.LState) *glua.LTable {
	t := ls.NewTable()
	ls.SetFuncs(t, map[string]glua.LGFunction{
		"send":     api.send,
		"tap":      api.keyFn(""),
		"down":     api.keyFn("^"),
		"up":       api.keyFn("~"),
		"hold":     api.hold,
		"sleep":    api.sleep,
		"type":     api.typeText,
		"move_rel": api.moveRel,
		"move":     api.unsupported("absolute_move"),
		"click":    api.click,
		"is_down":  api.isDown,
		"window":   api.nilResult,
		"cursor":   api.nilResult,
		"pixel":    api.unsupported("pixel"),
		"notify":   api.notify,
		"run":      api.run,
		"log":      api.log,
		"toggled":  func(ls *glua.LState) int { ls.Push(glua.LBool(api.rc.Toggled())); return 1 },
		"held":     func(ls *glua.LState) int { ls.Push(glua.LBool(api.rc.Held())); return 1 },
		"on_stop":  api.onStopFn,
	})

	// Сведения о событии.
	ev := ls.NewTable()
	ref := api.rc.Event()
	ev.RawSetString("project", glua.LString(ref.Project))
	ev.RawSetString("id", glua.LString(ref.Event))
	ev.RawSetString("name", glua.LString(ref.Name))
	t.RawSetString("event", ev)

	// Переменные проекта: mkey.var.x читает, mkey.var.x = 1 записывает.
	vars := ls.NewTable()
	meta := ls.NewTable()
	ls.SetFuncs(meta, map[string]glua.LGFunction{"__index": api.varGet, "__newindex": api.varSet})
	ls.SetMetatable(vars, meta)
	t.RawSetString("var", vars)
	return t
}

// do выполняет макрос DSL; ошибка превращается в ошибку Lua (скрипт останавливается).
func (api *luaAPI) do(ls *glua.LState, src string) int {
	if err := api.rc.Send(api.ctx, src); err != nil {
		ls.RaiseError("%s", err.Error())
	}
	return 0
}

// send — mkey.send("{A}[100]{B}").
func (api *luaAPI) send(ls *glua.LState) int { return api.do(ls, ls.CheckString(1)) }

// keyFn — mkey.tap / mkey.down / mkey.up: имя клавиши или сочетания.
func (api *luaAPI) keyFn(prefix string) glua.LGFunction {
	return func(ls *glua.LState) int {
		return api.do(ls, prefix+"{"+strings.Trim(ls.CheckString(1), "{}")+"}")
	}
}

// hold — mkey.hold("Space", 300).
func (api *luaAPI) hold(ls *glua.LState) int {
	return api.do(ls, fmt.Sprintf("{%s %d}", strings.Trim(ls.CheckString(1), "{}"), ls.CheckInt(2)))
}

// sleep — mkey.sleep(250): пауза, прерываемая остановкой.
func (api *luaAPI) sleep(ls *glua.LState) int {
	t := time.NewTimer(time.Duration(ls.CheckInt(1)) * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
	case <-api.ctx.Done():
		ls.RaiseError("stopped")
	}
	return 0
}

// typeText — mkey.type("Привет").
func (api *luaAPI) typeText(ls *glua.LState) int {
	return api.do(ls, dsl.Format([]dsl.Node{{Kind: dsl.KindText, Text: ls.CheckString(1)}}))
}

// moveRel — mkey.move_rel(10, -5).
func (api *luaAPI) moveRel(ls *glua.LState) int {
	return api.do(ls, fmt.Sprintf("{Move %+d %+d}", ls.CheckInt(1), ls.CheckInt(2)))
}

// click — mkey.click() или mkey.click("Right").
func (api *luaAPI) click(ls *glua.LState) int {
	if ls.GetTop() == 0 {
		return api.do(ls, "{Click}")
	}
	return api.do(ls, "{Click "+ls.CheckString(1)+"}")
}

// isDown — mkey.is_down("Mouse0"): зажата ли физическая клавиша.
func (api *luaAPI) isDown(ls *glua.LState) int {
	k, ok := keys.Lookup(strings.Trim(ls.CheckString(1), "{}"))
	if !ok {
		ls.ArgError(1, "unknown key")
	}
	ls.Push(glua.LBool(api.m.keyState != nil && api.m.keyState.IsDown(k)))
	return 1
}

// nilResult — функции, чьи данные появятся с десктоп-адаптерами (фаза 8): возвращают nil.
func (api *luaAPI) nilResult(ls *glua.LState) int {
	ls.Push(glua.LNil)
	return 1
}

// unsupported — функции, которые пока не поддерживаются: nil и текст ошибки.
func (api *luaAPI) unsupported(what string) glua.LGFunction {
	return func(ls *glua.LState) int {
		ls.Push(glua.LNil)
		ls.Push(glua.LString(what + " is not supported yet"))
		return 2
	}
}

// notify — mkey.notify("текст") или mkey.notify("текст", "заголовок").
func (api *luaAPI) notify(ls *glua.LState) int {
	body, title := ls.CheckString(1), ls.OptString(2, "mKey")
	if api.m.notifier != nil {
		if err := api.m.notifier.Notify(api.ctx, title, body); err != nil {
			api.rc.Logger().Warn("lua notify", "err", err)
		}
	} else {
		api.rc.Logger().Info("lua notify (no desktop notifications)", "title", title, "body", body)
	}
	return 0
}

// run — mkey.run("event_id") или mkey.run("event_id", "project"): запустить событие и дождаться.
func (api *luaAPI) run(ls *glua.LState) int {
	if api.m.events == nil {
		ls.RaiseError("events are unavailable")
	}
	proj := ls.OptString(2, api.rc.Event().Project)
	if err := api.m.events.RunEvent(api.ctx, proj, ls.CheckString(1)); err != nil {
		ls.RaiseError("%s", err.Error())
	}
	return 0
}

// log — mkey.log(...): запись в журнал mKey.
func (api *luaAPI) log(ls *glua.LState) int {
	parts := make([]string, 0, ls.GetTop())
	for i := 1; i <= ls.GetTop(); i++ {
		parts = append(parts, ls.ToStringMeta(ls.Get(i)).String())
	}
	api.rc.Logger().Info("lua: " + strings.Join(parts, " "))
	return 0
}

// onStopFn — mkey.on_stop(function() … end): вызвать при остановке скрипта.
func (api *luaAPI) onStopFn(ls *glua.LState) int {
	api.onStop = append(api.onStop, ls.CheckFunction(1))
	return 0
}

// runOnStop вызывает обработчики остановки без контекста отмены (у них есть время на уборку).
func (api *luaAPI) runOnStop(ls *glua.LState) {
	ls.RemoveContext()
	for _, f := range api.onStop {
		if err := ls.CallByParam(glua.P{Fn: f, NRet: 0, Protect: true}); err != nil {
			api.rc.Logger().Warn("lua on_stop", "err", err)
		}
	}
}

// varGet — чтение mkey.var.name.
func (api *luaAPI) varGet(ls *glua.LState) int {
	v, _ := api.rc.Vars().Get(ls.CheckString(2))
	ls.Push(toLua(v))
	return 1
}

// varSet — запись mkey.var.name = value.
func (api *luaAPI) varSet(ls *glua.LState) int {
	if err := api.rc.Vars().Set(ls.CheckString(2), fromLua(ls.Get(3))); err != nil {
		ls.RaiseError("%s", err.Error())
	}
	return 0
}

// toLua переводит значение переменной в значение Lua.
func toLua(v any) glua.LValue {
	switch x := v.(type) {
	case nil:
		return glua.LNil
	case bool:
		return glua.LBool(x)
	case int64:
		return glua.LNumber(x)
	case int:
		return glua.LNumber(x)
	case float64:
		return glua.LNumber(x)
	case string:
		return glua.LString(x)
	}
	return glua.LString(fmt.Sprint(v))
}

// fromLua переводит значение Lua в значение переменной.
func fromLua(v glua.LValue) any {
	switch x := v.(type) {
	case glua.LBool:
		return bool(x)
	case glua.LNumber:
		return float64(x)
	case glua.LString:
		return string(x)
	}
	return nil
}

// Проверки на этапе компиляции.
var (
	_ contracts.Module     = (*Module)(nil)
	_ contracts.ActionType = luaAction{}
)
