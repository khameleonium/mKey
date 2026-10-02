package output

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// Управление виртуальными устройствами проектов (contracts.VirtualDeviceManager): устройство
// создаётся, когда проект с ним включают (и при запуске mKey), и уничтожается, когда проект
// выключают или убирают устройство из проекта (решение владельца — устройства живут в проектах).

// reservedNames — имена, которые заняты в макросах самим mKey (основные клавиатура и мышь).
var reservedNames = []string{"keyboard", "mouse", "pointer"}

// vdevPhys — начало физического пути виртуальных устройств проектов; clonePhys — временных
// копий устройств для повтора записей.
const (
	vdevPhys  = contracts.VirtualPhysPrefix + "vdev/"
	clonePhys = contracts.VirtualPhysPrefix + "clone/"
)

// maxDeviceName — предел длины имени устройства в ядре (UINPUT_MAX_NAME_SIZE 80 с нулём в конце).
const maxDeviceName = 79

// wanted — устройство, которое должно существовать: описание и проект.
type wanted struct {
	spec    project.VirtualDevice
	project string
}

// Validate проверяет описание устройства из проекта (contracts.VirtualDeviceManager).
func (m *Module) Validate(v project.VirtualDevice) error {
	// Имя не занято mKey и помещается в имя устройства в системе.
	if slices.Contains(reservedNames, strings.ToLower(v.Name)) {
		return fmt.Errorf("virtual device name %q is reserved by mKey", v.Name)
	}
	if len(contracts.VirtualNamePrefix+v.Name) >= 80 {
		return fmt.Errorf("virtual device name %q is too long", v.Name)
	}

	// Шаблон известен, его описание строится (для custom — кнопки и оси правильные).
	t, ok := templates[v.Template]
	if !ok {
		return fmt.Errorf("unknown virtual device template %q (available: %s)", v.Template, strings.Join(templateOrder, ", "))
	}
	if v.Template != "custom" && (len(v.Buttons) > 0 || len(v.Axes) > 0) {
		return fmt.Errorf("virtual device %q: buttons and axes are set only for the custom template", v.Name)
	}
	_, err := t.build(v)
	return err
}

// Templates возвращает шаблоны устройств по порядку (contracts.VirtualDeviceManager).
func (m *Module) Templates() []string { return slices.Clone(templateOrder) }

// TemplateInfo возвращает кнопки и оси шаблона именами для макросов (contracts.VirtualDeviceManager).
func (m *Module) TemplateInfo(id string) (contracts.VirtualTemplateInfo, bool) {
	// Состав устройства по шаблону (custom без проекта не строится).
	t, ok := templates[id]
	if !ok {
		return contracts.VirtualTemplateInfo{}, false
	}
	setup, err := t.build(project.VirtualDevice{Template: id})
	if err != nil {
		return contracts.VirtualTemplateInfo{}, false
	}
	info := contracts.VirtualTemplateInfo{ID: id, Buttons: []string{}, Axes: []string{}}

	// Кнопки: коды устройства — в коды макросов (обратно переназначению), затем кнопки,
	// которые устройство передаёт осью (курки, крестовина Xbox).
	back := map[uint16]uint16{}
	for macro, dev := range t.remap {
		back[dev] = macro
	}
	codes := make([]uint16, 0, len(setup.Keys)+len(t.buttonAxes))
	for _, c := range setup.Keys {
		if mc, ok := back[c]; ok {
			c = mc
		}
		codes = append(codes, c)
	}
	extra := slices.Sorted(maps.Keys(t.buttonAxes))
	codes = append(codes, extra...)
	for _, c := range codes {
		if name, ok := keys.NameOf(c); ok && !slices.Contains(info.Buttons, name) {
			info.Buttons = append(info.Buttons, name)
		}
	}

	// Оси — по порядку кодов, с именами для макросов.
	for _, c := range slices.Sorted(maps.Keys(setup.Abs)) {
		if name, ok := keys.AxisNameOf(c); ok {
			info.Axes = append(info.Axes, name)
		}
	}
	return info, true
}

// reconcile приводит виртуальные устройства в соответствие с включёнными проектами: лишние
// уничтожаются, изменённые пересоздаются, недостающие создаются. Ошибки (имя занято другим
// проектом, нет прав на uinput) запоминаются у устройства и видны в списке.
func (m *Module) reconcile() {
	// Что должно существовать (первое объявление имени побеждает).
	want := map[string]wanted{}
	errs := map[string]string{}
	var conflicts []contracts.VirtualDeviceInfo
	if m.projects != nil {
		for _, st := range m.projects.List() {
			if !st.Project.IsEnabled() {
				continue
			}
			for _, v := range st.Project.VirtualDevices {
				key := strings.ToLower(v.Name)
				if other, dup := want[key]; dup {
					// Имя уже занято другим проектом: это устройство не создаётся.
					conflicts = append(conflicts, contracts.VirtualDeviceInfo{Name: v.Name, Template: v.Template, Project: st.Project.ID,
						SystemName: contracts.VirtualNamePrefix + v.Name, Error: fmt.Sprintf("name %q is already used by project %q", v.Name, other.project)})
					continue
				}
				want[key] = wanted{spec: v, project: st.Project.ID}
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Лишние и изменённые — уничтожаем (кнопки отпускаются).
	for key, d := range m.vdevs {
		if w, ok := want[key]; ok && reflect.DeepEqual(w.spec, d.spec) && w.project == d.project {
			continue
		}
		if err := d.close(); err != nil {
			m.log.Warn("virtual device close", "name", d.spec.Name, "err", err)
		}
		delete(m.vdevs, key)
		m.log.Info("virtual device removed", "name", d.spec.Name)
	}

	// Недостающие — создаём.
	for key, w := range want {
		if _, ok := m.vdevs[key]; ok {
			continue
		}
		d, err := m.createVirtual(w)
		if err != nil {
			errs[key] = err.Error()
			m.log.Warn("virtual device not created", "name", w.spec.Name, "project", w.project, "err", err)
			continue
		}
		m.vdevs[key] = d
		m.log.Info("virtual device created", "name", d.name, "template", w.spec.Template, "project", w.project, "node", d.node)
	}
	m.want, m.vdevErrs, m.conflicts = want, errs, conflicts
}

// createVirtual создаёт устройство по описанию из проекта (под m.mu).
func (m *Module) createVirtual(w wanted) (*vdevice, error) {
	if err := m.Validate(w.spec); err != nil {
		return nil, err
	}
	t := templates[w.spec.Template]
	setup, err := t.build(w.spec)
	if err != nil {
		return nil, err
	}
	setup.Name = contracts.VirtualNamePrefix + w.spec.Name
	setup.Phys = vdevPhys + strings.ToLower(w.spec.Name)
	writer, err := m.create(setup)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", contracts.ErrOutputUnavailable, err)
	}
	d := &vdevice{
		device: newDevice(setup.Name, writer, m.clk, time.Duration(m.cfg.SettleMS)*time.Millisecond, m.cfg.MaxEventsPerSecond),
		t:      t, setup: setup, spec: w.spec, project: w.project, held: map[uint16]bool{},
	}
	if u, ok := writer.(interface{ DevNode() (string, error) }); ok {
		d.node, _ = u.DevNode()
	}
	return d, nil
}

// Clone создаёт временную копию устройства для повтора записи (contracts.VirtualDeviceManager).
func (m *Module) Clone(name string, setup ev.Setup) (contracts.VirtualDevice, func() error, error) {
	// Имя «mKey …» и путь mkey/clone/… — по ним mKey не читает свои устройства (нет петли).
	setup.Name = contracts.VirtualNamePrefix + name
	if len(setup.Name) > maxDeviceName {
		setup.Name = strings.ToValidUTF8(setup.Name[:maxDeviceName], "")
	}
	setup.Phys = clonePhys + strconv.FormatInt(m.clones.Add(1), 10)
	writer, err := m.create(setup)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", contracts.ErrOutputUnavailable, err)
	}

	// Копия — как виртуальное устройство без шаблона: нажатия и оси как есть, ReleaseAll
	// отпускает кнопки, возвращает оси в покой и отрывает палец сенсорного экрана.
	d := &vdevice{
		device: newDevice(setup.Name, writer, m.clk, time.Duration(m.cfg.SettleMS)*time.Millisecond, m.cfg.MaxEventsPerSecond),
		setup:  setup, held: map[uint16]bool{},
	}
	if u, ok := writer.(interface{ DevNode() (string, error) }); ok {
		d.node, _ = u.DevNode()
	}
	closeFn := func() error {
		err := d.ReleaseAll()
		return errors.Join(err, d.close())
	}
	return d, closeFn, nil
}

// Device возвращает виртуальное устройство проекта по имени (contracts.VirtualDeviceManager).
func (m *Module) Device(name string) (contracts.VirtualDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(name)
	if d, ok := m.vdevs[key]; ok {
		return d, nil
	}
	if e, ok := m.vdevErrs[key]; ok {
		return nil, fmt.Errorf("%w: %s: %s", contracts.ErrOutputUnavailable, name, e)
	}
	return nil, fmt.Errorf("%w: %q", contracts.ErrUnknownVirtual, name)
}

// Resolve находит кнопку или ось устройства по имени (contracts.VirtualDeviceManager).
func (m *Module) Resolve(device, control string) (uint16, bool, error) {
	m.mu.Lock()
	w, described := m.want[strings.ToLower(device)]
	m.mu.Unlock()
	if !described {
		return 0, false, fmt.Errorf("%w: %q", contracts.ErrUnknownVirtual, device)
	}
	return m.ResolveIn(w.spec, control)
}

// ResolveIn находит кнопку или ось по описанию устройства (contracts.VirtualDeviceManager): кнопка —
// первый подходящий код, который есть у устройства сам или через правила шаблона; иначе ось.
func (m *Module) ResolveIn(v project.VirtualDevice, control string) (uint16, bool, error) {
	t, ok := templates[v.Template]
	if !ok {
		return 0, false, fmt.Errorf("unknown virtual device template %q", v.Template)
	}
	setup, err := t.build(v)
	if err != nil {
		return 0, false, err
	}

	// Кнопка.
	for _, code := range buttonCodes(control) {
		if _, axis := t.buttonAxes[code]; axis || slices.Contains(setup.Keys, code) {
			return code, false, nil
		}
		if dev, remapped := t.remap[code]; remapped && slices.Contains(setup.Keys, dev) {
			return code, false, nil
		}
	}

	// Ось.
	if code, ok := axisCode(control); ok {
		if _, has := setup.Abs[code]; has {
			return code, true, nil
		}
	}
	return 0, false, fmt.Errorf("%w: %s.%s", contracts.ErrUnknownControl, v.Name, control)
}

// List возвращает виртуальные устройства включённых проектов (contracts.VirtualDeviceManager).
func (m *Module) List() []contracts.VirtualDeviceInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]contracts.VirtualDeviceInfo, 0, len(m.want)+len(m.conflicts))
	out = append(out, m.conflicts...)
	for key, w := range m.want {
		info := contracts.VirtualDeviceInfo{Name: w.spec.Name, Template: w.spec.Template, Project: w.project,
			SystemName: contracts.VirtualNamePrefix + w.spec.Name, Error: m.vdevErrs[key]}
		if d, ok := m.vdevs[key]; ok {
			info.Node = d.node
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b contracts.VirtualDeviceInfo) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// watchProjects пересоздаёт виртуальные устройства при изменении проектов, пока подписка открыта.
func (m *Module) watchProjects(events <-chan contracts.Event) {
	defer close(m.watchDone)
	for range events {
		m.reconcile()
	}
}

// templateMeta — шаблон в точке расширения device_template (для окна: список и описания).
type templateMeta struct{ id string }

// Meta возвращает метаданные шаблона.
func (t templateMeta) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: t.id, NameKey: "vdev.template." + t.id, DescriptionKey: "vdev.template." + t.id + ".description", Provider: ModuleID}
}

// Проверки на этапе компиляции: Module — менеджер виртуальных устройств, ev.UInput — приёмник событий.
var (
	_ contracts.VirtualDeviceManager = (*Module)(nil)
	_ eventWriter                    = (*ev.UInput)(nil)
)
