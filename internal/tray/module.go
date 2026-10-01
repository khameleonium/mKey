package tray

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/sni"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "tray"

// Config — настройки модуля из секции modules.tray в config.yaml.
type Config struct {
	// Enabled — показывать значок в трее (по умолчанию да).
	Enabled bool `json:"enabled"`
}

// trayItem — значок в трее; реализация — *sni.Item, в тестах — фейк.
type trayItem interface {
	SetIcon(icon []sni.Pixmap)
	SetTooltip(title, body string)
	SetMenu(items []sni.MenuItem)
	Close() error
}

// Module — реализация contracts.Module для модуля «tray».
type Module struct {
	// log — логгер; cfg — настройки; tr — переводчик; bus — шина (заполняются в Init).
	log *slog.Logger
	cfg Config
	tr  contracts.Translator
	bus contracts.Bus

	// Сервисы других модулей (любой может быть nil — тогда его пунктов в меню нет).
	gui      contracts.GUIServer
	opener   contracts.URLOpener
	input    contracts.InputSource
	keys     contracts.KeyState
	life     contracts.Lifecycle
	projects contracts.Projects
	events   contracts.Events
	runner   contracts.SequenceRunner
	recorder contracts.Recorder
	player   contracts.Player
	notifier contracts.Notifier
	// ext — реестр расширений: папки проектов и записей (места «Где что лежит»).
	ext contracts.ExtensionRegistry

	// newItem создаёт значок (подменяется в тестах).
	newItem func(opts sni.Options, items []sni.MenuItem) (trayItem, error)

	// mu защищает item и paused; item — nil, если значок не показан.
	mu     sync.Mutex
	item   trayItem
	paused bool

	// Подписки на шину и фоновая горутина; ctx живёт до Stop (прерывает отсчёт перед повтором).
	unsub  []func()
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{
		cfg: Config{Enabled: true},
		newItem: func(opts sni.Options, items []sni.MenuItem) (trayItem, error) {
			return sni.New(opts, items)
		},
	}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и находит сервисы, которыми пользуется меню.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Логгер, переводчик, шина и секция конфига.
	m.log = host.Logger()
	m.tr = host.I18n()
	m.bus = host.Bus()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// Сервисы других модулей: отсутствующий сервис просто убирает пункт меню.
	s := host.Services()
	m.gui, _ = contracts.LookupService[contracts.GUIServer](s)
	m.opener, _ = contracts.LookupService[contracts.URLOpener](s)
	m.input, _ = contracts.LookupService[contracts.InputSource](s)
	m.keys, _ = contracts.LookupService[contracts.KeyState](s)
	m.life, _ = contracts.LookupService[contracts.Lifecycle](s)
	m.projects, _ = contracts.LookupService[contracts.Projects](s)
	m.events, _ = contracts.LookupService[contracts.Events](s)
	m.runner, _ = contracts.LookupService[contracts.SequenceRunner](s)
	m.recorder, _ = contracts.LookupService[contracts.Recorder](s)
	m.player, _ = contracts.LookupService[contracts.Player](s)
	m.notifier, _ = contracts.LookupService[contracts.Notifier](s)
	m.ext = host.Extensions()
	return nil
}

// Start показывает значок и начинает следить за экстренной остановкой.
// Без D-Bus или трея модуль продолжает работать без значка (это не ошибка).
func (m *Module) Start(context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	// Начальное состояние: mKey мог быть уже приостановлен.
	m.paused = m.keys != nil && m.keys.Suspended()
	item, err := m.newItem(sni.Options{
		ID:          "mkey",
		Title:       "mKey",
		Icon:        drawIcons(m.palette()),
		Tooltip:     "mKey",
		TooltipBody: m.tooltipBody(),
		OnActivate:  m.openGUI,
	}, m.menu())
	if err != nil {
		m.log.Info("tray icon is not shown", "err", err)
		return nil
	}
	m.mu.Lock()
	m.item = item
	m.mu.Unlock()

	// Следим за экстренной остановкой и возобновлением (значок и меню), а также за проектами
	// и записью (пункты меню: список проектов, «Начать/Закончить запись», записи).
	emer, u1 := m.bus.Subscribe(contracts.TopicEmergency)
	resumed, u2 := m.bus.Subscribe(contracts.TopicResumed)
	changes, u3 := m.bus.Subscribe("*")
	m.unsub = []func(){u1, u2, u3}
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx, m.cancel = ctx, cancel
	m.wg.Add(1)
	go m.watch(ctx, emer, resumed, changes)
	return nil
}

// menuTopics — темы шины, после которых меню собирается заново.
var menuTopics = map[string]bool{
	contracts.TopicProjectsChanged:  true,
	contracts.TopicRecordingStarted: true,
	contracts.TopicRecordingStopped: true,
}

// watch обновляет значок при экстренной остановке и возобновлении работы, а меню — при
// изменении проектов и записи.
func (m *Module) watch(ctx context.Context, emer, resumed, changes <-chan contracts.Event) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-emer:
			if !ok {
				return
			}
			m.setPaused(true)
		case _, ok := <-resumed:
			if !ok {
				return
			}
			m.setPaused(false)
		case e, ok := <-changes:
			if !ok {
				return
			}
			if menuTopics[e.Topic] {
				m.refresh()
			}
		}
	}
}

// refresh собирает меню заново (проекты, запись).
func (m *Module) refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.item != nil {
		m.item.SetMenu(m.menu())
	}
}

// setPaused меняет цвет значка, подсказку и пункты меню.
func (m *Module) setPaused(paused bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.item == nil {
		return
	}
	m.paused = paused
	m.item.SetIcon(drawIcons(m.palette()))
	m.item.SetTooltip("mKey", m.tooltipBody())
	m.item.SetMenu(m.menu())
}

// palette возвращает цвета значка для текущего состояния.
func (m *Module) palette() palette {
	if m.paused {
		return pausedPalette
	}
	return activePalette
}

// tooltipBody возвращает текст подсказки для текущего состояния.
func (m *Module) tooltipBody() string {
	if m.paused {
		return m.tr.T("tray.tooltip.paused")
	}
	return m.tr.T("tray.tooltip.active")
}

// maxRecordings — сколько последних записей показывать в «Повторить запись».
const maxRecordings = 10

// playCountdown — пауза перед повтором записи из меню: успеть переключиться в нужное окно.
const playCountdown = 3 * time.Second

// menu собирает пункты меню для текущего состояния: разделы через разделитель, пустые разделы
// не показываются; пункты без нужного сервиса тоже.
func (m *Module) menu() []sni.MenuItem {
	var sections [][]sni.MenuItem

	// Открыть окно программы.
	if m.gui != nil && m.opener != nil {
		sections = append(sections, []sni.MenuItem{{Label: m.tr.T("tray.open"), OnClick: m.openGUI}})
	}

	// Проекты (включить/выключить) и события «только вручную» (запустить).
	var proj []sni.MenuItem
	if p := m.projectsMenu(); p != nil {
		proj = append(proj, *p)
	}
	if e := m.eventsMenu(); e != nil {
		proj = append(proj, *e)
	}
	sections = append(sections, proj)

	// Запись и повтор, остановка макросов.
	rec := m.recordMenu()
	if m.runner != nil || m.player != nil {
		rec = append(rec, sni.MenuItem{Label: m.tr.T("tray.stop_all"), OnClick: m.stopAll})
	}
	sections = append(sections, rec)

	// Папки проектов и записей.
	var folders []sni.MenuItem
	for _, f := range []struct{ place, key string }{{contracts.PlaceProjects, "tray.open_projects"}, {contracts.PlaceRecordings, "tray.open_recordings"}} {
		if path := m.placePath(f.place); path != "" && m.opener != nil {
			folders = append(folders, sni.MenuItem{Label: m.tr.T(f.key), OnClick: func() { m.openPath(path) }})
		}
	}
	sections = append(sections, folders)

	// Экстренная остановка или, если mKey уже приостановлен, возобновление; выход.
	var tail []sni.MenuItem
	switch {
	case m.paused && m.keys != nil:
		tail = append(tail, sni.MenuItem{Label: m.tr.T("tray.resume"), OnClick: m.keys.Resume})
	case !m.paused && m.input != nil:
		tail = append(tail, sni.MenuItem{Label: m.tr.T("tray.emergency"), OnClick: func() { m.input.EmergencyStop("tray") }})
	}
	if m.life != nil {
		tail = append(tail, sni.MenuItem{Label: m.tr.T("tray.quit"), OnClick: m.life.Shutdown})
	}
	sections = append(sections, tail)

	// Разделы — через разделитель.
	var items []sni.MenuItem
	for _, sec := range sections {
		if len(sec) == 0 {
			continue
		}
		if len(items) > 0 {
			items = append(items, sni.MenuItem{Separator: true})
		}
		items = append(items, sec...)
	}
	return items
}

// projectsMenu — подменю «Проекты»: у каждого галочка «включён», щелчок включает или выключает.
func (m *Module) projectsMenu() *sni.MenuItem {
	if m.projects == nil {
		return nil
	}
	sub := sni.MenuItem{Label: m.tr.T("tray.projects")}
	for _, st := range m.projects.List() {
		p, on := st.Project, st.Project.IsEnabled()
		sub.Children = append(sub.Children, sni.MenuItem{
			Label: projectTitle(p.Name, p.ID), Checkable: true, Checked: on,
			OnClick: func() {
				// Проект с ошибкой не включается: человек видит, что не так.
				if !on && m.events != nil {
					if err := m.events.ValidateProject(p); err != nil {
						m.notify(m.tr.T("tray.project_invalid", contracts.Arg{Name: "project", Value: projectTitle(p.Name, p.ID)}), err.Error())
						return
					}
				}
				if err := m.projects.SetEnabled(p.ID, !on); err != nil {
					m.notify(m.tr.T("tray.error"), err.Error())
				}
			},
		})
	}
	if len(sub.Children) == 0 {
		sub.Children = []sni.MenuItem{{Label: m.tr.T("tray.no_projects"), Disabled: true}}
	}
	return &sub
}

// eventsMenu — подменю «Запустить событие»: события «только вручную» включённых проектов.
func (m *Module) eventsMenu() *sni.MenuItem {
	if m.events == nil {
		return nil
	}
	titles := map[string]string{}
	if m.projects != nil {
		for _, st := range m.projects.List() {
			titles[st.Project.ID] = projectTitle(st.Project.Name, st.Project.ID)
		}
	}
	sub := sni.MenuItem{Label: m.tr.T("tray.run_event")}
	for _, ev := range m.events.Statuses() {
		if !ev.Enabled || !slices.Contains(ev.Triggers, "manual") {
			continue
		}
		ref := ev.EventRef
		sub.Children = append(sub.Children, sni.MenuItem{
			Label:   cmp.Or(titles[ref.Project], ref.Project) + " → " + cmp.Or(ref.Name, ref.Event),
			OnClick: func() { go m.runEvent(ref) },
		})
	}
	if len(sub.Children) == 0 {
		return nil
	}
	return &sub
}

// recordMenu — «Начать/Закончить запись» и подменю «Повторить запись» с последними записями.
func (m *Module) recordMenu() []sni.MenuItem {
	var items []sni.MenuItem
	if m.recorder != nil {
		if cur, ok := m.recorder.Recording(); ok {
			items = append(items, sni.MenuItem{Label: m.tr.T("tray.record_stop", contracts.Arg{Name: "name", Value: cur.Name}), OnClick: m.stopRecording})
		} else {
			items = append(items, sni.MenuItem{Label: m.tr.T("tray.record_start"), OnClick: m.startRecording})
		}
	}
	if m.recorder != nil && m.player != nil {
		list, _ := m.recorder.Recordings()
		sub := sni.MenuItem{Label: m.tr.T("tray.play")}
		for _, r := range list {
			if r.Problem != nil {
				continue
			}
			name := r.Name
			sub.Children = append(sub.Children, sni.MenuItem{Label: name, OnClick: func() { go m.play(name) }})
			if len(sub.Children) == maxRecordings {
				break
			}
		}
		// Подменю есть всегда, как «Проекты»: записей нет — так и написано.
		if len(sub.Children) == 0 {
			sub.Children = []sni.MenuItem{{Label: m.tr.T("tray.no_recordings"), Disabled: true}}
		}
		items = append(items, sub)
	}
	return items
}

// projectTitle — название проекта для меню: имя, иначе ID.
func projectTitle(name, id string) string { return cmp.Or(name, id) }

// startRecording начинает запись (имя — по дате и времени) и напоминает, как её закончить.
func (m *Module) startRecording() {
	info, err := m.recorder.StartRecording(contracts.RecordOptions{})
	if err != nil {
		m.notify(m.tr.T("tray.error"), err.Error())
		return
	}
	body := m.tr.T("tray.recording_started")
	if info.StopHotkey != "" {
		body = m.tr.T("tray.recording_started_hotkey", contracts.Arg{Name: "hotkey", Value: info.StopHotkey})
	}
	m.notify("mKey", body)
}

// stopRecording заканчивает запись и сообщает, что она сохранена.
func (m *Module) stopRecording() {
	info, err := m.recorder.StopRecording("")
	if err != nil {
		m.notify(m.tr.T("tray.error"), err.Error())
		return
	}
	m.notify("mKey", m.tr.T("tray.recording_saved", contracts.Arg{Name: "name", Value: info.Name}))
}

// play повторяет запись после отсчёта (успеть переключиться в нужное окно).
func (m *Module) play(name string) {
	m.notify("mKey", m.tr.T("tray.play_soon", contracts.Arg{Name: "name", Value: name}, contracts.Arg{Name: "seconds", Value: int(playCountdown.Seconds())}))
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-time.After(playCountdown):
	case <-ctx.Done():
		return
	}
	// Повтор; по окончании — уведомление (чтобы было видно, что запись проиграна целиком).
	err := m.player.Play(ctx, name, contracts.PlayOptions{})
	switch {
	case err == nil:
		m.notify("mKey", m.tr.T("tray.play_done", contracts.Arg{Name: "name", Value: name}))
	case !errors.Is(err, context.Canceled):
		m.notify(m.tr.T("tray.error"), err.Error())
	}
}

// runEvent запускает событие вручную; ошибку показывает уведомлением.
func (m *Module) runEvent(ref contracts.EventRef) {
	if err := m.events.RunEvent(context.Background(), ref.Project, ref.Event); err != nil {
		m.notify(m.tr.T("tray.error"), err.Error())
	}
}

// stopAll останавливает все выполняющиеся макросы и повторы записей (их клавиши отпускаются).
func (m *Module) stopAll() {
	if m.runner != nil {
		m.runner.StopAll()
	}
	if m.player != nil {
		m.player.StopPlayback()
	}
}

// placePath возвращает путь места «Где что лежит» по ID ("" — места нет).
func (m *Module) placePath(id string) string {
	if m.ext == nil {
		return ""
	}
	if e, ok := m.ext.Get(contracts.PointPlace, id); ok {
		if p, ok := e.(contracts.Place); ok {
			return p.Path()
		}
	}
	return ""
}

// openPath открывает папку в файловом менеджере (папки ещё нет — создаёт её).
func (m *Module) openPath(path string) {
	_ = os.MkdirAll(path, 0o700)
	if err := m.opener.OpenURL(context.Background(), path); err != nil {
		m.notify(m.tr.T("tray.error"), err.Error())
	}
}

// notify показывает уведомление (если модуль уведомлений есть) и пишет его в журнал.
func (m *Module) notify(title, body string) {
	m.log.Info("tray notice", "title", title, "body", body)
	if m.notifier != nil {
		_ = m.notifier.Notify(context.Background(), title, body)
	}
}

// openGUI открывает веб-интерфейс в браузере.
func (m *Module) openGUI() {
	if m.gui == nil || m.opener == nil {
		return
	}
	url, err := m.gui.GUIURL()
	if err == nil {
		err = m.opener.OpenURL(context.Background(), url)
	}
	if err != nil {
		m.log.Warn("cannot open web interface", "err", err)
	}
}

// Stop убирает значок из трея.
func (m *Module) Stop(context.Context) error {
	// Останавливаем слежение за шиной.
	if m.cancel != nil {
		m.cancel()
	}
	for _, u := range m.unsub {
		u()
	}
	m.wg.Wait()

	// Убираем значок.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.item == nil {
		return nil
	}
	err := m.item.Close()
	m.item = nil
	return err
}

// Проверка на этапе компиляции, что Module реализует контракт.
var _ contracts.Module = (*Module)(nil)
