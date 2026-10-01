package api

import (
	"net/http"

	"mkey/internal/lib/config"
)

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
