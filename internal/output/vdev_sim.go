package output

import (
	"strconv"
	"strings"

	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Шаблоны для симуляторов (FR-VD-6, FR-VD-7, ADR-0039): руль с педалями и лётный джойстик.
// У них свои понятные имена кнопок и осей ({wheel.Gas}, {stick.Throttle}), потому что общие имена
// геймпада (LX, LT…) им не подходят.

// control — кнопка или ось шаблона под понятным именем: имя для макросов, код в кодах макросов
// (кнопка — код устройства или кнопки-оси, ось — код оси) и признак оси.
type control struct {
	name string
	code uint16
	axis bool
}

// Имена кнопок руля по порядку кнопок популярной модели (046d:c24f, режим PS3): так их
// нумеруют драйвер и игры (кнопка 1 — Cross, 5 — правый лепесток…; docs/adr/0039).
var wheelButtonNames = []string{
	"Cross", "Square", "Circle", "Triangle", "ShiftUp", "ShiftDown", "R2", "L2", "Share", "Options",
	"R3", "L3", "Gear1", "Gear2", "Gear3", "Gear4", "Gear5", "Gear6", "Reverse", "Plus", "Minus",
	"DialRight", "DialLeft", "Enter", "PS",
}

// joyButtons — первые n кнопок джойстика: BTN_JOYSTICK…BTN_DEAD (16), затем
// BTN_TRIGGER_HAPPY1…40 — так их понимают драйвер joydev, игровые библиотеки и слой совместимости.
func joyButtons(n int) []uint16 {
	var out []uint16
	for c := uint16(ev.BtnJoystick); c <= ev.BtnDead && len(out) < n; c++ {
		out = append(out, c)
	}
	for c := uint16(ev.BtnTriggerHappy1); c <= ev.BtnTriggerHappy40 && len(out) < n; c++ {
		out = append(out, c)
	}
	return out
}

// wheelAxes — оси руля: поворот 0…65535 (центр 32768), педали 0…255. Отпущенная педаль у этой
// модели — максимум (255), нажатая до упора — 0; игры ждут именно этого.
var (
	wheelTurn = ev.AbsInfo{Minimum: 0, Maximum: 65535, Value: 32768}
	pedal     = ev.AbsInfo{Minimum: 0, Maximum: 255, Value: 255}
)

// wheelSetup описывает руль с педалями: удаётся за популярную модель (USB 046d:c24f), чтобы игры
// узнали в нём руль; обратная связь руля — запросы эффектов принимаются (FR-VD-5, FR-VD-6).
func wheelSetup(project.VirtualDevice) (ev.Setup, error) {
	return ev.Setup{
		ID:   ev.ID{Bustype: ev.BusUSB, Vendor: 0x046d, Product: 0xc24f, Version: 0x0111},
		Keys: joyButtons(len(wheelButtonNames)),
		Abs: map[uint16]ev.AbsInfo{ev.AbsX: wheelTurn, ev.AbsY: pedal, ev.AbsZ: pedal, ev.AbsRz: pedal,
			ev.AbsHat0x: hat, ev.AbsHat0y: hat},
		FF:           wheelFF,
		FFEffectsMax: 32,
	}, nil
}

// wheelFF — эффекты обратной связи, которые руль объявляет играм: постоянная сила, пружина,
// трение, демпфер, инерция, нарастание, вибрации (периодические) и общие настройки (сила,
// автоцентрирование). Коды — linux/input.h (FF_CONSTANT 0x52 … FF_AUTOCENTER 0x61).
var wheelFF = []uint16{0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x60, 0x61}

// wheelControls — имена руля: поворот, педали, крестовина и кнопки по порядку.
func wheelControls() []control {
	out := []control{
		{name: "Wheel", code: ev.AbsX, axis: true},
		{name: "Gas", code: ev.AbsZ, axis: true},
		{name: "Brake", code: ev.AbsRz, axis: true},
		{name: "Clutch", code: ev.AbsY, axis: true},
		{name: "DPadX", code: ev.AbsHat0x, axis: true},
		{name: "DPadY", code: ev.AbsHat0y, axis: true},
	}
	for i, c := range joyButtons(len(wheelButtonNames)) {
		out = append(out, control{name: wheelButtonNames[i], code: c})
	}
	return out
}

// flightButtons — сколько кнопок у лётного джойстика: все 56 кнопочных кодов джойстика.
const flightButtons = 56

// Оси лётного джойстика: ручка и прочие с центром — как у стика геймпада; РУД и ползунок — 0…65535
// от минимума (РУД «на себя» — 0).
var flightLever = ev.AbsInfo{Minimum: 0, Maximum: 65535}

// flightSetup описывает лётный джойстик: ручка (наклон X/Y, поворот RZ), РУД (THROTTLE), педали руля
// направления (RUDDER), ползунок (Z), мини-стик (RX/RY), 4 шляпки и 56 кнопок (FR-VD-7). Свой
// идентификатор mKey: симуляторы принимают любой джойстик и дают назначить его оси сами.
func flightSetup(project.VirtualDevice) (ev.Setup, error) {
	abs := map[uint16]ev.AbsInfo{ev.AbsX: stick, ev.AbsY: stick, ev.AbsRz: stick, ev.AbsRx: stick,
		ev.AbsRy: stick, ev.AbsRudder: stick, ev.AbsThrottle: flightLever, ev.AbsZ: flightLever}
	for c := uint16(ev.AbsHat0x); c <= ev.AbsHat3y; c++ {
		abs[c] = hat
	}
	return ev.Setup{
		ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0013, Version: 1},
		Keys: joyButtons(flightButtons),
		Abs:  abs,
	}, nil
}

// flightControls — имена лётного джойстика: оси, шляпки (Hat1 — ещё и кнопками DPadUp…), кнопки
// Button1…Button56 (Button1 — ещё и Trigger, гашетка).
func flightControls() []control {
	out := []control{
		{name: "StickX", code: ev.AbsX, axis: true},
		{name: "StickY", code: ev.AbsY, axis: true},
		{name: "Twist", code: ev.AbsRz, axis: true},
		{name: "Throttle", code: ev.AbsThrottle, axis: true},
		{name: "Rudder", code: ev.AbsRudder, axis: true},
		{name: "Slider", code: ev.AbsZ, axis: true},
		{name: "MiniX", code: ev.AbsRx, axis: true},
		{name: "MiniY", code: ev.AbsRy, axis: true},
	}
	for i := range 4 {
		n := strconv.Itoa(i + 1)
		out = append(out,
			control{name: "Hat" + n + "X", code: uint16(ev.AbsHat0x + 2*i), axis: true},
			control{name: "Hat" + n + "Y", code: uint16(ev.AbsHat0y + 2*i), axis: true})
	}
	out = append(out, control{name: "Trigger", code: ev.BtnJoystick})
	for i, c := range joyButtons(flightButtons) {
		out = append(out, control{name: "Button" + strconv.Itoa(i+1), code: c})
	}
	return out
}

// lookupControl находит кнопку или ось шаблона по имени без учёта регистра.
func (t vtemplate) lookupControl(name string) (control, bool) {
	for _, c := range t.controls {
		if strings.EqualFold(c.name, name) {
			return c, true
		}
	}
	return control{}, false
}
