package output

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/clock"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/registry"
)

// fakeWriter — фейковое uinput-устройство, записывающее отправленные пакеты.
type fakeWriter struct {
	mu      sync.Mutex
	packets [][]ev.Event
	closed  bool
	failErr error
}

func (f *fakeWriter) Write(events ...ev.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	f.packets = append(f.packets, append([]ev.Event(nil), events...))
	return nil
}

func (f *fakeWriter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// newTestDevice создаёт устройство с фейковыми часами и нулевым прогревом.
func newTestDevice() (*device, *fakeWriter, *clock.Fake) {
	w := &fakeWriter{}
	clk := clock.NewFake(time.Unix(0, 0))
	return newDevice("mKey Test", w, clk, 0, 0), w, clk
}

// TestTap проверяет пакеты нажатия/отпускания и паузу удержания.
func TestTap(t *testing.T) {
	t.Parallel()
	d, w, clk := newTestDevice()

	// Нажатие с удержанием 30 мс.
	if err := d.Tap(context.Background(), ev.KeyA, 30*time.Millisecond); err != nil {
		t.Fatalf("Tap: %v", err)
	}

	// Два пакета, каждый завершён SYN_REPORT; между ними пауза 30 мс; ничего не зажато.
	if len(w.packets) != 2 {
		t.Fatalf("packets = %v", w.packets)
	}
	down, up := w.packets[0], w.packets[1]
	if down[0].Value != ev.ValueDown || !down[1].IsSync() || up[0].Value != ev.ValueUp || !up[1].IsSync() {
		t.Fatalf("unexpected packets: %v", w.packets)
	}
	if !slices.Contains(clk.Sleeps(), 30*time.Millisecond) {
		t.Errorf("hold pause missing: %v", clk.Sleeps())
	}
	if len(d.Held()) != 0 {
		t.Errorf("Held = %v", d.Held())
	}
}

// TestTapCancelledReleases проверяет, что отменённое нажатие не оставляет клавишу зажатой.
func TestTapCancelledReleases(t *testing.T) {
	t.Parallel()
	d, _, _ := newTestDevice()

	// Зажимаем клавишу вручную, затем отменённый Tap другой клавиши.
	ctx, cancel := context.WithCancel(context.Background())
	if err := d.Press(ctx, ev.KeyLeftshift); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := d.Tap(ctx, ev.KeyA, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Tap on cancelled ctx = %v", err)
	}

	// A не зажата (Press не прошёл из-за отмены), Shift зажат, пока его не отпустили явно.
	if held := d.Held(); !slices.Equal(held, []uint16{ev.KeyLeftshift}) {
		t.Fatalf("Held = %v", held)
	}
	if err := d.ReleaseAll(); err != nil || len(d.Held()) != 0 {
		t.Fatalf("ReleaseAll: %v, held %v", err, d.Held())
	}
}

// TestSettleWait проверяет, что первые события ждут прогрева устройства.
func TestSettleWait(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	clk := clock.NewFake(time.Unix(0, 0))
	d := newDevice("mKey Test", w, clk, 500*time.Millisecond, 0)

	// Первое событие ждёт 500 мс, второе — нет.
	_ = d.Press(context.Background(), ev.KeyA)
	_ = d.Release(context.Background(), ev.KeyA)
	if s := clk.Sleeps(); !slices.Equal(s, []time.Duration{500 * time.Millisecond}) {
		t.Fatalf("Sleeps = %v", s)
	}
}

// TestWriteFailureKeepsHeld проверяет, что при ошибке записи набор зажатых клавиш не теряется.
func TestWriteFailureKeepsHeld(t *testing.T) {
	t.Parallel()
	d, w, _ := newTestDevice()
	_ = d.Press(context.Background(), ev.KeyA)

	// Запись ломается: ReleaseAll возвращает ошибку, клавиша остаётся в списке для повтора.
	w.failErr = errors.New("device gone")
	if err := d.ReleaseAll(); err == nil || len(d.Held()) != 1 {
		t.Fatalf("ReleaseAll = %v, held %v", err, d.Held())
	}
}

// TestTemplates проверяет имена и возможности шаблонов устройств.
func TestTemplates(t *testing.T) {
	t.Parallel()

	// Клавиатура: префиксы mKey, есть буквы, нет кнопок мыши.
	kb := keyboardSetup()
	if !strings.HasPrefix(kb.Name, contracts.VirtualNamePrefix) || !strings.HasPrefix(kb.Phys, contracts.VirtualPhysPrefix) {
		t.Errorf("keyboard name/phys = %q/%q", kb.Name, kb.Phys)
	}
	if !slices.Contains(kb.Keys, ev.KeyA) || slices.Contains(kb.Keys, ev.BtnLeft) || slices.Contains(kb.Keys, ev.BtnToolPen) {
		t.Error("keyboard must have KEY_* and no BTN_*")
	}

	// Мышь: левая кнопка и перемещение.
	ms := mouseSetup()
	if !slices.Contains(ms.Keys, ev.BtnLeft) || !slices.Contains(ms.Rels, ev.RelX) {
		t.Error("mouse must have BTN_LEFT and REL_X")
	}
}

// startModule запускает модуль в менеджере с фабрикой устройств create.
func startModule(t *testing.T, create creator) (*registry.Manager, *Module) {
	t.Helper()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mod := newModule(create, clock.NewFake(time.Unix(0, 0)))
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return mgr, mod
}

// TestModuleLifecycle проверяет создание устройств при старте и отпускание/уничтожение при остановке.
func TestModuleLifecycle(t *testing.T) {
	t.Parallel()

	// Фабрика запоминает созданные фейки.
	var writers []*fakeWriter
	mgr, mod := startModule(t, func(ev.Setup) (eventWriter, error) {
		w := &fakeWriter{}
		writers = append(writers, w)
		return w, nil
	})

	// Сервис опубликован, обе устройства созданы.
	svc, err := contracts.LookupService[contracts.VirtualDevices](mgr.Services())
	if err != nil || !svc.Status().Available || len(writers) != 2 {
		t.Fatalf("service %v, status %+v, writers %d", err, mod.Status(), len(writers))
	}

	// Зажимаем клавишу и останавливаем модуль: клавиша отпущена, устройства закрыты.
	kb, _ := svc.Keyboard()
	_ = kb.Press(context.Background(), ev.KeyA)
	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last := writers[0].packets[len(writers[0].packets)-1]
	if last[0].Code != ev.KeyA || last[0].Value != ev.ValueUp || !writers[0].closed || !writers[1].closed {
		t.Fatalf("keyboard not released/closed: %v", writers[0].packets)
	}
}

// TestModuleUnavailable проверяет, что без прав модуль не падает, а сообщает о недоступности.
func TestModuleUnavailable(t *testing.T) {
	t.Parallel()

	// Фабрика всегда отказывает в доступе.
	_, mod := startModule(t, func(ev.Setup) (eventWriter, error) { return nil, os.ErrPermission })

	// Модуль запущен, но недоступен; Keyboard возвращает ErrOutputUnavailable.
	if st := mod.Status(); st.Available || st.Error == "" {
		t.Fatalf("status = %+v", st)
	}
	if _, err := mod.Keyboard(); !errors.Is(err, contracts.ErrOutputUnavailable) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Keyboard err = %v", err)
	}
}

// TestRateLimit проверяет, что сверх предела события замедляются до следующей секунды (SEC-4).
func TestRateLimit(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	d := newDevice("mKey Test", w, clk, 0, 5)

	// Пять пакетов (нажатие + SYN — один пакет) укладываются в предел 5/с без ожидания.
	for range 5 {
		if err := d.Press(context.Background(), ev.KeyA); err != nil {
			t.Fatal(err)
		}
	}
	if len(clk.Sleeps()) != 5 || slices.ContainsFunc(clk.Sleeps(), func(s time.Duration) bool { return s > 0 }) {
		t.Fatalf("unexpected waits: %v", clk.Sleeps())
	}

	// Шестой пакет превышает предел — ждём до конца текущей секунды, и событие всё равно отправлено.
	if err := d.Press(context.Background(), ev.KeyB); err != nil {
		t.Fatal(err)
	}
	if last := clk.Sleeps()[len(clk.Sleeps())-1]; last != time.Second {
		t.Fatalf("throttle wait = %v, want 1s", last)
	}
	if len(w.packets) != 6 {
		t.Fatalf("packets = %d", len(w.packets))
	}
}

// TestCenterPointer проверяет виртуальный указатель: создаётся при первом использовании
// и получает середину обеих осей.
func TestCenterPointer(t *testing.T) {
	t.Parallel()
	var setups []ev.Setup
	var writers []*fakeWriter
	mgr, mod := startModule(t, func(s ev.Setup) (eventWriter, error) {
		w := &fakeWriter{}
		setups, writers = append(setups, s), append(writers, w)
		return w, nil
	})
	defer func() { _ = mgr.Stop(context.Background()) }()
	mod.cfg.SettleMS = 0
	if err := mod.CenterPointer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(setups) != 3 || setups[2].Name != "mKey Pointer" || len(setups[2].Abs) != 2 {
		t.Fatalf("setups = %+v", setups)
	}
	// Два пакета: соседнее значение (ядро отбрасывает повтор прежних значений), затем середина;
	// повторная постановка отправляет то же самое — и снова доходит до композитора.
	if err := mod.CenterPointer(context.Background()); err != nil {
		t.Fatal(err)
	}
	pk := writers[2].packets
	if len(pk) != 4 {
		t.Fatalf("packets = %v", pk)
	}
	for i, want := range []int32{16383, 16384, 16383, 16384} {
		if pk[i][0].Code != ev.AbsX || pk[i][0].Value != want || pk[i][1].Code != ev.AbsY || pk[i][1].Value != want || !pk[i][2].IsSync() {
			t.Fatalf("packet %d = %v", i, pk[i])
		}
	}
}
