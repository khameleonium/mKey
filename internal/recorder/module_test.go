package recorder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/clock"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/mkrec"
	"github.com/khameleonium/mKey/internal/lib/project"
	"github.com/khameleonium/mKey/internal/registry"
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

	// Левый Ctrl + левый Alt + Пробел — не то сочетание: запись не начинается.
	f.key(0, "/kbd", ev.KeyLeftctrl, 1)
	f.key(0, "/kbd", ev.KeyLeftalt, 1)
	f.key(0, "/kbd", ev.KeySpace, 1)
	f.key(0, "/kbd", ev.KeySpace, 0)
	f.key(0, "/kbd", ev.KeyLeftalt, 0)
	f.key(0, "/kbd", ev.KeyLeftctrl, 0)
	if _, ok := m.Recording(); ok {
		t.Fatal("left Alt started recording")
	}

	// Запись начинается сочетанием левый Ctrl + правый Alt + Пробел; его отпускание не записывается.
	f.key(0, "/kbd", ev.KeyLeftctrl, 1)
	f.key(1*ms, "/kbd", ev.KeyRightalt, 1)
	f.key(2*ms, "/kbd", ev.KeySpace, 1)
	if _, ok := m.Recording(); !ok {
		t.Fatal("hotkey did not start recording")
	}
	f.key(50*ms, "/kbd", ev.KeySpace, 0)
	f.key(51*ms, "/kbd", ev.KeyRightalt, 0)
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
	f.key(3001*ms, "/kbd", ev.KeyRightalt, 1)
	f.key(3002*ms, "/kbd", ev.KeySpace, 1)
	if _, ok := m.Recording(); ok {
		t.Fatal("hotkey did not stop recording")
	}

	// В файле — только клавиша A и перемещение мыши, понятным текстом.
	list, err := m.Recordings()
	if err != nil || len(list) != 1 {
		t.Fatalf("recordings = %+v, %v", list, err)
	}
	info := list[0]
	if want := defaultName(f.base); info.Name != want {
		t.Errorf("name = %q, want %q", info.Name, want)
	}
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
	if strings.Contains(text, "Ctrl") || strings.Contains(text, "{Space}") {
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

// dryTimeline — таймлайн сухого прогона для проверки DryRun действий: запоминает заметки и время.
type dryTimeline struct {
	contracts.DryRunContext
	notes []string
	args  []map[string]string
	spent int64
}

// Note запоминает заметку.
func (d *dryTimeline) Note(key string, args map[string]string) {
	d.notes, d.args = append(d.notes, key), append(d.args, args)
}

// Spend накапливает время.
func (d *dryTimeline) Spend(ms int64) { d.spent += ms }

// TestPlayActionDryRun: сухой прогон повтора записи — имя, повторы, длительность с учётом скорости;
// ничего не воспроизводится.
func TestPlayActionDryRun(t *testing.T) {
	t.Parallel()
	m, _, devs := newTestModule(t)
	writeRecording(t, m.cfg.Dir, "игра", "0.000 0 ^{A}\n0.100 0 ~{A}\n2.000 end\n")
	d := &dryTimeline{}
	act := project.Action{Type: "play", Value: map[string]any{"name": "игра", "speed": 2.0, "repeat": 3}}
	if err := (playAction{m: m}).DryRun(context.Background(), d, act); err != nil {
		t.Fatal(err)
	}
	if len(d.notes) != 1 || d.notes[0] != "dry.play" || d.args[0]["ms"] != "3000" || d.args[0]["repeat"] != "3" || d.spent != 3000 {
		t.Fatalf("notes = %v %v, spent = %d", d.notes, d.args, d.spent)
	}
	// Записи нет — так и сказано, время не идёт.
	missing := &dryTimeline{}
	if err := (playAction{m: m}).DryRun(context.Background(), missing, project.Action{Type: "play", Value: "нет"}); err != nil {
		t.Fatal(err)
	}
	if len(missing.notes) != 1 || missing.notes[0] != "dry.play_missing" || missing.spent != 0 {
		t.Fatalf("missing = %v, spent = %d", missing.notes, missing.spent)
	}
	devs.kb.mu.Lock()
	defer devs.kb.mu.Unlock()
	if len(devs.kb.events) != 0 {
		t.Fatal("dry run played the recording")
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

// memProjects — хранилище проектов в памяти: запоминает созданный проект.
type memProjects struct {
	contracts.Projects
	id   string
	data []byte
}

func (p *memProjects) Create(id string, data []byte) (string, error) {
	p.id, p.data = id, data
	return id, nil
}

// checkEvents — движок, который проверяет только, что виды действий известны.
type checkEvents struct {
	contracts.Events
	checked bool
}

func (e *checkEvents) ValidateProject(p project.Project) error {
	e.checked = true
	for _, a := range p.Events[0].Actions {
		switch a.Type {
		case "pointer_center", "tap", "pause", "mouse_move", "mouse_click", "key_down", "key_up", "hold", "wheel":
		default:
			return fmt.Errorf("unknown action %q", a.Type)
		}
	}
	return nil
}

// TestConvertRecording проверяет превращение записи в выключенный проект с одним событием.
func TestConvertRecording(t *testing.T) {
	t.Parallel()
	m, _, _ := newTestModule(t)
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	m.tr = i18n.New(cat, "ru")
	store, engine := &memProjects{}, &checkEvents{}
	m.projects, m.events = store, engine
	writeRecording(t, m.cfg.Dir, "игра", "pointer center\n0.000 0 ^{A}\n0.080 0 ~{A}\n0.500 1 move +5 +0\n0.600 1 ^{Mouse0}\n0.650 1 ~{Mouse0}\n1.000 end\n")
	writeRecording(t, m.cfg.Dir, "пусто", "pointer center\n0.100 2 ^{South}\n0.200 2 ~{South}\n")

	// Проект создан, выключен, проверен движком, начинается с калибровки.
	id, err := m.ConvertRecording("игра", contracts.ConvertOptions{Simplify: true})
	if err != nil || id != "rec-игра" || !engine.checked {
		t.Fatalf("convert: %q %v checked=%v", id, err, engine.checked)
	}
	p, err := project.Parse(store.data, id)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, store.data)
	}
	ev := p.Events[0]
	if p.IsEnabled() || p.Name != "Из записи «игра»" || ev.Trigger.Type != "manual" || ev.Actions[0].Type != "pointer_center" || len(ev.Actions) != 6 {
		t.Fatalf("project = %+v\n%s", p, store.data)
	}

	// Пустая запись (только геймпад) и нет записи — понятные ошибки.
	if _, err := m.ConvertRecording("пусто", contracts.ConvertOptions{}); !errors.Is(err, contracts.ErrRecordingEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := m.ConvertRecording("нет", contracts.ConvertOptions{}); !errors.Is(err, contracts.ErrRecordingNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// TestRecordingsCache: список записей перечитывает только изменившиеся файлы (размер или время
// изменения), отдаёт копии сведений и забывает удалённые файлы.
func TestRecordingsCache(t *testing.T) {
	t.Parallel()
	m, _, _ := newTestModule(t)
	writeRecording(t, m.cfg.Dir, "a", "0.100 0 ^{A}\n0.200 0 ~{A}\n")
	path := filepath.Join(m.cfg.Dir, "a"+mkrec.FileExt)
	list, err := m.Recordings()
	if err != nil || len(list) != 1 || list[0].Events != 2 || len(list[0].Devices) != 2 {
		t.Fatalf("first listing: %+v %v", list, err)
	}
	list[0].Devices[0] = "changed by the caller"

	// Содержимое другое, но размер и время изменения прежние — файл не перечитывается.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(fi.Size())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	list, _ = m.Recordings()
	if len(list) != 1 || list[0].Problem != nil || list[0].Events != 2 || list[0].Devices[0] != "Kbd" {
		t.Fatalf("unchanged file was re-read or the copy was shared: %+v", list)
	}

	// Время изменения другое — файл перечитан (теперь он испорчен).
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.Recordings(); len(list) != 1 || list[0].Problem == nil {
		t.Fatalf("changed file not re-read: %+v", list)
	}

	// Удалённый файл исчезает из списка и из сохранённых сведений.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.Recordings(); len(list) != 0 || len(m.summaries) != 0 {
		t.Fatalf("removed file kept: %+v, %d summaries", list, len(m.summaries))
	}
}

// TestRecordingsProblem проверяет, что файл с ошибкой (после ручной правки) или запись первой
// версии остаются в списке с описанием ошибки, а не с нулевой длительностью без объяснений,
// и что у каждой записи есть дата.
func TestRecordingsProblem(t *testing.T) {
	t.Parallel()
	m, _, _ := newTestModule(t)
	writeRecording(t, m.cfg.Dir, "ok", "0.100 0 ^{A}\n0.200 0 ~{A}\n")
	writeRecording(t, m.cfg.Dir, "опечатка", "0.100 0 ^{A}\n0.200 0 ~{Hh}\n")
	old := `{"format":"mkrec","version":1,"devices":[]}` + "\n[1350712,0,2,0,1]\n"
	if err := os.WriteFile(filepath.Join(m.cfg.Dir, "старая"+mkrec.FileExt), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ошибки — у двух записей: вид, строка и неверное слово.
	list, err := m.Recordings()
	if err != nil || len(list) != 3 {
		t.Fatalf("recordings = %+v, %v", list, err)
	}
	got := map[string]*contracts.RecordingProblem{}
	for _, r := range list {
		got[r.Name] = r.Problem
	}
	if got["ok"] != nil {
		t.Errorf("ok: %+v", got["ok"])
	}
	if p := got["опечатка"]; p == nil || p.Code != mkrec.ProblemKey || p.Arg != "Hh" || p.Text != "0.200 0 ~{Hh}" || p.Line == 0 {
		t.Errorf("опечатка: %+v", p)
	}
	if p := got["старая"]; p == nil || p.Code != mkrec.ProblemOldFormat {
		t.Errorf("старая: %+v", p)
	}

	// Файл без строки created (написан вручную) — с датой изменения файла, а не «01.01.0001».
	for _, r := range list {
		if r.Created.IsZero() {
			t.Errorf("%s: no date", r.Name)
		}
	}
}
