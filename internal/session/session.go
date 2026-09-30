package session

import (
	"context"
	"os"
	"strings"

	"mkey/internal/contracts"
)

// ModuleID — идентификатор модуля.
const ModuleID = "session"

// Detect определяет сведения о сессии по переменным окружения (getenv передаётся явно для тестов).
func Detect(getenv func(string) string) contracts.SessionInfo {
	info := contracts.SessionInfo{
		Desktop:        getenv("XDG_CURRENT_DESKTOP"),
		Display:        getenv("DISPLAY"),
		WaylandDisplay: getenv("WAYLAND_DISPLAY"),
	}

	// Тип сессии: явное значение от менеджера входа, иначе — по наличию дисплеев.
	switch t := strings.ToLower(getenv("XDG_SESSION_TYPE")); {
	case t == "x11" || t == "wayland" || t == "tty":
		info.Type = t
	case info.WaylandDisplay != "":
		info.Type = "wayland"
	case info.Display != "":
		info.Type = "x11"
	default:
		info.Type = "unknown"
	}

	// Композиторы со своими сокетами управления определяются надёжнее всего.
	switch {
	case getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		info.Compositor = "hyprland"
		return info
	case getenv("SWAYSOCK") != "":
		info.Compositor = "sway"
		return info
	case getenv("NIRI_SOCKET") != "":
		info.Compositor = "niri"
		return info
	}

	// Иначе — по XDG_CURRENT_DESKTOP (список через двоеточие, например "ubuntu:GNOME").
	info.Compositor = "unknown"
	for _, part := range strings.Split(info.Desktop, ":") {
		p := strings.ToLower(strings.TrimSpace(part))
		switch p {
		case "":
			continue
		case "gnome":
			info.Compositor = "gnome"
			return info
		case "kde":
			info.Compositor = "kde"
			return info
		case "ubuntu", "unity":
			// «ubuntu» идёт в паре с GNOME — ищем дальше.
			continue
		default:
			// Прочие окружения (xfce, x-cinnamon, mate, lxqt, cosmic, labwc, river, wayfire…).
			info.Compositor = strings.TrimPrefix(p, "x-")
		}
	}
	return info
}

// Module — модуль сведений о сессии, реализует contracts.Session.
type Module struct {
	// getenv — источник переменных окружения.
	getenv func(string) string
	// info — определённые при Init сведения.
	info contracts.SessionInfo
}

// New создаёт модуль, читающий окружение процесса.
func New() *Module { return &Module{getenv: os.Getenv} }

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init определяет сведения о сессии и публикует сервис.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	m.info = Detect(m.getenv)
	host.Logger().Info("session detected", "type", m.info.Type, "compositor", m.info.Compositor, "desktop", m.info.Desktop)
	return contracts.ProvideService[contracts.Session](host.Services(), m)
}

// Start ничего не делает: модулю не нужна фоновая работа.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает: модуль не держит ресурсов.
func (m *Module) Stop(context.Context) error { return nil }

// Info возвращает сведения о сессии.
func (m *Module) Info() contracts.SessionInfo { return m.info }

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module  = (*Module)(nil)
	_ contracts.Session = (*Module)(nil)
)
