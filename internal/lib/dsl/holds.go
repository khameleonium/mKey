package dsl

import (
	"strconv"

	"github.com/khameleonium/mKey/internal/lib/keys"
)

// Проверки зажатых клавиш (решение владельца): клавишу нельзя зажать дважды (^{A}^{A}),
// нажать, пока она зажата (^{A}{A}), и отпустить, если её не зажимали (~{A} без ^{A}).
// Проверяется один макрос целиком; клавиши, оставшиеся зажатыми в конце, mKey отпускает сам.

// CheckHolds проверяет зажатия и отпускания в макросе. Ошибка — *Error с местом ошибки
// (ErrAlreadyHeld или ErrNotHeld).
func CheckHolds(nodes []Node) error {
	return checkHolds(nodes, map[string]bool{})
}

// checkHolds проверяет узлы, изменяя набор held зажатых клавиш.
func checkHolds(nodes []Node, held map[string]bool) error {
	for _, n := range nodes {
		switch n.Kind {
		// Нажатие и зажатие зажатой клавиши невозможны.
		case KindTap, KindDown:
			for _, k := range n.Keys {
				id := keyID(k)
				if held[id] {
					return newError(n.Pos, ErrAlreadyHeld, "key", formatKeyRef(k))
				}
				if n.Kind == KindDown {
					held[id] = true
				}
			}

		// Отпустить можно только зажатую.
		case KindUp:
			for _, k := range n.Keys {
				id := keyID(k)
				if !held[id] {
					return newError(n.Pos, ErrNotHeld, "key", formatKeyRef(k))
				}
				delete(held, id)
			}

		// ~{*} отпускает всё.
		case KindUpAll:
			clear(held)

		// Группа с повтором: двух проходов хватает, чтобы найти несбалансированные зажатия.
		case KindGroup:
			for range min(max(n.Repeat, 1), 2) {
				if err := checkHolds(n.Children, held); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// keyID — одинаковое для одной и той же клавиши при разных записях: ^{Ctrl} и ~{LCtrl} — одна
// клавиша (левый Ctrl), {#29} — тоже она.
func keyID(k KeyRef) string {
	switch {
	case k.Device != "":
		return k.Device + "." + k.Name
	case k.Code != nil:
		return "#" + strconv.Itoa(int(*k.Code))
	}
	if key, ok := keys.Lookup(k.Name); ok {
		return "#" + strconv.Itoa(int(key.Code))
	}
	return k.Name
}

// ParseHotkey разбирает сочетание горячей клавиши в записи зажатием: «^{Ctrl}^{Alt}{H}» —
// зажатые клавиши и последняя нажатая; «{F8}» — одна клавиша. Возвращает клавиши по порядку
// (последняя — та, по нажатию которой срабатывает сочетание). Ошибка — *Error (ErrBadHotkey и др.).
func ParseHotkey(src string) ([]KeyRef, error) {
	nodes, err := Parse(src)
	if err != nil {
		return nil, err
	}

	// Только зажатия и одно нажатие в конце, без повторов и удержаний.
	var out []KeyRef
	for i, n := range nodes {
		last := i == len(nodes)-1
		switch {
		case !last && n.Kind == KindDown && len(n.Keys) == 1:
		case last && n.Kind == KindTap && len(n.Keys) == 1 && n.Repeat <= 1 && n.HoldMS == 0:
		default:
			return nil, newError(n.Pos, ErrBadHotkey)
		}
		out = append(out, n.Keys[0])
	}
	if len(out) == 0 {
		return nil, newError(Pos{Line: 1, Col: 1}, ErrBadHotkey)
	}

	// Одна клавиша не может быть в сочетании дважды.
	if err := CheckHolds(nodes); err != nil {
		return nil, err
	}
	return out, nil
}
