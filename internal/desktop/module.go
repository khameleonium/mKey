package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/desktop/gnome"
	"github.com/khameleonium/mKey/internal/desktop/kde"
	"github.com/khameleonium/mKey/internal/desktop/x11"
)

// ModuleID — идентификатор модуля.
const ModuleID = "desktop"

// Config — настройки модуля из секции modules.desktop.
type Config struct {
	// Layouts — раскладки пользователя для окружений, где их нельзя узнать автоматически.
	Layouts []string `json:"layouts"`
	// Layout — текущая раскладка для таких окружений (по умолчанию — первая из Layouts).
	Layout string `json:"layout"`
	// Screen — размер рабочего стола в пикселях ("1920x1080") для касаний в пикселях
	// ({Touch 960 540}); пусто — неизвестен (проценты работают всегда).
	Screen string `json:"screen"`
}

// Module — модуль десктоп-адаптеров; сейчас публикует contracts.LayoutProvider.
type Module struct {
	// log — логгер модуля; cfg — настройки.
	log *slog.Logger
	cfg Config
	// compositor — окружение из модуля session ("" — модуль отключён); session — сведения о сессии.
	compositor string
	session    contracts.SessionInfo
	// ext — точки расширения: источники раскладок (встроенные и от других модулей и плагинов).
	ext contracts.ExtensionRegistry
	// warned — о каких сбоях источников раскладок уже написано в журнал (не повторять каждый раз).
	warnMu sync.Mutex
	warned map[string]bool
	// connect подключается к сессионной шине D-Bus (в тестах подменяется).
	connect func() (*dbus.Conn, error)
	// conn — подключение к D-Bus (nil, если не нужно или не удалось).
	conn *dbus.Conn
	// kde — адаптер раскладок KDE (создаётся при запуске, нужен D-Bus); fallback — раскладки из
	// настроек, когда ни один источник не видит раскладку.
	kde      contracts.LayoutProvider
	fallback contracts.LayoutProvider
	// tr — переводчик уведомлений; ctx живёт до Stop; wg ждёт горутину уведомлений.
	tr     contracts.Translator
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// notices — подписки на события, о которых нужно уведомить пользователя; unsub — отписки.
	notices [3]<-chan contracts.Event
	unsub   []func()
}

// New создаёт модуль.
func New() *Module {
	return &Module{connect: func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }, cfg: Config{Layouts: []string{"us"}}}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, узнаёт окружение и публикует сервис раскладок.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Настройки и запасной источник раскладок.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	m.fallback = configLayouts{cfg: m.cfg}
	if _, _, err := parseScreen(m.cfg.Screen); err != nil {
		return fmt.Errorf("%s: screen: %w", ModuleID, err)
	}

	// Окружение из модуля session (если он отключён — работаем на настройках).
	if s, err := contracts.LookupService[contracts.Session](host.Services()); err == nil {
		m.session = s.Info()
		m.compositor = m.session.Compositor
	}

	// Встроенные источники раскладок по порядку: KDE (D-Bus, умеет переключать), GNOME (gsettings),
	// X11 (любое окружение X11). Другие модули и плагины добавляют свои в ту же точку.
	m.ext = host.Extensions()
	m.warned = map[string]bool{}
	for _, src := range []contracts.LayoutSource{kdeSource{m}, gnomeSource{gnome.New()}, x11.New("")} {
		if err := m.ext.Register(contracts.PointLayoutSource, src); err != nil {
			return err
		}
	}

	// Подписки на события, о которых нужно уведомлять пользователя.
	m.tr = host.I18n()
	var u [3]func()
	m.notices[0], u[0] = host.Bus().Subscribe(contracts.TopicEmergency)
	m.notices[1], u[1] = host.Bus().Subscribe(contracts.TopicProjectError)
	m.notices[2], u[2] = host.Bus().Subscribe(contracts.TopicEngineError)
	m.unsub = u[:]

	// Сервисы: раскладки и уведомления.
	if err := contracts.ProvideService[contracts.LayoutProvider](host.Services(), m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.URLOpener](host.Services(), m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.ScreenInfo](host.Services(), m); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.Notifier](host.Services(), m)
}

// Start подключается к сессионной шине D-Bus (уведомления, KDE), выбирает адаптер раскладок
// и начинает показывать уведомления. Без D-Bus модуль работает: раскладки из настроек, без уведомлений.
func (m *Module) Start(context.Context) error {
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// D-Bus сессии.
	conn, err := m.connect()
	if err != nil {
		m.log.Warn("session D-Bus unavailable: no notifications, keyboard layouts from config", "err", err)
	} else {
		m.conn = conn
	}

	// Адаптер раскладок KDE работает через D-Bus.
	if m.conn != nil {
		m.kde = kde.New(m.conn)
	}

	// Уведомления о важных событиях.
	m.wg.Add(1)
	go m.watchNotices(m.notices[0], m.notices[1], m.notices[2])
	return nil
}

// Stop прекращает уведомления и закрывает подключение к D-Bus.
func (m *Module) Stop(context.Context) error {
	for _, u := range m.unsub {
		u()
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

// sources возвращает источники раскладок, которые работают в этой сессии, по порядку регистрации.
func (m *Module) sources() []contracts.LayoutSource {
	if m.ext == nil {
		return nil
	}
	var out []contracts.LayoutSource
	for _, e := range m.ext.List(contracts.PointLayoutSource) {
		if src, ok := e.(contracts.LayoutSource); ok && src.Supports(m.session) {
			out = append(out, src)
		}
	}
	return out
}

// Layouts возвращает раскладки от первого источника, который их видит; ни один не видит —
// раскладки из настроек с пометкой Blind (текст печатается нажатиями клавиш как есть).
func (m *Module) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	for _, src := range m.sources() {
		info, err := src.Layouts(ctx)
		if err == nil {
			return info, nil
		}
		m.warnOnce(src.Meta().ID, err)
	}
	return m.fallback.Layouts(ctx)
}

// Switch переключает раскладку через первый источник, который видит раскладку.
func (m *Module) Switch(ctx context.Context, name string) error {
	for _, src := range m.sources() {
		if _, err := src.Layouts(ctx); err == nil {
			return src.Switch(ctx, name)
		}
	}
	return contracts.ErrUnsupported
}

// warnOnce пишет в журнал сбой источника раскладок один раз для каждой пары «источник, ошибка».
func (m *Module) warnOnce(source string, err error) {
	key := source + ": " + err.Error()
	m.warnMu.Lock()
	seen := m.warned[key]
	m.warned[key] = true
	m.warnMu.Unlock()
	if !seen {
		m.log.Warn("keyboard layouts unavailable from source", "source", source, "err", err)
	}
}

// kdeSource — источник раскладок KDE Plasma (D-Bus org.kde.keyboard).
type kdeSource struct{ m *Module }

// Meta возвращает метаданные источника.
func (kdeSource) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "kde", NameKey: "layout_source.kde", Provider: ModuleID}
}

// Supports — окружение KDE и подключение к D-Bus.
func (s kdeSource) Supports(si contracts.SessionInfo) bool {
	return si.Compositor == "kde" && s.m.kde != nil
}

// Layouts возвращает раскладки KDE.
func (s kdeSource) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	return s.m.kde.Layouts(ctx)
}

// Switch переключает раскладку KDE.
func (s kdeSource) Switch(ctx context.Context, name string) error { return s.m.kde.Switch(ctx, name) }

// gnomeSource — источник раскладок GNOME (gsettings).
type gnomeSource struct{ g *gnome.Layouts }

// Meta возвращает метаданные источника.
func (gnomeSource) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "gnome", NameKey: "layout_source.gnome", Provider: ModuleID}
}

// Supports — окружение GNOME.
func (gnomeSource) Supports(si contracts.SessionInfo) bool { return si.Compositor == "gnome" }

// Layouts возвращает раскладки GNOME.
func (s gnomeSource) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	return s.g.Layouts(ctx)
}

// Switch — GNOME переключать раскладку извне не позволяет.
func (s gnomeSource) Switch(ctx context.Context, name string) error { return s.g.Switch(ctx, name) }

// configLayouts — раскладки из настроек модуля, когда ни один источник не видит раскладку: mKey
// не знает, какая раскладка включена, поэтому Blind — текст печатается нажатиями клавиш как есть.
type configLayouts struct {
	cfg Config
}

// Layouts возвращает раскладки из настроек; текущая — Layout или первая из списка.
func (c configLayouts) Layouts(context.Context) (contracts.LayoutInfo, error) {
	available := c.cfg.Layouts
	if len(available) == 0 {
		available = []string{"us"}
	}
	current := c.cfg.Layout
	if current == "" {
		current = available[0]
	}
	return contracts.LayoutInfo{Current: current, Available: available, Source: "config", Blind: true}, nil
}

// Switch не поддерживается: окружение неизвестно.
func (configLayouts) Switch(context.Context, string) error { return contracts.ErrUnsupported }

// Проверки на этапе компиляции, что типы реализуют контракты.
var (
	_ contracts.Module         = (*Module)(nil)
	_ contracts.LayoutProvider = (*Module)(nil)
	_ contracts.LayoutSource   = kdeSource{}
	_ contracts.LayoutSource   = gnomeSource{}
	_ contracts.Notifier       = (*Module)(nil)
)
