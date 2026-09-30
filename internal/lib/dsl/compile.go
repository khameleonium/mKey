package dsl

import (
	"math"
	"strings"

	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// Устройства по умолчанию, на которые компилируются клавиши без префикса.
const (
	// DeviceKeyboard — основная виртуальная клавиатура mKey.
	DeviceKeyboard = "keyboard"
	// DeviceMouse — основная виртуальная мышь mKey.
	DeviceMouse = "mouse"
)

// maxWheel — наибольшее число щелчков колеса в одной команде.
const maxWheel = 1000

// Target — клавиша, найденная на конкретном устройстве.
type Target struct {
	// Device — устройство: DeviceKeyboard, DeviceMouse или имя виртуального устройства.
	Device string `json:"device"`
	// Code — код EV_KEY.
	Code uint16 `json:"code"`
	// Name — имя для сообщений и журналов.
	Name string `json:"name"`
}

// Resolver находит устройство и код для ссылки на клавишу.
type Resolver interface {
	// Resolve возвращает цель для ссылки ref или *Error (например, ErrUnknownDevice).
	Resolve(ref KeyRef, pos Pos) (Target, error)
}

// DefaultResolver — клавиши без префикса идут на клавиатуру mKey, кнопки мыши — на мышь mKey.
// Префиксы устройств (pad2, Sega…) появятся вместе с виртуальными и подписанными устройствами (фаза 7).
type DefaultResolver struct{}

// Resolve находит цель для ссылки на клавишу.
func (DefaultResolver) Resolve(ref KeyRef, pos Pos) (Target, error) {
	// Устройства, кроме основных, пока неизвестны.
	if ref.Device != "" {
		return Target{}, newError(pos, ErrUnknownDevice, "device", ref.Device)
	}

	// Сырой код или имя из таблицы клавиш.
	var t Target
	switch {
	case ref.Code != nil:
		t = Target{Code: *ref.Code, Name: formatKeyRef(ref)}
	default:
		k, ok := keys.Lookup(ref.Name)
		if !ok {
			return Target{}, newError(pos, ErrUnknownKey, "name", ref.Name)
		}
		t = Target{Code: k.Code, Name: k.Name}
	}

	// Кнопки мыши — на мышь, остальное — на клавиатуру.
	t.Device = DeviceKeyboard
	if t.Code >= ev.BtnMouse && t.Code < ev.BtnJoystick {
		t.Device = DeviceMouse
	}
	return t, nil
}

// StepKind — вид шага плана выполнения.
type StepKind string

// Виды шагов.
const (
	// StepPress — зажать клавиши по порядку.
	StepPress StepKind = "press"
	// StepRelease — отпустить клавиши в обратном порядке.
	StepRelease StepKind = "release"
	// StepReleaseAll — отпустить всё, что зажал макрос.
	StepReleaseAll StepKind = "release_all"
	// StepTap — нажать и отпустить сочетание Count раз.
	StepTap StepKind = "tap"
	// StepWait — пауза от MinMS до MaxMS.
	StepWait StepKind = "wait"
	// StepText — набрать текст.
	StepText StepKind = "text"
	// StepMove — относительное перемещение курсора.
	StepMove StepKind = "move"
	// StepWheel — прокрутка колеса.
	StepWheel StepKind = "wheel"
	// StepLoop — повторить Body Count раз.
	StepLoop StepKind = "loop"
)

// Step — шаг плана выполнения. Используются поля, относящиеся к виду шага.
type Step struct {
	// Kind — вид шага.
	Kind StepKind `json:"kind"`
	// Pos — позиция исходной записи (для сообщений об ошибках выполнения).
	Pos Pos `json:"pos"`
	// Targets — клавиши (press, release, tap).
	Targets []Target `json:"targets,omitempty"`
	// Count — число повторов (tap, loop, wheel).
	Count int `json:"count,omitempty"`
	// HoldMS — удержание (tap); 0 — удержание по умолчанию исполнителя.
	HoldMS int64 `json:"hold_ms,omitempty"`
	// MinMS и MaxMS — длительность паузы (wait).
	MinMS int64 `json:"min_ms,omitempty"`
	MaxMS int64 `json:"max_ms,omitempty"`
	// Text — текст (text).
	Text string `json:"text,omitempty"`
	// DX и DY — смещение курсора (move); для wheel — щелчки по горизонтали (DX) и вертикали (DY) за один раз.
	DX int32 `json:"dx,omitempty"`
	DY int32 `json:"dy,omitempty"`
	// Body — вложенные шаги (loop).
	Body []Step `json:"body,omitempty"`
}

// Compile превращает дерево разбора в план выполнения, находя устройства и коды клавиш.
// Команды, которые появятся позже (абсолютные координаты, тач), дают ErrNotSupported.
func Compile(nodes []Node, r Resolver) ([]Step, error) {
	steps := make([]Step, 0, len(nodes))
	for _, n := range nodes {
		s, err := compileNode(n, r)
		if err != nil {
			return nil, err
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// compileNode компилирует один узел.
func compileNode(n Node, r Resolver) (Step, error) {
	switch n.Kind {
	case KindTap, KindDown, KindUp:
		targets, err := resolveAll(n.Keys, n.Pos, r)
		if err != nil {
			return Step{}, err
		}
		kind := map[Kind]StepKind{KindTap: StepTap, KindDown: StepPress, KindUp: StepRelease}[n.Kind]
		return Step{Kind: kind, Pos: n.Pos, Targets: targets, Count: n.times(), HoldMS: n.HoldMS}, nil
	case KindUpAll:
		return Step{Kind: StepReleaseAll, Pos: n.Pos}, nil
	case KindAxis:
		// Оси есть только у виртуальных геймпадов (фаза 7); сначала проверяем устройство.
		if _, err := resolveAll(n.Keys, n.Pos, r); err != nil {
			return Step{}, err
		}
		return Step{}, newError(n.Pos, ErrNotSupported, "what", "axis")
	case KindPause:
		return Step{Kind: StepWait, Pos: n.Pos, MinMS: n.MinMS, MaxMS: n.MaxMS}, nil
	case KindText:
		return Step{Kind: StepText, Pos: n.Pos, Text: n.Text}, nil
	case KindCommand:
		return compileCommand(n, r)
	case KindGroup:
		body, err := Compile(n.Children, r)
		if err != nil {
			return Step{}, err
		}
		return Step{Kind: StepLoop, Pos: n.Pos, Count: n.times(), Body: body}, nil
	}
	return Step{}, newError(n.Pos, ErrNotSupported, "what", string(n.Kind))
}

// resolveAll находит цели для всех клавиш сочетания.
func resolveAll(refs []KeyRef, pos Pos, r Resolver) ([]Target, error) {
	targets := make([]Target, 0, len(refs))
	for _, ref := range refs {
		t, err := r.Resolve(ref, pos)
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// Подсказки по записи команд для сообщений об ошибках.
const (
	usageMove  = "{Move +dx +dy}"
	usageClick = "{Click} / {Click Right}"
	usageWheel = "{Wheel Up 3}"
)

// compileCommand компилирует встроенную команду.
func compileCommand(n Node, r Resolver) (Step, error) {
	bad := func(usage string) (Step, error) {
		return Step{}, newError(n.Pos, ErrBadCommandArgs, "command", n.Command, "usage", usage)
	}

	switch n.Command {
	// Перемещение: пока только относительное (оба числа со знаком).
	case "Move":
		if len(n.Args) != 2 || n.Args[0].Word != "" || n.Args[1].Word != "" {
			return bad(usageMove)
		}
		if !n.Args[0].Signed || !n.Args[1].Signed {
			return Step{}, newError(n.Pos, ErrNotSupported, "what", "absolute_move")
		}
		dx, okX := toInt32(n.Args[0].Number)
		dy, okY := toInt32(n.Args[1].Number)
		if !okX || !okY {
			return bad(usageMove)
		}
		return Step{Kind: StepMove, Pos: n.Pos, DX: dx, DY: dy}, nil

	// Щелчок кнопкой мыши в текущей позиции курсора.
	case "Click":
		button := "Mouse0"
		switch {
		case len(n.Args) == 0:
		case len(n.Args) == 1 && n.Args[0].Word != "":
			names := map[string]string{"left": "Mouse0", "right": "Mouse1", "middle": "Mouse2", "back": "Mouse3", "forward": "Mouse4"}
			b, ok := names[strings.ToLower(n.Args[0].Word)]
			if !ok {
				return bad(usageClick)
			}
			button = b
		case len(n.Args) == 2 && n.Args[0].Word == "" && n.Args[1].Word == "":
			return Step{}, newError(n.Pos, ErrNotSupported, "what", "absolute_click")
		default:
			return bad(usageClick)
		}
		t, err := r.Resolve(KeyRef{Name: button}, n.Pos)
		if err != nil {
			return Step{}, err
		}
		return Step{Kind: StepTap, Pos: n.Pos, Targets: []Target{t}, Count: 1}, nil

	// Прокрутка: направление и необязательное число щелчков.
	case "Wheel":
		if len(n.Args) < 1 || len(n.Args) > 2 || n.Args[0].Word == "" {
			return bad(usageWheel)
		}
		count := 1
		if len(n.Args) == 2 {
			c, ok := toInt32(n.Args[1].Number)
			if n.Args[1].Word != "" || n.Args[1].Signed || !ok || c < 1 || c > maxWheel {
				return bad(usageWheel)
			}
			count = int(c)
		}
		s := Step{Kind: StepWheel, Pos: n.Pos, Count: count}
		switch strings.ToLower(n.Args[0].Word) {
		case "up":
			s.DY = 1
		case "down":
			s.DY = -1
		case "left":
			s.DX = -1
		case "right":
			s.DX = 1
		default:
			return bad(usageWheel)
		}
		return s, nil

	// Тач появится вместе с виртуальным сенсорным экраном (фаза 7).
	case "Touch", "Swipe":
		return Step{}, newError(n.Pos, ErrNotSupported, "what", "touch")
	}
	return Step{}, newError(n.Pos, ErrNotSupported, "what", n.Command)
}

// toInt32 переводит число в int32, если оно целое и помещается.
func toInt32(v float64) (int32, bool) {
	if v != math.Trunc(v) || v > math.MaxInt32 || v < math.MinInt32 {
		return 0, false
	}
	return int32(v), true
}
