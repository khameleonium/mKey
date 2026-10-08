package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/config"
	"github.com/khameleonium/mKey/internal/lib/devmap"
	"github.com/khameleonium/mKey/internal/lib/dsl"
)

// keyResolver — распознаватель клавиш макросов: виртуальные устройства проектов ({pad2.South},
// FR-VD-1), кнопки физических устройств с авто-ID ({UnKey001}, FR-DEV-2), остальные — как обычно.
func (m *Module) keyResolver() dsl.Resolver {
	// Кнопки, которых нет у клавиатуры и мыши mKey, движок нажимает «от имени» устройства.
	r := dsl.DeviceResolver{Physical: m.svc.devOut != nil && m.svc.inspect != nil}
	if insp := m.svc.inspect; insp != nil {
		r.Lookup = func(device, button string) (uint16, string, error) {
			k, err := insp.ResolveKey(device, button)
			return k.Code, k.Name, err
		}
	}
	if vd := m.svc.vdevs; vd != nil {
		r.Virtual = func(device, control string) (uint16, bool, bool, error) {
			code, axis, err := vd.Resolve(device, control)
			if errors.Is(err, contracts.ErrUnknownVirtual) {
				return 0, false, false, nil
			}
			if err != nil {
				return 0, false, true, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", device, "button", control)
			}
			return code, axis, true, nil
		}
	}
	return r
}

// virtualCard — виртуальное устройство любого проекта для страницы «Виртуальные устройства»
// (FR-VD-8): где описано, в каком оно состоянии и что вокруг него в проекте.
type virtualCard struct {
	// Name, Template, SystemName — имя в макросах, шаблон и имя в системе («mKey pad2»).
	Name       string `json:"name"`
	Template   string `json:"template"`
	SystemName string `json:"system_name"`
	// Project и ProjectName — проект, в котором устройство описано.
	Project     string `json:"project"`
	ProjectName string `json:"project_name"`
	// State — "on" (подключено: игры его видят), "off" (проект выключен — устройства в системе
	// нет) или "error" (проект включён, но устройство не создано; причина — Error).
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Node — файл устройства (/dev/input/eventN), пока оно подключено.
	Node string `json:"node,omitempty"`
	// Bindings — сколько привязок проекта управляют устройством; Hides — какая-то из них прячет
	// клавиши от других программ (пока проект включён, они нажимают только устройство).
	Bindings int  `json:"bindings"`
	Hides    bool `json:"hides"`
	// Others — сколько ещё в проекте событий, переназначений и других устройств: они включаются
	// и выключаются вместе с этим устройством.
	Others int `json:"others"`
}

// virtualCards собирает виртуальные устройства всех проектов (включённых и выключенных) с
// состоянием: у включённых — по менеджеру устройств (подключено или ошибка), у выключенных — "off".
func (m *Module) virtualCards() []virtualCard {
	out := []virtualCard{}
	if m.svc.projects == nil {
		return out
	}

	// Состояние устройств включённых проектов — по имени и проекту.
	live := map[string]contracts.VirtualDeviceInfo{}
	for _, d := range m.svc.vdevs.List() {
		live[strings.ToLower(d.Name)+"\x00"+d.Project] = d
	}

	// Устройства всех проектов по порядку проектов.
	for _, st := range m.svc.projects.List() {
		p := st.Project
		for _, v := range p.VirtualDevices {
			c := virtualCard{Name: v.Name, Template: v.Template, SystemName: contracts.VirtualNamePrefix + v.Name,
				Project: p.ID, ProjectName: p.Name, State: "off",
				Others: len(p.Events) + len(p.Remaps) + len(p.VirtualDevices) - 1}

			// Привязки, которые управляют этим устройством ({pad2.…}).
			prefix := "{" + strings.ToLower(v.Name) + "."
			for _, b := range p.Bindings {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(b.To)), prefix) {
					c.Bindings++
					c.Hides = c.Hides || b.Hide
				}
			}

			// Состояние: включённый проект — по менеджеру; ошибка файла проекта — ошибка.
			if p.IsEnabled() {
				c.State = "on"
				if d, ok := live[strings.ToLower(v.Name)+"\x00"+p.ID]; ok {
					c.Node = d.Node
					if d.Error != "" {
						c.State, c.Error = "error", d.Error
					}
				}
				if st.Error != "" {
					c.State, c.Error = "error", st.Error
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// handleVirtualDevices возвращает виртуальные устройства и шаблоны (FR-VD-1, FR-VD-8):
// {devices: [VirtualDeviceInfo] — включённых проектов, all: [virtualCard] — всех проектов с
// состоянием, templates: [...], template_info: [VirtualTemplateInfo]}.
func (m *Module) handleVirtualDevices(w http.ResponseWriter, r *http.Request) {
	if m.svc.vdevs == nil {
		m.unavailable(w, r)
		return
	}
	// Шаблоны: имена и состав (кнопки и оси — для мастера «второй геймпад»).
	var infos []contracts.VirtualTemplateInfo
	for _, id := range m.svc.vdevs.Templates() {
		if info, ok := m.svc.vdevs.TemplateInfo(id); ok {
			infos = append(infos, info)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": m.svc.vdevs.List(), "all": m.virtualCards(),
		"templates": m.svc.vdevs.Templates(), "template_info": infos})
}

// handleVirtualState возвращает, что нажато и куда наклонены оси у подключённого виртуального
// устройства (проверка вживую, FR-VD-8): {buttons, axes}. Не подключено — 404 api.virtual_off.
func (m *Module) handleVirtualState(w http.ResponseWriter, r *http.Request) {
	if m.svc.vdevs == nil {
		m.unavailable(w, r)
		return
	}
	name := r.PathValue("name")
	st, err := m.svc.vdevs.State(name)
	if err != nil {
		m.writeError(w, r, http.StatusNotFound, "api.virtual_off", map[string]string{"name": name})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// deviceSettings — настройки устройств в окне: каким устройствам давать авто-ID (FR-DEV-2).
type deviceSettings struct {
	// AutoIDs — "smart" (всем, кроме служебных), "all" или "unusual".
	AutoIDs string `json:"auto_ids"`
}

// handleDeviceSettingsGet возвращает настройки устройств.
func (m *Module) handleDeviceSettingsGet(w http.ResponseWriter, r *http.Request) {
	if m.svc.inspect == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, deviceSettings{AutoIDs: m.svc.inspect.AutoIDMode()})
}

// handleDeviceSettingsPut меняет режим авто-ID: применяется сразу (подходящие устройства
// получают имена) и сохраняется в config.yaml. Неизвестный режим — 400 api.auto_ids_bad.
func (m *Module) handleDeviceSettingsPut(w http.ResponseWriter, r *http.Request) {
	if m.svc.inspect == nil {
		m.unavailable(w, r)
		return
	}
	var req deviceSettings
	if !m.readJSON(w, r, &req) {
		return
	}

	// Применяем и сохраняем.
	if err := m.svc.inspect.SetAutoIDMode(req.AutoIDs); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.auto_ids_bad", map[string]string{"mode": req.AutoIDs})
		return
	}
	if err := config.SetModuleValue(m.cfg.ConfigFile, "inspector", "auto_ids", m.svc.inspect.AutoIDMode()); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, deviceSettings{AutoIDs: m.svc.inspect.AutoIDMode()})
}

// handleDeviceRename даёт имя устройству или его кнопке (FR-DEV-3): {device, control, name}.
// control пусто — устройство; name пусто — убрать имя (авто-ID и номера работают всегда).
// Ошибка имени — 400 api.rename_<вид> с понятным текстом.
func (m *Module) handleDeviceRename(w http.ResponseWriter, r *http.Request) {
	if m.svc.inspect == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Device  string `json:"device"`
		Control string `json:"control"`
		Name    string `json:"name"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}

	// Переименование; ошибки имени — понятным текстом.
	err := m.svc.inspect.Rename(req.Device, req.Control, req.Name)
	var ne *devmap.NameError
	switch {
	case errors.As(err, &ne):
		m.writeError(w, r, http.StatusBadRequest, "api.rename_"+ne.Code, map[string]string{"name": ne.Name, "other": ne.Other})
	case err != nil:
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
