package dsl

import (
	"errors"
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
	// Code — код EV_KEY (или EV_ABS, если Axis).
	Code uint16 `json:"code"`
	// Name — имя для сообщений и журналов.
	Name string `json:"name"`
	// Axis — это ось виртуального устройства ({pad2.LX=0.5}), а не кнопка.
	Axis bool `json:"axis,omitempty"`
}

// Resolver находит устройство и код для ссылки на клавишу.
type Resolver interface {
	// Resolve возвращает цель для ссылки ref или *Error (например, ErrUnknownDevice).
	Resolve(ref KeyRef, pos Pos) (Target, error)
}

// DefaultResolver — клавиши без префикса идут на клавиатуру mKey, кнопки мыши — на мышь mKey.
// Префиксы устройств (pad2, Sega…) не знает: их понимает DeviceResolver (виртуальные устройства
// проектов и кнопки устройств с авто-ID).
type DefaultResolver struct{}

// Resolve находит цель для ссылки на клавишу.
func (DefaultResolver) Resolve(ref KeyRef, pos Pos) (Target, error) {
	// Устройства, кроме основных, этому резолверу неизвестны.
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

// KeyLookup находит кнопку устройства с авто-ID (FR-DEV-2): device — "UnKey2", button — "001".
// Возвращает код EV_KEY и имя для сообщений; ошибка — *Error.
type KeyLookup func(device, button string) (code uint16, name string, err error)

// VirtualLookup находит кнопку или ось виртуального устройства проекта ({pad2.South},
// {pad2.LX=0.5}, FR-VD-1): found = false — такого виртуального устройства нет (тогда имя
// ищется среди физических устройств); ошибка — *Error (например, нет такой кнопки).
type VirtualLookup func(device, control string) (code uint16, axis, found bool, err error)

// DeviceResolver — как DefaultResolver, но с устройствами: сначала виртуальные устройства проектов
// (Virtual — нажатия и оси идут на них), затем кнопки физических устройств ({UnKey001}) через
// Lookup: клавиши KEY_* нажимает виртуальная клавиатура mKey, кнопки мыши — мышь; остальные
// (кнопки джойстика) нажать нельзя — ErrCannotSend.
type DeviceResolver struct {
	Lookup  KeyLookup
	Virtual VirtualLookup
}

// Resolve находит цель для ссылки на клавишу.
func (r DeviceResolver) Resolve(ref KeyRef, pos Pos) (Target, error) {
	// Виртуальное устройство проекта.
	if ref.Device != "" && r.Virtual != nil {
		code, axis, found, err := r.Virtual(ref.Device, ref.Name)
		if found {
			if err != nil {
				return Target{}, withPos(err, pos, ref)
			}
			return Target{Device: strings.ToLower(ref.Device), Code: code, Name: formatKeyRef(ref), Axis: axis}, nil
		}
	}
	if ref.Device == "" || r.Lookup == nil {
		return DefaultResolver{}.Resolve(ref, pos)
	}

	// Кнопка устройства: ошибка поиска — с позицией в макросе.
	code, name, err := r.Lookup(ref.Device, ref.Name)
	if err != nil {
		var de *Error
		if errors.As(err, &de) {
			e := *de
			e.Pos = pos
			return Target{}, &e
		}
		return Target{}, newError(pos, ErrUnknownDevice, "device", ref.Device)
	}

	// Куда отправлять: клавиатура, мышь или пока никуда.
	kernel := ev.CodeName(ev.EvKey, code)
	switch {
	case strings.HasPrefix(kernel, "KEY_"):
		return Target{Device: DeviceKeyboard, Code: code, Name: name}, nil
	case code >= ev.BtnMouse && code < ev.BtnJoystick:
		return Target{Device: DeviceMouse, Code: code, Name: name}, nil
	}
	return Target{}, newError(pos, ErrCannotSend, "key", name, "kernel", kernel)
}

// withPos переносит ошибку поиска в место макроса: *Error получает позицию, другая ошибка
// становится «неизвестная кнопка устройства».
func withPos(err error, pos Pos, ref KeyRef) error {
	var de *Error
	if errors.As(err, &de) {
		e := *de
		e.Pos = pos
		return &e
	}
	return newError(pos, ErrUnknownButton, "device", ref.Device, "button", ref.Name)
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
	// StepAxis — поставить оси Targets в положение Value (−1…1, у курков 0…1).
	StepAxis StepKind = "axis"
	// StepTouch — коснуться сенсорного экрана Device в точке Points[0] на HoldMS (0 — по умолчанию).
	StepTouch StepKind = "touch"
	// StepSwipe — провести по сенсорному экрану Device от Points[0] до Points[1] за HoldMS.
	StepSwipe StepKind = "swipe"
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
	// Value — положение оси (axis).
	Value float64 `json:"value,omitempty"`
	// Device — сенсорный экран (touch, swipe): имя виртуального устройства; пусто — единственный.
	Device string `json:"device,omitempty"`
	// Points — точки касания (touch — одна, swipe — начало и конец).
	Points []Point `json:"points,omitempty"`
	// Body — вложенные шаги (loop).
	Body []Step `json:"body,omitempty"`
}

// Coord — координата точки экрана: процент ширины или высоты (Percent) или пиксели.
type Coord struct {
	Value   float64 `json:"value"`
	Percent bool    `json:"percent,omitempty"`
}

// Point — точка экрана.
type Point struct {
	X Coord `json:"x"`
	Y Coord `json:"y"`
}

// Compile превращает дерево разбора в план выполнения, находя устройства и коды клавиш.
// Команды, которые появятся позже (абсолютные координаты курсора), дают ErrNotSupported.
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
		// Ось нельзя нажать — ей задают положение.
		for _, t := range targets {
			if t.Axis {
				return Step{}, newError(n.Pos, ErrAxisAsKey, "name", t.Name)
			}
		}
		kind := map[Kind]StepKind{KindTap: StepTap, KindDown: StepPress, KindUp: StepRelease}[n.Kind]
		return Step{Kind: kind, Pos: n.Pos, Targets: targets, Count: n.times(), HoldMS: n.HoldMS}, nil
	case KindUpAll:
		return Step{Kind: StepReleaseAll, Pos: n.Pos}, nil
	case KindAxis:
		// Положение оси виртуального устройства ({pad2.LX=0.5}): цель должна быть осью.
		targets, err := resolveAll(n.Keys, n.Pos, r)
		if err != nil {
			return Step{}, err
		}
		for _, t := range targets {
			if !t.Axis {
				return Step{}, newError(n.Pos, ErrAxisExpected, "name", t.Name)
			}
		}
		return Step{Kind: StepAxis, Pos: n.Pos, Targets: targets, Value: n.Value}, nil
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
	usageTouch = "{Touch 50% 80%} / {Touch 50% 80% 500} / {Touch экран 960 860}"
	usageSwipe = "{Swipe 50% 80% 50% 20% 300}"
)

// Пределы касаний: координата в пикселях и длительность касания или свайпа.
const (
	maxPixel   = 100000
	maxTouchMS = 60000
	// defaultSwipeMS — длительность свайпа, если она не указана.
	defaultSwipeMS = 300
)

// touchArgs разбирает аргументы касания: необязательное имя устройства, points точек (по два
// числа: проценты 0…100 или пиксели без знака) и необязательную длительность в мс.
func touchArgs(args []Arg, points int) (device string, pts []Point, ms int64, ok bool) {
	// Имя сенсорного экрана — слово в начале.
	if len(args) > 0 && args[0].Word != "" {
		device, args = args[0].Word, args[1:]
	}
	if len(args) != points*2 && len(args) != points*2+1 {
		return "", nil, 0, false
	}

	// Координаты.
	coord := func(a Arg) (Coord, bool) {
		if a.Word != "" || a.Signed || a.Number < 0 {
			return Coord{}, false
		}
		if a.Percent {
			return Coord{Value: a.Number, Percent: true}, a.Number <= 100
		}
		return Coord{Value: a.Number}, a.Number == math.Trunc(a.Number) && a.Number <= maxPixel
	}
	for i := range points {
		x, okX := coord(args[i*2])
		y, okY := coord(args[i*2+1])
		if !okX || !okY {
			return "", nil, 0, false
		}
		pts = append(pts, Point{X: x, Y: y})
	}

	// Длительность (мс, целое без знака и процента).
	if len(args) == points*2+1 {
		a := args[points*2]
		v, okV := toInt32(a.Number)
		if a.Word != "" || a.Signed || a.Percent || !okV || v < 1 || v > maxTouchMS {
			return "", nil, 0, false
		}
		ms = int64(v)
	}
	return device, pts, ms, true
}

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

	// Касание сенсорного экрана: точка и необязательное удержание (долгое нажатие).
	case "Touch":
		dev, pts, ms, ok := touchArgs(n.Args, 1)
		if !ok {
			return bad(usageTouch)
		}
		return Step{Kind: StepTouch, Pos: n.Pos, Device: dev, Points: pts, HoldMS: ms}, nil

	// Свайп: от точки до точки за заданное время.
	case "Swipe":
		dev, pts, ms, ok := touchArgs(n.Args, 2)
		if !ok {
			return bad(usageSwipe)
		}
		if ms == 0 {
			ms = defaultSwipeMS
		}
		return Step{Kind: StepSwipe, Pos: n.Pos, Device: dev, Points: pts, HoldMS: ms}, nil
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
