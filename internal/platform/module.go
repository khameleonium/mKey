package platform

import (
	"context"
	"os"
	"slices"

	"golang.org/x/sys/unix"

	"mkey/internal/contracts"
	"mkey/internal/platform/detect"
	"mkey/internal/platform/devaccess"
	"mkey/internal/platform/elevate"
)

// ModuleID — идентификатор модуля.
const ModuleID = "platform"

// Module — модуль платформенных сведений и бэкендов, реализует contracts.Platform.
type Module struct {
	// probe — доступ к системе для определения сведений.
	probe detect.Probe
	// isTTY сообщает, подключён ли stdin к терминалу.
	isTTY func() bool
	// exec выполняет внешние команды для бэкендов повышения прав.
	exec func(ctx context.Context, name string, args []string, interactive bool) error

	// info — сведения о системе, определённые при Init.
	info contracts.PlatformInfo
	// ext — реестр точек расширения (бэкенды, включая добавленные плагинами).
	ext contracts.ExtensionRegistry
	// elevatorOrder и accessOrder — порядок предпочтения встроенных бэкендов.
	elevatorOrder, accessOrder []string
}

// New создаёт модуль для настоящей системы.
func New() *Module {
	return &Module{
		probe: detect.OS{},
		isTTY: func() bool {
			_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
			return err == nil
		},
		exec: elevate.RealExec,
	}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init определяет сведения о системе, регистрирует встроенные бэкенды и публикует сервис.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Сведения о системе.
	m.info = detect.Detect(m.probe)
	m.ext = host.Extensions()
	host.Logger().Info("platform detected",
		"init", m.info.Init, "logind", m.info.Logind, "device_manager", m.info.DeviceManager,
		"distro", m.info.DistroID, "package_manager", m.info.PackageManager)

	// Графическая сессия: из модуля session, а если он отключён — по переменным окружения.
	graphical := m.probe.Getenv("WAYLAND_DISPLAY") != "" || m.probe.Getenv("DISPLAY") != ""
	if s, err := contracts.LookupService[contracts.Session](host.Services()); err == nil {
		graphical = s.Info().Graphical()
	}

	// Бэкенды повышения прав с переведёнными пояснениями для окна терминала.
	tr := host.I18n()
	env := elevate.Env{
		Getenv:     m.probe.Getenv,
		LookPath:   m.probe.LookPath,
		IsTTY:      m.isTTY(),
		Graphical:  graphical,
		Exec:       m.exec,
		Prompt:     tr.T("platform.elevate.prompt"),
		PressEnter: tr.T("platform.elevate.press_enter"),
	}
	for _, e := range elevate.All(env) {
		if err := host.Extensions().Register(contracts.PointElevator, e); err != nil {
			return err
		}
		m.elevatorOrder = append(m.elevatorOrder, e.Meta().ID)
	}

	// Способы выдачи доступа к устройствам.
	for _, a := range devaccess.All() {
		if err := host.Extensions().Register(contracts.PointDeviceAccess, a); err != nil {
			return err
		}
		m.accessOrder = append(m.accessOrder, a.Meta().ID)
	}

	// Публикуем сервис.
	return contracts.ProvideService[contracts.Platform](host.Services(), m)
}

// Start ничего не делает: модулю не нужна фоновая работа.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает: модуль не держит ресурсов.
func (m *Module) Stop(context.Context) error { return nil }

// Info возвращает сведения о системе.
func (m *Module) Info() contracts.PlatformInfo { return m.info }

// Elevators возвращает доступные бэкенды повышения прав в порядке предпочтения:
// встроенные по порядку, бэкенды плагинов — перед «ручным» вариантом.
func (m *Module) Elevators() []contracts.Elevator {
	var out []contracts.Elevator
	for _, ext := range ordered(m.ext.List(contracts.PointElevator), m.elevatorOrder, "manual") {
		if e, ok := ext.(contracts.Elevator); ok && e.Available() {
			out = append(out, e)
		}
	}
	return out
}

// DeviceAccess возвращает первый способ выдачи доступа, подходящий для системы.
func (m *Module) DeviceAccess() (contracts.DeviceAccess, error) {
	for _, ext := range ordered(m.ext.List(contracts.PointDeviceAccess), m.accessOrder, "") {
		if a, ok := ext.(contracts.DeviceAccess); ok && a.Applicable(m.info) {
			return a, nil
		}
	}
	return nil, contracts.ErrUnsupported
}

// ordered упорядочивает расширения: сначала встроенные в порядке builtin (кроме last),
// затем прочие (плагины) по ID, в конце — расширение с ID last.
func ordered(all []contracts.Extension, builtin []string, last string) []contracts.Extension {
	// Раскладываем расширения по ID.
	byID := map[string]contracts.Extension{}
	for _, e := range all {
		byID[e.Meta().ID] = e
	}

	// Встроенные по порядку, кроме последнего.
	var out []contracts.Extension
	for _, id := range builtin {
		if e, ok := byID[id]; ok && id != last {
			out = append(out, e)
		}
	}

	// Расширения плагинов (список all уже отсортирован по ID).
	for _, e := range all {
		if id := e.Meta().ID; !slices.Contains(builtin, id) {
			out = append(out, e)
		}
	}

	// Последний вариант.
	if e, ok := byID[last]; ok {
		out = append(out, e)
	}
	return out
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module   = (*Module)(nil)
	_ contracts.Platform = (*Module)(nil)
)
