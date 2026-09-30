package engine

import (
	"context"
	"errors"
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
