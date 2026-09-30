package contracts

import (
	"context"
	"errors"
)

// SessionInfo — сведения о графической сессии пользователя (модуль session).
type SessionInfo struct {
	// Type — тип сессии: "x11", "wayland", "tty" или "unknown".
	Type string `json:"type"`
	// Desktop — значение XDG_CURRENT_DESKTOP, например "KDE" или "GNOME".
	Desktop string `json:"desktop,omitempty"`
	// Compositor — определённое окружение: "gnome", "kde", "sway", "hyprland", "niri", "xfce"…, или "unknown".
	Compositor string `json:"compositor"`
	// Display — значение DISPLAY (X11 или XWayland).
	Display string `json:"display,omitempty"`
	// WaylandDisplay — значение WAYLAND_DISPLAY.
	WaylandDisplay string `json:"wayland_display,omitempty"`
}

// Graphical сообщает, запущена ли графическая сессия (X11 или Wayland).
func (s SessionInfo) Graphical() bool {
	return s.Type == "x11" || s.Type == "wayland"
}

// Session — сервис сведений о сессии.
type Session interface {
	// Info возвращает сведения о текущей сессии.
	Info() SessionInfo
}

// PlatformInfo — сведения о системе, от которых зависят установка и выдача прав (FR-INST-7).
type PlatformInfo struct {
	// Init — система инициализации: "systemd", "openrc", "runit", "dinit", "s6", "sysvinit" или "unknown".
	Init string `json:"init"`
	// Logind — менеджер сеансов: "systemd-logind", "elogind" или "none".
	Logind string `json:"logind"`
	// DeviceManager — менеджер устройств: "systemd-udevd", "eudev", "mdev", "mdevd" или "unknown".
	DeviceManager string `json:"device_manager"`
	// DistroID — ID из /etc/os-release, например "linuxmint".
	DistroID string `json:"distro_id,omitempty"`
	// DistroName — PRETTY_NAME из /etc/os-release.
	DistroName string `json:"distro_name,omitempty"`
	// PackageManager — пакетный менеджер: "apt", "dnf", "pacman", "zypper", "xbps", "apk", "emerge" или "".
	PackageManager string `json:"package_manager,omitempty"`
	// HasPkexec, HasSudo, HasDoas — какие средства повышения прав установлены.
	HasPkexec bool `json:"has_pkexec"`
	HasSudo   bool `json:"has_sudo"`
	HasDoas   bool `json:"has_doas"`
	// RuntimeDir — каталог для сокета и токена (XDG_RUNTIME_DIR или запасной /tmp/mkey-UID).
	RuntimeDir string `json:"runtime_dir"`
	// RuntimeDirFallback — XDG_RUNTIME_DIR не задан, используется запасной каталог.
	RuntimeDirFallback bool `json:"runtime_dir_fallback"`
}

// Platform — сервис платформенных сведений и бэкендов (модуль platform).
type Platform interface {
	// Info возвращает сведения о системе.
	Info() PlatformInfo
	// Elevators возвращает доступные бэкенды повышения прав в порядке предпочтения.
	// Последний в списке всегда «ручной» — показать команду пользователю.
	Elevators() []Elevator
	// DeviceAccess возвращает подходящий для этой системы способ выдачи доступа к устройствам.
	DeviceAccess() (DeviceAccess, error)
}

// ErrManualAction возвращается бэкендом повышения прав, который не может выполнить
// команду сам и просит пользователя выполнить её вручную (текст — в ManualActionError).
var ErrManualAction = errors.New("manual action required")

// ManualActionError — ошибка с командой, которую пользователь должен выполнить сам.
type ManualActionError struct {
	// Command — готовая к копированию команда.
	Command string
}

// Error возвращает текст ошибки.
func (e *ManualActionError) Error() string { return "run manually: " + e.Command }

// Unwrap позволяет проверять ошибку через errors.Is(err, ErrManualAction).
func (e *ManualActionError) Unwrap() error { return ErrManualAction }

// Elevator — бэкенд повышения прав (точка расширения PointElevator): pkexec, sudo, doas и другие.
type Elevator interface {
	Extension
	// Available сообщает, можно ли использовать бэкенд в текущем окружении.
	Available() bool
	// Command возвращает команду в виде, понятном человеку (для показа и копирования).
	Command(argv []string) string
	// Run выполняет argv с правами администратора. Бэкенды, открывающие окно терминала,
	// могут вернуться до завершения команды — результат нужно перепроверить (doctor).
	Run(ctx context.Context, argv []string) error
}

// CommandRunner выполняет внешние команды (в тестах подменяется записью вызовов).
type CommandRunner interface {
	// Run выполняет команду name с аргументами args и возвращает ошибку с её выводом при неудаче.
	Run(ctx context.Context, name string, args ...string) error
}

// PrivilegedEnv — окружение привилегированной операции (выполняется от root в `mkey privileged`).
type PrivilegedEnv struct {
	// Root — корень файловой системы: "/" в работе, временный каталог в тестах.
	Root string
	// Runner — исполнитель системных команд (modprobe, udevadm, usermod…).
	Runner CommandRunner
	// User — имя пользователя, для которого выдаются права (из PKEXEC_UID/SUDO_UID/DOAS_USER).
	User string
	// Platform — сведения о системе.
	Platform PlatformInfo
}

// DeviceAccess — способ выдачи доступа к устройствам ввода (точка расширения PointDeviceAccess):
// udev uaccess, группа input, mdev.
type DeviceAccess interface {
	Extension
	// Applicable сообщает, подходит ли способ для системы.
	Applicable(info PlatformInfo) bool
	// RequiresRelogin сообщает, нужен ли перелогин, чтобы права вступили в силу.
	RequiresRelogin() bool
	// Install выдаёт доступ (правила, модуль uinput, группы). Выполняется от root.
	Install(ctx context.Context, env PrivilegedEnv) error
	// Uninstall удаляет всё, что установил Install. Выполняется от root.
	Uninstall(ctx context.Context, env PrivilegedEnv) error
	// Installed сообщает, установлены ли настройки этого способа в системе с корнем root
	// (проверка только чтением, права root не нужны).
	Installed(root string) bool
}

// CheckStatus — результат одной проверки диагностики.
type CheckStatus string

// Возможные результаты проверки.
const (
	// CheckOK — всё в порядке.
	CheckOK CheckStatus = "ok"
	// CheckInfo — справочная информация, не проблема.
	CheckInfo CheckStatus = "info"
	// CheckWarn — работает с ограничениями.
	CheckWarn CheckStatus = "warn"
	// CheckFail — функция не работает, нужно исправить.
	CheckFail CheckStatus = "fail"
)

// Check — результат одной проверки диагностики (SPEC §5.11, шаг 2).
type Check struct {
	// ID — идентификатор проверки, например "uinput_access".
	ID string `json:"id"`
	// Status — результат.
	Status CheckStatus `json:"status"`
	// MessageKey — i18n-ключ понятного пользователю описания результата.
	MessageKey string `json:"message_key"`
	// Args — параметры для подстановки в сообщение.
	Args map[string]string `json:"args,omitempty"`
	// ArgKeys — параметры, значения которых — i18n-ключи: перед подстановкой их нужно перевести
	// (например, название способа повышения прав).
	ArgKeys map[string]string `json:"arg_keys,omitempty"`
	// Fix — идентификатор автоматического исправления ("" — исправления нет).
	Fix string `json:"fix,omitempty"`
}

// FixDeviceAccess — исправление «выдать доступ к устройствам ввода» (mkey privileged install-rules).
const FixDeviceAccess = "device_access"

// Doctor — диагностика окружения (модуль setup).
type Doctor interface {
	// Run выполняет все проверки.
	Run(ctx context.Context) []Check
	// Fix выполняет исправление fix через бэкенд повышения прав elevator.
	Fix(ctx context.Context, fix string, elevator Elevator) error
}
