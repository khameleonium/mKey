// Package layout — таблицы раскладок клавиатуры: какой символ набирается какой физической
// клавишей и нужен ли для него Shift (FR-DSL-1a, FR-AD-4).
//
// Набор текста в mKey идёт через виртуальную клавиатуру, которая отправляет коды физических
// клавиш. Композитор превращает их в символы по текущей раскладке пользователя, поэтому,
// чтобы набрать «П», mKey должен знать: в русской раскладке это Shift + клавиша G.
//
// Встроены раскладки "us" и "ru" (стандартная ЙЦУКЕН). Других раскладок (ua, by, de, fr…)
// и разбора раскладки прямо из системы (xkb) нет: фаза 8 отменена.
package layout

import (
	"slices"
	"strings"

	ev "mkey/internal/lib/evdev"
)

// Stroke — способ набрать символ: клавиша и нужен ли Shift.
type Stroke struct {
	// Code — код физической клавиши EV_KEY.
	Code uint16
	// Shift — символ набирается с зажатым Shift.
	Shift bool
}

// Layout — раскладка: символ → нажатие.
type Layout struct {
	// Name — короткое имя раскладки ("us", "ru").
	Name string
	// strokes — таблица символов.
	strokes map[rune]Stroke
	// chars — обратная таблица: нажатие → символ.
	chars map[Stroke]rune
}

// Find возвращает нажатие для символа r в этой раскладке.
func (l *Layout) Find(r rune) (Stroke, bool) {
	s, ok := l.strokes[r]
	return s, ok
}

// Char возвращает символ, который набирает клавиша code (с Shift или без) в этой раскладке.
func (l *Layout) Char(code uint16, shift bool) (rune, bool) {
	r, ok := l.chars[Stroke{Code: code, Shift: shift}]
	return r, ok
}

// row — ряд клавиш: коды по порядку, символы без Shift и с Shift в том же порядке.
type row struct {
	codes   []uint16
	lower   string
	shifted string
}

// Физические ряды клавиатуры (коды одинаковы для всех раскладок).
var (
	rowNumbers = []uint16{ev.KeyGrave, ev.Key1, ev.Key2, ev.Key3, ev.Key4, ev.Key5, ev.Key6, ev.Key7, ev.Key8, ev.Key9, ev.Key0, ev.KeyMinus, ev.KeyEqual}
	rowTop     = []uint16{ev.KeyQ, ev.KeyW, ev.KeyE, ev.KeyR, ev.KeyT, ev.KeyY, ev.KeyU, ev.KeyI, ev.KeyO, ev.KeyP, ev.KeyLeftbrace, ev.KeyRightbrace, ev.KeyBackslash}
	rowHome    = []uint16{ev.KeyA, ev.KeyS, ev.KeyD, ev.KeyF, ev.KeyG, ev.KeyH, ev.KeyJ, ev.KeyK, ev.KeyL, ev.KeySemicolon, ev.KeyApostrophe}
	rowBottom  = []uint16{ev.KeyZ, ev.KeyX, ev.KeyC, ev.KeyV, ev.KeyB, ev.KeyN, ev.KeyM, ev.KeyComma, ev.KeyDot, ev.KeySlash}
)

// definitions — встроенные раскладки: для каждого ряда символы без Shift и с Shift.
var definitions = map[string][]row{
	"us": {
		{rowNumbers, "`1234567890-=", "~!@#$%^&*()_+"},
		{rowTop, `qwertyuiop[]\`, "QWERTYUIOP{}|"},
		{rowHome, "asdfghjkl;'", `ASDFGHJKL:"`},
		{rowBottom, "zxcvbnm,./", "ZXCVBNM<>?"},
	},
	"ru": {
		{rowNumbers, "ё1234567890-=", `Ё!"№;%:?*()_+`},
		{rowTop, `йцукенгшщзхъ\`, "ЙЦУКЕНГШЩЗХЪ/"},
		{rowHome, "фывапролджэ", "ФЫВАПРОЛДЖЭ"},
		{rowBottom, "ячсмитьбю.", "ЯЧСМИТЬБЮ,"},
	},
}

// Get возвращает встроенную раскладку по имени (регистр и вариант после "(" игнорируются: "ru(winkeys)" → "ru").
func Get(name string) (*Layout, bool) {
	// Нормализуем имя.
	base := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexAny(base, "(+:"); i >= 0 {
		base = base[:i]
	}
	rows, ok := definitions[base]
	if !ok {
		return nil, false
	}

	// Строим таблицу символов; пробел одинаков во всех раскладках.
	l := &Layout{
		Name:    base,
		strokes: map[rune]Stroke{' ': {Code: ev.KeySpace}},
		chars:   map[Stroke]rune{{Code: ev.KeySpace}: ' ', {Code: ev.KeySpace, Shift: true}: ' '},
	}
	for _, r := range rows {
		lower, shifted := []rune(r.lower), []rune(r.shifted)
		for i, code := range r.codes {
			// Первое вхождение символа важнее (если символ встречается дважды).
			if _, exists := l.strokes[lower[i]]; !exists {
				l.strokes[lower[i]] = Stroke{Code: code}
			}
			if _, exists := l.strokes[shifted[i]]; !exists {
				l.strokes[shifted[i]] = Stroke{Code: code, Shift: true}
			}
			l.chars[Stroke{Code: code}] = lower[i]
			l.chars[Stroke{Code: code, Shift: true}] = shifted[i]
		}
	}
	return l, true
}

// Names возвращает имена встроенных раскладок в алфавитном порядке.
func Names() []string {
	names := make([]string, 0, len(definitions))
	for n := range definitions {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
