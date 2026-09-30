package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// ModuleID — идентификатор модуля.
const ModuleID = "setup"

// Минимальная поддерживаемая версия ядра (NFR-6).
const (
	minKernelMajor = 5
	minKernelMinor = 4
)

// sysProbe — доступ к системе, нужный проверкам (в тестах подменяется).
type sysProbe interface {
	// KernelRelease возвращает версию ядра, например "6.8.0-45-generic".
	KernelRelease() (string, error)
	// Exists сообщает, существует ли путь.
	Exists(path string) bool
	// CanOpen пробует открыть файл на чтение или запись и сразу закрывает его.
	CanOpen(path string, write bool) error
	// ProcDevices возвращает список устройств ввода из /proc/bus/input/devices.
	ProcDevices() ([]ev.ProcDevice, error)
	// Executable возвращает путь к исполняемому файлу mkey.
	Executable() (string, error)
}

// Module — модуль диагностики, реализует contracts.Doctor.
type Module struct {
	// probe — доступ к системе.
	probe sysProbe
	// session и platform — сервисы других модулей (nil, если модуль отключён).
	session  contracts.Session
	platform contracts.Platform
}

// New создаёт модуль для настоящей системы.
func New() *Module { return &Module{probe: osProbe{}} }

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init получает сервисы сессии и платформы (если есть) и публикует сервис диагностики.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Сервисы других модулей необязательны: без них соответствующие проверки пропускаются.
	if s, err := contracts.LookupService[contracts.Session](host.Services()); err == nil {
		m.session = s
	}
	if p, err := contracts.LookupService[contracts.Platform](host.Services()); err == nil {
		m.platform = p
	}
	return contracts.ProvideService[contracts.Doctor](host.Services(), m)
}

// Start ничего не делает: проверки выполняются по запросу.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает: модуль не держит ресурсов.
func (m *Module) Stop(context.Context) error { return nil }

// Run выполняет все проверки в порядке, удобном для чтения пользователем.
func (m *Module) Run(context.Context) []contracts.Check {
	checks := []contracts.Check{m.checkKernel()}
	if m.session != nil {
		checks = append(checks, m.checkSession())
	}
	if m.platform != nil {
		checks = append(checks, m.checkInit())
	}
	checks = append(checks, m.checkUinput()...)
	checks = append(checks, m.checkInputDevices())
	if m.platform != nil {
		checks = append(checks, m.checkRules(), m.checkElevator(), m.checkRuntimeDir())
	}
	return checks
}

// Fix выполняет исправление fix через бэкенд повышения прав elevator.
func (m *Module) Fix(ctx context.Context, fix string, elevator contracts.Elevator) error {
	// Поддерживается одно исправление — выдача доступа к устройствам.
	if fix != contracts.FixDeviceAccess {
		return fmt.Errorf("setup: unknown fix %q", fix)
	}

	// Запускаем `mkey privileged install-rules` с правами администратора.
	exe, err := m.probe.Executable()
	if err != nil {
		return fmt.Errorf("setup: find mkey executable: %w", err)
	}
	return elevator.Run(ctx, []string{exe, "privileged", "install-rules"})
}

// Healthy сообщает, что среди проверок нет проваленных.
func Healthy(checks []contracts.Check) bool {
	for _, c := range checks {
		if c.Status == contracts.CheckFail {
			return false
		}
	}
	return true
}

// checkKernel проверяет версию ядра (нужна 5.4 или новее).
func (m *Module) checkKernel() contracts.Check {
	c := contracts.Check{ID: "kernel"}

	// Читаем версию ядра.
	release, err := m.probe.KernelRelease()
	if err != nil {
		c.Status, c.MessageKey, c.Args = contracts.CheckWarn, "setup.check.kernel.unknown", map[string]string{"error": err.Error()}
		return c
	}
	c.Args = map[string]string{"version": release}

	// Сравниваем «мажор.минор» с минимальной версией.
	major, minor := parseKernel(release)
	if major > minKernelMajor || (major == minKernelMajor && minor >= minKernelMinor) {
		c.Status, c.MessageKey = contracts.CheckOK, "setup.check.kernel.ok"
	} else {
		c.Status, c.MessageKey = contracts.CheckFail, "setup.check.kernel.old"
	}
	return c
}

// checkSession проверяет, запущена ли графическая сессия.
func (m *Module) checkSession() contracts.Check {
	info := m.session.Info()
	c := contracts.Check{ID: "session", Args: map[string]string{"type": info.Type, "compositor": info.Compositor}}
	if info.Graphical() {
		c.Status, c.MessageKey = contracts.CheckOK, "setup.check.session.ok"
	} else {
		c.Status, c.MessageKey = contracts.CheckWarn, "setup.check.session.none"
	}
	return c
}

// checkInit сообщает систему инициализации, менеджер сеансов и менеджер устройств.
func (m *Module) checkInit() contracts.Check {
	info := m.platform.Info()
	return contracts.Check{
		ID: "system", Status: contracts.CheckInfo, MessageKey: "setup.check.system",
		Args: map[string]string{"distro": info.DistroName, "init": info.Init, "logind": info.Logind, "device_manager": info.DeviceManager},
	}
}

// checkUinput проверяет наличие /dev/uinput и право на запись в него.
func (m *Module) checkUinput() []contracts.Check {
	// Нет файла устройства — модуль ядра не загружен.
	if !m.probe.Exists(ev.DefaultUInputPath) {
		return []contracts.Check{{ID: "uinput", Status: contracts.CheckFail, MessageKey: "setup.check.uinput.missing", Fix: contracts.FixDeviceAccess}}
	}

	// Файл есть — пробуем открыть на запись.
	err := m.probe.CanOpen(ev.DefaultUInputPath, true)
	switch {
	case err == nil:
		return []contracts.Check{{ID: "uinput", Status: contracts.CheckOK, MessageKey: "setup.check.uinput.ok"}}
	case errors.Is(err, os.ErrPermission):
		return []contracts.Check{{ID: "uinput", Status: contracts.CheckFail, MessageKey: "setup.check.uinput.denied", Fix: contracts.FixDeviceAccess}}
	default:
		return []contracts.Check{{ID: "uinput", Status: contracts.CheckFail, MessageKey: "setup.check.uinput.error", Args: map[string]string{"error": err.Error()}}}
	}
}

// checkInputDevices проверяет, сколько физических устройств ввода mKey может читать.
func (m *Module) checkInputDevices() contracts.Check {
	c := contracts.Check{ID: "input_devices"}

	// Список устройств доступен всем пользователям через /proc.
	devs, err := m.probe.ProcDevices()
	if err != nil {
		c.Status, c.MessageKey, c.Args = contracts.CheckWarn, "setup.check.input.unknown", map[string]string{"error": err.Error()}
		return c
	}

	// Пробуем открыть каждое физическое устройство (собственные устройства mKey не считаем).
	total, readable := 0, 0
	for _, d := range devs {
		path := d.EventPath()
		if path == "" || strings.HasPrefix(d.Name, contracts.VirtualNamePrefix) {
			continue
		}
		total++
		if m.probe.CanOpen(path, false) == nil {
			readable++
		}
	}
	c.Args = map[string]string{"readable": strconv.Itoa(readable), "total": strconv.Itoa(total)}

	// Итог: все, часть или ни одного.
	switch {
	case total == 0:
		c.Status, c.MessageKey = contracts.CheckWarn, "setup.check.input.none"
	case readable == total:
		c.Status, c.MessageKey = contracts.CheckOK, "setup.check.input.ok"
	case readable > 0:
		c.Status, c.MessageKey, c.Fix = contracts.CheckWarn, "setup.check.input.partial", contracts.FixDeviceAccess
	default:
		c.Status, c.MessageKey, c.Fix = contracts.CheckFail, "setup.check.input.denied", contracts.FixDeviceAccess
	}
	return c
}

// checkRules сообщает, установлены ли настройки доступа mKey и каким способом они выдаются на этой системе.
func (m *Module) checkRules() contracts.Check {
	// Способ выдачи доступа для этой системы.
	access, err := m.platform.DeviceAccess()
	if err != nil {
		return contracts.Check{ID: "rules", Status: contracts.CheckWarn, MessageKey: "setup.check.rules.unsupported"}
	}
	c := contracts.Check{ID: "rules", Status: contracts.CheckInfo, ArgKeys: map[string]string{"method": access.Meta().NameKey}}

	// Установлены или нет — справочно: права могли быть выданы и иначе (например, Steam).
	if access.Installed("/") {
		c.MessageKey = "setup.check.rules.installed"
	} else {
		c.MessageKey = "setup.check.rules.missing"
	}
	return c
}

// checkElevator сообщает, каким способом mKey попросит права администратора.
func (m *Module) checkElevator() contracts.Check {
	elevators := m.platform.Elevators()
	if len(elevators) == 0 || elevators[0].Meta().ID == "manual" {
		return contracts.Check{ID: "elevator", Status: contracts.CheckWarn, MessageKey: "setup.check.elevator.manual"}
	}
	return contracts.Check{ID: "elevator", Status: contracts.CheckOK, MessageKey: "setup.check.elevator.ok", ArgKeys: map[string]string{"method": elevators[0].Meta().NameKey}}
}

// checkRuntimeDir проверяет, задан ли XDG_RUNTIME_DIR (без него используется запасной каталог).
func (m *Module) checkRuntimeDir() contracts.Check {
	info := m.platform.Info()
	c := contracts.Check{ID: "runtime_dir", Args: map[string]string{"dir": info.RuntimeDir}}
	if info.RuntimeDirFallback {
		c.Status, c.MessageKey = contracts.CheckWarn, "setup.check.runtime_dir.fallback"
	} else {
		c.Status, c.MessageKey = contracts.CheckOK, "setup.check.runtime_dir.ok"
	}
	return c
}

// parseKernel извлекает мажорную и минорную версии из строки вида "6.8.0-45-generic".
func parseKernel(release string) (major, minor int) {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ = strconv.Atoi(leadingDigits(parts[0]))
	minor, _ = strconv.Atoi(leadingDigits(parts[1]))
	return major, minor
}

// leadingDigits возвращает ведущие цифры строки: "10rc1" → "10".
func leadingDigits(s string) string {
	end := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		return s
	}
	return s[:end]
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module = (*Module)(nil)
	_ contracts.Doctor = (*Module)(nil)
)
