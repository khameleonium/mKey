package inspector

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "inspector"

// Config — настройки модуля из секции modules.inspector в config.yaml.
type Config struct {
	// InputDir — папка устройств ввода с подпапками by-id и by-path ("" — /dev/input; для тестов).
	InputDir string `json:"input_dir"`
}

// Module — инспектор устройств (contracts.Inspector).
type Module struct {
	// log — логгер модуля; cfg — настройки (заполняются в Init).
	log *slog.Logger
	cfg Config
	// input — источник устройств (nil — модуль input отключён: список пуст).
	input contracts.InputSource
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, находит источник устройств и предоставляет сервис contracts.Inspector.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Логгер и настройки.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.InputDir == "" {
		m.cfg.InputDir = "/dev/input"
	}

	// Источник устройств (без модуля input инспектору нечего показывать, но он не мешает другим).
	m.input, _ = contracts.LookupService[contracts.InputSource](host.Services())
	return contracts.ProvideService[contracts.Inspector](host.Services(), m)
}

// Start ничего не делает: сведения собираются по запросу.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает.
func (m *Module) Stop(context.Context) error { return nil }

// Devices возвращает сведения обо всех открытых устройствах (contracts.Inspector).
func (m *Module) Devices() []contracts.DeviceDetails {
	if m.input == nil {
		return nil
	}
	links := ev.ReadLinks(m.cfg.InputDir)
	devs := m.input.Devices()
	out := make([]contracts.DeviceDetails, 0, len(devs))
	for _, d := range devs {
		out = append(out, details(d, links[d.Info.Path]))
	}
	return out
}

// Find ищет устройства по ссылке (contracts.Inspector): путь, имя файла и постоянное имя — точно,
// иначе часть названия без учёта регистра.
func (m *Module) Find(ref string) []contracts.DeviceDetails {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	all := m.Devices()

	// Точное совпадение: путь, имя файла (event6) или постоянное имя.
	for _, d := range all {
		if d.Info.Path == ref || filepath.Base(d.Info.Path) == ref || d.ByID == ref || d.ByPath == ref {
			return []contracts.DeviceDetails{d}
		}
	}

	// Часть названия.
	low := strings.ToLower(ref)
	var out []contracts.DeviceDetails
	for _, d := range all {
		if strings.Contains(strings.ToLower(d.Info.Name), low) {
			out = append(out, d)
		}
	}
	return out
}

// details собирает подробности устройства: кнопки, оси, индикаторы с именами mKey и ядра.
func details(d contracts.InputDevice, links ev.Links) contracts.DeviceDetails {
	caps := d.Info.Caps
	out := contracts.DeviceDetails{InputDevice: d, Links: links, Bus: ev.BusName(d.Info.ID.Bustype)}

	// control — код с именем ядра и (если есть) именем для макросов.
	control := func(typ, code uint16, name string) contracts.DeviceControl {
		return contracts.DeviceControl{Code: code, Kernel: ev.CodeName(typ, code), Name: name}
	}

	// Клавиши и кнопки: имя mKey — из таблицы клавиш (клавиатура, мышь, геймпад).
	for _, c := range caps.Codes[ev.EvKey] {
		name, _ := keys.NameOf(c)
		out.Keys = append(out.Keys, control(ev.EvKey, c, name))
	}

	// Относительные оси (движение мыши, колёса) — имён в макросах у них нет.
	for _, c := range caps.Codes[ev.EvRel] {
		out.Rel = append(out.Rel, control(ev.EvRel, c, ""))
	}

	// Абсолютные оси с диапазонами (значение — на момент подключения устройства).
	for _, c := range caps.Codes[ev.EvAbs] {
		name, _ := keys.AxisNameOf(c)
		out.Axes = append(out.Axes, contracts.DeviceAxis{DeviceControl: control(ev.EvAbs, c, name), AbsInfo: caps.Abs[c]})
	}

	// Переключатели, индикаторы, отдача и свойства — только имена ядра.
	for _, c := range caps.Codes[ev.EvSw] {
		out.Switches = append(out.Switches, control(ev.EvSw, c, ""))
	}
	for _, c := range caps.Codes[ev.EvLed] {
		out.LEDs = append(out.LEDs, control(ev.EvLed, c, ""))
	}
	for _, c := range caps.Codes[ev.EvFf] {
		out.FF = append(out.FF, control(ev.EvFf, c, ""))
	}
	for _, p := range caps.Props {
		out.Props = append(out.Props, ev.PropName(p))
	}
	return out
}

// Проверка на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module    = (*Module)(nil)
	_ contracts.Inspector = (*Module)(nil)
)
