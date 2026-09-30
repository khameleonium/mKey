package recorder

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/mkrec"
	"mkey/internal/registry"
)

// fakeInput — источник ввода с тремя устройствами: клавиатура, мышь, геймпад.
type fakeInput struct{ contracts.InputSource }

func (fakeInput) Devices() []contracts.InputDevice {
	return []contracts.InputDevice{
		{Info: ev.Info{Path: "/kbd", Name: "Keyboard"}, Kinds: []ev.Kind{ev.KindKeyboard}},
		{Info: ev.Info{Path: "/mouse", Name: "Mouse"}, Kinds: []ev.Kind{ev.KindMouse}},
		{Info: ev.Info{Path: "/pad", Name: "Pad"}, Kinds: []ev.Kind{ev.KindGamepad}},
	}
}

// fakeDevice — виртуальное устройство, запоминающее отправленные события и отпускания.
type fakeDevice struct {
	mu       sync.Mutex
	events   []ev.Event
	released []uint16
}

func (d *fakeDevice) Name() string                                     { return "fake" }
func (d *fakeDevice) Press(context.Context, uint16) error              { return nil }
func (d *fakeDevice) Tap(context.Context, uint16, time.Duration) error { return nil }
func (d *fakeDevice) Held() []uint16                                   { return nil }
func (d *fakeDevice) ReleaseAll() error                                { return nil }
func (d *fakeDevice) Release(_ context.Context, code uint16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.released = append(d.released, code)
	return nil
}
func (d *fakeDevice) Emit(_ context.Context, events ...ev.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, events...)
	return nil
}

// fakeDevs — пара «клавиатура + мышь» и счётчик постановок указателя в центр.
type fakeDevs struct {
	kb, mouse *fakeDevice
	centers   *int
}

func (f fakeDevs) CenterPointer(context.Context) error {
	*f.centers++
	return nil
}

func (f fakeDevs) Keyboard() (contracts.VirtualDevice, error) { return f.kb, nil }
func (f fakeDevs) Mouse() (contracts.VirtualDevice, error)    { return f.mouse, nil }
func (fakeDevs) Status() contracts.OutputStatus               { return contracts.OutputStatus{Available: true} }
func (fakeDevs) ReleaseAll() error                            { return nil }

// newTestModule создаёт модуль с фейковыми часами, вводом и выводом и временным каталогом записей.
func newTestModule(t *testing.T) (*Module, *clock.Fake, fakeDevs) {
	t.Helper()
	m := New()
	clk := clock.NewFake(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	devs := fakeDevs{kb: &fakeDevice{}, mouse: &fakeDevice{}, centers: new(int)}
	m.clk, m.input, m.devs = clk, fakeInput{}, devs
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.bus = bus.New(0)
	m.cfg.Dir = t.TempDir()
	c, err := parseChord(m.cfg.Hotkey)
	if err != nil {
		t.Fatal(err)
	}
	m.hotkey = c
	return m, clk, devs
}

// feeder отправляет события в модуль с метками времени от начала теста.
type feeder struct {
	m    *Module
	clk  *clock.Fake
	base time.Time
}

// at отправляет событие устройства dev через d после начала (часы сдвигаются к этому моменту).
func (f feeder) at(d time.Duration, dev string, typ, code uint16, value int32) {
	if now := f.clk.Now(); f.base.Add(d).After(now) {
		f.clk.Advance(f.base.Add(d).Sub(now))
	}
	e := ev.Event{Time: f.base.Add(d), Type: typ, Code: code, Value: value}
	f.m.handle(contracts.InputEvent{Device: dev, Event: e, Delivered: e})
}

// consumed отправляет нажатие, которое «съел» mKey (горячая клавиша с consume), с SYN_REPORT.
func (f feeder) consumed(d time.Duration, dev string, code uint16, value int32) {
	f.clk.Advance(f.base.Add(d).Sub(f.clk.Now()))
	e := ev.Event{Time: f.base.Add(d), Type: ev.EvKey, Code: code, Value: value}
	f.m.handle(contracts.InputEvent{Device: dev, Event: e, Delivered: e, Consumed: true})
	f.at(d, dev, ev.EvSyn, ev.SynReport, 0)
}

// key отправляет нажатие или отпускание клавиши с SYN_REPORT.
func (f feeder) key(d time.Duration, dev string, code uint16, value int32) {
	f.at(d, dev, ev.EvKey, code, value)
	f.at(d, dev, ev.EvSyn, ev.SynReport, 0)
}

// TestRecordWithHotkey проверяет запись: фильтр устройств, отпускания до начала, вырезание сочетаний.
func TestRecordWithHotkey(t *testing.T) {
	t.Parallel()
	m, clk, _ := newTestModule(t)
	f := feeder{m: m, clk: clk, base: clk.Now()}
	ms := time.Millisecond

	// Запись начинается сочетанием Ctrl+Alt+R; его отпускание не записывается.
	f.key(0, "/kbd", ev.KeyLeftctrl, 1)
	f.key(1*ms, "/kbd", ev.KeyLeftalt, 1)
	f.key(2*ms, "/kbd", ev.KeyR, 1)
	if _, ok := m.Recording(); !ok {
		t.Fatal("hotkey did not start recording")
	}
	f.key(50*ms, "/kbd", ev.KeyR, 0)
	f.key(51*ms, "/kbd", ev.KeyLeftalt, 0)
	f.key(52*ms, "/kbd", ev.KeyLeftctrl, 0)

	// Полезные действия: клавиша, перемещение мыши, кнопка геймпада (не записывается).
	f.key(100*ms, "/kbd", ev.KeyA, 1)
	f.key(150*ms, "/kbd", ev.KeyA, 0)
	f.at(200*ms, "/mouse", ev.EvRel, ev.RelX, 5)
	f.at(200*ms, "/mouse", ev.EvSyn, ev.SynReport, 0)
	f.key(250*ms, "/pad", ev.BtnSouth, 1)
	f.at(260*ms, "/kbd", ev.EvKey, ev.KeyA, ev.ValueRepeat) // автоповтор не записывается

	// Остановка тем же сочетанием: оно вырезается, длительность — до его начала.
	f.key(3000*ms, "/kbd", ev.KeyLeftctrl, 1)
	f.key(3001*ms, "/kbd", ev.KeyLeftalt, 1)
	f.key(3002*ms, "/kbd", ev.KeyR, 1)
	if _, ok := m.Recording(); ok {
		t.Fatal("hotkey did not stop recording")
	}

	// В файле — только клавиша A и перемещение мыши, понятным текстом.
	list, err := m.Recordings()
	if err != nil || len(list) != 1 {
		t.Fatalf("recordings = %+v, %v", list, err)
	}
	info := list[0]
	if info.Events != 3 || info.DurationMS < 2900 || info.DurationMS > 3000 || len(info.Devices) != 2 {
		t.Fatalf("info = %+v", info)
	}
	data, _ := os.ReadFile(info.Path)
	text := string(data)
	for _, want := range []string{"0 ^{A}\n", "0 ~{A}\n", "1 move +5 +0\n", "device 0 keyboard \"Keyboard\""} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Ctrl") || strings.Contains(text, "{R}") {
		t.Errorf("hotkey recorded:\n%s", text)
	}
}

// TestStopWithCtrlC проверяет вырезание Ctrl+C при остановке из терминала и ошибки команд.
func TestStopWithCtrlC(t *testing.T) {
	t.Parallel()
	m, clk, _ := newTestModule(t)
	f := feeder{m: m, clk: clk, base: clk.Now()}
	ms := time.Millisecond

	// Ошибки: остановка без записи, плохое имя, повторный старт.
	if _, err := m.StopRecording(""); !errors.Is(err, contracts.ErrNotRecording) {
		t.Fatalf("stop without recording: %v", err)
	}
	if _, err := m.StartRecording(contracts.RecordOptions{Name: "../x"}); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := m.StartRecording(contracts.RecordOptions{Name: "игра 1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartRecording(contracts.RecordOptions{}); !errors.Is(err, contracts.ErrAlreadyRecording) {
		t.Fatalf("second start: %v", err)
	}

	// Клавиша, затем Ctrl+C в терминале и команда остановки.
	f.key(100*ms, "/kbd", ev.KeyW, 1)
	f.key(400*ms, "/kbd", ev.KeyW, 0)
	f.key(1000*ms, "/kbd", ev.KeyLeftctrl, 1)
	f.key(1010*ms, "/kbd", ev.KeyC, 1)
	clk.Advance(200 * ms)
	info, err := m.StopRecording(CutCtrlC)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "игра 1" || info.Events != 2 || info.DurationMS != 1000 {
		t.Fatalf("info = %+v", info)
	}

	// Удаление записи; повторное — «не найдена».
	if err := m.DeleteRecording("игра 1"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteRecording("игра 1"); !errors.Is(err, contracts.ErrRecordingNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

// writeRecording создаёт файл записи из текста действий (устройство 0 — клавиатура, 1 — мышь).
func writeRecording(t *testing.T, dir, name, body string) {
	t.Helper()
	src := "mkrec 1\ndevice 0 keyboard \"Kbd\"\ndevice 1 mouse \"Mouse\"\n" + body
	if err := os.WriteFile(filepath.Join(dir, name+mkrec.FileExt), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPlay проверяет воспроизведение: маршрутизацию, темп и скорость, повторы, отпускание клавиш,
// калибровку указателя (центр экрана перед каждым проходом).
func TestPlay(t *testing.T) {
	t.Parallel()
	m, clk, devs := newTestModule(t)
	ms := time.Millisecond
	writeRecording(t, m.cfg.Dir, "demo", "pointer center\n"+
		"0.000 0 ~{B}\n"+ // отпускание без нажатия — пропускается
		"0.100 0 ^{A}\n"+
		"0.200 1 move +7 +0\n"+
		"0.300 1 ^{Mouse0}\n"+
		"0.400 0 ~{A}\n"+
		"0.500 1 ^{South}\n"+ // геймпад — пропускается
		"0.600 end\n")

	// Два прохода вдвое быстрее: каждый длится как запись (600 мс / 2), всего 600 мс.
	start := clk.Now()
	if err := m.Play(context.Background(), "demo", contracts.PlayOptions{Speed: 2, Repeat: 2}); err != nil {
		t.Fatal(err)
	}
	if got := clk.Now().Sub(start); got != 600*ms {
		t.Fatalf("elapsed = %v", got)
	}
	if n := len(devs.kb.events); n != 4 { // A↓ A↑ × 2 (без SYN: его добавляет устройство)
		t.Fatalf("keyboard events = %v", devs.kb.events)
	}
	if devs.mouse.events[0].Code != ev.RelX || devs.mouse.events[1].Code != ev.BtnLeft {
		t.Fatalf("mouse events = %v", devs.mouse.events)
	}
	// Указатель — в центр экрана перед каждым проходом.
	if *devs.centers != 2 {
		t.Fatalf("centers = %d", *devs.centers)
	}
	// Кнопка мыши осталась нажатой в конце записи — отпущена после каждого прохода.
	if len(devs.mouse.released) != 2 || devs.mouse.released[0] != ev.BtnLeft {
		t.Fatalf("released = %v", devs.mouse.released)
	}

	// Без перемещений мыши (и без калибровки); отменённый контекст; нет записи; плохая скорость.
	devs.mouse.events = nil
	*devs.centers = 0
	if err := m.Play(context.Background(), "demo", contracts.PlayOptions{SkipMoves: true}); err != nil || devs.mouse.events[0].Code != ev.BtnLeft || *devs.centers != 0 {
		t.Fatalf("skip moves: %v %v %d", err, devs.mouse.events, *devs.centers)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Play(ctx, "demo", contracts.PlayOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if err := m.Play(context.Background(), "nope", contracts.PlayOptions{}); !errors.Is(err, contracts.ErrRecordingNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := m.Play(context.Background(), "demo", contracts.PlayOptions{Speed: 50}); err == nil {
		t.Fatal("speed 50 accepted")
	}
}

// TestRecordPointerConsumedEmergency проверяет калибровку указателя в начале записи,
// пропуск «съеденных» mKey нажатий и остановку записи экстренной остановкой без самого сочетания.
func TestRecordPointerConsumedEmergency(t *testing.T) {
	t.Parallel()
	m, clk, devs := newTestModule(t)
	f := feeder{m: m, clk: clk, base: clk.Now()}
	ms := time.Millisecond
	if _, err := m.StartRecording(contracts.RecordOptions{Name: "p"}); err != nil {
		t.Fatal(err)
	}

	// Щелчок мыши.
	f.key(100*ms, "/mouse", ev.BtnLeft, 1)
	f.key(150*ms, "/mouse", ev.BtnLeft, 0)
	// F8 «съел» проект (consume) — не записывается.
	f.consumed(200*ms, "/kbd", ev.KeyF8, 1)
	f.consumed(250*ms, "/kbd", ev.KeyF8, 0)
	f.key(300*ms, "/kbd", ev.KeyA, 1)
	f.key(350*ms, "/kbd", ev.KeyA, 0)
	// Экстренная остановка: Esc+Backspace+Enter вырезаются, запись сохраняется.
	f.key(1000*ms, "/kbd", ev.KeyEsc, 1)
	f.key(1010*ms, "/kbd", ev.KeyBackspace, 1)
	f.key(1020*ms, "/kbd", ev.KeyEnter, 1)
	m.stopOnEmergency()
	if _, ok := m.Recording(); ok {
		t.Fatal("still recording")
	}

	data, err := os.ReadFile(filepath.Join(m.cfg.Dir, "p"+mkrec.FileExt))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if *devs.centers != 1 {
		t.Fatalf("centers = %d", *devs.centers)
	}
	for _, want := range []string{"pointer center\n", "0 ^{Mouse0}\n", "1 ^{A}\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	for _, bad := range []string{"F8", "Esc", "Backspace", "Enter"} {
		if strings.Contains(text, bad) {
			t.Errorf("%s recorded:\n%s", bad, text)
		}
	}
}

// TestPlayAction проверяет проверку параметров действия play.
func TestPlayAction(t *testing.T) {
	t.Parallel()
	a := playAction{}
	for v, ok := range map[any]bool{"игра": true, "": false} {
		if _, err := a.params(v); (err == nil) != ok {
			t.Errorf("%v: %v", v, err)
		}
	}
	if _, err := a.params(map[string]any{"name": "x", "speed": 20.0}); err == nil {
		t.Error("speed 20 accepted")
	}
}

// TestLifecycle проверяет полный цикл Init → Start → Stop в менеджере модулей (без ввода и вывода).
func TestLifecycle(t *testing.T) {
	t.Parallel()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: New()}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := mgr.Statuses()[0]; st.State != registry.StateRunning {
		t.Fatalf("state = %s, err = %v", st.State, st.Err)
	}
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
