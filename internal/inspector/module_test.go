package inspector

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
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
