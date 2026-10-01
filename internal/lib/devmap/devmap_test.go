package devmap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ev "mkey/internal/lib/evdev"
)

// caps составляет возможности устройства из кодов по типам.
func caps(codes map[uint16][]uint16) ev.Capabilities { return ev.Capabilities{Codes: codes} }

// Устройства, похожие на настоящие: по ним проверяются правила.
var (
	powerButton = caps(map[uint16][]uint16{ev.EvKey: {ev.KeyPower, ev.KeyWakeup}})
	videoBus    = caps(map[uint16][]uint16{ev.EvKey: {ev.KeySwitchvideomode, ev.KeyVideoNext, ev.KeyBrightnessCycle, ev.KeyDisplayOff}})
	keyboard    = caps(map[uint16][]uint16{ev.EvKey: {ev.KeyA, ev.KeyEnter, ev.KeyCalc, ev.KeyMail, ev.KeyPower}, ev.EvLed: {0}})
	touchpad    = caps(map[uint16][]uint16{ev.EvKey: {ev.BtnLeft, ev.BtnToolFinger, ev.BtnTouch, ev.BtnToolDoubletap}, ev.EvAbs: {ev.AbsX, ev.AbsPressure, ev.AbsMtSlot}})
	mouse       = caps(map[uint16][]uint16{ev.EvKey: {ev.BtnLeft, ev.BtnRight}, ev.EvRel: {ev.RelX, ev.RelY, ev.RelWheel, ev.RelWheelHiRes}})
	joystick    = caps(map[uint16][]uint16{ev.EvKey: {ev.BtnTrigger, ev.BtnThumb, 0x2c2}, ev.EvAbs: {ev.AbsX, ev.AbsThrottle}, ev.EvRel: {ev.RelDial}})
	jacks       = caps(map[uint16][]uint16{ev.EvSw: {2}})
	receiver    = caps(map[uint16][]uint16{ev.EvAbs: {ev.AbsMisc}})
)

// TestParseMode проверяет режимы: пусто — smart, неизвестный — ошибка.
func TestParseMode(t *testing.T) {
	t.Parallel()
	// Пробелы вокруг режима (ручная правка config.yaml) не мешают.
	for _, c := range []struct {
		in   string
		want Mode
	}{{"", ModeSmart}, {"smart", ModeSmart}, {" all ", ModeAll}, {"unusual", ModeUnusual}} {
		if got, err := ParseMode(c.in); err != nil || got != c.want {
			t.Errorf("ParseMode(%q) = %q, %v", c.in, got, err)
		}
	}
	if _, err := ParseMode("some"); !errors.Is(err, ErrMode) {
		t.Errorf("ParseMode(some) err = %v", err)
	}
}

// TestUnnamed проверяет, что получает номера: кнопки без имени (не признаки касания), оси без
// имени (не у сенсорных устройств), относительные оси кроме движения и колёс.
func TestUnnamed(t *testing.T) {
	t.Parallel()
	k, a, r := Unnamed(keyboard, []ev.Kind{ev.KindKeyboard})
	if len(k) != 3 || k[0] != ev.KeyPower || k[1] != ev.KeyCalc || k[2] != ev.KeyMail || a != nil || r != nil {
		t.Errorf("keyboard: %v %v %v", k, a, r)
	}
	if k, a, r := Unnamed(touchpad, []ev.Kind{ev.KindTouchpad}); k != nil || a != nil || r != nil {
		t.Errorf("touchpad: %v %v %v", k, a, r)
	}
	if k, a, r := Unnamed(mouse, []ev.Kind{ev.KindMouse}); k != nil || a != nil || r != nil {
		t.Errorf("mouse: %v %v %v", k, a, r)
	}
	k, a, r = Unnamed(joystick, []ev.Kind{ev.KindJoystick})
	// У кнопок джойстика (BTN_TRIGGER, BTN_THUMB) имён в mKey нет — номера получают все.
	if len(k) != 3 || k[0] != ev.BtnTrigger || k[2] != 0x2c2 || len(a) != 1 || a[0] != ev.AbsThrottle || len(r) != 1 || r[0] != ev.RelDial {
		t.Errorf("joystick: %v %v %v", k, a, r)
	}
}

// TestQualifies проверяет, кто получает авто-ID в каждом режиме.
func TestQualifies(t *testing.T) {
	t.Parallel()
	type dev struct {
		caps  ev.Capabilities
		kinds []ev.Kind
	}
	devs := map[string]dev{
		"power":    {powerButton, []ev.Kind{ev.KindOther}},
		"video":    {videoBus, []ev.Kind{ev.KindOther}},
		"keyboard": {keyboard, []ev.Kind{ev.KindKeyboard}},
		"touchpad": {touchpad, []ev.Kind{ev.KindTouchpad}},
		"mouse":    {mouse, []ev.Kind{ev.KindMouse}},
		"joystick": {joystick, []ev.Kind{ev.KindJoystick}},
		"jacks":    {jacks, []ev.Kind{ev.KindOther}},
		"receiver": {receiver, []ev.Kind{ev.KindOther}},
	}
	want := map[Mode][]string{
		ModeAll:     {"power", "video", "keyboard", "joystick", "receiver"},
		ModeSmart:   {"keyboard", "joystick"},
		ModeUnusual: {"joystick"},
	}
	for mode, names := range want {
		for name, d := range devs {
			got := Qualifies(mode, d.caps, d.kinds)
			exp := strings.Contains(" "+strings.Join(names, " ")+" ", " "+name+" ")
			if got != exp {
				t.Errorf("%s/%s: Qualifies = %v, want %v", mode, name, got, exp)
			}
		}
	}
}

// TestAddAndUpdate проверяет авто-ID (первый свободный) и номера: по возрастанию кодов,
// выданные не меняются, новые коды — следующими номерами.
func TestAddAndUpdate(t *testing.T) {
	t.Parallel()
	f := &File{Version: Version}
	info := ev.Info{Name: "Joy", ID: ev.ID{Vendor: 0x79, Product: 0x11}, Caps: joystick}
	d := f.Add(info, []ev.Kind{ev.KindJoystick}, ev.Links{ByPath: "pci-usb-0:2"})
	if d.AutoID != "UnKey" || d.Buttons["001"].Code != "BTN_TRIGGER" || d.Buttons["003"].Code != "BTN_TRIGGER_HAPPY3" || d.Axes["Axis01"].Code != "ABS_THROTTLE" ||
		d.Axes["Rel01"].Code != "REL_DIAL" || d.Match.Vid != "0079" || d.Match.ByPath != "pci-usb-0:2" {
		t.Fatalf("device = %+v", d)
	}

	// Второе и третье устройство — UnKey2, UnKey3; после удаления UnKey2 освободившееся имя выдаётся снова.
	f.Add(ev.Info{Name: "Kbd", Caps: keyboard}, []ev.Kind{ev.KindKeyboard}, ev.Links{})
	f.Add(ev.Info{Name: "Other", Caps: keyboard}, []ev.Kind{ev.KindKeyboard}, ev.Links{})
	if f.Devices[1].AutoID != "UnKey2" || f.Devices[2].AutoID != "UnKey3" {
		t.Fatalf("ids = %s %s", f.Devices[1].AutoID, f.Devices[2].AutoID)
	}
	kbd := f.Devices[1]
	if kbd.Buttons["001"].Code != "KEY_POWER" || kbd.Buttons["002"].Code != "KEY_CALC" || kbd.Buttons["003"].Code != "KEY_MAIL" {
		t.Errorf("keyboard buttons = %v", kbd.Buttons)
	}
	f.Devices = append(f.Devices[:1], f.Devices[2:]...)
	if id := f.Add(ev.Info{Name: "New", Caps: keyboard}, nil, ev.Links{}).AutoID; id != "UnKey2" {
		t.Errorf("reused id = %s", id)
	}

	// Новая кнопка (меньший код) не сдвигает выданные номера, а получает следующий.
	info.Caps = caps(map[uint16][]uint16{ev.EvKey: {ev.KeyCalc, 0x2c2}, ev.EvAbs: {ev.AbsThrottle}, ev.EvRel: {ev.RelDial}})
	if !f.Devices[0].Update(info, []ev.Kind{ev.KindJoystick}) {
		t.Fatal("update: no change")
	}
	// Пропавшие кнопки (BTN_TRIGGER, BTN_THUMB) остаются в файле со своими номерами.
	if b := f.Devices[0].Buttons; b["001"].Code != "BTN_TRIGGER" || b["003"].Code != "BTN_TRIGGER_HAPPY3" || b["004"].Code != "KEY_CALC" || len(b) != 4 {
		t.Errorf("updated buttons = %v", b)
	}
	if f.Devices[0].Update(info, []ev.Kind{ev.KindJoystick}) {
		t.Error("second update changed something")
	}
}

// TestFind проверяет узнавание после переподключения (FR-DEV-6): серийный номер, порт,
// смена порта у единственного устройства, два одинаковых устройства.
func TestFind(t *testing.T) {
	t.Parallel()
	m := func(uniq, port string) Match {
		return Match{Vid: "0079", Pid: "0011", Version: "0110", Name: "USB Gamepad", Uniq: uniq, ByPath: port}
	}
	f := &File{Devices: []Device{
		{AutoID: "UnKey", Match: m("", "port-1")},
		{AutoID: "UnKey2", Match: m("", "port-2")},
		{AutoID: "UnKey3", Match: m("SN42", "port-9")},
	}}
	cases := []struct {
		name string
		m    Match
		busy map[string]bool
		want string
	}{
		{"same port", m("", "port-2"), nil, "UnKey2"},
		{"serial wins over port", m("SN42", "port-1"), nil, "UnKey3"},
		{"unknown serial", m("SN43", "port-9"), nil, ""},
		{"two identical, new port — ambiguous", m("", "port-5"), nil, ""},
		{"one free left — port changed", m("", "port-5"), map[string]bool{"UnKey": true}, "UnKey2"},
		{"busy entry is skipped", m("", "port-1"), map[string]bool{"UnKey": true}, "UnKey2"},
		{"other model", Match{Vid: "1234", Pid: "0011", Name: "USB Gamepad"}, nil, ""},
		{"version change", Match{Vid: "0079", Pid: "0011", Version: "0200", Name: "USB Gamepad", ByPath: "port-1"}, nil, ""},
	}
	for _, c := range cases {
		got := ""
		if d := f.Find(c.m, c.busy); d != nil {
			got = d.AutoID
		}
		if got != c.want {
			t.Errorf("%s: Find = %q, want %q", c.name, got, c.want)
		}
	}

	// Поиск по имени: авто-ID и имя человека, без учёта регистра.
	f.Devices[1].Name = "Sega"
	if f.Lookup("unkey2") != &f.Devices[1] || f.Lookup("SEGA") != &f.Devices[1] || f.Lookup("UnKey9") != nil {
		t.Error("Lookup")
	}
}

// TestSaveLoad проверяет запись и чтение файла: пояснение в начале, номера по порядку, то же содержимое.
func TestSaveLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sub", FileName)

	// Нет файла — пустой список.
	f, err := Load(path)
	if err != nil || len(f.Devices) != 0 || f.Version != Version {
		t.Fatalf("empty: %+v, %v", f, err)
	}

	// Запись и чтение.
	f.Add(ev.Info{Name: "Joy", Caps: joystick}, []ev.Kind{ev.KindJoystick}, ev.Links{})
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.HasPrefix(text, "# Устройства mKey") || !strings.Contains(text, "auto_id: UnKey\n") || !strings.Contains(text, `"001":`) {
		t.Errorf("file:\n%s", text)
	}
	g, err := Load(path)
	if err != nil || len(g.Devices) != 1 || g.Devices[0].Buttons["003"].Code != "BTN_TRIGGER_HAPPY3" {
		t.Fatalf("load: %+v, %v", g, err)
	}

	// Ошибки: испорченный файл, файл новее программы.
	_ = os.WriteFile(path, []byte("devices: [oops"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("broken file: no error")
	}
	_ = os.WriteFile(path, []byte("version: 9\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("future version: no error")
	}
}

// TestRefs проверяет запись кнопок в макросах: слитная форма только у UnKey и трёх цифр, разбор
// слитной формы (последние три цифры — кнопка).
func TestRefs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ dev, ctl, want string }{
		{"UnKey", "001", "UnKey001"}, {"unkey", "001", "unkey001"}, {"UnKey2", "001", "UnKey2.001"},
		{"UnKey", "Axis01", "UnKey.Axis01"}, {"Sega", "001", "Sega.001"}, {"UnKey", "01", "UnKey.01"},
	} {
		if got := Ref(c.dev, c.ctl); got != c.want {
			t.Errorf("Ref(%q, %q) = %q, want %q", c.dev, c.ctl, got, c.want)
		}
	}
	for _, c := range []struct {
		in, dev, btn string
		ok           bool
	}{
		{"UnKey001", "UnKey", "001", true}, {"unkey2001", "UnKey2", "001", true}, {"UNKEY12034", "UnKey12", "034", true},
		{"UnKey01", "", "", false}, {"UnKey", "", "", false}, {"Sega001", "", "", false}, {"UnKeyA001", "", "", false},
	} {
		dev, btn, ok := SplitJoined(c.in)
		if dev != c.dev || btn != c.btn || ok != c.ok {
			t.Errorf("SplitJoined(%q) = %q %q %v", c.in, dev, btn, ok)
		}
	}
}
