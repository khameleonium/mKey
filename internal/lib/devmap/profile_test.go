package devmap

import (
	"errors"
	"strings"
	"testing"

	ev "mkey/internal/lib/evdev"
)

// sega — профиль джойстика из примера ADR-0027.
const sega = `
version: 1
title: Sega USB Joystick
match: { vid: "0079", pid: "0011" }
device_name: Sega
buttons: { BTN_TRIGGER: A, BTN_THUMB: B, BTN_TRIGGER_HAPPY3: Start }
axes: { ABS_THROTTLE: Gas }
`

// TestParseProfile проверяет разбор профиля и его ошибки.
func TestParseProfile(t *testing.T) {
	t.Parallel()
	p, err := ParseProfile([]byte(sega))
	if err != nil || p.Match.Vid != "0079" || p.Buttons["BTN_THUMB"] != "B" || p.DeviceName != "Sega" {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	for _, bad := range []string{
		"version: 9\nmatch: {vid: \"0079\", pid: \"0011\"}",
		"match: {vid: \"79\", pid: \"0011\"}",
		"match: {vid: \"0079\", pid: \"zzzz\"}",
		"match: {vid: \"0079\", pid: \"0011\"}\nbuttons: {BTN_NOPE: A}",
		"match: {vid: \"0079\", pid: \"0011\"}\nbuttons: {BTN_TRIGGER: 1a}",
		"match: {vid: \"0079\", pid: \"0011\"}\nbuttons: {BTN_TRIGGER: A, BTN_THUMB: a}",
		"match: {vid: \"0079\", pid: \"0011\"}\ndevice_name: \"Sega 2\"",
		"match: [oops",
	} {
		if _, err := ParseProfile([]byte(bad)); !errors.Is(err, ErrProfile) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// TestProfileApply проверяет подстановку: только пустые имена, по кодам; совпадение модели; экспорт.
func TestProfileApply(t *testing.T) {
	t.Parallel()
	p, _ := ParseProfile([]byte(sega))
	f := &File{Version: Version}
	info := ev.Info{Name: "USB Gamepad", ID: ev.ID{Vendor: 0x79, Product: 0x11}, Caps: joystick}
	d := f.Add(info, []ev.Kind{ev.KindJoystick}, ev.Links{})

	// Модель: vid/pid; если в профиле есть название — и оно.
	if !p.Matches(d.Match) || p.Matches(Match{Vid: "0079", Pid: "0012"}) {
		t.Error("Matches by vid/pid")
	}
	named := *p
	named.Match.Name = "Other"
	if named.Matches(d.Match) {
		t.Error("Matches with another name")
	}

	// Имя кнопки, данное человеком, не меняется; остальные подставляются.
	_ = d.SetButtonName("002", "Огонь")
	if !p.Apply(f, d) {
		t.Fatal("Apply: no change")
	}
	if d.Name != "Sega" || d.Buttons["001"].Name != "A" || d.Buttons["002"].Name != "Огонь" || d.Buttons["003"].Name != "Start" || d.Axes["Axis01"].Name != "Gas" {
		t.Errorf("applied: %+v", d)
	}
	if p.Apply(f, d) {
		t.Error("second Apply changed something")
	}

	// Занятое имя устройства пропускается, кнопки всё равно получают имена.
	d2 := f.Add(info, []ev.Kind{ev.KindJoystick}, ev.Links{})
	if !p.Apply(f, d2) || d2.Name != "" || d2.Buttons["001"].Name != "A" {
		t.Errorf("second device: %+v", d2)
	}

	// Экспорт: имена человека и профиля, по кодам; файл снова читается.
	out, err := MarshalProfile(ExportProfile(d, "Мой джойстик"))
	if err != nil || !strings.HasPrefix(string(out), "# Профиль устройства mKey") || !strings.Contains(string(out), "BTN_THUMB: Огонь") {
		t.Fatalf("export:\n%s %v", out, err)
	}
	back, err := ParseProfile(out)
	if err != nil || back.Title != "Мой джойстик" || back.DeviceName != "Sega" || back.Axes["ABS_THROTTLE"] != "Gas" {
		t.Errorf("round trip: %+v %v", back, err)
	}
}
