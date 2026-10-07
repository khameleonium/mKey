package api

import (
	"errors"
	"net/http"

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

// handleVirtualDevices возвращает виртуальные устройства включённых проектов и шаблоны (FR-VD-1):
// {devices: [VirtualDeviceInfo], templates: [...], template_info: [VirtualTemplateInfo]}.
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
	writeJSON(w, http.StatusOK, map[string]any{"devices": m.svc.vdevs.List(), "templates": m.svc.vdevs.Templates(), "template_info": infos})
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
