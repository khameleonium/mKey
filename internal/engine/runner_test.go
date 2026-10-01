package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/layout"
	"mkey/internal/lib/project"
)

// fakeDevice — виртуальное устройство, записывающее события.
type fakeDevice struct {
	name   string
	mu     sync.Mutex
	events []ev.Event
	held   map[uint16]bool
}

func newFakeDevice(name string) *fakeDevice {
	return &fakeDevice{name: name, held: map[uint16]bool{}}
}

func (f *fakeDevice) Name() string { return f.name }

func (f *fakeDevice) Press(ctx context.Context, code uint16) error {
	return f.Emit(ctx, ev.Event{Type: ev.EvKey, Code: code, Value: 1})
}

func (f *fakeDevice) Release(ctx context.Context, code uint16) error {
	return f.Emit(ctx, ev.Event{Type: ev.EvKey, Code: code, Value: 0})
}

func (f *fakeDevice) Tap(ctx context.Context, code uint16, _ time.Duration) error {
	if err := f.Press(ctx, code); err != nil {
		return err
	}
	return f.Release(ctx, code)
}

func (f *fakeDevice) Emit(ctx context.Context, events ...ev.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range events {
		f.events = append(f.events, e)
		if e.Type == ev.EvKey {
			if e.Value == 1 {
				f.held[e.Code] = true
			} else {
				delete(f.held, e.Code)
			}
		}
	}
	return nil
}

func (f *fakeDevice) Held() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uint16
	for c := range f.held {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func (f *fakeDevice) ReleaseAll() error { return nil }

func (f *fakeDevice) log() []ev.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ev.Event(nil), f.events...)
}

// fakeDevices — пара «клавиатура + мышь».
type fakeDevices struct{ kb, mouse *fakeDevice }

func (f fakeDevices) Keyboard() (contracts.VirtualDevice, error) { return f.kb, nil }
func (f fakeDevices) Mouse() (contracts.VirtualDevice, error)    { return f.mouse, nil }
func (fakeDevices) Status() contracts.OutputStatus               { return contracts.OutputStatus{Available: true} }
func (fakeDevices) ReleaseAll() error                            { return nil }

// CenterPointer не нужен тестам движка.
func (fakeDevices) CenterPointer(context.Context) error { return nil }

// fakeLayouts — раскладки с журналом переключений.
type fakeLayouts struct {
	mu       sync.Mutex
	info     contracts.LayoutInfo
	switches []string
}

func (f *fakeLayouts) Layouts(context.Context) (contracts.LayoutInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}

func (f *fakeLayouts) Switch(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info.Current = name
	f.switches = append(f.switches, name)
	return nil
}

// newTestModule создаёт исполнитель с фейковыми устройствами, часами и раскладками.
func newTestModule(clk clock.Clock, layouts contracts.LayoutProvider) (*Module, fakeDevices) {
	devs := fakeDevices{kb: newFakeDevice("mKey Keyboard"), mouse: newFakeDevice("mKey Mouse")}
	m := newModule(clk, rand.New(rand.NewPCG(1, 2)))
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.devices = devs
	m.layouts = layouts
	return m, devs
}

// typed воспроизводит, какой текст увидит приложение: переводит нажатия клавиатуры в символы
// по раскладке, которая была активна в момент нажатия (с учётом Shift).
func typed(t *testing.T, events []ev.Event, layoutAt func(i int) string) string {
	t.Helper()

	// Обратные таблицы раскладок: (клавиша, Shift) → символ.
	type key struct {
		code  uint16
		shift bool
	}
	reverse := map[string]map[key]rune{}
	for _, name := range layout.Names() {
		l, _ := layout.Get(name)
		reverse[name] = map[key]rune{}
		for _, r := range "`~1!2@3#4$5%6^7&8*9(0)-_=+qwertyuiopQWERTYUIOP[]{}\\|asdfghjklASDFGHJKL;:'\"zxcvbnmZXCVBNM,<.>/? йцукенгшщзхъЙЦУКЕНГШЩЗХЪфывапролджэФЫВАПРОЛДЖЭячсмитьбюЯЧСМИТЬБЮёЁ№" {
			if st, ok := l.Find(r); ok {
				if _, exists := reverse[name][key{st.Code, st.Shift}]; !exists {
					reverse[name][key{st.Code, st.Shift}] = r
				}
			}
		}
	}

	// Проходим по событиям, следя за Shift.
	var b strings.Builder
	shift := false
	for i, e := range events {
		if e.Type != ev.EvKey {
			continue
		}
		switch {
		case e.Code == ev.KeyLeftshift || e.Code == ev.KeyRightshift:
			shift = e.Value == 1
		case e.Value == 1 && e.Code == ev.KeyEnter:
			b.WriteRune('\n')
		case e.Value == 1 && e.Code == ev.KeySpace:
			// Пробел одинаков с Shift и без него.
			b.WriteRune(' ')
		case e.Value == 1:
			r, ok := reverse[layoutAt(i)][key{e.Code, shift}]
			if !ok {
				t.Fatalf("event %d: no char for code %#x shift=%v in %s", i, e.Code, shift, layoutAt(i))
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestShiftHeldAffectsText — пример из ТЗ: зажатый Shift действует на набираемый текст (FR-DSL-1a).
func TestShiftHeldAffectsText(t *testing.T) {
	t.Parallel()
	lp := &fakeLayouts{info: contracts.LayoutInfo{Current: "ru", Available: []string{"ru", "us"}, CanSwitch: true}}
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), lp)

	// Выполняем макрос.
	if err := m.Run(context.Background(), `^{Shift}{"Привет, Вера!"}~{Shift}{Enter}`); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Приложение в русской раскладке увидит «ПРИВЕТ, ВЕРА!» и перевод строки.
	got := typed(t, devs.kb.log(), func(int) string { return "ru" })
	if got != "ПРИВЕТ, ВЕРА!\n" {
		t.Fatalf("typed %q", got)
	}
	if len(lp.switches) != 0 || len(devs.kb.Held()) != 0 {
		t.Fatalf("switches %v, held %v", lp.switches, devs.kb.Held())
	}
}

// TestTextWithoutShift проверяет обычный набор: mKey сам зажимает Shift для заглавных.
func TestTextWithoutShift(t *testing.T) {
	t.Parallel()
	lp := &fakeLayouts{info: contracts.LayoutInfo{Current: "us", Available: []string{"us"}}}
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), lp)
	if err := m.Run(context.Background(), `{"Hello, World!\nOK"}`); err != nil {
		t.Fatal(err)
	}
	if got := typed(t, devs.kb.log(), func(int) string { return "us" }); got != "Hello, World!\nOK" {
		t.Fatalf("typed %q", got)
	}
}

// TestLayoutSwitching проверяет набор смешанного текста с переключением раскладки и возвратом исходной.
func TestLayoutSwitching(t *testing.T) {
	t.Parallel()
	lp := &fakeLayouts{info: contracts.LayoutInfo{Current: "us", Available: []string{"us", "ru"}, CanSwitch: true}}
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), lp)
	if err := m.Run(context.Background(), `{"Hi Вера"}`); err != nil {
		t.Fatal(err)
	}

	// Переключения: на ru для кириллицы, затем возврат на us.
	if !slices.Equal(lp.switches, []string{"ru", "us"}) {
		t.Fatalf("switches = %v", lp.switches)
	}

	// Раскладка в момент нажатия: us до первой кириллической буквы, затем ru.
	events := devs.kb.log()
	firstRu := slices.IndexFunc(events, func(e ev.Event) bool { return e.Type == ev.EvKey && e.Code == ev.KeyD && e.Value == 1 })
	got := typed(t, events, func(i int) string {
		if i >= firstRu-1 {
			return "ru"
		}
		return "us"
	})
	if got != "Hi Вера" {
		t.Fatalf("typed %q", got)
	}
}

// TestUntypeable проверяет понятную ошибку для символа, которого нет ни в одной раскладке.
func TestUntypeable(t *testing.T) {
	t.Parallel()
	lp := &fakeLayouts{info: contracts.LayoutInfo{Current: "us", Available: []string{"us"}}}
	m, _ := newTestModule(clock.NewFake(time.Unix(0, 0)), lp)
	err := m.Run(context.Background(), `{"Привет"}`)
	var de *dsl.Error
	if !errors.As(err, &de) || de.Code != dsl.ErrUntypeable || de.Args["char"] != "П" {
		t.Fatalf("err = %v", err)
	}
}

// TestAutoRelease проверяет, что зажатое макросом отпускается по его завершению (FR-DSL-5).
func TestAutoRelease(t *testing.T) {
	t.Parallel()
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	if err := m.Run(context.Background(), `^{Ctrl}^{Shift}^{Mouse0}`); err != nil {
		t.Fatal(err)
	}
	if len(devs.kb.Held()) != 0 || len(devs.mouse.Held()) != 0 {
		t.Fatalf("held after run: kb %v, mouse %v", devs.kb.Held(), devs.mouse.Held())
	}

	// Порядок отпускания обратный порядку нажатия: сначала Shift, потом Ctrl.
	kb := devs.kb.log()
	last := kb[len(kb)-2:]
	if last[0].Code != ev.KeyLeftshift || last[1].Code != ev.KeyLeftctrl {
		t.Fatalf("release order = %v", last)
	}
}

// blockingClock — часы, у которых пауза длится до отмены контекста.
type blockingClock struct{ clock.Real }

func (blockingClock) Sleep(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestStopAll проверяет, что остановка прерывает паузу и отпускает зажатые клавиши.
func TestStopAll(t *testing.T) {
	t.Parallel()
	m, devs := newTestModule(blockingClock{}, nil)

	// Макрос зажимает Shift и «вечно» ждёт.
	done := make(chan error, 1)
	go func() { done <- m.Run(context.Background(), `^{Shift}[3600s]`) }()
	deadline := time.Now().Add(2 * time.Second)
	for m.Running() == 0 || len(devs.kb.Held()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("macro did not start")
		}
		time.Sleep(time.Millisecond)
	}

	// Останавливаем: макрос прерван, Shift отпущен.
	m.StopAll()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("macro not stopped")
	}
	if len(devs.kb.Held()) != 0 || m.Running() != 0 {
		t.Fatalf("held %v, running %d", devs.kb.Held(), m.Running())
	}
}

// TestTimingAndMouse проверяет паузы, повторы и события мыши.
func TestTimingAndMouse(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(0, 0))
	m, devs := newTestModule(clk, nil)
	if err := m.Run(context.Background(), `{A*2}[100..200]{Space 500}{Move +10 -5}{Wheel Down 2}{Click Right}`); err != nil {
		t.Fatal(err)
	}

	// Паузы: два нажатия A (20 удержание + 10 после), случайная пауза в [100, 200], удержание Space 500.
	sleeps := clk.Sleeps()
	if sleeps[0] != 20*time.Millisecond || sleeps[1] != 10*time.Millisecond {
		t.Errorf("tap timing = %v", sleeps[:4])
	}
	if r := sleeps[4]; r < 100*time.Millisecond || r > 200*time.Millisecond {
		t.Errorf("random pause = %v", r)
	}
	if !slices.Contains(sleeps, 500*time.Millisecond) {
		t.Errorf("hold 500ms missing: %v", sleeps)
	}

	// Мышь: перемещение, два щелчка колеса вниз (обычный и высокого разрешения), правая кнопка.
	mouse := devs.mouse.log()
	want := []ev.Event{
		{Type: ev.EvRel, Code: ev.RelX, Value: 10}, {Type: ev.EvRel, Code: ev.RelY, Value: -5},
		{Type: ev.EvRel, Code: ev.RelWheel, Value: -1}, {Type: ev.EvRel, Code: ev.RelWheelHiRes, Value: -120},
		{Type: ev.EvRel, Code: ev.RelWheel, Value: -1}, {Type: ev.EvRel, Code: ev.RelWheelHiRes, Value: -120},
		{Type: ev.EvKey, Code: ev.BtnRight, Value: 1}, {Type: ev.EvKey, Code: ev.BtnRight, Value: 0},
	}
	if len(mouse) != len(want) {
		t.Fatalf("mouse events = %v", mouse)
	}
	for i := range want {
		if mouse[i].Type != want[i].Type || mouse[i].Code != want[i].Code || mouse[i].Value != want[i].Value {
			t.Errorf("mouse event %d = %v, want %v", i, mouse[i], want[i])
		}
	}
}

// TestParseErrorReturned проверяет, что ошибка в тексте макроса возвращается как *dsl.Error и ничего не нажимается.
func TestParseErrorReturned(t *testing.T) {
	t.Parallel()
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	err := m.Run(context.Background(), `{A}{Mous0}`)
	var de *dsl.Error
	if !errors.As(err, &de) || de.Code != dsl.ErrUnknownKeyHint {
		t.Fatalf("err = %v", err)
	}
	if len(devs.kb.log()) != 0 {
		t.Fatal("nothing must be pressed when the macro has errors")
	}
}

// fakePad — виртуальный геймпад проекта с осями (записывает положения осей).
type fakePad struct {
	*fakeDevice
	axes []string
}

// SetAxis записывает «код=положение».
func (p *fakePad) SetAxis(_ context.Context, code uint16, v float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.axes = append(p.axes, fmt.Sprintf("%d=%v", code, v))
	return nil
}

// fakeVdevs — виртуальные устройства проектов: только pad2 (кнопка South, ось LX).
type fakeVdevs struct {
	contracts.VirtualDeviceManager
	pad *fakePad
}

func (f fakeVdevs) Device(name string) (contracts.VirtualDevice, error) {
	if strings.EqualFold(name, "pad2") {
		return f.pad, nil
	}
	return nil, contracts.ErrUnknownVirtual
}

func (f fakeVdevs) Resolve(device, control string) (uint16, bool, error) {
	switch {
	case !strings.EqualFold(device, "pad2"):
		return 0, false, contracts.ErrUnknownVirtual
	case control == "South":
		return ev.BtnSouth, false, nil
	case control == "LX":
		return ev.AbsX, true, nil
	}
	return 0, false, contracts.ErrUnknownControl
}

// TestVirtualDevicesInMacro проверяет макрос с виртуальным устройством проекта: кнопка нажимается
// на нём, ось ставится, в конце макроса ось возвращается в покой; ошибки имён — понятные.
func TestVirtualDevicesInMacro(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	pad := &fakePad{fakeDevice: newFakeDevice("mKey pad2")}
	m.vdevs = fakeVdevs{pad: pad}

	// Кнопка и ось; после макроса ось — 0 (покой).
	if err := m.Run(context.Background(), "{pad2.South}{pad2.LX=0.75}"); err != nil {
		t.Fatal(err)
	}
	if log := pad.log(); len(log) != 2 || log[0].Code != ev.BtnSouth || log[0].Value != 1 || log[1].Value != 0 {
		t.Errorf("buttons: %v", log)
	}
	if strings.Join(pad.axes, " ") != fmt.Sprintf("%d=0.75 %d=0", ev.AbsX, ev.AbsX) {
		t.Errorf("axes: %v", pad.axes)
	}

	// Нет такой кнопки — понятная ошибка с местом; не виртуальное устройство — «не найдено».
	for src, code := range map[string]string{"{pad2.Nope}": dsl.ErrUnknownButton, "{pad3.South}": dsl.ErrUnknownDevice} {
		err := m.Run(context.Background(), src)
		var de *dsl.Error
		if !errors.As(err, &de) || de.Code != code {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// fakeVdevsIn — описание виртуальных устройств проекта: у xbox360 есть South и ось LX.
type fakeVdevsIn struct{ contracts.VirtualDeviceManager }

func (fakeVdevsIn) Validate(v project.VirtualDevice) error {
	if v.Template != "xbox360" {
		return errors.New("unknown template")
	}
	return nil
}

func (fakeVdevsIn) ResolveIn(_ project.VirtualDevice, control string) (uint16, bool, error) {
	switch control {
	case "South":
		return ev.BtnSouth, false, nil
	case "LX":
		return ev.AbsX, true, nil
	}
	return 0, false, contracts.ErrUnknownControl
}

func (fakeVdevsIn) Resolve(string, string) (uint16, bool, error) {
	return 0, false, contracts.ErrUnknownVirtual
}

// TestValidateBindings проверяет проверку привязок и виртуальных устройств проекта: место ошибки
// (привязка № N) и понятный вид ошибки.
func TestValidateBindings(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	m.vdevs = fakeVdevsIn{}
	pad := []project.VirtualDevice{{Name: "pad2", Template: "xbox360"}}
	ok := project.Project{VirtualDevices: pad, Bindings: []project.Binding{
		{From: "{W}", To: "{pad2.South}"}, {From: "{A}", To: "{pad2.LX}", Value: -1}, {From: "{F1}", To: "{Space}"},
	}}
	if err := m.validate(ok, true); err != nil {
		t.Fatalf("valid project: %v", err)
	}

	for _, c := range []struct {
		p    project.Project
		part string
		code string
	}{
		{project.Project{VirtualDevices: pad, Bindings: []project.Binding{{From: "{Nope}", To: "{pad2.South}"}}}, project.PartBinding, dsl.ErrUnknownKey},
		{project.Project{VirtualDevices: pad, Bindings: []project.Binding{{From: "{W}", To: "{pad3.South}"}}}, project.PartBinding, dsl.ErrUnknownDevice},
		{project.Project{VirtualDevices: pad, Bindings: []project.Binding{{From: "{W}", To: "{pad2.Nope}"}}}, project.PartBinding, dsl.ErrUnknownButton},
		{project.Project{VirtualDevices: pad, Bindings: []project.Binding{{From: "{W}", To: "{pad2.LX}"}}}, project.PartBinding, dsl.ErrBindingValue},
		{project.Project{VirtualDevices: pad, Bindings: []project.Binding{{From: "{W}", To: "{pad2.South}", Value: 1}}}, project.PartBinding, dsl.ErrAxisExpected},
		{project.Project{VirtualDevices: []project.VirtualDevice{{Name: "x", Template: "nope"}}}, project.PartVirtualDevice, ""},
	} {
		err := m.validate(c.p, true)
		var pr *project.Problem
		var de *dsl.Error
		if !errors.As(err, &pr) || pr.Part != c.part || pr.Index != 0 || (c.code != "" && (!errors.As(err, &de) || de.Code != c.code)) {
			t.Errorf("%+v: err = %v", c.p.Bindings, err)
		}
	}
}
