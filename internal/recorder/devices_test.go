package recorder

import (
	"strings"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// devInput — источник ввода с устройствами разных категорий и своими устройствами mKey.
type devInput struct{ contracts.InputSource }

// Devices — подключённые устройства: клавиатура, геймпад с «клавиатурным» интерфейсом,
// сенсорный экран, кнопка питания и виртуальная клавиатура другой программы.
func (devInput) Devices() []contracts.InputDevice {
	id := func(v, p uint16) ev.ID { return ev.ID{Vendor: v, Product: p} }
	return []contracts.InputDevice{
		{Info: ev.Info{Path: "/kbd", Name: "AT Keyboard", ID: id(1, 1)}, Kinds: []ev.Kind{ev.KindKeyboard}},
		{Info: ev.Info{Path: "/pad", Name: "8BitDo Ultimate 2C Wireless Controller", ID: id(0x2dc8, 0x310a)}, Kinds: []ev.Kind{ev.KindKeyboard, ev.KindGamepad}},
		{Info: ev.Info{Path: "/touch", Name: "Touch Panel", ID: id(2, 2)}, Kinds: []ev.Kind{ev.KindTouchscreen}},
		{Info: ev.Info{Path: "/power", Name: "Power Button", ID: id(0, 1)}, Kinds: []ev.Kind{ev.KindOther}},
		{Info: ev.Info{Path: "/remote", Name: "Remote Keyboard", ID: id(0x1234, 0x5678)}, Kinds: []ev.Kind{ev.KindKeyboard}, Virtual: true},
	}
}

// OwnDevices — своя клавиатура mKey.
func (devInput) OwnDevices() []contracts.InputDevice {
	return []contracts.InputDevice{{Info: ev.Info{Path: "/mkey", Name: "mKey Keyboard", ID: ev.ID{Vendor: 0x6d6b, Product: 1}}, Virtual: true}}
}

// TestRecordDevices проверяет выбор «что записывать»: категории в порядке показа (геймпад
// с клавиатурным интерфейсом — в геймпадах, виртуальные — отдельно, своё устройство mKey без
// выбора), ключ «VID:PID имя», выбор по классам и свой выбор устройства (он важнее классов);
// виртуальное устройство чужой программы без своей галочки не записывается.
func TestRecordDevices(t *testing.T) {
	t.Parallel()
	m, _, _ := newTestModule(t)
	m.input = devInput{}
	s := m.RecordSettings()
	s.Kinds = []string{"keyboard"}
	s.Devices = map[string]bool{"2dc8:310a 8BitDo Ultimate 2C Wireless Controller": false, "0002:0002 Touch Panel": true}
	if err := m.SetRecordSettings(s); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, d := range m.RecordDevices() {
		got = append(got, strings.Join([]string{d.Category, d.ID, d.Name, map[bool]string{true: "on", false: "off"}[d.Selected],
			map[bool]string{true: "explicit", false: ""}[d.Explicit], map[bool]string{true: "own", false: ""}[d.Own]}, "|"))
	}
	want := []string{
		"keyboards|0001:0001|AT Keyboard|on||",
		"gamepads|2dc8:310a|8BitDo Ultimate 2C Wireless Controller|off|explicit|",
		"touch|0002:0002|Touch Panel|on|explicit|",
		"other|0000:0001|Power Button|off||",
		"virtual|6d6b:0001|mKey Keyboard|off||own",
		"virtual|1234:5678|Remote Keyboard|off||",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("devices:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestRecordSelection: записываются только выбранные устройства (свой выбор важнее классов);
// при начале записи видно, какие устройства будут записаны, а пустой выбор допустим — список пуст.
func TestRecordSelection(t *testing.T) {
	t.Parallel()
	m, clk, _ := newTestModule(t)
	f := feeder{m: m, clk: clk, base: clk.Now()}
	ms := time.Millisecond

	// Классы: клавиатура и мышь; клавиатуру по отдельности выключили, геймпад — включили.
	s := m.RecordSettings()
	s.Devices = map[string]bool{"0000:0000 Keyboard": false, "0000:0000 Pad": true}
	if err := m.SetRecordSettings(s); err != nil {
		t.Fatal(err)
	}
	info, err := m.StartRecording(contracts.RecordOptions{Name: "выбор"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(info.Devices, ",") != "Mouse,Pad" {
		t.Fatalf("devices at start = %v", info.Devices)
	}
	f.key(100*ms, "/kbd", ev.KeyA, 1)
	f.key(150*ms, "/kbd", ev.KeyA, 0)
	f.key(200*ms, "/pad", ev.BtnSouth, 1)
	f.key(250*ms, "/pad", ev.BtnSouth, 0)
	clk.Advance(100 * ms)
	info, err = m.StopRecording("")
	if err != nil {
		t.Fatal(err)
	}
	if info.Events != 2 || strings.Join(info.Devices, ",") != "Pad" {
		t.Fatalf("recorded = %+v", info)
	}

	// Ничего не выбрано — запись начинается, но выбранных устройств нет.
	s.Kinds, s.Devices = []string{}, map[string]bool{"0000:0000 Pad": false}
	if err := m.SetRecordSettings(s); err != nil {
		t.Fatal(err)
	}
	info, err = m.StartRecording(contracts.RecordOptions{Name: "пусто"})
	if err != nil || len(info.Devices) != 0 {
		t.Fatalf("empty selection: %v %v", info.Devices, err)
	}
	if _, err := m.StopRecording(""); err != nil {
		t.Fatal(err)
	}
}
