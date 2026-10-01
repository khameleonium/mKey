package inspector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/devmap"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/registry"
)

// TestLifecycle проверяет, что модуль проходит полный цикл Init → Start → Stop.
func TestLifecycle(t *testing.T) {
	t.Parallel()

	// Собираем менеджер с одним этим модулем.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	m, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: New()}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Запускаем и останавливаем; модуль должен быть в состоянии running, затем stopped.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := m.Statuses()[0]; st.State != registry.StateRunning {
		t.Fatalf("state = %s, err = %v", st.State, st.Err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// fakeInput — источник с заданным списком устройств (остальные методы не нужны инспектору).
type fakeInput struct {
	contracts.InputSource
	devs []contracts.InputDevice
}

// Devices возвращает заданные устройства.
func (f fakeInput) Devices() []contracts.InputDevice { return f.devs }

// testModule — инспектор с клавиатурой, мышью и геймпадом; у мыши есть постоянные имена.
func testModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	for name, target := range map[string]string{"by-id/usb-Logitech_Mouse-event-mouse": "../event6", "by-path/pci-usb-0:3.2-event-mouse": "../event6"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	dev := func(event, name string, caps ev.Capabilities, kinds ...ev.Kind) contracts.InputDevice {
		return contracts.InputDevice{Info: ev.Info{Path: filepath.Join(dir, event), Name: name, Caps: caps}, Kinds: kinds}
	}
	return &Module{cfg: Config{InputDir: dir}, input: fakeInput{devs: []contracts.InputDevice{
		dev("event3", "AT Keyboard", ev.Capabilities{Codes: map[uint16][]uint16{ev.EvKey: {ev.KeyA}, ev.EvLed: {0}}}, ev.KindKeyboard),
		dev("event6", "Logitech USB Optical Mouse", ev.Capabilities{
			Codes: map[uint16][]uint16{ev.EvKey: {ev.BtnLeft}, ev.EvRel: {ev.RelX, ev.RelWheel}},
			Props: []uint16{0},
		}, ev.KindMouse),
		dev("event9", "USB Gamepad", ev.Capabilities{
			Codes: map[uint16][]uint16{ev.EvKey: {ev.BtnSouth, 0x2c2}, ev.EvAbs: {ev.AbsX}},
			Abs:   map[uint16]ev.AbsInfo{ev.AbsX: {Minimum: -32768, Maximum: 32767, Flat: 128}},
		}, ev.KindGamepad),
	}}}
}

// TestDetails проверяет подробности: имена mKey и ядра, оси с диапазоном, постоянные имена.
func TestDetails(t *testing.T) {
	t.Parallel()
	devs := testModule(t).Devices()
	if len(devs) != 3 {
		t.Fatalf("devices = %d", len(devs))
	}
	kbd, mouse, pad := devs[0], devs[1], devs[2]

	// Клавиатура: клавиша A и индикатор.
	if len(kbd.Keys) != 1 || kbd.Keys[0].Name != "A" || kbd.Keys[0].Kernel != "KEY_A" || len(kbd.LEDs) != 1 || kbd.LEDs[0].Kernel != "LED_NUML" {
		t.Errorf("keyboard = %+v", kbd)
	}

	// Мышь: левая кнопка — Mouse0, оси движения и колеса, постоянные имена и свойство.
	if mouse.Keys[0].Name != "Mouse0" || mouse.Bus != "0x00" || len(mouse.Rel) != 2 || mouse.Rel[1].Kernel != "REL_WHEEL" ||
		mouse.ByID != "usb-Logitech_Mouse-event-mouse" || mouse.ByPath != "pci-usb-0:3.2-event-mouse" || mouse.Props[0] != "INPUT_PROP_POINTER" {
		t.Errorf("mouse = %+v", mouse)
	}

	// Геймпад: South с именем, BTN_TRIGGER_HAPPY3 без имени (будет авто-ID), ось с диапазоном.
	if pad.Keys[0].Name != "South" || pad.Keys[1].Name != "" || pad.Keys[1].Kernel != "BTN_TRIGGER_HAPPY3" {
		t.Errorf("gamepad keys = %+v", pad.Keys)
	}
	if len(pad.Axes) != 1 || pad.Axes[0].Name != "LX" || pad.Axes[0].Minimum != -32768 || pad.Axes[0].Flat != 128 {
		t.Errorf("gamepad axes = %+v", pad.Axes)
	}
}

// TestFind проверяет поиск: путь, имя файла и постоянное имя — точно, иначе часть названия.
func TestFind(t *testing.T) {
	t.Parallel()
	m := testModule(t)
	mousePath := m.input.Devices()[1].Info.Path
	cases := []struct {
		ref  string
		want []string // имена найденных устройств
	}{
		{mousePath, []string{"Logitech USB Optical Mouse"}},
		{"event6", []string{"Logitech USB Optical Mouse"}},
		{"usb-Logitech_Mouse-event-mouse", []string{"Logitech USB Optical Mouse"}},
		{"pci-usb-0:3.2-event-mouse", []string{"Logitech USB Optical Mouse"}},
		{"logitech", []string{"Logitech USB Optical Mouse"}},
		{"USB", []string{"Logitech USB Optical Mouse", "USB Gamepad"}},
		{"event", nil},
		{"  ", nil},
		{"нет такого", nil},
	}
	for _, c := range cases {
		var got []string
		for _, d := range m.Find(c.ref) {
			got = append(got, d.Info.Name)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("Find(%q) = %v, want %v", c.ref, got, c.want)
		}
	}

	// Без модуля input — пусто, без паники.
	if (&Module{}).Find("event6") != nil || (&Module{}).Devices() != nil {
		t.Error("expected nothing without input")
	}
}

// autoModule — инспектор с файлом авто-ID в папке dir и заданными устройствами, уже «запущенный»
// (файл прочитан, имена розданы), как после Start.
func autoModule(t *testing.T, dir string, devs ...contracts.InputDevice) *Module {
	t.Helper()
	m := &Module{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg:   Config{InputDir: dir, DevicesFile: filepath.Join(dir, "devices.yaml")},
		input: fakeInput{devs: devs}, mode: "smart", bound: map[string]string{},
	}
	f, err := devmap.Load(m.cfg.DevicesFile)
	m.file, m.fileOK = f, err == nil
	m.assignAll()
	return m
}

// TestAutoIDs проверяет раздачу авто-ID: кому (режим smart), имена кнопок для макросов, файл,
// узнавание после перезапуска при другом eventN, смену режима и испорченный файл.
func TestAutoIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dev := func(event, name string, vid uint16, codes map[uint16][]uint16, kinds ...ev.Kind) contracts.InputDevice {
		return contracts.InputDevice{Info: ev.Info{Path: "/dev/input/" + event, Name: name, ID: ev.ID{Vendor: vid, Product: 1}, Caps: ev.Capabilities{Codes: codes}}, Kinds: kinds}
	}
	joy := dev("event9", "Sega Joystick", 0x79, map[uint16][]uint16{ev.EvKey: {ev.BtnTrigger, ev.BtnThumb}, ev.EvAbs: {ev.AbsThrottle}}, ev.KindJoystick)
	kbd := dev("event3", "AT Keyboard", 0x01, map[uint16][]uint16{ev.EvKey: {ev.KeyA, ev.KeyCalc}}, ev.KindKeyboard)
	power := dev("event2", "Power Button", 0x02, map[uint16][]uint16{ev.EvKey: {ev.KeyPower}}, ev.KindOther)
	mouse := dev("event6", "Mouse", 0x03, map[uint16][]uint16{ev.EvKey: {ev.BtnLeft}}, ev.KindMouse)

	// Режим smart: клавиатура (event3) и джойстик (event9) — да, по номеру устройства, а не по
	// порядку списка; кнопка питания и мышь — нет.
	m := autoModule(t, dir, joy, kbd, power, mouse)
	got := map[string]contracts.DeviceDetails{}
	for _, d := range m.Devices() {
		got[d.Info.Name] = d
	}
	if got["AT Keyboard"].AutoID != "UnKey" || got["Sega Joystick"].AutoID != "UnKey2" || got["Power Button"].AutoID != "" || got["Mouse"].AutoID != "" {
		t.Fatalf("auto ids: joy=%q kbd=%q power=%q mouse=%q", got["Sega Joystick"].AutoID, got["AT Keyboard"].AutoID, got["Power Button"].AutoID, got["Mouse"].AutoID)
	}

	// Имена для макросов: у UnKey — слитно, у UnKey2 и у осей — через точку; у {A} авто-ID нет.
	j, k := got["Sega Joystick"], got["AT Keyboard"]
	if j.Keys[0].Label != "UnKey2.001" || j.Keys[1].Label != "UnKey2.002" || j.Axes[0].Label != "UnKey2.Axis01" {
		t.Errorf("joystick labels: %+v %+v", j.Keys, j.Axes)
	}
	if k.Keys[0].Label != "" || k.Keys[1].Label != "UnKey001" || m.Label("/dev/input/event3", ev.EvKey, ev.KeyCalc) != "UnKey001" {
		t.Errorf("keyboard labels: %+v", k.Keys)
	}

	// Файл записан; после перезапуска устройства узнаются, даже если сменился eventN.
	data, err := os.ReadFile(filepath.Join(dir, "devices.yaml"))
	if err != nil || !strings.Contains(string(data), "auto_id: UnKey2") {
		t.Fatalf("file: %v\n%s", err, data)
	}
	joy.Info.Path, kbd.Info.Path = "/dev/input/event20", "/dev/input/event21"
	m2 := autoModule(t, dir, kbd, joy, power)
	if m2.Label("/dev/input/event20", ev.EvKey, ev.BtnTrigger) != "UnKey2.001" || m2.Label("/dev/input/event21", ev.EvKey, ev.KeyCalc) != "UnKey001" {
		t.Errorf("after restart: %v", m2.bound)
	}

	// Режим all: кнопка питания получает следующий авто-ID; неизвестный режим — ошибка.
	if err := m2.SetAutoIDMode("all"); err != nil || m2.Label("/dev/input/event2", ev.EvKey, ev.KeyPower) != "UnKey3.001" {
		t.Errorf("mode all: %v %q", err, m2.Label("/dev/input/event2", ev.EvKey, ev.KeyPower))
	}
	if err := m2.SetAutoIDMode("some"); err == nil || m2.AutoIDMode() != "all" {
		t.Errorf("bad mode: %v %s", err, m2.AutoIDMode())
	}

	// Испорченный файл: имена не раздаются, файл не перезаписывается.
	bad := t.TempDir()
	path := filepath.Join(bad, "devices.yaml")
	_ = os.WriteFile(path, []byte("devices: [oops"), 0o600)
	m3 := autoModule(t, bad, joy)
	if m3.fileOK || m3.Label(joy.Info.Path, ev.EvKey, ev.BtnTrigger) != "" {
		t.Error("broken file: auto-ids assigned")
	}
	if data, _ := os.ReadFile(path); string(data) != "devices: [oops" {
		t.Errorf("broken file overwritten: %s", data)
	}
}

// TestResolveKey проверяет поиск кнопок устройств для макросов: номер, имя человека, стандартное
// имя на устройстве, отключённое устройство (по файлу), ошибки; и DeviceOf.
func TestResolveKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	joy := contracts.InputDevice{Info: ev.Info{Path: "/dev/input/event9", Name: "Joy", ID: ev.ID{Vendor: 0x79},
		Caps: ev.Capabilities{Codes: map[uint16][]uint16{ev.EvKey: {ev.BtnTrigger, ev.BtnThumb}}}}, Kinds: []ev.Kind{ev.KindJoystick}}
	m := autoModule(t, dir, joy)
	m.file.Devices[0].Buttons["002"] = devmap.Control{Code: "BTN_THUMB", Name: "Start"}

	cases := []struct {
		dev, btn string
		code     uint16
		name     string
	}{
		{"UnKey", "001", ev.BtnTrigger, "UnKey001"},
		{"unkey", "002", ev.BtnThumb, "UnKey002"},
		{"UnKey", "start", ev.BtnThumb, "UnKey002"},
		{"UnKey", "A", ev.KeyA, "UnKey.A"},
	}
	for _, c := range cases {
		k, err := m.ResolveKey(c.dev, c.btn)
		if err != nil || k.Code != c.code || k.Name != c.name || k.Device != "UnKey" {
			t.Errorf("ResolveKey(%q, %q) = %+v, %v", c.dev, c.btn, k, err)
		}
	}

	// Ошибки — коды языка макросов (понятные сообщения).
	for _, c := range []struct{ dev, btn, code string }{
		{"UnKey9", "001", dsl.ErrUnknownDevice}, {"UnKey", "099", dsl.ErrUnknownButton},
	} {
		_, err := m.ResolveKey(c.dev, c.btn)
		var de *dsl.Error
		if !errors.As(err, &de) || de.Code != c.code {
			t.Errorf("ResolveKey(%q, %q) err = %v", c.dev, c.btn, err)
		}
	}

	// DeviceOf: подключённое — авто-ID; после отключения устройство по-прежнему находится по файлу.
	if m.DeviceOf("/dev/input/event9") != "UnKey" || m.DeviceOf("/dev/input/event1") != "" {
		t.Error("DeviceOf")
	}
	delete(m.bound, "/dev/input/event9")
	if _, err := m.ResolveKey("UnKey", "001"); err != nil {
		t.Errorf("disconnected: %v", err)
	}
}
