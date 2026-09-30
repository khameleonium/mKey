package desktop

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"

	"mkey/internal/contracts"
	"mkey/internal/desktop/gnome"
	"mkey/internal/desktop/kde"
)

// ModuleID — идентификатор модуля.
const ModuleID = "desktop"

// Config — настройки модуля из секции modules.desktop.
type Config struct {
	// Layouts — раскладки пользователя для окружений, где их нельзя узнать автоматически.
	Layouts []string `json:"layouts"`
	// Layout — текущая раскладка для таких окружений (по умолчанию — первая из Layouts).
	Layout string `json:"layout"`
}

// Module — модуль десктоп-адаптеров; сейчас публикует contracts.LayoutProvider.
type Module struct {
	// log — логгер модуля; cfg — настройки.
	log *slog.Logger
	cfg Config
	// compositor — окружение из модуля session ("" — модуль отключён).
	compositor string
	// connect подключается к сессионной шине D-Bus (в тестах подменяется).
	connect func() (*dbus.Conn, error)
	// conn — подключение к D-Bus (nil, если не нужно или не удалось).
	conn *dbus.Conn
	// adapter — выбранный адаптер раскладок; fallback — раскладки из настроек.
	adapter  contracts.LayoutProvider
	fallback contracts.LayoutProvider
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

	// Окружение из модуля session (если он отключён — работаем на настройках).
	if s, err := contracts.LookupService[contracts.Session](host.Services()); err == nil {
		m.compositor = s.Info().Compositor
	}
	return contracts.ProvideService[contracts.LayoutProvider](host.Services(), m)
}

// Start выбирает адаптер раскладок для окружения; при сбое остаётся источник из настроек.
func (m *Module) Start(context.Context) error {
	switch m.compositor {
	case "kde":
		conn, err := m.connect()
		if err != nil {
			m.log.Warn("D-Bus unavailable, keyboard layouts from config", "err", err)
			return nil
		}
		m.conn = conn
		m.adapter = kde.New(conn)
	case "gnome":
		m.adapter = gnome.New()
	}
	return nil
}

// Stop закрывает подключение к D-Bus.
func (m *Module) Stop(context.Context) error {
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

// Layouts возвращает раскладки от адаптера окружения, а при его сбое — из настроек.
func (m *Module) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	if m.adapter != nil {
		info, err := m.adapter.Layouts(ctx)
		if err == nil {
			return info, nil
		}
		m.log.Warn("keyboard layouts from desktop failed, using config", "err", err)
	}
	return m.fallback.Layouts(ctx)
}

// Switch переключает раскладку через адаптер окружения, если он это умеет.
func (m *Module) Switch(ctx context.Context, name string) error {
	if m.adapter == nil {
		return contracts.ErrUnsupported
	}
	return m.adapter.Switch(ctx, name)
}

// configLayouts — раскладки из настроек модуля (окружения без автоматического определения).
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
	return contracts.LayoutInfo{Current: current, Available: available, Source: "config"}, nil
}

// Switch не поддерживается: окружение неизвестно.
func (configLayouts) Switch(context.Context, string) error { return contracts.ErrUnsupported }

// Проверки на этапе компиляции, что типы реализуют контракты.
var (
	_ contracts.Module         = (*Module)(nil)
	_ contracts.LayoutProvider = (*Module)(nil)
)
