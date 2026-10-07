package tray

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/buildinfo"
	"github.com/khameleonium/mKey/internal/lib/project"
	"github.com/khameleonium/mKey/internal/lib/sni"
	"github.com/khameleonium/mKey/internal/registry"
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
func (f *fakeServices) Restart(string)       {}
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

// find ищет пункт меню с текстом label, в том числе в подменю.
func find(items []sni.MenuItem, label string) (sni.MenuItem, bool) {
	for _, it := range items {
		if it.Label == label {
			return it, true
		}
		if c, ok := find(it.Children, label); ok {
			return c, true
		}
	}
	return sni.MenuItem{}, false
}

// click выбирает пункт меню с текстом label (и в подменю).
func click(t *testing.T, item *fakeItem, label string) {
	t.Helper()
	item.mu.Lock()
	it, ok := find(item.menu, label)
	item.mu.Unlock()
	if !ok || it.OnClick == nil {
		t.Fatalf("no menu item %q", label)
	}
	it.OnClick()
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
	want := []string{"Open mKey", "-", "Emergency stop", "Quit mKey", "-", "Creator: " + buildinfo.Creator}
	if got := item.labels(); strings.Join(got, "|") != strings.Join(want, "|") {
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

// fullServices — проекты, события, запись, повтор, остановка, уведомления (для полного меню).
type fullServices struct {
	contracts.Projects
	contracts.Events
	contracts.Recorder
	contracts.Player
	contracts.SequenceRunner
	mu        sync.Mutex
	b         contracts.Bus
	enabled   map[string]bool
	recording bool
	ran       []string
	played    []string
	stopped   int
	notices   []string
}

func (f *fullServices) List() []contracts.ProjectState {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.ProjectState
	for _, id := range []string{"games", "work"} {
		on := f.enabled[id]
		out = append(out, contracts.ProjectState{Project: project.Project{ID: id, Name: strings.ToUpper(id[:1]) + id[1:], Enabled: &on}})
	}
	return out
}
func (f *fullServices) SetEnabled(id string, on bool) error {
	f.mu.Lock()
	f.enabled[id] = on
	f.mu.Unlock()
	f.b.Publish(contracts.TopicProjectsChanged, nil)
	return nil
}
func (f *fullServices) Statuses() []contracts.EventStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []contracts.EventStatus{
		{EventRef: contracts.EventRef{Project: "games", Event: "macro", Name: "Combo"}, Enabled: f.enabled["games"], Triggers: []string{"manual"}},
		{EventRef: contracts.EventRef{Project: "games", Event: "f8", Name: "Autoclick"}, Enabled: f.enabled["games"], Triggers: []string{"hotkey"}},
		{EventRef: contracts.EventRef{Project: "work", Event: "report"}, Enabled: f.enabled["work"], Triggers: []string{"manual"}},
	}
}
func (f *fullServices) ValidateProject(p project.Project) error {
	if p.ID == "work" && f.enabled["broken"] {
		return errors.New("broken")
	}
	return nil
}
func (f *fullServices) RunEvent(_ context.Context, p, e string) error {
	f.mu.Lock()
	f.ran = append(f.ran, p+"/"+e)
	f.mu.Unlock()
	return nil
}
func (f *fullServices) Recording() (contracts.RecordingInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return contracts.RecordingInfo{Name: "2026-10-01"}, f.recording
}
func (f *fullServices) StartRecording(contracts.RecordOptions) (contracts.RecordingInfo, error) {
	f.mu.Lock()
	f.recording = true
	f.mu.Unlock()
	f.b.Publish(contracts.TopicRecordingStarted, nil)
	return contracts.RecordingInfo{Name: "2026-10-01", StopHotkey: "^{Ctrl}^{Alt}{R}"}, nil
}
func (f *fullServices) StopRecording(string) (contracts.RecordingInfo, error) {
	f.mu.Lock()
	f.recording = false
	f.mu.Unlock()
	f.b.Publish(contracts.TopicRecordingStopped, nil)
	return contracts.RecordingInfo{Name: "2026-10-01"}, nil
}
func (f *fullServices) Recordings() ([]contracts.RecordingInfo, error) {
	return []contracts.RecordingInfo{{Name: "game"}, {Name: "broken", Problem: &contracts.RecordingProblem{Code: "key"}}}, nil
}
func (f *fullServices) Play(_ context.Context, name string, _ contracts.PlayOptions) error {
	f.mu.Lock()
	f.played = append(f.played, name)
	f.mu.Unlock()
	return nil
}
func (f *fullServices) StopPlayback() int { f.mu.Lock(); f.stopped++; f.mu.Unlock(); return 0 }
func (f *fullServices) StopAll()          { f.mu.Lock(); f.stopped++; f.mu.Unlock() }
func (f *fullServices) Notify(_ context.Context, _, body string) error {
	f.mu.Lock()
	f.notices = append(f.notices, body)
	f.mu.Unlock()
	return nil
}

// TestFullMenu проверяет полное меню: проекты с галочками (включить/выключить), запуск событий
// «только вручную», начало и конец записи, повтор записи, остановку всего, открытие папок.
func TestFullMenu(t *testing.T) {
	t.Parallel()
	m, svc, item := newTestModule(t)
	full := &fullServices{b: m.bus, enabled: map[string]bool{"games": true}}
	m.projects, m.events, m.recorder, m.player, m.runner, m.notifier = full, full, full, full, full, full
	ext := registry.NewExtensions()
	for _, id := range []string{contracts.PlaceProjects, contracts.PlaceRecordings} {
		_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{M: contracts.ExtensionMeta{ID: id}, P: t.TempDir() + "/" + id, Dir: true})
	}
	m.ext = ext
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	// Разделы меню; у проектов — галочки, «Повторить запись» — без записей с ошибкой.
	want := "Open mKey|-|Projects|Run event|-|● Start recording|Replay recording|Stop all macros|-|Open the projects folder|Open the recordings folder|-|Emergency stop|Quit mKey|-|Creator: " + buildinfo.Creator
	if got := strings.Join(item.labels(), "|"); got != want {
		t.Fatalf("menu =\n%s\nwant\n%s", got, want)
	}
	item.mu.Lock()
	projects, _ := find(item.menu, "Projects")
	events, _ := find(item.menu, "Run event")
	replay, _ := find(item.menu, "Replay recording")
	item.mu.Unlock()
	if len(projects.Children) != 2 || !projects.Children[0].Checked || projects.Children[1].Checked || projects.Children[0].Label != "Games" {
		t.Errorf("projects: %+v", projects.Children)
	}
	if len(events.Children) != 1 || events.Children[0].Label != "Games → Combo" || len(replay.Children) != 1 || replay.Children[0].Label != "game" {
		t.Errorf("events: %+v replay: %+v", events.Children, replay.Children)
	}

	// Включить «Work»: меню обновляется, его событие появляется.
	click(t, item, "Work")
	eventually(t, func() bool {
		item.mu.Lock()
		defer item.mu.Unlock()
		ev, _ := find(item.menu, "Run event")
		return len(ev.Children) == 2
	})

	// Запуск события, начало и конец записи (пункт меняется), остановка всего.
	click(t, item, "Games → Combo")
	click(t, item, "● Start recording")
	eventually(t, func() bool {
		return strings.Contains(strings.Join(item.labels(), "|"), "■ Stop recording «2026-10-01»")
	})
	click(t, item, "■ Stop recording «2026-10-01»")
	eventually(t, func() bool { return strings.Contains(strings.Join(item.labels(), "|"), "● Start recording") })
	click(t, item, "Stop all macros")
	eventually(t, func() bool {
		full.mu.Lock()
		defer full.mu.Unlock()
		return len(full.ran) == 1 && full.stopped == 2 && len(full.notices) == 2
	})

	// Папка проектов открывается (и создаётся, если её нет).
	click(t, item, "Open the projects folder")
	if !strings.HasSuffix(svc.opened, "/"+contracts.PlaceProjects) {
		t.Errorf("opened %q", svc.opened)
	}
}
