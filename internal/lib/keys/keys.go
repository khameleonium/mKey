package keys

import (
	"strings"

	ev "mkey/internal/lib/evdev"
)

// Key — клавиша или кнопка, найденная по имени.
type Key struct {
	// Name — каноническое имя, например "Ctrl" или "Mouse0".
	Name string
	// Type — тип события evdev (EvKey для клавиш и кнопок, EvAbs для осей).
	Type uint16
	// Code — код evdev; для модификаторов без стороны — код левой клавиши.
	Code uint16
	// AnySide — модификатор без стороны (Ctrl, Shift, Alt, Super).
	AnySide bool
}

// Matches сообщает, соответствует ли код события code этой клавише.
// Модификатор без стороны соответствует и левой, и правой клавише.
func (k Key) Matches(code uint16) bool {
	if code == k.Code {
		return true
	}
	return k.AnySide && sidePairs[k.Code] == code
}

// namespace — индекс одного пространства имён.
type namespace struct {
	// byName — все имена и алиасы в нижнем регистре → строка таблицы.
	byName map[string]entry
	// byCode — код → первая строка таблицы с этим кодом (каноническое имя для вывода).
	byCode map[uint16]entry
	// typ — тип событий evdev этого пространства.
	typ uint16
}

// Индексы пространств имён, построенные из таблиц при инициализации пакета.
var (
	keyboardNS = buildNamespace(keyboardTable, ev.EvKey)
	gamepadNS  = buildNamespace(gamepadTable, ev.EvKey)
	axisNS     = buildNamespace(axisTable, ev.EvAbs)
	relNS      = buildNamespace(relTable, ev.EvRel)
)

// buildNamespace строит индексы «имя → клавиша» и «код → клавиша» для таблицы.
// Повторяющееся имя в таблице — ошибка программиста, поэтому вызывает панику при запуске.
func buildNamespace(table []entry, typ uint16) namespace {
	ns := namespace{byName: map[string]entry{}, byCode: map[uint16]entry{}, typ: typ}
	for _, e := range table {
		// Регистрируем каноническое имя и все алиасы без учёта регистра.
		for _, n := range append([]string{e.name}, e.aliases...) {
			key := strings.ToLower(n)
			if _, dup := ns.byName[key]; dup {
				panic("keys: duplicate name " + n)
			}
			ns.byName[key] = e
		}

		// Для обратного поиска запоминаем первую строку с этим кодом.
		if _, exists := ns.byCode[e.code]; !exists {
			ns.byCode[e.code] = e
		}
	}
	return ns
}

// lookup ищет имя в пространстве ns без учёта регистра.
func (ns namespace) lookup(name string) (Key, bool) {
	e, ok := ns.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Key{}, false
	}
	return Key{Name: e.name, Type: ns.typ, Code: e.code, AnySide: e.anySide}, true
}

// Lookup ищет клавишу клавиатуры или кнопку мыши по имени или алиасу (без учёта регистра).
func Lookup(name string) (Key, bool) { return keyboardNS.lookup(name) }

// LookupGamepad ищет кнопку геймпада по имени или алиасу (South, A, LB, Start…).
func LookupGamepad(name string) (Key, bool) { return gamepadNS.lookup(name) }

// LookupAxis ищет ось геймпада по имени (LX, LY, RX, RY, LT, RT, DPadX, DPadY).
func LookupAxis(name string) (Key, bool) { return axisNS.lookup(name) }

// LookupRel ищет относительную ось мыши по имени (MouseX, MouseY, MouseWheel, MouseHWheel).
func LookupRel(name string) (Key, bool) { return relNS.lookup(name) }

// RelNameOf возвращает имя относительной оси для кода EV_REL.
func RelNameOf(code uint16) (string, bool) {
	e, ok := relNS.byCode[code]
	return e.name, ok
}

// NameOf возвращает каноническое имя mKey для кода EV_KEY: сначала среди клавиатуры и мыши,
// затем среди кнопок геймпада. Для кода без имени возвращает false —
// такие кнопки получают авто-ID вида UnKey.001 (FR-DEV-2, фаза 7).
func NameOf(code uint16) (string, bool) {
	if e, ok := keyboardNS.byCode[code]; ok {
		return e.name, true
	}
	if e, ok := gamepadNS.byCode[code]; ok {
		return e.name, true
	}
	return "", false
}

// AxisNameOf возвращает каноническое имя оси для кода EV_ABS.
func AxisNameOf(code uint16) (string, bool) {
	e, ok := axisNS.byCode[code]
	return e.name, ok
}

// Names возвращает канонические имена клавиатуры и мыши в порядке таблицы (для GUI и автодополнения).
func Names() []string {
	out := make([]string, 0, len(keyboardTable))
	for _, e := range keyboardTable {
		out = append(out, e.name)
	}
	return out
}

// Suggest возвращает каноническое имя клавиши клавиатуры/мыши, ближайшее к name
// (расстояние Левенштейна не больше 2 и не больше трети длины), или пустую строку.
// Используется для подсказок вида «возможно, вы имели в виду "Mouse0"?» (FR-DSL-3).
func Suggest(name string) string {
	// Сравниваем без учёта регистра со всеми именами и алиасами.
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return ""
	}
	limit := min(2, max(1, len([]rune(target))/3))
	best, bestDist := "", limit+1
	for n, e := range keyboardNS.byName {
		if d := levenshtein(target, n); d < bestDist || (d == bestDist && e.name < best) {
			best, bestDist = e.name, d
		}
	}
	if bestDist > limit {
		return ""
	}
	return best
}

// levenshtein считает расстояние редактирования между строками (по рунам).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	// Динамическое программирование на двух строках матрицы.
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
