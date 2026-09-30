package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	"mkey/internal/lib/project"
)

// minIterationSpacing — наименьшая длительность итерации repeat: цикл без пауз не должен
// загружать процессор полностью (SEC-4).
const minIterationSpacing = time.Millisecond

// builtinActions возвращает встроенные действия движка (FR-EV-4).
func (m *Module) builtinActions() []contracts.ActionType {
	return []contracts.ActionType{
		// Действия ввода — все сводятся к макросу DSL.
		dslAction("send", "keyboard", `{"type":"string","x-widget":"macro"}`, sendDSL),
		dslAction("tap", "keyboard", `{"type":"string","x-widget":"key"}`, keyDSL("")),
		dslAction("key_down", "keyboard", `{"type":"string","x-widget":"key"}`, keyDSL("^")),
		dslAction("key_up", "keyboard", `{"type":"string","x-widget":"key"}`, keyDSL("~")),
		dslAction("hold", "keyboard", `{"type":"object","required":["key","ms"],"properties":{"key":{"type":"string","x-widget":"key"},"ms":{"type":"integer","minimum":1,"default":500,"x-widget":"ms"}}}`, holdDSL),
		dslAction("pause", "time", `{"oneOf":[{"type":"integer","minimum":0,"default":100,"x-widget":"ms"},{"type":"object","required":["min_ms","max_ms"],"properties":{"min_ms":{"type":"integer","minimum":0,"x-widget":"ms"},"max_ms":{"type":"integer","minimum":0,"x-widget":"ms"}}}]}`, pauseDSL),
		dslAction("type_text", "keyboard", `{"type":"string","x-widget":"multiline"}`, textDSL),
		dslAction("mouse_move", "mouse", `{"type":"object","required":["dx","dy"],"properties":{"dx":{"type":"integer","default":0},"dy":{"type":"integer","default":0}}}`, moveDSL),
		dslAction("mouse_click", "mouse", `{"enum":["Left","Right","Middle","Back","Forward"],"default":"Left"}`, clickDSL),
		dslAction("wheel", "mouse", `{"type":"object","required":["direction"],"properties":{"direction":{"enum":["Up","Down","Left","Right"],"default":"Down"},"count":{"type":"integer","minimum":1,"default":1}}}`, wheelDSL),

		// Логика и переменные.
		m.repeatAction(),
		m.ifAction(),
		m.setVarAction(),

		// Управление событиями и уведомления.
		m.runEventAction(),
		m.notifyAction(),
		m.enableAction("enable", true),
		m.enableAction("disable", false),
		m.stopAction(),
	}
}

// dslAction создаёт действие, которое сводится к макросу DSL: проверка — разбор и компиляция макроса.
func dslAction(id, category, schema string, toDSL func(any) (string, error)) builtinAction {
	return builtinAction{
		meta:  meta("action", id, category, schema),
		toDSL: toDSL,
		validate: func(a project.Action) error {
			src, err := toDSL(a.Value)
			if fe := (*project.FieldError)(nil); errors.As(err, &fe) && fe.Kind == "" {
				// Незаполненное поле: подставляем вид блока, чтобы окно назвало поле.
				fe.Kind = id
			}
			if err != nil {
				return err
			}
			if _, err = compileSource(src); err != nil || id != "send" {
				return err
			}
			// Макрос целиком (блок «Макрос»): зажатия и отпускания должны сходиться (^ дважды, ~ без ^ — ошибки).
			nodes, err := dsl.Parse(src)
			if err != nil {
				return err
			}
			return dsl.CheckHolds(nodes)
		},
		run: func(ctx context.Context, rc contracts.RunContext, a project.Action) error {
			src, err := toDSL(a.Value)
			if err != nil {
				return err
			}
			return rc.Send(ctx, src)
		},
	}
}

// sendDSL — действие send: значение и есть макрос.
func sendDSL(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", errors.New(`send: expected a macro string, e.g. send: "{Enter}"`)
	}
	return s, nil
}

// keyDSL — действия tap/key_down/key_up: имя одной клавиши ("Shift", "{F8}"); сочетание в одних
// скобках ("Ctrl+C") разбор макроса отвергнет с подсказкой, как записать его зажатием.
func keyDSL(prefix string) func(any) (string, error) {
	return func(v any) (string, error) {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", project.Required("action", "", "")
		}
		return prefix + braces(s), nil
	}
}

// holdDSL — действие hold: {key, ms} → "{key ms}".
func holdDSL(v any) (string, error) {
	var p struct {
		Key string `json:"key"`
		MS  int    `json:"ms"`
	}
	if err := project.Decode(v, &p); err != nil {
		return "", err
	}
	if strings.Trim(p.Key, "{} ") == "" {
		return "", project.Required("action", "hold", "key")
	}
	return "{" + strings.Trim(p.Key, "{}") + " " + strconv.Itoa(p.MS) + "}", nil
}

// pauseDSL — действие pause: число миллисекунд или {min_ms, max_ms}.
func pauseDSL(v any) (string, error) {
	if n, err := toFloat(v); err == nil && v != nil {
		return "[" + strconv.Itoa(int(n)) + "]", nil
	}
	var p struct {
		MinMS int `json:"min_ms"`
		MaxMS int `json:"max_ms"`
	}
	if err := project.Decode(v, &p); err != nil {
		return "", errors.New("pause: expected milliseconds or {min_ms, max_ms}")
	}
	return fmt.Sprintf("[%d..%d]", p.MinMS, p.MaxMS), nil
}

// textDSL — действие type_text: текст как есть.
func textDSL(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", errors.New("type_text: expected text")
	}
	return dsl.Format([]dsl.Node{{Kind: dsl.KindText, Text: s}}), nil
}

// moveDSL — действие mouse_move: {dx, dy} → относительное перемещение.
func moveDSL(v any) (string, error) {
	var p struct {
		DX int `json:"dx"`
		DY int `json:"dy"`
	}
	if err := project.Decode(v, &p); err != nil {
		return "", err
	}
	return fmt.Sprintf("{Move %+d %+d}", p.DX, p.DY), nil
}

// clickDSL — действие mouse_click: кнопка (по умолчанию левая).
func clickDSL(v any) (string, error) {
	switch b := v.(type) {
	case nil:
		return "{Click}", nil
	case string:
		return "{Click " + b + "}", nil
	}
	return "", errors.New("mouse_click: expected a button name")
}

// wheelDSL — действие wheel: {direction, count}.
func wheelDSL(v any) (string, error) {
	var p struct {
		Direction string `json:"direction"`
		Count     int    `json:"count"`
	}
	if err := project.Decode(v, &p); err != nil {
		return "", err
	}
	if p.Count <= 0 {
		p.Count = 1
	}
	return fmt.Sprintf("{Wheel %s %d}", p.Direction, p.Count), nil
}

// braces заключает имя клавиши в фигурные скобки, если их нет.
func braces(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return s
	}
	return "{" + s + "}"
}

// repeatParams — параметры действия repeat.
type repeatParams struct {
	// Times — сколько раз повторить.
	Times int `json:"times"`
	// While — пока: toggled (переключатель включён), held (клавиша триггера зажата), forever.
	While string `json:"while"`
	// Conditions — повторять, пока условия выполняются.
	Conditions any `json:"conditions"`
	// Do — повторяемые действия.
	Do any `json:"do"`
}

// repeatAction — действие repeat.
func (m *Module) repeatAction() builtinAction {
	// parse разбирает параметры и вложенные действия и условия.
	parse := func(a project.Action) (repeatParams, []project.Action, []project.Condition, error) {
		var p repeatParams
		if err := project.Decode(a.Value, &p); err != nil {
			return p, nil, nil, err
		}
		body, err := project.DecodeActions(p.Do)
		if err != nil {
			return p, nil, nil, fmt.Errorf("do: %w", err)
		}
		var conds []project.Condition
		if p.Conditions != nil {
			if conds, err = project.DecodeConditions(p.Conditions); err != nil {
				return p, nil, nil, fmt.Errorf("conditions: %w", err)
			}
		}

		// Ровно один способ задать число повторов.
		modes := 0
		for _, set := range []bool{p.Times > 0, p.While != "", conds != nil} {
			if set {
				modes++
			}
		}
		if modes != 1 {
			return p, nil, nil, errors.New("set exactly one of times, while, conditions")
		}
		switch p.While {
		case "", "toggled", "held", "forever":
		default:
			return p, nil, nil, fmt.Errorf("unknown while %q (use toggled, held or forever)", p.While)
		}
		return p, body, conds, nil
	}

	return builtinAction{
		meta: meta("action", "repeat", "logic", `{"oneOf":[{"type":"object","required":["times","do"],"properties":{"times":{"type":"integer","minimum":1,"default":3},"do":{"type":"array","x-widget":"actions"}}},{"type":"object","required":["while","do"],"properties":{"while":{"enum":["toggled","held","forever"],"default":"toggled"},"do":{"type":"array","x-widget":"actions"}}},{"type":"object","required":["conditions","do"],"properties":{"conditions":{"type":"array","x-widget":"conditions"},"do":{"type":"array","x-widget":"actions"}}}]}`),
		validate: func(a project.Action) error {
			_, body, conds, err := parse(a)
			if err != nil {
				return err
			}
			if err := m.validateConditions(conds); err != nil {
				return err
			}
			return m.validateActions(body)
		},
		run: func(ctx context.Context, rc contracts.RunContext, a project.Action) error {
			p, body, conds, err := parse(a)
			if err != nil {
				return err
			}
			for i := 0; ; i++ {
				// Продолжать ли: по числу повторов, состоянию триггера или условиям.
				switch {
				case p.Times > 0 && i >= p.Times:
					return nil
				case p.While == "toggled" && !rc.Toggled():
					return nil
				case p.While == "held" && !rc.Held():
					return nil
				case conds != nil:
					ok, err := rc.Check(ctx, conds)
					if err != nil || !ok {
						return err
					}
				}

				// Итерация; слишком быстрая итерация чуть притормаживается (SEC-4).
				start := m.clk.Now()
				if err := rc.RunActions(ctx, body); err != nil {
					return err
				}
				if spent := m.clk.Now().Sub(start); spent < minIterationSpacing {
					if err := m.clk.Sleep(ctx, minIterationSpacing-spent); err != nil {
						return err
					}
				}
			}
		},
	}
}

// ifAction — действие if: {conditions, then, else}.
func (m *Module) ifAction() builtinAction {
	parse := func(a project.Action) (conds []project.Condition, then, els []project.Action, err error) {
		var p struct {
			Conditions any `json:"conditions"`
			Then       any `json:"then"`
			Else       any `json:"else"`
		}
		if err = project.Decode(a.Value, &p); err != nil {
			return
		}
		if conds, err = project.DecodeConditions(p.Conditions); err != nil {
			return nil, nil, nil, fmt.Errorf("conditions: %w", err)
		}
		if then, err = project.DecodeActions(p.Then); err != nil {
			return nil, nil, nil, fmt.Errorf("then: %w", err)
		}
		if p.Else != nil {
			if els, err = project.DecodeActions(p.Else); err != nil {
				return nil, nil, nil, fmt.Errorf("else: %w", err)
			}
		}
		return
	}
	return builtinAction{
		meta: meta("action", "if", "logic", `{"type":"object","required":["conditions","then"],"properties":{"conditions":{"type":"array","x-widget":"conditions"},"then":{"type":"array","x-widget":"actions"},"else":{"type":"array","x-widget":"actions"}}}`),
		validate: func(a project.Action) error {
			conds, then, els, err := parse(a)
			if err != nil {
				return err
			}
			if err := m.validateConditions(conds); err != nil {
				return err
			}
			if err := m.validateActions(then); err != nil {
				return err
			}
			return m.validateActions(els)
		},
		run: func(ctx context.Context, rc contracts.RunContext, a project.Action) error {
			conds, then, els, err := parse(a)
			if err != nil {
				return err
			}
			ok, err := rc.Check(ctx, conds)
			if err != nil {
				return err
			}
			if ok {
				return rc.RunActions(ctx, then)
			}
			return rc.RunActions(ctx, els)
		},
	}
}

// setVarParams — параметры действия set_var.
type setVarParams struct {
	Name  string   `json:"name"`
	Value any      `json:"value"`
	Add   *float64 `json:"add"`
}

// setVarAction — действие set_var: {name, value} или {name, add}.
func (m *Module) setVarAction() builtinAction {
	parse := func(a project.Action) (setVarParams, error) {
		var p setVarParams
		if err := project.Decode(a.Value, &p); err != nil {
			return p, err
		}
		if p.Name == "" {
			return p, project.Required("action", "set_var", "name")
		}
		return p, nil
	}
	return builtinAction{
		meta:     meta("action", "set_var", "logic", `{"oneOf":[{"type":"object","required":["name","value"],"properties":{"name":{"type":"string","x-widget":"variable"},"value":{"x-widget":"value"}}},{"type":"object","required":["name","add"],"properties":{"name":{"type":"string","x-widget":"variable"},"add":{"type":"number","default":1}}}]}`),
		validate: func(a project.Action) error { _, err := parse(a); return err },
		run: func(_ context.Context, rc contracts.RunContext, a project.Action) error {
			p, err := parse(a)
			if err != nil {
				return err
			}
			if p.Add != nil {
				return rc.Vars().Add(p.Name, *p.Add)
			}
			return rc.Vars().Set(p.Name, p.Value)
		},
	}
}

// eventTarget — ссылка на событие или проект в параметрах действий.
type eventTarget struct {
	Project string `json:"project"`
	Event   string `json:"event"`
	Wait    *bool  `json:"wait"`
}

// parseTarget разбирает ссылку: строка — событие того же проекта, иначе {project, event, wait}.
func parseTarget(v any) (eventTarget, error) {
	if s, ok := v.(string); ok {
		return eventTarget{Event: s}, nil
	}
	var t eventTarget
	err := project.Decode(v, &t)
	return t, err
}

// runEventAction — действие run_event: запустить другое событие (по умолчанию — дождаться).
func (m *Module) runEventAction() builtinAction {
	return builtinAction{
		meta: meta("action", "run_event", "system", `{"oneOf":[{"type":"string","x-widget":"event"},{"type":"object","required":["project","event"],"properties":{"project":{"type":"string","x-widget":"project"},"event":{"type":"string"},"wait":{"type":"boolean"}}}]}`),
		validate: func(a project.Action) error {
			t, err := parseTarget(a.Value)
			if err == nil && t.Event == "" {
				err = project.Required("action", "run_event", "event")
			}
			return err
		},
		run: func(ctx context.Context, rc contracts.RunContext, a project.Action) error {
			t, err := parseTarget(a.Value)
			if err != nil {
				return err
			}
			if t.Project == "" {
				t.Project = rc.Event().Project
			}
			if t.Wait != nil && !*t.Wait {
				go func() {
					if err := m.RunEvent(context.WithoutCancel(ctx), t.Project, t.Event); err != nil {
						rc.Logger().Warn("run_event", "err", err)
					}
				}()
				return nil
			}
			return m.RunEvent(ctx, t.Project, t.Event)
		},
	}
}

// notifyAction — действие notify: уведомление рабочего стола (строка или {title, body}).
func (m *Module) notifyAction() builtinAction {
	parse := func(v any) (string, string, error) {
		if s, ok := v.(string); ok {
			return "mKey", s, nil
		}
		var p struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := project.Decode(v, &p); err != nil {
			return "", "", err
		}
		if p.Title == "" {
			p.Title = "mKey"
		}
		return p.Title, p.Body, nil
	}
	return builtinAction{
		meta:     meta("action", "notify", "system", `{"oneOf":[{"type":"string"},{"type":"object","required":["title","body"],"properties":{"title":{"type":"string"},"body":{"type":"string","x-widget":"multiline"}}}]}`),
		validate: func(a project.Action) error { _, _, err := parse(a.Value); return err },
		run: func(ctx context.Context, rc contracts.RunContext, a project.Action) error {
			title, body, err := parse(a.Value)
			if err != nil {
				return err
			}
			if m.notifier == nil {
				rc.Logger().Info("notification (no desktop notifications)", "title", title, "body", body)
				return nil
			}
			if err := m.notifier.Notify(ctx, title, body); err != nil {
				rc.Logger().Warn("notification failed", "err", err)
			}
			return nil
		},
	}
}

// enableAction — действия enable/disable: событие того же проекта или {project, event}; без event — весь проект.
func (m *Module) enableAction(id string, on bool) builtinAction {
	return builtinAction{
		meta: meta("action", id, "system", `{"oneOf":[{"type":"string","x-widget":"event"},{"type":"object","required":["project"],"properties":{"project":{"type":"string","x-widget":"project"},"event":{"type":"string"}}}]}`),
		validate: func(a project.Action) error {
			_, err := parseTarget(a.Value)
			return err
		},
		run: func(_ context.Context, rc contracts.RunContext, a project.Action) error {
			t, err := parseTarget(a.Value)
			if err != nil {
				return err
			}
			if m.projects == nil {
				return errors.New("the store module is disabled")
			}
			if t.Project == "" {
				t.Project = rc.Event().Project
			}
			if t.Event == "" {
				return m.projects.SetEnabled(t.Project, on)
			}
			return m.projects.SetEventEnabled(t.Project, t.Event, on)
		},
	}
}

// stopAction — действие stop: self (по умолчанию) — закончить это выполнение, all — остановить все макросы.
func (m *Module) stopAction() builtinAction {
	parse := func(v any) (string, error) {
		switch s := v.(type) {
		case nil:
			return "self", nil
		case string:
			if s == "self" || s == "all" {
				return s, nil
			}
		}
		return "", errors.New(`stop: expected "self" or "all"`)
	}
	return builtinAction{
		meta:     meta("action", "stop", "system", `{"enum":["self","all"],"default":"self"}`),
		validate: func(a project.Action) error { _, err := parse(a.Value); return err },
		run: func(_ context.Context, _ contracts.RunContext, a project.Action) error {
			what, err := parse(a.Value)
			if err != nil {
				return err
			}
			if what == "all" {
				go m.StopAll()
			}
			return errStopSelf
		},
	}
}
