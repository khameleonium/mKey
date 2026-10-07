package devmap

import (
	"errors"
	"testing"

	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// gamepadNames — имена кнопок геймпада, как их хранит проект.
var gamepadNames = Names{
	Match: NamesMatch{Vid: "0079", Pid: "0011"}, Name: "Геймпад",
	Buttons: map[string]string{"BTN_TRIGGER": "A", "BTN_THUMB": "B", "BTN_TRIGGER_HAPPY3": "Старт"},
	Axes:    map[string]string{"ABS_THROTTLE": "Газ"},
}

// TestNamesValidate проверяет проверку имён: правильные и все виды ошибок.
func TestNamesValidate(t *testing.T) {
	t.Parallel()
	if err := gamepadNames.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]Names{
		"short vid":    {Match: NamesMatch{Vid: "79", Pid: "0011"}},
		"bad pid":      {Match: NamesMatch{Vid: "0079", Pid: "zzzz"}},
		"unknown code": {Match: gamepadNames.Match, Buttons: map[string]string{"BTN_NOPE": "A"}},
		"bad name":     {Match: gamepadNames.Match, Buttons: map[string]string{"BTN_TRIGGER": "1a"}},
		"twice":        {Match: gamepadNames.Match, Buttons: map[string]string{"BTN_TRIGGER": "A", "BTN_THUMB": "a"}},
		"device name":  {Match: gamepadNames.Match, Name: "Гейм пад"},
	} {
		if err := n.Validate(); !errors.Is(err, ErrNames) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestNamesApply проверяет подстановку (только пустые имена, по кодам), модель и сбор имён записи.
func TestNamesApply(t *testing.T) {
	t.Parallel()
	f := &File{Version: Version}
	info := ev.Info{Name: "USB Gamepad", ID: ev.ID{Vendor: 0x79, Product: 0x11}, Caps: joystick}
	d := f.Add(info, []ev.Kind{ev.KindJoystick}, ev.Links{})

	// Модель: vid/pid без учёта регистра; если указано название — и оно.
	if !gamepadNames.Matches(d.Match) || gamepadNames.Matches(Match{Vid: "0079", Pid: "0012"}) {
		t.Error("Matches by vid/pid")
	}
	named := gamepadNames
	named.Match.Name = "Other"
	if named.Matches(d.Match) {
		t.Error("Matches with another name")
	}

	// Имя кнопки, данное человеком, не меняется; остальные подставляются; повтор ничего не меняет.
	_ = d.SetButtonName("002", "Огонь")
	if !gamepadNames.Apply(f, d) || gamepadNames.Apply(f, d) {
		t.Fatal("Apply: want change once")
	}
	if d.Name != "Геймпад" || d.Buttons["001"].Name != "A" || d.Buttons["002"].Name != "Огонь" || d.Buttons["003"].Name != "Старт" || d.Axes["Axis01"].Name != "Газ" {
		t.Errorf("applied: %+v", d)
	}

	// Занятое имя устройства пропускается, кнопки всё равно получают имена.
	d2 := f.Add(info, []ev.Kind{ev.KindJoystick}, ev.Links{})
	if !gamepadNames.Apply(f, d2) || d2.Name != "" || d2.Buttons["001"].Name != "A" {
		t.Errorf("second device: %+v", d2)
	}

	// Сбор имён записи: модель, имя устройства, имена кнопок по кодам; без имён — false.
	n, ok := NamesOf(d)
	if !ok || n.Match.Vid != "0079" || n.Name != "Геймпад" || n.Buttons["BTN_THUMB"] != "Огонь" || n.Axes["ABS_THROTTLE"] != "Газ" || n.Validate() != nil {
		t.Errorf("NamesOf: %+v %v", n, ok)
	}
	if _, ok := NamesOf(f.Add(info, nil, ev.Links{})); ok {
		t.Error("NamesOf of a device without names: ok")
	}
}
