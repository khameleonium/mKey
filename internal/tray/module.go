package tray

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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

	// Сервисы других модулей (любой может быть nil).
	gui    contracts.GUIServer
	opener contracts.URLOpener
	input  contracts.InputSource
	keys   contracts.KeyState
	life   contracts.Lifecycle

	// newItem создаёт значок (подменяется в тестах).
	newItem func(opts sni.Options, items []sni.MenuItem) (trayItem, error)

	// mu защищает item и paused; item — nil, если значок не показан.
	mu     sync.Mutex
	item   trayItem
	paused bool

	// Подписки на шину и фоновая горутина.
	unsub  []func()
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

	// Следим за экстренной остановкой и возобновлением, чтобы менять значок и меню.
	emer, u1 := m.bus.Subscribe(contracts.TopicEmergency)
	resumed, u2 := m.bus.Subscribe(contracts.TopicResumed)
	m.unsub = []func(){u1, u2}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go m.watch(ctx, emer, resumed)
	return nil
}

// watch обновляет значок при экстренной остановке и возобновлении работы.
func (m *Module) watch(ctx context.Context, emer, resumed <-chan contracts.Event) {
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
		}
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

// menu собирает пункты меню для текущего состояния; пункты без нужного сервиса не показываются.
func (m *Module) menu() []sni.MenuItem {
	var items []sni.MenuItem

	// Открыть окно программы.
	if m.gui != nil && m.opener != nil {
		items = append(items, sni.MenuItem{Label: m.tr.T("tray.open"), OnClick: m.openGUI}, sni.MenuItem{Separator: true})
	}

	// Экстренная остановка или, если mKey уже приостановлен, возобновление.
	switch {
	case m.paused && m.keys != nil:
		items = append(items, sni.MenuItem{Label: m.tr.T("tray.resume"), OnClick: m.keys.Resume})
	case !m.paused && m.input != nil:
		items = append(items, sni.MenuItem{Label: m.tr.T("tray.emergency"), OnClick: func() { m.input.EmergencyStop("tray") }})
	}

	// Выход из программы.
	if m.life != nil {
		items = append(items, sni.MenuItem{Separator: true}, sni.MenuItem{Label: m.tr.T("tray.quit"), OnClick: m.life.Shutdown})
	}
	return items
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
