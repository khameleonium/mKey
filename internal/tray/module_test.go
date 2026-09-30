package tray

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/sni"
	"mkey/internal/registry"
)

// fakeItem — значок в памяти: запоминает меню и подсказку.
type fakeItem struct {
	mu      sync.Mutex
	menu    []sni.MenuItem
	tooltip string
	closed  bool
}

func (f *fakeItem) SetIcon([]sni.Pixmap) {}
func (f *fakeItem) SetTooltip(_, body string) {
	f.mu.Lock()
	f.tooltip = body
	f.mu.Unlock()
}
func (f *fakeItem) SetMenu(items []sni.MenuItem) {
	f.mu.Lock()
	f.menu = items
	f.mu.Unlock()
}
func (f *fakeItem) Close() error { f.closed = true; return nil }

// labels возвращает тексты пунктов меню (разделители — "-").
func (f *fakeItem) labels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, it := range f.menu {
		if it.Separator {
			out = append(out, "-")
		} else {
			out = append(out, it.Label)
		}
	}
	return out
}

// fakeServices — все сервисы, которыми пользуется меню, в одном типе.
type fakeServices struct {
	contracts.InputSource
	contracts.KeyState
	mu        sync.Mutex
	b         contracts.Bus
	suspended bool
	opened    string
	shutdown  bool
}

func (f *fakeServices) GUIURL() (string, error) { return "http://127.0.0.1:1/?t=x", nil }
func (f *fakeServices) OpenURL(_ context.Context, url string) error {
	f.mu.Lock()
	f.opened = url
	f.mu.Unlock()
	return nil
}
func (f *fakeServices) EmergencyStop(string) {
	f.mu.Lock()
	f.suspended = true
	f.mu.Unlock()
	f.b.Publish(contracts.TopicEmergency, nil)
}
func (f *fakeServices) Resume() {
	f.mu.Lock()
	f.suspended = false
	f.mu.Unlock()
	f.b.Publish(contracts.TopicResumed, nil)
}
func (f *fakeServices) Suspended() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suspended
}
func (f *fakeServices) Shutdown()            { f.mu.Lock(); f.shutdown = true; f.mu.Unlock() }
func (f *fakeServices) StartedAt() time.Time { return time.Time{} }

// newTestModule создаёт модуль с фейковыми сервисами и значком.
func newTestModule(t *testing.T) (*Module, *fakeServices, *fakeItem) {
	t.Helper()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New(0)
	svc := &fakeServices{b: b}
	item := &fakeItem{}
	m := New()
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.tr = i18n.New(cat, "en")
	m.bus = b
	m.gui, m.opener, m.input, m.keys, m.life = svc, svc, svc, svc, svc
	m.newItem = func(_ sni.Options, items []sni.MenuItem) (trayItem, error) {
		item.SetMenu(items)
		return item, nil
	}
	return m, svc, item
}

// click выбирает пункт меню с текстом label.
func click(t *testing.T, item *fakeItem, label string) {
	t.Helper()
	item.mu.Lock()
	defer item.mu.Unlock()
	for _, it := range item.menu {
		if it.Label == label {
			it.OnClick()
			return
		}
	}
	t.Fatalf("no menu item %q", label)
}

// eventually ждёт выполнения условия (обновление меню идёт через шину в фоне).
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestMenuFlow проверяет пункты меню: открыть окно, экстренная остановка, продолжить, выйти.
func TestMenuFlow(t *testing.T) {
	t.Parallel()
	m, svc, item := newTestModule(t)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Начальное меню.
	want := []string{"Open mKey", "-", "Emergency stop", "-", "Quit mKey"}
	if got := item.labels(); len(got) != len(want) || got[0] != want[0] || got[2] != want[2] || got[4] != want[4] {
		t.Fatalf("menu = %v", got)
	}

	// «Открыть» передаёт адрес окна браузеру.
	click(t, item, "Open mKey")
	if svc.opened == "" {
		t.Fatal("GUI not opened")
	}

	// Экстренная остановка меняет пункт на «Продолжить работу», а тот возвращает прежний.
	click(t, item, "Emergency stop")
	eventually(t, func() bool { return item.labels()[2] == "Resume work" })
	click(t, item, "Resume work")
	eventually(t, func() bool { return item.labels()[2] == "Emergency stop" })

	// Выход завершает программу; Stop убирает значок.
	click(t, item, "Quit mKey")
	if !svc.shutdown {
		t.Fatal("not shut down")
	}
	if err := m.Stop(context.Background()); err != nil || !item.closed {
		t.Fatalf("stop: %v closed=%v", err, item.closed)
	}
}

// TestNoTray проверяет, что без трея и с выключенной настройкой модуль работает без ошибок.
func TestNoTray(t *testing.T) {
	t.Parallel()

	// Нет D-Bus или трея: Start не падает.
	m, _, _ := newTestModule(t)
	m.newItem = func(sni.Options, []sni.MenuItem) (trayItem, error) { return nil, errors.New("no bus") }
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Значок выключен в настройках: значок не создаётся.
	m2, _, _ := newTestModule(t)
	m2.cfg.Enabled = false
	m2.newItem = func(sni.Options, []sni.MenuItem) (trayItem, error) { t.Fatal("item created"); return nil, nil }
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestLifecycle проверяет полный цикл Init → Start → Stop в менеджере модулей (значок выключен,
// чтобы тест не показывал его на рабочем столе).
func TestLifecycle(t *testing.T) {
	t.Parallel()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	mod := New()
	mod.newItem = func(sni.Options, []sni.MenuItem) (trayItem, error) { return &fakeItem{}, nil }
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: mod}})
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
