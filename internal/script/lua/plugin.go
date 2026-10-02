package lua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	glua "github.com/yuin/gopher-lua"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
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
// Триггер — функция poll, которую mKey вызывает раз в interval_ms (по умолчанию 1000, не меньше
// 50), пока событие включено; вернула истину или таблицу (значения для действий) — событие
// срабатывает:
//
//	mkey.register_trigger{
//	  id = "counter_full", interval_ms = 500,
//	  poll = function(params, state, event)      -- state — таблица этого события между вызовами
//	    if count >= (params.value or 10) and not state.fired then
//	      state.fired = true
//	      return { count = count }
//	    end
//	    if count < (params.value or 10) then state.fired = false end
//	  end,
//	}
//
// В poll можно смотреть (mkey.var, mkey.is_down, mkey.log), но не нажимать и не ждать: такие
// функции дают ошибку. Один вызов poll — не дольше pollTimeout.
//
// У плагина одно состояние Lua на всё время работы (в нём можно хранить данные, как count выше),
// поэтому его вызовы идут по очереди. Доступа к файлам и программам у плагина нет (библиотеки
// os и io не открываются, dofile/loadfile убраны); функции mkey.* проверяют разрешения манифеста.

// Опрос триггеров: интервал по умолчанию и наименьший (чаще — плагин нагружал бы процессор),
// предел одного вызова poll и как часто повторять в журнале одну и ту же ошибку опроса.
const (
	defaultPollInterval = time.Second
	minPollInterval     = 50 * time.Millisecond
	pollTimeout         = time.Second
	pollErrorEvery      = time.Minute
)

// luaPlugin — загруженный Lua-плагин.
type luaPlugin struct {
	m     *Module
	id    string
	perms []string

	// mu — вызовы по очереди: у состояния Lua один поток.
	mu sync.Mutex
	ls *glua.LState
	// closed — состояние Lua освобождено (опросы триггеров заканчиваются).
	closed bool
	// actions, conditions и triggers — виды и их функции; validators — проверки параметров;
	// interval — интервал опроса каждого триггера.
	actions    []contracts.PluginType
	conditions []contracts.PluginType
	triggers   []contracts.PluginType
	run        map[string]*glua.LFunction
	check      map[string]*glua.LFunction
	poll       map[string]*glua.LFunction
	interval   map[string]time.Duration
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
		check: map[string]*glua.LFunction{}, poll: map[string]*glua.LFunction{},
		interval: map[string]time.Duration{}, validators: map[string]*glua.LFunction{}}

	// Регистрация видов: при загрузке у mkey есть только register_* и log.
	reg := ls.NewTable()
	ls.SetFuncs(reg, map[string]glua.LGFunction{
		"register_action":    p.register(kindAction),
		"register_condition": p.register(kindCondition),
		"register_trigger":   p.register(kindTrigger),
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

// typeKind — что регистрирует mkey.register_*: действие, условие или триггер.
type typeKind int

// Виды регистрации и имя главной функции каждого.
const (
	kindAction typeKind = iota
	kindCondition
	kindTrigger
)

// mainFunc — имя обязательной функции вида: run, check или poll.
var mainFunc = map[typeKind]string{kindAction: "run", kindCondition: "check", kindTrigger: "poll"}

// register — mkey.register_action{...}, mkey.register_condition{...} или mkey.register_trigger{...}.
func (p *luaPlugin) register(kind typeKind) glua.LGFunction {
	return func(ls *glua.LState) int {
		// Описание вида.
		t := ls.CheckTable(1)
		id := glua.LVAsString(t.RawGetString("id"))
		fn, ok := t.RawGetString(mainFunc[kind]).(*glua.LFunction)
		if id == "" || !ok {
			ls.ArgError(1, "id and "+mainFunc[kind]+" are required")
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
		switch kind {
		case kindAction:
			p.actions = append(p.actions, pt)
			p.run[id] = fn
		case kindCondition:
			p.conditions = append(p.conditions, pt)
			p.check[id] = fn
		case kindTrigger:
			// Интервал опроса: заданный (не меньше наименьшего) или по умолчанию.
			every := defaultPollInterval
			if ms, ok := t.RawGetString("interval_ms").(glua.LNumber); ok && ms > 0 {
				every = max(time.Duration(ms)*time.Millisecond, minPollInterval)
			}
			p.triggers = append(p.triggers, pt)
			p.poll[id], p.interval[id] = fn, every
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

// Triggers возвращает триггеры плагина.
func (p *luaPlugin) Triggers() []contracts.PluginType { return p.triggers }

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

// ArmTrigger начинает опрашивать триггер typ события ev (contracts.LuaPlugin): до снятия или
// отмены ctx раз в интервал вызывает poll; истина или таблица — срабатывание fire.
func (p *luaPlugin) ArmTrigger(ctx context.Context, ev contracts.EventRef, typ string, params any, fire func(map[string]any)) (func(), error) {
	fn := p.poll[typ]
	if fn == nil {
		return nil, fmt.Errorf("lua plugin %s: unknown trigger %q", p.id, typ)
	}

	// Своя таблица state у каждого взведения: плагин хранит в ней, что нужно между вызовами.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("lua plugin %s: closed", p.id)
	}
	state := p.ls.NewTable()
	p.mu.Unlock()

	// Опрос в отдельной горутине до снятия; ошибки — в журнал, одна и та же не чаще раза в минуту.
	ctx, cancel := context.WithCancel(ctx)
	every := p.interval[typ]
	go func() {
		var lastErr string
		var lastLogged time.Time
		for {
			if err := p.m.clk.Sleep(ctx, every); err != nil {
				return
			}
			fired, vars, err := p.pollOnce(ctx, ev, fn, params, state)
			switch {
			case errors.Is(err, errPluginClosed) || ctx.Err() != nil:
				return
			case err != nil:
				if now := p.m.clk.Now(); err.Error() != lastErr || now.Sub(lastLogged) >= pollErrorEvery {
					p.m.log.Warn("lua plugin trigger failed", "plugin", p.id, "trigger", typ, "event", ev.Event, "err", err)
					lastErr, lastLogged = err.Error(), now
				}
			case fired:
				fire(vars)
			}
		}
	}()
	return cancel, nil
}

// errPluginClosed — состояние Lua уже освобождено: опрос заканчивается.
var errPluginClosed = errors.New("lua plugin closed")

// pollOnce вызывает poll(params, state, event) один раз (не дольше pollTimeout) с mkey только для
// чтения. Возвращает, сработал ли триггер, и значения для действий (если poll вернул таблицу).
func (p *luaPlugin) pollOnce(ctx context.Context, ev contracts.EventRef, fn *glua.LFunction, params any, state *glua.LTable) (bool, map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, nil, errPluginClosed
	}

	// mkey этого опроса: только смотреть (нажатия и ожидание недоступны); предел времени вызова.
	pctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	api := &luaAPI{m: p.m, rc: &pollContext{m: p.m, ref: ev, plugin: p.id}, ctx: pctx, perms: p.perms, trigger: true}
	p.ls.SetGlobal("mkey", api.table(p.ls))
	p.ls.SetContext(pctx)
	defer p.ls.RemoveContext()

	// Вызов и разбор ответа: таблица — срабатывание со значениями, истина — без них.
	evt := p.ls.NewTable()
	evt.RawSetString("project", glua.LString(ev.Project))
	evt.RawSetString("id", glua.LString(ev.Event))
	if err := p.ls.CallByParam(glua.P{Fn: fn, NRet: 1, Protect: true}, luaValue(p.ls, params), state, evt); err != nil {
		return false, nil, err
	}
	ret := p.ls.Get(-1)
	p.ls.Pop(1)
	if t, ok := ret.(*glua.LTable); ok {
		vars, _ := goValue(t).(map[string]any)
		return true, vars, nil
	}
	return glua.LVAsBool(ret), nil, nil
}

// pollContext — контекст опроса триггера для функций mkey.*: событие и его переменные, без
// выполнения действий (нажимать и запускать из опроса нельзя).
type pollContext struct {
	m      *Module
	ref    contracts.EventRef
	plugin string
}

// errInTrigger — из опроса триггера действия не выполняются.
var errInTrigger = errors.New("not available in a trigger: do it in the event's actions")

// Event возвращает событие, к которому относится триггер.
func (c *pollContext) Event() contracts.EventRef { return c.ref }

// Send недоступен в триггере.
func (c *pollContext) Send(context.Context, string) error { return errInTrigger }

// RunActions недоступен в триггере.
func (c *pollContext) RunActions(context.Context, []project.Action) error { return errInTrigger }

// Check недоступен в триггере.
func (c *pollContext) Check(context.Context, []project.Condition) (bool, error) {
	return false, errInTrigger
}

// Toggled — у опроса нет переключателя.
func (c *pollContext) Toggled() bool { return false }

// Held — у опроса нет зажатой клавиши.
func (c *pollContext) Held() bool { return false }

// Fire — срабатывания ещё нет.
func (c *pollContext) Fire() contracts.Fire { return contracts.Fire{} }

// Vars возвращает переменные проекта события (нет движка или проекта — пустые, только чтение).
func (c *pollContext) Vars() contracts.VarStore {
	if c.m.events != nil {
		if vs := c.m.events.Vars(c.ref.Project); vs != nil {
			return vs
		}
	}
	return noVars{}
}

// Logger возвращает журнал с именем плагина и событием.
func (c *pollContext) Logger() *slog.Logger {
	return c.m.log.With("plugin", c.plugin, "event", c.ref.Event)
}

// noVars — переменные, когда проекта нет: пусто, изменить нельзя.
type noVars struct{}

// Get — переменных нет.
func (noVars) Get(string) (any, bool) { return nil, false }

// Set — изменить нельзя.
func (noVars) Set(string, any) error { return errors.New("project variables are unavailable") }

// Add — изменить нельзя.
func (noVars) Add(string, float64) error { return errors.New("project variables are unavailable") }

// All — переменных нет.
func (noVars) All() map[string]any { return map[string]any{} }

// Close освобождает состояние Lua; опросы триггеров заканчиваются при следующем вызове.
func (p *luaPlugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
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
