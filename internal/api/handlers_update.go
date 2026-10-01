package api

import (
	"errors"
	"net/http"

	"mkey/internal/contracts"
	"mkey/internal/lib/config"
)

// Обновление mKey (ADR-0030): сведения, проверка по просьбе, установка, включение проверки раз
// в сутки (сохраняется в config.yaml, modules.update.check).

// registerUpdateRoutes добавляет маршруты обновления.
func (m *Module) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/update", m.handleUpdateInfo)
	mux.HandleFunc("POST /api/v1/update/check", m.handleUpdateCheck)
	mux.HandleFunc("POST /api/v1/update/apply", m.handleUpdateApply)
	mux.HandleFunc("PUT /api/v1/settings/update", m.handleUpdateSettings)
}

// handleUpdateInfo возвращает сведения по последней проверке (без обращения к сети).
func (m *Module) handleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	if m.svc.updater == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, m.svc.updater.Info())
}

// handleUpdateCheck спрашивает последний выпуск.
func (m *Module) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if m.svc.updater == nil {
		m.unavailable(w, r)
		return
	}
	info, err := m.svc.updater.Check(r.Context())
	if err != nil {
		m.writeError(w, r, http.StatusBadGateway, "api.update_check_failed", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleUpdateApply устанавливает новую версию; mKey затем перезапускается.
func (m *Module) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if m.svc.updater == nil {
		m.unavailable(w, r)
		return
	}
	info, err := m.svc.updater.Apply(r.Context())
	switch {
	case errors.Is(err, contracts.ErrCannotUpdate):
		m.writeError(w, r, http.StatusBadRequest, "api.update_cannot", map[string]string{"reason": m.translator(r).T("update.reason." + info.Reason)})
	case err != nil:
		m.writeError(w, r, http.StatusBadGateway, "api.update_failed", map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, info)
	}
}

// handleUpdateSettings включает или выключает проверку раз в сутки: {check}.
func (m *Module) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if m.svc.updater == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Check bool `json:"check"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	m.svc.updater.SetCheck(req.Check)
	if err := config.SetModuleValue(m.cfg.ConfigFile, "update", "check", req.Check); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m.svc.updater.Info())
}
