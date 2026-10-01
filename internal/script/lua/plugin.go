package lua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	glua "github.com/yuin/gopher-lua"

	"mkey/internal/contracts"
)

// Lua-плагины (ADR-0029 п. 8, docs/plugins.md): файл main.lua регистрирует виды —
//
//	local count = 0
//	mkey.register_action{
//	  id = "count", name = { ru = "Посчитать", en = "Count" },
//	  params = { type = "object", properties = { step = { type = "integer", default = 1 } } },
//	  run = function(params, event)
//	    count = count + (params.step or 1)
//	    mkey.log("count = " .. count)
//	  end,
//	}
//
// У плагина одно состояние Lua на всё время работы (в нём можно хранить данные, как count выше),
// поэтому его вызовы идут по очереди. Доступа к файлам и программам у плагина нет (библиотеки
// os и io не открываются, dofile/loadfile убраны); функции mkey.* проверяют разрешения манифеста.

// luaPlugin — загруженный Lua-плагин.
type luaPlugin struct {
	m     *Module
	id    string
	perms []string

	// mu — вызовы по очереди: у состояния Lua один поток.
	mu sync.Mutex
	ls *glua.LState
	// actions и conditions — виды и их функции; validators — проверки параметров.
	actions    []contracts.PluginType
	conditions []contracts.PluginType
	run        map[string]*glua.LFunction
	check      map[string]*glua.LFunction
	validators map[string]*glua.LFunction
}

// LoadPlugin выполняет main.lua плагина и собирает зарегистрированные им виды
// (contracts.LuaPluginLoader).
func (m *Module) LoadPlugin(id, path string, permissions []string) (contracts.LuaPlugin, error) {
	// Состояние без os и io: только базовые библиотеки, строки, таблицы, математика.
	ls := glua.NewState(glua.Options{SkipOpenLibs: true})
	for _, lib := range []struct {
		name string
		fn   glua.LGFunction
	}{{glua.BaseLibName, glua.OpenBase}, {glua.TabLibName, glua.OpenTable}, {glua.StringLibName, glua.OpenString}, {glua.MathLibName, glua.OpenMath}} {
		ls.Push(ls.NewFunction(lib.fn))
		ls.Push(glua.LString(lib.name))
		ls.Call(1, 0)
	}
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring"} {
		ls.SetGlobal(name, glua.LNil)
	}
	if permissions == nil {
		permissions = []string{}
	}
	p := &luaPlugin{m: m, id: id, perms: permissions, ls: ls, run: map[string]*glua.LFunction{},
		check: map[string]*glua.LFunction{}, validators: map[string]*glua.LFunction{}}

	// Регистрация видов: при загрузке у mkey есть только register_* и log.
	reg := ls.NewTable()
	ls.SetFuncs(reg, map[string]glua.LGFunction{
		"register_action":    p.register(true),
		"register_condition": p.register(false),
		"log": func(ls *glua.LState) int {
			m.log.Info("lua plugin: "+ls.ToStringMeta(ls.Get(1)).String(), "plugin", id)
			return 0
		},
	})
	ls.SetGlobal("mkey", reg)
	if err := ls.DoFile(path); err != nil {
		ls.Close()
		return nil, fmt.Errorf("%w: %s: %w", contracts.ErrBadPlugin, path, err)
	}
	return p, nil
}

// register — mkey.register_action{...} или mkey.register_condition{...}.
func (p *luaPlugin) register(action bool) glua.LGFunction {
	return func(ls *glua.LState) int {
		// Описание вида.
		t := ls.CheckTable(1)
		id := glua.LVAsString(t.RawGetString("id"))
		fn, ok := t.RawGetString(map[bool]string{true: "run", false: "check"}[action]).(*glua.LFunction)
		if id == "" || !ok {
			ls.ArgError(1, "id and run (or check for a condition) are required")
		}
		schema, err := json.Marshal(goValue(t.RawGetString("params")))
		if err != nil || string(schema) == "null" {
			schema = nil
		}
		pt := contracts.PluginType{ID: id, Names: texts(t.RawGetString("name")), Descriptions: texts(t.RawGetString("description")),
			Category: glua.LVAsString(t.RawGetString("category")), ParamsSchema: schema}

		// Функции вида.
		if v, ok := t.RawGetString("validate").(*glua.LFunction); ok {
			p.validators[id] = v
		}
		if action {
			p.actions = append(p.actions, pt)
			p.run[id] = fn
		} else {
			p.conditions = append(p.conditions, pt)
			p.check[id] = fn
		}
		return 0
	}
}

// texts — название на языках: таблица {ru=…, en=…} или строка (одна на все языки).
func texts(v glua.LValue) map[string]string {
	switch x := v.(type) {
	case glua.LString:
		return map[string]string{"en": string(x)}
	case *glua.LTable:
		out := map[string]string{}
		x.ForEach(func(k, v glua.LValue) { out[k.String()] = v.String() })
		return out
	}
	return nil
}

// Actions возвращает действия плагина.
func (p *luaPlugin) Actions() []contracts.PluginType { return p.actions }

// Conditions возвращает условия плагина.
func (p *luaPlugin) Conditions() []contracts.PluginType { return p.conditions }

// Validate вызывает validate(params) вида: строка или ошибка Lua — текст ошибки для человека.
func (p *luaPlugin) Validate(typ string, params any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn := p.validators[typ]
	if fn == nil {
		return nil
	}
	p.ls.SetContext(context.Background())
	if err := p.ls.CallByParam(glua.P{Fn: fn, NRet: 1, Protect: true}, luaValue(p.ls, params)); err != nil {
		return err
	}
	ret := p.ls.Get(-1)
	p.ls.Pop(1)
	if s, ok := ret.(glua.LString); ok && s != "" {
		return errors.New(string(s))
	}
	return nil
}

// call вызывает функцию вида с params и event при mkey, привязанном к выполнению события.
func (p *luaPlugin) call(ctx context.Context, rc contracts.RunContext, fn *glua.LFunction, params any) (glua.LValue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// mkey этого выполнения (с проверкой разрешений) и контекст отмены.
	api := &luaAPI{m: p.m, rc: rc, ctx: ctx, perms: p.perms}
	p.ls.SetGlobal("mkey", api.table(p.ls))
	p.ls.SetContext(ctx)
	defer p.ls.RemoveContext()

	// Вызов; при отмене — обработчики mkey.on_stop.
	ev := p.ls.NewTable()
	ref := rc.Event()
	ev.RawSetString("project", glua.LString(ref.Project))
	ev.RawSetString("id", glua.LString(ref.Event))
	err := p.ls.CallByParam(glua.P{Fn: fn, NRet: 1, Protect: true}, luaValue(p.ls, params), ev)
	if ctx.Err() != nil {
		api.runOnStop(p.ls)
		return glua.LNil, ctx.Err()
	}
	if err != nil {
		return glua.LNil, fmt.Errorf("lua plugin %s: %w", p.id, err)
	}
	ret := p.ls.Get(-1)
	p.ls.Pop(1)
	return ret, nil
}

// RunAction выполняет действие typ.
func (p *luaPlugin) RunAction(ctx context.Context, rc contracts.RunContext, typ string, params any) error {
	fn := p.run[typ]
	if fn == nil {
		return fmt.Errorf("lua plugin %s: unknown action %q", p.id, typ)
	}
	_, err := p.call(ctx, rc, fn, params)
	return err
}

// CheckCondition вычисляет условие typ: истина — всё, кроме nil и false.
func (p *luaPlugin) CheckCondition(ctx context.Context, rc contracts.RunContext, typ string, params any) (bool, error) {
	fn := p.check[typ]
	if fn == nil {
		return false, fmt.Errorf("lua plugin %s: unknown condition %q", p.id, typ)
	}
	ret, err := p.call(ctx, rc, fn, params)
	return glua.LVAsBool(ret), err
}

// Close освобождает состояние Lua.
func (p *luaPlugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ls.Close()
}

// luaValue переводит значение Go (параметры из проекта: карты, списки, строки, числа) в Lua.
func luaValue(ls *glua.LState, v any) glua.LValue {
	switch x := v.(type) {
	case map[string]any:
		t := ls.NewTable()
		for k, e := range x {
			t.RawSetString(k, luaValue(ls, e))
		}
		return t
	case []any:
		t := ls.NewTable()
		for _, e := range x {
			t.Append(luaValue(ls, e))
		}
		return t
	}
	return toLua(v)
}

// goValue переводит значение Lua (таблицу схемы параметров) в Go: таблица с ключами 1…n — список.
func goValue(v glua.LValue) any {
	t, ok := v.(*glua.LTable)
	if !ok {
		return fromLua(v)
	}
	if n := t.Len(); n > 0 {
		list := make([]any, 0, n)
		for i := 1; i <= n; i++ {
			list = append(list, goValue(t.RawGetInt(i)))
		}
		return list
	}
	m := map[string]any{}
	t.ForEach(func(k, v glua.LValue) { m[k.String()] = goValue(v) })
	return m
}

// Проверка на этапе компиляции.
var (
	_ contracts.LuaPluginLoader = (*Module)(nil)
	_ contracts.LuaPlugin       = (*luaPlugin)(nil)
)
