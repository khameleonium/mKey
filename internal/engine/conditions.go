package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// builtinConditions возвращает встроенные условия движка (FR-EV-3; window и pixel — фаза 8).
func (m *Module) builtinConditions() []contracts.ConditionType {
	return []contracts.ConditionType{
		// variable — сравнение переменной проекта со значением.
		builtinCondition{
			meta:     meta("condition", "variable", "logic", `{"type":"object","required":["name","op"],"properties":{"name":{"type":"string"},"op":{"enum":["==","!=","<",">","<=",">="]},"value":{}}}`),
			validate: func(c project.Condition) error { _, err := variableParams(c); return err },
			check:    checkVariable,
		},
		// key_state — клавиша сейчас зажата или отпущена.
		builtinCondition{
			meta:     meta("condition", "key_state", "input", `{"type":"object","required":["key"],"properties":{"key":{"type":"string"},"state":{"enum":["down","up"]}}}`),
			validate: func(c project.Condition) error { _, _, err := keyStateParams(c); return err },
			check:    m.checkKeyState,
		},
		// toggle — состояние переключателя события (по умолчанию — своего).
		builtinCondition{
			meta:     meta("condition", "toggle", "logic", `{"type":"object","properties":{"event":{"type":"string"},"state":{"type":"boolean"}}}`),
			validate: func(c project.Condition) error { _, err := toggleParams(c); return err },
			check:    m.checkToggle,
		},
		// time — текущее время суток в интервале [from, to) (через полночь — тоже).
		builtinCondition{
			meta:     meta("condition", "time", "system", `{"type":"object","required":["from","to"],"properties":{"from":{"type":"string"},"to":{"type":"string"}}}`),
			validate: func(c project.Condition) error { _, _, err := timeParams(c); return err },
			check:    checkTime,
		},
		// any, all, not — группы условий.
		m.groupCondition("any"),
		m.groupCondition("all"),
		m.groupCondition("not"),
	}
}

// variableCond — параметры условия variable.
type variableCond struct {
	Name  string `json:"name"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// variableParams разбирает параметры условия variable.
func variableParams(c project.Condition) (variableCond, error) {
	var p variableCond
	if err := project.Decode(c.Params, &p); err != nil {
		return p, err
	}
	switch p.Op {
	case "==", "!=", "<", ">", "<=", ">=":
	default:
		return p, fmt.Errorf("unknown op %q", p.Op)
	}
	if p.Name == "" {
		return p, errors.New("name is required")
	}
	return p, nil
}

// checkVariable сравнивает переменную со значением: числа — как числа, остальное — на равенство.
func checkVariable(_ context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
	p, err := variableParams(c)
	if err != nil {
		return false, err
	}
	cur, _ := rc.Vars().Get(p.Name)

	// Числовое сравнение, если оба значения — числа.
	a, errA := toFloat(cur)
	b, errB := toFloat(p.Value)
	if errA == nil && errB == nil {
		switch p.Op {
		case "==":
			return a == b, nil
		case "!=":
			return a != b, nil
		case "<":
			return a < b, nil
		case ">":
			return a > b, nil
		case "<=":
			return a <= b, nil
		default:
			return a >= b, nil
		}
	}

	// Не числа — только равенство и неравенство.
	equal := reflect.DeepEqual(cur, p.Value) || fmt.Sprint(cur) == fmt.Sprint(p.Value)
	switch p.Op {
	case "==":
		return equal, nil
	case "!=":
		return !equal, nil
	}
	return false, fmt.Errorf("variable %q is not a number, op %q is not applicable", p.Name, p.Op)
}

// keyStateParams разбирает параметры условия key_state.
func keyStateParams(c project.Condition) (keys.Key, bool, error) {
	var p struct {
		Key   string `json:"key"`
		State string `json:"state"`
	}
	if err := project.Decode(c.Params, &p); err != nil {
		return keys.Key{}, false, err
	}
	name := p.Key
	if len(name) > 2 && name[0] == '{' && name[len(name)-1] == '}' {
		name = name[1 : len(name)-1]
	}
	k, ok := keys.Lookup(name)
	if !ok {
		return keys.Key{}, false, fmt.Errorf("unknown key %q", p.Key)
	}
	switch p.State {
	case "", "down":
		return k, true, nil
	case "up":
		return k, false, nil
	}
	return keys.Key{}, false, fmt.Errorf("unknown state %q", p.State)
}

// checkKeyState проверяет, зажата ли клавиша (нужен модуль hotkeys).
func (m *Module) checkKeyState(_ context.Context, _ contracts.RunContext, c project.Condition) (bool, error) {
	k, wantDown, err := keyStateParams(c)
	if err != nil {
		return false, err
	}
	if m.keyState == nil {
		return false, errors.New("key_state: the hotkeys module is disabled")
	}
	return m.keyState.IsDown(k) == wantDown, nil
}

// toggleCond — параметры условия toggle.
type toggleCond struct {
	Event string `json:"event"`
	State *bool  `json:"state"`
}

// toggleParams разбирает параметры условия toggle.
func toggleParams(c project.Condition) (toggleCond, error) {
	var p toggleCond
	err := project.Decode(c.Params, &p)
	return p, err
}

// checkToggle проверяет состояние переключателя события (по умолчанию — выполняемого).
func (m *Module) checkToggle(_ context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
	p, err := toggleParams(c)
	if err != nil {
		return false, err
	}
	want := p.State == nil || *p.State
	if p.Event == "" {
		return rc.Toggled() == want, nil
	}
	er, ok := m.findEvent(rc.Event().Project, p.Event)
	if !ok {
		return false, fmt.Errorf("toggle: event %q not found", p.Event)
	}
	return er.toggled.Load() == want, nil
}

// timeParams разбирает параметры условия time ("08:00", "22:30").
func timeParams(c project.Condition) (time.Duration, time.Duration, error) {
	var p struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := project.Decode(c.Params, &p); err != nil {
		return 0, 0, err
	}
	from, err1 := time.Parse("15:04", p.From)
	to, err2 := time.Parse("15:04", p.To)
	if err1 != nil || err2 != nil {
		return 0, 0, errors.New(`from and to must be times like "08:00"`)
	}
	sinceMidnight := func(t time.Time) time.Duration {
		return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
	}
	return sinceMidnight(from), sinceMidnight(to), nil
}

// checkTime проверяет, что сейчас время суток в интервале [from, to).
func checkTime(_ context.Context, _ contracts.RunContext, c project.Condition) (bool, error) {
	from, to, err := timeParams(c)
	if err != nil {
		return false, err
	}
	now := time.Now()
	cur := time.Duration(now.Hour())*time.Hour + time.Duration(now.Minute())*time.Minute
	if from <= to {
		return cur >= from && cur < to, nil
	}
	return cur >= from || cur < to, nil // интервал через полночь
}

// groupCondition создаёт группу условий: any — хотя бы одно, all — все, not — ни одного.
func (m *Module) groupCondition(kind string) builtinCondition {
	// sub разбирает вложенные условия из параметра of.
	sub := func(c project.Condition) ([]project.Condition, error) {
		var p struct {
			Of any `json:"of"`
		}
		if err := project.Decode(c.Params, &p); err != nil {
			return nil, err
		}
		return project.DecodeConditions(p.Of)
	}
	return builtinCondition{
		meta: meta("condition", kind, "logic", `{"type":"object","required":["of"],"properties":{"of":{"type":"array"}}}`),
		validate: func(c project.Condition) error {
			conds, err := sub(c)
			if err != nil {
				return err
			}
			return m.validateConditions(conds)
		},
		check: func(ctx context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
			conds, err := sub(c)
			if err != nil {
				return false, err
			}
			// Каждое вложенное условие вычисляется отдельно.
			matched := 0
			for _, cc := range conds {
				ok, err := rc.Check(ctx, []project.Condition{cc})
				if err != nil {
					return false, err
				}
				if ok {
					matched++
				}
			}
			switch kind {
			case "any":
				return matched > 0, nil
			case "all":
				return matched == len(conds), nil
			default:
				return matched == 0, nil
			}
		},
	}
}
