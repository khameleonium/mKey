package dsl

// Kind — вид узла дерева разбора.
type Kind string

// Виды узлов.
const (
	// KindTap — нажать и отпустить клавишу или сочетание ({A}, {Ctrl+C}, {A*3}, {Space 500}).
	KindTap Kind = "tap"
	// KindDown — зажать (^{Shift}).
	KindDown Kind = "down"
	// KindUp — отпустить (~{Shift}).
	KindUp Kind = "up"
	// KindUpAll — отпустить всё, что зажал макрос (~{*}).
	KindUpAll Kind = "up_all"
	// KindAxis — установить значение оси ({pad2.LX=0.5}).
	KindAxis Kind = "axis"
	// KindPause — пауза ([250], [100..300]).
	KindPause Kind = "pause"
	// KindText — набор текста ({"Привет"}).
	KindText Kind = "text"
	// KindCommand — команда ({Move +10 -5}, {Wheel Up 3}).
	KindCommand Kind = "command"
	// KindGroup — группа с повтором (({A}[50])*10).
	KindGroup Kind = "group"
)

// Pos — позиция в исходном тексте. Строка и столбец считаются в символах, с 1.
type Pos struct {
	// Line — номер строки.
	Line int `json:"line"`
	// Col — номер столбца.
	Col int `json:"col"`
}

// KeyRef — ссылка на клавишу или кнопку, как она записана в макросе.
type KeyRef struct {
	// Device — префикс устройства ("pad2" в {pad2.South}); пусто — устройство по умолчанию.
	Device string `json:"device,omitempty"`
	// Name — имя клавиши. Для устройства по умолчанию — каноническое имя из таблицы клавиш.
	Name string `json:"name,omitempty"`
	// Code — сырой код evdev для записи {#30}; nil — ссылка по имени.
	Code *uint16 `json:"code,omitempty"`
}

// Arg — аргумент команды: число (возможно, со знаком) или слово.
type Arg struct {
	// Number — числовое значение (если Word пусто).
	Number float64 `json:"number,omitempty"`
	// Signed — у числа был явный знак "+" или "-" ({Move +10 -5}).
	Signed bool `json:"signed,omitempty"`
	// Word — слово ({Wheel Up}); пусто для чисел.
	Word string `json:"word,omitempty"`
}

// Node — узел дерева разбора. Используются только поля, относящиеся к виду узла Kind;
// такая плоская структура удобна для JSON, который получает конструктор блоков в GUI.
type Node struct {
	// Kind — вид узла.
	Kind Kind `json:"type"`
	// Pos — начало узла в тексте.
	Pos Pos `json:"pos"`
	// Keys — клавиши (tap, down, up, axis); несколько — сочетание.
	Keys []KeyRef `json:"keys,omitempty"`
	// Repeat — число повторов (tap, group); 0 означает 1.
	Repeat int `json:"repeat,omitempty"`
	// HoldMS — удержание в миллисекундах (tap); 0 — удержание по умолчанию.
	HoldMS int64 `json:"hold_ms,omitempty"`
	// Value — значение оси (axis).
	Value float64 `json:"value,omitempty"`
	// MinMS и MaxMS — границы паузы (pause); для фиксированной паузы равны.
	MinMS int64 `json:"min_ms,omitempty"`
	MaxMS int64 `json:"max_ms,omitempty"`
	// Text — текст для набора (text).
	Text string `json:"text,omitempty"`
	// Command — каноническое имя команды (command).
	Command string `json:"command,omitempty"`
	// Args — аргументы команды (command).
	Args []Arg `json:"args,omitempty"`
	// Children — содержимое группы (group).
	Children []Node `json:"children,omitempty"`
}

// times возвращает число повторов узла (Repeat = 0 означает 1).
func (n Node) times() int {
	if n.Repeat <= 0 {
		return 1
	}
	return n.Repeat
}
