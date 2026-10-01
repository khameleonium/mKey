package inspector

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"mkey/internal/contracts"
	"mkey/internal/lib/devmap"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/paths"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "inspector"

// Config — настройки модуля из секции modules.inspector в config.yaml.
type Config struct {
	// InputDir — папка устройств ввода с подпапками by-id и by-path ("" — /dev/input; для тестов).
	InputDir string `json:"input_dir"`
	// AutoIDs — каким устройствам давать авто-ID (FR-DEV-2): smart (по умолчанию — всем,
	// кроме служебных), all или unusual. Меняется в окне («Устройства») и через API.
	AutoIDs string `json:"auto_ids"`
	// DevicesFile — файл авто-ID ("" — ~/.config/mkey/devices.yaml).
	DevicesFile string `json:"devices_file"`
}

// Module — инспектор устройств (contracts.Inspector).
type Module struct {
	// log — логгер модуля; cfg — настройки (заполняются в Init).
	log *slog.Logger
	cfg Config
	// input — источник устройств (nil — модуль input отключён: список пуст).
	input contracts.InputSource
	// bus — шина (подключение и отключение устройств); events и unsub — подписка на них.
	bus    contracts.Bus
	events <-chan contracts.Event
	unsub  func()
	done   chan struct{}

	// mu защищает поля ниже.
	mu sync.Mutex
	// mode — режим авто-ID; file — содержимое devices.yaml; fileOK — файл прочитан без ошибок
	// (испорченный файл не перезаписывается, чтобы не потерять правки человека).
	mode   devmap.Mode
	file   *devmap.File
	fileOK bool
	// bound — авто-ID подключённых устройств по пути ("/dev/input/event9" → "UnKey").
	bound map[string]string
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
	if m.cfg.DevicesFile == "" {
		m.cfg.DevicesFile = filepath.Join(paths.Config(os.Getenv), devmap.FileName)
	}

	// Режим авто-ID: неверное значение в настройках — по умолчанию и предупреждение в журнале.
	mode, err := devmap.ParseMode(m.cfg.AutoIDs)
	if err != nil {
		m.log.Warn("bad auto_ids setting, using smart", "err", err)
		mode = devmap.ModeSmart
	}
	m.mode, m.bound = mode, map[string]string{}

	// Источник устройств (без модуля input инспектору нечего показывать, но он не мешает другим)
	// и подписка на подключение и отключение — до Start, чтобы не пропустить устройства.
	m.input, _ = contracts.LookupService[contracts.InputSource](host.Services())
	m.bus = host.Bus()
	m.events, m.unsub = m.bus.Subscribe("input.*")

	// Файл devices.yaml — в списке «Где что лежит».
	if err := host.Extensions().Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceDevices, NameKey: "place.devices", DescriptionKey: "place.devices.description", Provider: ModuleID},
		P: m.cfg.DevicesFile, N: 25,
	}); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.Inspector](host.Services(), m)
}

// Start читает devices.yaml, раздаёт авто-ID подключённым устройствам и дальше следит
// за подключениями. Испорченный файл не мешает работе: авто-ID тогда не выдаются и не сохраняются.
func (m *Module) Start(context.Context) error {
	// Файл авто-ID.
	f, err := devmap.Load(m.cfg.DevicesFile)
	m.mu.Lock()
	if err != nil {
		m.log.Warn("devices file is broken: auto-ids are paused until it is fixed", "file", m.cfg.DevicesFile, "err", err)
		m.file, m.fileOK = &devmap.File{Version: devmap.Version}, false
	} else {
		m.file, m.fileOK = f, true
	}
	m.mu.Unlock()

	// Уже подключённые устройства и дальнейшие подключения.
	m.assignAll()
	m.done = make(chan struct{})
	go m.watch()
	return nil
}

// Stop прекращает следить за подключениями.
func (m *Module) Stop(context.Context) error {
	if m.unsub != nil {
		m.unsub()
	}
	if m.done != nil {
		<-m.done
	}
	return nil
}

// watch раздаёт авто-ID новым устройствам и забывает отключённые, пока подписка не закрыта.
func (m *Module) watch() {
	defer close(m.done)
	for e := range m.events {
		d, ok := e.Payload.(contracts.InputDevice)
		if !ok {
			continue
		}
		switch e.Topic {
		case contracts.TopicInputDeviceAdded:
			m.assign([]contracts.InputDevice{d})
		case contracts.TopicInputDeviceRemoved:
			m.mu.Lock()
			delete(m.bound, d.Info.Path)
			m.mu.Unlock()
		}
	}
}

// assignAll раздаёт авто-ID всем подключённым устройствам.
func (m *Module) assignAll() {
	if m.input != nil {
		m.assign(m.input.Devices())
	}
}

// assign узнаёт устройства по devices.yaml (FR-DEV-6) или выдаёт новые авто-ID подходящим
// под режим (FR-DEV-2) и сохраняет файл, если он изменился.
func (m *Module) assign(devs []contracts.InputDevice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.fileOK {
		return
	}
	links := ev.ReadLinks(m.cfg.InputDir)

	// Занятые записи — уже узнанные подключённые устройства.
	busy := map[string]bool{}
	for _, id := range m.bound {
		busy[id] = true
	}

	// Порядок — по номеру устройства (event3 раньше event10): первые имена — первым устройствам.
	devs = slices.Clone(devs)
	slices.SortStableFunc(devs, func(a, b contracts.InputDevice) int {
		return cmp.Or(cmp.Compare(eventNumber(a.Info.Path), eventNumber(b.Info.Path)), cmp.Compare(a.Info.Path, b.Info.Path))
	})

	changed, bound := false, false
	for _, d := range devs {
		if m.bound[d.Info.Path] != "" {
			continue
		}
		match := devmap.MatchOf(d.Info, links[d.Info.Path])

		// Знакомое устройство: новые кнопки — следующими номерами, порт — текущий.
		if rec := m.file.Find(match, busy); rec != nil {
			if rec.Update(d.Info, d.Kinds) || rec.Match != match {
				rec.Match = match
				changed = true
			}
			m.bound[d.Info.Path], busy[rec.AutoID] = rec.AutoID, true
			bound = true
			continue
		}

		// Новое устройство, подходящее под режим, — очередной авто-ID.
		if devmap.Qualifies(m.mode, d.Info.Caps, d.Kinds) {
			rec := m.file.Add(d.Info, d.Kinds, links[d.Info.Path])
			m.bound[d.Info.Path], busy[rec.AutoID] = rec.AutoID, true
			m.log.Info("auto-id assigned", "device", d.Info.Name, "path", d.Info.Path, "id", rec.AutoID)
			changed, bound = true, true
		}
	}

	// Сохраняем изменения и сообщаем окну, что у устройств появились имена.
	if changed {
		if err := devmap.Save(m.cfg.DevicesFile, m.file); err != nil {
			m.log.Warn("cannot save devices file", "file", m.cfg.DevicesFile, "err", err)
		}
	}
	if bound && m.bus != nil {
		m.bus.Publish(contracts.TopicAutoIDsChanged, nil)
	}
}

// eventNumber возвращает номер N из пути ".../eventN" (-1 — путь другого вида).
func eventNumber(path string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "event"))
	if err != nil {
		return -1
	}
	return n
}

// Label возвращает авто-ID кнопки или оси для макросов (contracts.Inspector).
func (m *Module) Label(path string, typ, code uint16) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.labelLocked(path, typ, code)
}

// labelLocked — Label под удерживаемым m.mu.
func (m *Module) labelLocked(path string, typ, code uint16) string {
	id := m.bound[path]
	if id == "" || m.file == nil {
		return ""
	}
	rec := m.file.Lookup(id)
	if rec == nil {
		return ""
	}
	if n := rec.Label(typ, code); n != "" {
		return devmap.Ref(id, n)
	}
	return ""
}

// AutoIDMode возвращает режим авто-ID (contracts.Inspector).
func (m *Module) AutoIDMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.mode)
}

// SetAutoIDMode меняет режим и сразу раздаёт имена подходящим устройствам (contracts.Inspector).
func (m *Module) SetAutoIDMode(mode string) error {
	md, err := devmap.ParseMode(mode)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.mode = md
	m.mu.Unlock()
	m.assignAll()
	return nil
}

// Devices возвращает сведения обо всех открытых устройствах (contracts.Inspector).
func (m *Module) Devices() []contracts.DeviceDetails {
	if m.input == nil {
		return nil
	}
	links := ev.ReadLinks(m.cfg.InputDir)
	devs := m.input.Devices()
	out := make([]contracts.DeviceDetails, 0, len(devs))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range devs {
		dd := details(d, links[d.Info.Path])

		// Авто-ID устройства и его кнопок и осей без стандартного имени.
		dd.AutoID = m.bound[d.Info.Path]
		if dd.AutoID != "" {
			for _, list := range []struct {
				typ uint16
				c   []contracts.DeviceControl
			}{{ev.EvKey, dd.Keys}, {ev.EvRel, dd.Rel}} {
				for i := range list.c {
					list.c[i].Label = m.labelLocked(d.Info.Path, list.typ, list.c[i].Code)
				}
			}
			for i := range dd.Axes {
				dd.Axes[i].Label = m.labelLocked(d.Info.Path, ev.EvAbs, dd.Axes[i].Code)
			}
		}
		out = append(out, dd)
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
