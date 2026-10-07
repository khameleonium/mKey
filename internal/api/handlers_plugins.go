package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/config"
)

// Плагины (FR-PLG-4, ADR-0029): список, включение и выключение (выбор сохраняется в config.yaml,
// modules.plugins.active), установка из папки или архива .zip, удаление, журнал.

// maxPluginUpload — предел размера архива плагина, загружаемого из окна.
const maxPluginUpload = 100 << 20

// registerPluginRoutes добавляет маршруты плагинов.
func (m *Module) registerPluginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/plugins", m.handlePlugins)
	mux.HandleFunc("POST /api/v1/plugins/install", m.handlePluginInstall)
	mux.HandleFunc("POST /api/v1/plugins/{id}/{action}", m.handlePluginToggle)
	mux.HandleFunc("DELETE /api/v1/plugins/{id}", m.handlePluginRemove)
	mux.HandleFunc("GET /api/v1/plugins/{id}/log", m.handlePluginLog)
}

// handlePlugins возвращает плагины и папку плагинов: {plugins, dir}.
func (m *Module) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if m.svc.plugins == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": m.svc.plugins.List(), "dir": m.svc.plugins.Dir()})
}

// handlePluginInstall устанавливает плагин: JSON {path} — папка или архив на этом компьютере;
// тело application/zip — архив, загруженный из окна. Плагин остаётся выключенным.
func (m *Module) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	if m.svc.plugins == nil {
		m.unavailable(w, r)
		return
	}

	// Откуда: загруженный архив (во временный файл) или путь.
	src := ""
	if r.Header.Get("Content-Type") == "application/zip" {
		tmp, err := os.CreateTemp("", "mkey-plugin-*.zip")
		if err != nil {
			m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
			return
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		_, err = io.Copy(tmp, http.MaxBytesReader(w, r.Body, maxPluginUpload))
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			m.writeError(w, r, http.StatusBadRequest, "api.plugin_bad", map[string]string{"error": err.Error()})
			return
		}
		src = tmp.Name()
	} else {
		var req struct {
			Path string `json:"path"`
		}
		if !m.readJSON(w, r, &req) {
			return
		}
		src = filepath.Clean(req.Path)
	}

	// Установка.
	info, err := m.svc.plugins.Install(src)
	if err != nil {
		m.writePluginError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handlePluginToggle включает (enable) или выключает (disable) плагин и сохраняет список
// включённых в config.yaml.
func (m *Module) handlePluginToggle(w http.ResponseWriter, r *http.Request) {
	if m.svc.plugins == nil {
		m.unavailable(w, r)
		return
	}
	action := r.PathValue("action")
	if action != "enable" && action != "disable" {
		m.writeError(w, r, http.StatusNotFound, "api.bad_request", map[string]string{"error": "unknown action " + action})
		return
	}
	if err := m.svc.plugins.SetActive(r.PathValue("id"), action == "enable"); err != nil {
		m.writePluginError(w, r, err)
		return
	}
	if err := config.SetModuleValue(m.cfg.ConfigFile, "plugins", "active", m.svc.plugins.Active()); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePluginRemove удаляет плагин пользователя (и убирает его из включённых).
func (m *Module) handlePluginRemove(w http.ResponseWriter, r *http.Request) {
	if m.svc.plugins == nil {
		m.unavailable(w, r)
		return
	}
	if err := m.svc.plugins.Remove(r.PathValue("id")); err != nil {
		m.writePluginError(w, r, err)
		return
	}
	if err := config.SetModuleValue(m.cfg.ConfigFile, "plugins", "active", m.svc.plugins.Active()); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePluginLog возвращает журнал плагина: ?lines=N (по умолчанию 200) → {lines}.
func (m *Module) handlePluginLog(w http.ResponseWriter, r *http.Request) {
	if m.svc.plugins == nil {
		m.unavailable(w, r)
		return
	}
	n, err := strconv.Atoi(r.URL.Query().Get("lines"))
	if err != nil || n <= 0 {
		n = 200
	}
	lines, err := m.svc.plugins.Log(r.PathValue("id"), n)
	if err != nil {
		m.writePluginError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// writePluginError переводит ошибку управления плагинами в понятный ответ.
func (m *Module) writePluginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, contracts.ErrPluginNotFound):
		m.writeError(w, r, http.StatusNotFound, "api.plugin_not_found", nil)
	case errors.Is(err, contracts.ErrPluginExists):
		m.writeError(w, r, http.StatusConflict, "api.plugin_exists", nil)
	case errors.Is(err, contracts.ErrPluginSystem):
		m.writeError(w, r, http.StatusBadRequest, "api.plugin_system", nil)
	case errors.Is(err, contracts.ErrBadPlugin), errors.Is(err, os.ErrNotExist):
		m.writeError(w, r, http.StatusBadRequest, "api.plugin_bad", map[string]string{"error": err.Error()})
	default:
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
	}
}
