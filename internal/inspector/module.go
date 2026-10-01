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
	"mkey/internal/lib/dsl"
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
	// projects — проекты: имена кнопок устройств из их раздела devices (nil — модуль store отключён).
	projects contracts.Projects
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
	return &Module{cfg: Config{AutoIDs: string(devmap.ModeSmart)}}
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
	m.projects, _ = contracts.LookupService[contracts.Projects](host.Services())
	m.bus = host.Bus()
	m.events, m.unsub = m.bus.Subscribe("*")

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
		// Проекты изменились: имена кнопок из их раздела devices — подключённым устройствам.
		if e.Topic == contracts.TopicProjectsChanged {
			m.applyProjectNames()
			continue
		}
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
	names := m.projectNames()
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

		named := matchingNames(names, match)

		// Знакомое устройство: новые кнопки — следующими номерами, порт — текущий.
		if rec := m.file.Find(match, busy); rec != nil {
			if rec.Update(d.Info, d.Kinds) || rec.Match != match {
				rec.Match = match
				changed = true
			}
			changed = m.applyNames(rec, named) || changed
			m.bound[d.Info.Path], busy[rec.AutoID] = rec.AutoID, true
			bound = true
			continue
		}

		// Новое устройство, подходящее под режим или с именами из проекта, — очередной авто-ID.
		if len(named) > 0 || devmap.Qualifies(m.mode, d.Info.Caps, d.Kinds) {
			rec := m.file.Add(d.Info, d.Kinds, links[d.Info.Path])
			m.applyNames(rec, named)
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

// projectName — имена кнопок модели из раздела devices проекта.
type projectName struct {
	project string
	names   devmap.Names
}

// projectNames собирает имена кнопок устройств из всех проектов (включённых и нет: загруженный
// чужой проект выключен, а его события уже должны понимать {Геймпад.Старт}).
func (m *Module) projectNames() []projectName {
	if m.projects == nil {
		return nil
	}
	var out []projectName
	for _, st := range m.projects.List() {
		for _, n := range st.Project.Devices {
			out = append(out, projectName{project: st.Project.ID, names: n})
		}
	}
	return out
}

// matchingNames оставляет имена, подходящие устройству с приметами match.
func matchingNames(all []projectName, match devmap.Match) []projectName {
	var out []projectName
	for _, n := range all {
		if n.names.Matches(match) {
			out = append(out, n)
		}
	}
	return out
}

// applyNames подставляет в запись имена из проектов, которые к ней ещё не применялись (под m.mu):
// каждый проект — один раз, поэтому убранное человеком имя не возвращается. true — запись изменилась.
func (m *Module) applyNames(rec *devmap.Device, named []projectName) bool {
	changed := false
	for _, n := range named {
		if slices.Contains(rec.Applied, n.project) {
			continue
		}
		n.names.Apply(m.file, rec)
		rec.Applied = append(rec.Applied, n.project)
		m.log.Info("device names applied from project", "device", rec.AutoID, "project", n.project)
		changed = true
	}
	return changed
}

// applyProjectNames подставляет имена из проектов подключённым устройствам (после изменения
// проектов); устройство без авто-ID с подходящими именами его получает.
func (m *Module) applyProjectNames() {
	if m.input == nil {
		return
	}
	names := m.projectNames()
	devs := m.input.Devices()
	links := ev.ReadLinks(m.cfg.InputDir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.fileOK || len(names) == 0 {
		return
	}
	changed := false
	for _, d := range devs {
		named := matchingNames(names, devmap.MatchOf(d.Info, links[d.Info.Path]))
		if len(named) == 0 {
			continue
		}
		rec := m.recordLocked(d.Info.Path)
		if rec == nil {
			rec = m.file.Add(d.Info, d.Kinds, links[d.Info.Path])
			m.bound[d.Info.Path] = rec.AutoID
			changed = true
		}
		changed = m.applyNames(rec, named) || changed
	}
	if changed {
		if err := devmap.Save(m.cfg.DevicesFile, m.file); err != nil {
			m.log.Warn("cannot save devices file", "file", m.cfg.DevicesFile, "err", err)
		}
		if m.bus != nil {
			m.bus.Publish(contracts.TopicAutoIDsChanged, nil)
		}
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

// ResolveKey находит кнопку устройства по авто-ID и номеру или имени (contracts.Inspector):
// номер из devices.yaml ("001"), имя, данное человеком, или стандартное имя клавиши ("A").
func (m *Module) ResolveKey(device, button string) (contracts.DeviceKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Устройство — по авто-ID или имени (работает и для отключённого: по файлу).
	var rec *devmap.Device
	if m.file != nil {
		rec = m.file.Lookup(device)
	}
	if rec == nil {
		return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownDevice, "device", device)
	}
	key := func(code uint16, name string) contracts.DeviceKey {
		return contracts.DeviceKey{Key: keys.Key{Name: devmap.Ref(rec.Display(), name), Type: ev.EvKey, Code: code}, Device: rec.AutoID}
	}

	// Номер кнопки или её имя, данное человеком.
	for num, c := range rec.Buttons {
		if num == button || (c.Name != "" && strings.EqualFold(c.Name, button)) {
			if code, ok := ev.ParseCode(ev.EvKey, c.Code); ok {
				return key(code, rec.ControlDisplay(num)), nil
			}
		}
	}

	// Стандартное имя клавиши — именно на этом устройстве ({UnKey.A}).
	if k, ok := keys.Lookup(button); ok {
		dk := key(k.Code, k.Name)
		dk.AnySide = k.AnySide
		return dk, nil
	}
	return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", rec.AutoID, "button", button)
}

// ResolveAxis находит ось устройства для привязок (contracts.Inspector).
func (m *Module) ResolveAxis(device, axis string) (contracts.DeviceKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Устройство — по авто-ID или имени.
	var rec *devmap.Device
	if m.file != nil {
		rec = m.file.Lookup(device)
	}
	if rec == nil {
		return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownDevice, "device", device)
	}
	key := func(typ, code uint16, name string) contracts.DeviceKey {
		return contracts.DeviceKey{Key: keys.Key{Name: devmap.Ref(rec.Display(), name), Type: typ, Code: code}, Device: rec.AutoID}
	}

	// Номер оси ("Axis01", "Rel01") или её имя, данное человеком.
	for num, c := range rec.Axes {
		if !strings.EqualFold(num, axis) && (c.Name == "" || !strings.EqualFold(c.Name, axis)) {
			continue
		}
		for _, typ := range []uint16{ev.EvAbs, ev.EvRel} {
			if code, ok := ev.ParseCode(typ, c.Code); ok {
				return key(typ, code, rec.ControlDisplay(num)), nil
			}
		}
	}

	// Стандартное имя оси — именно на этом устройстве ({Pad.LX}, {Мышь.MouseX}).
	if k, ok := keys.LookupAxis(axis); ok {
		return key(ev.EvAbs, k.Code, k.Name), nil
	}
	if k, ok := keys.LookupRel(axis); ok {
		return key(ev.EvRel, k.Code, k.Name), nil
	}
	return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", rec.AutoID, "button", axis)
}

// DeviceOf возвращает авто-ID подключённого устройства по пути (contracts.Inspector).
func (m *Module) DeviceOf(path string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound[path]
}

// Label возвращает авто-ID кнопки или оси для макросов (contracts.Inspector).
func (m *Module) Label(path string, typ, code uint16) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.labelLocked(path, typ, code)
}

// fillControl дописывает кнопке или оси номер, имя человека и имя для макросов из записи устройства.
func fillControl(c *contracts.DeviceControl, rec *devmap.Device, typ uint16, label string) {
	c.Number = rec.Label(typ, c.Code)
	if c.Number != "" {
		if name := rec.ControlDisplay(c.Number); name != c.Number {
			c.CustomName = name
		}
	}
	c.Label = label
}

// recordLocked — запись devices.yaml подключённого устройства (nil — у него нет авто-ID); под m.mu.
func (m *Module) recordLocked(path string) *devmap.Device {
	id := m.bound[path]
	if id == "" || m.file == nil {
		return nil
	}
	return m.file.Lookup(id)
}

// labelLocked — Label под удерживаемым m.mu: имя устройства и кнопки, если их дал человек
// ({Sega.Start}), иначе авто-ID и номер ({UnKey001}).
func (m *Module) labelLocked(path string, typ, code uint16) string {
	rec := m.recordLocked(path)
	if rec == nil {
		return ""
	}
	if n := rec.Label(typ, code); n != "" {
		return devmap.Ref(rec.Display(), rec.ControlDisplay(n))
	}
	return ""
}

// recordFor находит запись devices.yaml устройства (под m.mu): по авто-ID или имени, иначе —
// среди подключённых found (по пути, eventN, части названия). Подключённому устройству без
// авто-ID при create он выдаётся. Ошибка — *devmap.NameError (не найдено, неоднозначно).
func (m *Module) recordFor(device string, found []contracts.DeviceDetails, create bool) (*devmap.Device, error) {
	if rec := m.file.Lookup(device); rec != nil {
		return rec, nil
	}

	// Подключённое устройство: ровно одно.
	switch len(found) {
	case 0:
		return nil, &devmap.NameError{Code: devmap.NameUnknown, Name: device}
	case 1:
	default:
		names := make([]string, 0, len(found))
		for _, d := range found {
			names = append(names, filepath.Base(d.Info.Path)+" "+d.Info.Name)
		}
		return nil, &devmap.NameError{Code: devmap.NameAmbiguous, Name: device, Other: strings.Join(names, "; ")}
	}

	// Его запись или новая.
	d := found[0]
	if id := m.bound[d.Info.Path]; id != "" {
		return m.file.Lookup(id), nil
	}
	if !create {
		return nil, &devmap.NameError{Code: devmap.NameUnknown, Name: device}
	}
	links := ev.ReadLinks(m.cfg.InputDir)
	rec := m.file.Add(d.Info, d.Kinds, links[d.Info.Path])
	m.bound[d.Info.Path] = rec.AutoID
	return rec, nil
}

// NamesFor возвращает имена кнопок устройств devices (авто-ID или имена, без учёта регистра) для
// раздела devices проекта (contracts.Inspector); неизвестные устройства и устройства без имён
// пропускаются, каждое устройство — один раз.
func (m *Module) NamesFor(devices []string) []devmap.Names {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil
	}
	var out []devmap.Names
	seen := map[*devmap.Device]bool{}
	for _, ref := range devices {
		rec := m.file.Lookup(ref)
		if rec == nil || seen[rec] {
			continue
		}
		seen[rec] = true
		if n, ok := devmap.NamesOf(rec); ok {
			out = append(out, n)
		}
	}
	return out
}

// Rename даёт имя устройству или его кнопке и сохраняет devices.yaml (contracts.Inspector).
func (m *Module) Rename(device, control, name string) error {
	name = strings.TrimSpace(name)

	// Подключённое устройство по пути, eventN или части названия (сведения — до блокировки).
	found := m.Find(device)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.fileOK {
		return fmt.Errorf("devices file is broken: %s", m.cfg.DevicesFile)
	}

	// Запись: по авто-ID или имени; иначе — подключённое устройство (без авто-ID — выдаём его).
	rec, err := m.recordFor(device, found, true)
	if err != nil {
		return err
	}

	// Имя устройства или кнопки.
	if control == "" {
		err = m.file.SetDeviceName(rec.AutoID, name)
	} else {
		err = rec.SetButtonName(control, name)
	}
	if err != nil {
		return err
	}

	// Сохранение; окно обновит список устройств.
	if err := devmap.Save(m.cfg.DevicesFile, m.file); err != nil {
		return err
	}
	m.log.Info("device renamed", "device", rec.AutoID, "control", control, "name", name)
	if m.bus != nil {
		m.bus.Publish(contracts.TopicAutoIDsChanged, nil)
	}
	return nil
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

		// Авто-ID и имя устройства, имена и номера его кнопок и осей без стандартного имени.
		dd.AutoID = m.bound[d.Info.Path]
		if rec := m.recordLocked(d.Info.Path); rec != nil {
			dd.DeviceName = rec.Name
			for _, list := range []struct {
				typ uint16
				c   []contracts.DeviceControl
			}{{ev.EvKey, dd.Keys}, {ev.EvRel, dd.Rel}} {
				for i := range list.c {
					fillControl(&list.c[i], rec, list.typ, m.labelLocked(d.Info.Path, list.typ, list.c[i].Code))
				}
			}
			for i := range dd.Axes {
				fillControl(&dd.Axes[i].DeviceControl, rec, ev.EvAbs, m.labelLocked(d.Info.Path, ev.EvAbs, dd.Axes[i].Code))
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

	// Относительные оси (движение мыши, колёса): имена MouseX, MouseWheel… — для привязок.
	for _, c := range caps.Codes[ev.EvRel] {
		name, _ := keys.RelNameOf(c)
		out.Rel = append(out.Rel, control(ev.EvRel, c, name))
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
