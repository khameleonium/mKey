package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/jsonrpc"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Представители видов плагина в точках расширения: для движка и конструктора это обычные
// действия, условия и триггеры, а работу они передают процессу плагина по протоколу.

// proxy — общее у представителей: плагин и метаданные вида.
type proxy struct {
	p    *plugin
	meta contracts.ExtensionMeta
}

// Meta возвращает метаданные вида.
func (x proxy) Meta() contracts.ExtensionMeta { return x.meta }

// validate спрашивает плагин, верны ли параметры (method — action.validate…). Плагин без такого
// метода — параметры не проверяются (их уже проверяет схема в окне).
func (x proxy) validate(method string, params any) error {
	err := x.p.call(context.Background(), shortTimeout, method, map[string]any{"type": x.meta.ID, "params": params}, nil)
	var e *jsonrpc.Error
	if errors.As(err, &e) && e.Code == jsonrpc.CodeMethodNotFound {
		return nil
	}
	return err
}

// actionProxy — действие плагина.
type actionProxy struct{ proxy }

// Validate проверяет параметры действия у плагина.
func (x actionProxy) Validate(a project.Action) error { return x.validate("action.validate", a.Value) }

// Run выполняет действие в плагине; отмена события отменяет и запрос ($/cancelRequest).
func (x actionProxy) Run(ctx context.Context, rc contracts.RunContext, a project.Action) error {
	return x.p.call(ctx, 0, "action.run", map[string]any{
		"type": x.meta.ID, "params": a.Value, "event": rc.Event(), "vars": rc.Fire().Vars,
	}, nil)
}

// conditionProxy — условие плагина.
type conditionProxy struct{ proxy }

// Validate проверяет параметры условия у плагина.
func (x conditionProxy) Validate(c project.Condition) error {
	return x.validate("condition.validate", c.Params)
}

// Check спрашивает плагин, выполняется ли условие (не дольше 2 с).
func (x conditionProxy) Check(ctx context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
	var res struct {
		Result bool `json:"result"`
	}
	err := x.p.call(ctx, shortTimeout, "condition.check", map[string]any{
		"type": x.meta.ID, "params": c.Params, "event": rc.Event(), "vars": rc.Fire().Vars,
	}, &res)
	return res.Result, err
}

// triggerProxy — триггер плагина.
type triggerProxy struct{ proxy }

// Validate проверяет параметры триггера у плагина.
func (x triggerProxy) Validate(t project.Trigger) error {
	return x.validate("trigger.validate", t.Params)
}

// Arm взводит триггер: запоминает его (после перезапуска плагина он взводится снова) и, если
// плагин работает, просит его следить. Срабатывания приходят уведомлением trigger.fire.
func (x triggerProxy) Arm(_ context.Context, ev contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	// Номер взвода и запись о нём.
	p := x.p
	p.mu.Lock()
	p.nextHandle++
	handle := "t" + strconv.Itoa(p.nextHandle)
	p.triggers[handle] = &armed{typ: x.meta.ID, params: t.Params, event: ev, fire: fire}
	p.mu.Unlock()

	// Плагин работает — взводим сейчас; ошибка (неверные параметры) — триггер не взведён.
	params := map[string]any{"type": x.meta.ID, "params": t.Params, "handle": handle, "event": ev}
	if _, err := p.running(); err == nil {
		if err := p.call(context.Background(), shortTimeout, "trigger.arm", params, nil); err != nil {
			p.mu.Lock()
			delete(p.triggers, handle)
			p.mu.Unlock()
			return nil, err
		}
	}

	// Снятие: забыть и сказать плагину (в фоне — снятие не должно ждать плагин).
	disarm := func() {
		p.mu.Lock()
		delete(p.triggers, handle)
		p.mu.Unlock()
		go func() {
			_ = p.call(context.Background(), shortTimeout, "trigger.disarm", map[string]any{"handle": handle}, nil)
		}()
	}
	return disarm, nil
}

// handle обрабатывает запросы и уведомления плагина к mKey (docs/plugins.md, «Методы плагин →
// mKey»). Методы, требующие разрешений, без объявленного разрешения отвечают -32001.
func (p *plugin) handle(ctx context.Context, method string, params json.RawMessage, _ bool) (any, error) {
	h := p.h
	switch method {
	case "trigger.fire":
		// Срабатывание взведённого триггера: движок запустит событие.
		var a struct {
			Handle string         `json:"handle"`
			Vars   map[string]any `json:"vars"`
		}
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "trigger.fire: %v", err)
		}
		p.mu.Lock()
		t := p.triggers[a.Handle]
		p.mu.Unlock()
		if t == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown trigger handle %q", a.Handle)
		}
		t.fire(contracts.Fire{Vars: a.Vars})
		return nil, nil

	case "mkey.log":
		// Строка журнала плагина — в журнал mKey.
		var a struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(params, &a)
		switch a.Level {
		case "error":
			h.log.Error(a.Message, "plugin", p.man.ID)
		case "warn":
			h.log.Warn(a.Message, "plugin", p.man.ID)
		default:
			h.log.Info(a.Message, "plugin", p.man.ID)
		}
		return nil, nil

	case "mkey.send":
		// Нажать клавиши макросом.
		var a struct {
			Macro string `json:"macro"`
		}
		if err := p.need("output.send", params, &a); err != nil {
			return nil, err
		}
		if h.runner == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeFailed, "output is unavailable")
		}
		return nil, h.runner.Run(ctx, a.Macro)

	case "mkey.vars.get", "mkey.vars.set":
		// Переменные проекта.
		perm := "vars.read"
		if method == "mkey.vars.set" {
			perm = "vars.write"
		}
		var a struct {
			Project string `json:"project"`
			Name    string `json:"name"`
			Value   any    `json:"value"`
		}
		if err := p.need(perm, params, &a); err != nil {
			return nil, err
		}
		var vars contracts.VarStore
		if h.events != nil {
			vars = h.events.Vars(a.Project)
		}
		if vars == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeFailed, "project %q not found", a.Project)
		}
		if method == "mkey.vars.set" {
			return nil, vars.Set(a.Name, a.Value)
		}
		v, ok := vars.Get(a.Name)
		if !ok {
			return nil, jsonrpc.Errorf(jsonrpc.CodeFailed, "variable %q not found", a.Name)
		}
		return map[string]any{"value": v}, nil

	case "mkey.notify":
		// Уведомление рабочего стола.
		var a struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := p.need("notify", params, &a); err != nil {
			return nil, err
		}
		if h.notifier == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeFailed, "notifications are unavailable")
		}
		return nil, h.notifier.Notify(ctx, a.Title, a.Body)
	}
	return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "method %q not found", method)
}

// need проверяет разрешение perm и разбирает параметры в v.
func (p *plugin) need(perm string, params json.RawMessage, v any) error {
	if !p.man.allows(perm) {
		return jsonrpc.Errorf(jsonrpc.CodeForbidden, "permission %q is not declared in %s", perm, ManifestFile)
	}
	if err := json.Unmarshal(params, v); err != nil {
		return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%v", err)
	}
	return nil
}

// Проверки на этапе компиляции: представители реализуют контракты видов.
var (
	_ contracts.ActionType    = actionProxy{}
	_ contracts.ConditionType = conditionProxy{}
	_ contracts.TriggerType   = triggerProxy{}
)
