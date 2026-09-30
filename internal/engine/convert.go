package engine

import (
	"fmt"
	"strings"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	"mkey/internal/lib/project"
)

// Перевод действий в макрос и обратно для режима DSL конструктора (FR-UI-4, contracts.ActionConverter).
// Логика живёт здесь, а не в GUI, чтобы у действий и макросов был один источник истины.

// ActionsToDSL склеивает действия ввода в один макрос.
// Ошибка — действие неизвестно или его нельзя записать макросом (повтор, условие, скрипт…).
func (m *Module) ActionsToDSL(actions []project.Action) (string, error) {
	var b strings.Builder
	for i, a := range actions {
		// Только встроенные действия, которые сводятся к макросу.
		at, err := m.actionType(a.Type)
		if err != nil {
			return "", fmt.Errorf("action #%d: %w", i+1, err)
		}
		ba, ok := at.(builtinAction)
		if !ok || ba.toDSL == nil {
			return "", fmt.Errorf("action #%d (%s): %w", i+1, a.Type, errNotMacro)
		}

		// Перевод значения и проверка получившегося фрагмента.
		src, err := ba.toDSL(a.Value)
		if err != nil {
			return "", fmt.Errorf("action #%d (%s): %w", i+1, a.Type, err)
		}
		if _, err := dsl.Parse(src); err != nil {
			return "", fmt.Errorf("action #%d (%s): %w", i+1, a.Type, err)
		}
		b.WriteString(src)
	}
	return b.String(), nil
}

// errNotMacro — действие нельзя записать макросом DSL.
var errNotMacro = fmt.Errorf("this action cannot be written as a macro")

// DSLToActions разбирает макрос на отдельные действия. Узлы без отдельного действия
// (группы с повтором, повтор клавиши, оси, отпустить всё) остаются макросом в действии send;
// соседние такие узлы склеиваются в один send.
func (m *Module) DSLToActions(text string) ([]project.Action, error) {
	nodes, err := dsl.Parse(text)
	if err != nil {
		return nil, err
	}
	if err := dsl.CheckHolds(nodes); err != nil {
		return nil, err
	}
	out := []project.Action{}
	for _, n := range nodes {
		a, ok := nodeAction(n)
		if !ok {
			a = project.Action{Type: "send", Value: dsl.Format([]dsl.Node{n})}
		}

		// Соседние фрагменты send склеиваются.
		if last := len(out) - 1; a.Type == "send" && last >= 0 && out[last].Type == "send" {
			out[last].Value = out[last].Value.(string) + a.Value.(string)
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// nodeAction переводит узел макроса в отдельное действие; false — такого действия нет.
func nodeAction(n dsl.Node) (project.Action, bool) {
	switch n.Kind {
	// Нажатие: простое, с удержанием; с повтором — остаётся макросом.
	case dsl.KindTap:
		if n.Repeat > 1 {
			return project.Action{}, false
		}
		if n.HoldMS > 0 {
			return project.Action{Type: "hold", Value: map[string]any{"key": keysName(n), "ms": n.HoldMS}}, true
		}
		return project.Action{Type: "tap", Value: keysName(n)}, true

	// Зажать и отпустить.
	case dsl.KindDown:
		return project.Action{Type: "key_down", Value: keysName(n)}, true
	case dsl.KindUp:
		return project.Action{Type: "key_up", Value: keysName(n)}, true

	// Пауза: фиксированная или случайная в диапазоне.
	case dsl.KindPause:
		if n.MinMS == n.MaxMS {
			return project.Action{Type: "pause", Value: n.MinMS}, true
		}
		return project.Action{Type: "pause", Value: map[string]any{"min_ms": n.MinMS, "max_ms": n.MaxMS}}, true

	// Набор текста.
	case dsl.KindText:
		return project.Action{Type: "type_text", Value: n.Text}, true

	// Команды мыши.
	case dsl.KindCommand:
		return commandAction(n)
	}
	return project.Action{}, false
}

// commandAction переводит команды Move, Click и Wheel в действия мыши.
func commandAction(n dsl.Node) (project.Action, bool) {
	args := n.Args
	switch {
	// Относительное перемещение: два числа со знаком.
	case n.Command == "Move" && len(args) == 2 && args[0].Word == "" && args[1].Word == "" && args[0].Signed && args[1].Signed:
		return project.Action{Type: "mouse_move", Value: map[string]any{"dx": int(args[0].Number), "dy": int(args[1].Number)}}, true

	// Щелчок: без аргументов — левой кнопкой, иначе названной.
	case n.Command == "Click" && len(args) == 0:
		return project.Action{Type: "mouse_click", Value: "Left"}, true
	case n.Command == "Click" && len(args) == 1 && args[0].Word != "":
		return project.Action{Type: "mouse_click", Value: capitalize(args[0].Word)}, true

	// Прокрутка: направление и число щелчков.
	case n.Command == "Wheel" && len(args) >= 1 && len(args) <= 2 && args[0].Word != "":
		count := 1
		if len(args) == 2 {
			count = int(args[1].Number)
		}
		return project.Action{Type: "wheel", Value: map[string]any{"direction": capitalize(args[0].Word), "count": count}}, true
	}
	return project.Action{}, false
}

// keysName возвращает клавишу узла без скобок: "C", "pad2.South".
func keysName(n dsl.Node) string {
	s := dsl.Format([]dsl.Node{{Kind: dsl.KindTap, Keys: n.Keys}})
	return strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
}

// capitalize делает первую букву слова заглавной, остальные — строчными ("left" → "Left").
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// Проверка на этапе компиляции, что Module реализует контракт.
var _ contracts.ActionConverter = (*Module)(nil)
