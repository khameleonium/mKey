package evdev

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestIoctlNumbers сверяет вычисленные номера ioctl с известными значениями из заголовков ядра
// (посчитаны на amd64: например, EVIOCGRAB = 0x40044590).
func TestIoctlNumbers(t *testing.T) {
	t.Parallel()

	// Таблица: имя, вычисленное значение, эталон.
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"EVIOCGVERSION", eviocgversion, 0x80044501},
		{"EVIOCGID", eviocgid, 0x80084502},
		{"EVIOCGRAB", eviocgrab, 0x40044590},
		{"EVIOCGNAME(256)", eviocgname(256), 0x81004506},
		{"EVIOCGBIT(EV_KEY,96)", eviocgbit(EvKey, 96), 0x80604521},
		{"EVIOCGABS(ABS_X)", eviocgabs(AbsX), 0x80184540},
		{"UI_DEV_CREATE", uiDevCreate, 0x5501},
		{"UI_DEV_DESTROY", uiDevDestroy, 0x5502},
		{"UI_DEV_SETUP", uiDevSetup, 0x405c5503},
		{"UI_ABS_SETUP", uiAbsSetup, 0x401c5504},
		{"UI_SET_EVBIT", uiSetEvBit, 0x40045564},
		{"UI_SET_KEYBIT", uiSetKeyBit, 0x40045565},
		{"UI_SET_PHYS", uiSetPhys, 0x4008556c},
		{"UI_SET_PROPBIT", uiSetPropBit, 0x4004556e},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

// TestEventRoundTrip проверяет кодирование и разбор события в формате ядра.
func TestEventRoundTrip(t *testing.T) {
	t.Parallel()

	// Кодируем событие и проверяем размер.
	in := Event{Time: time.Unix(1700000000, 123456000), Type: EvKey, Code: KeyA, Value: ValueDown}
	buf, err := in.MarshalBinary()
	if err != nil || len(buf) != EventSize {
		t.Fatalf("MarshalBinary: len %d, err %v", len(buf), err)
	}

	// Разбираем обратно и сравниваем.
	var out Event
	if err := out.UnmarshalBinary(buf); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if !out.Time.Equal(in.Time) || out.Type != in.Type || out.Code != in.Code || out.Value != in.Value {
		t.Fatalf("round trip: got %+v, want %+v", out, in)
	}

	// Короткий буфер — ошибка.
	if err := out.UnmarshalBinary(buf[:10]); err == nil {
		t.Fatal("short buffer must fail")
	}
}

// TestNames проверяет читаемые имена типов, кодов и свойств.
func TestNames(t *testing.T) {
	t.Parallel()
	if got := (Event{Type: EvKey, Code: KeyA, Value: 1}).String(); got != "EV_KEY KEY_A 1" {
		t.Errorf("String() = %q", got)
	}
	if got := CodeName(EvKey, BtnSouth); got != "BTN_SOUTH" {
		t.Errorf("CodeName(BTN_SOUTH) = %q (canonical name expected, not alias BTN_A)", got)
	}
	if got := CodeName(EvKey, 0x2c2); got != "BTN_TRIGGER_HAPPY3" {
		t.Errorf("CodeName(0x2c2) = %q", got)
	}
	if got := CodeName(EvRel, 0x7ff); got != "0x7ff" {
		t.Errorf("unknown code name = %q", got)
	}
	if got := PropName(InputPropDirect); got != "INPUT_PROP_DIRECT" {
		t.Errorf("PropName = %q", got)
	}

	// Отдача — по своей таблице (её нет в input-event-codes.h); тот же код другого типа — не отдача.
	if got := CodeName(EvFf, 0x50); got != "FF_RUMBLE" {
		t.Errorf("CodeName(FF_RUMBLE) = %q", got)
	}
	if got := CodeName(EvSnd, 0x50); got == "FF_RUMBLE" {
		t.Errorf("CodeName(EV_SND 0x50) = %q", got)
	}

	// Разбор имён: каноническое имя, отдача, шестнадцатеричный код; неизвестное — нет.
	for _, c := range []struct {
		t    uint16
		name string
		code uint16
		ok   bool
	}{
		{EvKey, "KEY_CALC", 0x8c, true}, {EvKey, "BTN_TRIGGER_HAPPY3", 0x2c2, true}, {EvFf, "FF_RUMBLE", 0x50, true},
		{EvAbs, "ABS_X", 0, true}, {EvKey, "0x2c2", 0x2c2, true}, {EvKey, "KEY_NOPE", 0, false}, {EvKey, "0xzz", 0, false},
	} {
		if code, ok := ParseCode(c.t, c.name); code != c.code || ok != c.ok {
			t.Errorf("ParseCode(%q) = %#x, %v", c.name, code, ok)
		}
	}

	// Шины: известные — словом, неизвестные — кодом.
	if BusName(0x03) != "USB" || BusName(0x11) != "i8042" || BusName(0x42) != "0x42" {
		t.Errorf("BusName = %q %q %q", BusName(0x03), BusName(0x11), BusName(0x42))
	}
}

// TestBitsToCodes проверяет разбор битовой маски ядра.
func TestBitsToCodes(t *testing.T) {
	t.Parallel()
	// Биты 0, 3 и 9 установлены.
	got := bitsToCodes([]byte{0b0000_1001, 0b0000_0010})
	if !slices.Equal(got, []uint16{0, 3, 9}) {
		t.Fatalf("bitsToCodes = %v", got)
	}
}

// TestSetupBytes проверяет раскладку struct uinput_setup и uinput_abs_setup.
func TestSetupBytes(t *testing.T) {
	t.Parallel()

	// uinput_setup: VID/PID в little-endian, имя с 8-го байта.
	b := devSetupBytes(Setup{Name: "mKey Test", ID: ID{Bustype: BusVirtual, Vendor: 0x045e, Product: 0x028e, Version: 1}})
	if len(b) != sizeofUinputSetup || b[0] != 0x06 || b[2] != 0x5e || b[3] != 0x04 || string(b[8:17]) != "mKey Test" || b[17] != 0 {
		t.Fatalf("devSetupBytes = %v", b[:20])
	}

	// uinput_abs_setup: код, затем min/max со смещением 4.
	a := absSetupBytes(AbsX, AbsInfo{Minimum: -32768, Maximum: 32767})
	if len(a) != sizeofUinputAbsSetup || a[0] != 0 || a[8] != 0x00 || a[9] != 0x80 || a[12] != 0xff || a[13] != 0x7f {
		t.Fatalf("absSetupBytes = %v", a)
	}
}

// TestClassify проверяет эвристику классификации на типичных устройствах.
func TestClassify(t *testing.T) {
	t.Parallel()

	// caps собирает возможности из пар «тип → коды».
	caps := func(codes map[uint16][]uint16, props ...uint16) Capabilities {
		return Capabilities{Codes: codes, Props: props}
	}

	// Таблица: описание, возможности, ожидаемые классы.
	cases := []struct {
		name string
		caps Capabilities
		want []Kind
	}{
		{"keyboard", caps(map[uint16][]uint16{EvKey: {KeyA, KeyZ, KeySpace}}), []Kind{KindKeyboard}},
		{"mouse", caps(map[uint16][]uint16{EvKey: {BtnLeft}, EvRel: {RelX, RelY}}), []Kind{KindMouse}},
		{"touchpad", caps(map[uint16][]uint16{EvKey: {BtnToolFinger, BtnTouch}, EvAbs: {AbsX, AbsY, AbsMtPositionX}}, InputPropPointer), []Kind{KindTouchpad}},
		{"touchscreen", caps(map[uint16][]uint16{EvKey: {BtnTouch}, EvAbs: {AbsMtPositionX}}, InputPropDirect), []Kind{KindTouchscreen}},
		{"tablet", caps(map[uint16][]uint16{EvKey: {BtnToolPen, BtnTouch}, EvAbs: {AbsX, AbsY}}), []Kind{KindTablet}},
		{"gamepad", caps(map[uint16][]uint16{EvKey: {BtnSouth, BtnEast}, EvAbs: {AbsX, AbsY}}), []Kind{KindGamepad}},
		{"sega-like joystick", caps(map[uint16][]uint16{EvKey: {BtnTrigger, BtnThumb}, EvAbs: {AbsX, AbsY}}), []Kind{KindJoystick}},
		{"power button", caps(map[uint16][]uint16{EvKey: {KeyPower}}), []Kind{KindOther}},
	}
	for _, c := range cases {
		if got := Classify(c.caps); !slices.Equal(got, c.want) {
			t.Errorf("%s: Classify = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestParseProcDevices проверяет разбор /proc/bus/input/devices.
func TestParseProcDevices(t *testing.T) {
	t.Parallel()

	// Фрагмент реального файла: клавиатура и устройство без обработчика evdev.
	src := `I: Bus=0003 Vendor=046d Product=c52b Version=0111
N: Name="Logitech USB Receiver"
P: Phys=usb-0000:00:14.0-2/input0
S: Sysfs=/devices/pci0000:00/0000:00:14.0/usb1/1-2/1-2:1.0/0003:046D:C52B.0001/input/input3
U: Uniq=
H: Handlers=sysrq kbd leds event3
B: EV=120013

I: Bus=0019 Vendor=0000 Product=0001 Version=0000
N: Name="Power Button"
H: Handlers=kbd`
	devs, err := ParseProcDevices(strings.NewReader(src))
	if err != nil || len(devs) != 2 {
		t.Fatalf("ParseProcDevices: %d devices, err %v", len(devs), err)
	}

	// Проверяем поля первого устройства.
	d := devs[0]
	if d.Name != "Logitech USB Receiver" || d.ID.String() != "046d:c52b" || d.ID.Bustype != 3 || d.EventPath() != "/dev/input/event3" {
		t.Errorf("device 0 = %+v", d)
	}

	// У второго устройства нет evdev-обработчика.
	if devs[1].EventPath() != "" {
		t.Errorf("device 1 EventPath = %q", devs[1].EventPath())
	}
}

// TestNoBitQueryForRepeat защищает от регрессии: запрос EVIOCGBIT(EV_REP) ядро отвергает (EINVAL),
// и из-за него не открывались клавиатуры (у всех настоящих клавиатур есть автоповтор).
func TestNoBitQueryForRepeat(t *testing.T) {
	t.Parallel()
	if _, ok := maxCodes[EvRep]; ok {
		t.Fatal("EV_REP must not be queried with EVIOCGBIT")
	}
}
