package api

import (
	"net/http"
	"slices"

	"mkey/internal/lib/config"
)

// Настройки окна программы: язык и тема. Хранятся в config.yaml (язык — language в начале файла,
// он же — язык команд и меню значка; тема — modules.api.theme), чтобы их можно было задать
// и в окне, и в файле. Окно при открытии берёт их отсюда.

// interfaceSettings — язык и тема окна.
type interfaceSettings struct {
	// Language — язык: ru, en или "" (как в системе).
	Language string `json:"language"`
	// Theme — тема: system, light, dark ("" — system).
	Theme string `json:"theme"`
}

// Допустимые значения языка и темы.
var (
	interfaceLangs  = []string{"", "ru", "en"}
	interfaceThemes = []string{"", "system", "light", "dark"}
)

// handleInterfaceGet возвращает язык и тему из config.yaml.
func (m *Module) handleInterfaceGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(m.cfg.ConfigFile)
	if err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	m.mu.Lock()
	theme := m.cfg.Theme
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, interfaceSettings{Language: cfg.Language, Theme: theme})
}

// handleInterfacePut сохраняет язык и тему в config.yaml. Неизвестные значения — 400
// api.interface_bad.
func (m *Module) handleInterfacePut(w http.ResponseWriter, r *http.Request) {
	var req interfaceSettings
	if !m.readJSON(w, r, &req) {
		return
	}

	// Проверка значений.
	if !slices.Contains(interfaceLangs, req.Language) || !slices.Contains(interfaceThemes, req.Theme) {
		m.writeError(w, r, http.StatusBadRequest, "api.interface_bad", nil)
		return
	}

	// Сохраняем: язык — в начало файла, тема — в настройки модуля api.
	err := config.SetValue(m.cfg.ConfigFile, "language", req.Language)
	if err == nil {
		err = config.SetModuleValue(m.cfg.ConfigFile, ModuleID, "theme", req.Theme)
	}
	if err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	m.mu.Lock()
	m.cfg.Theme = req.Theme
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, req)
}
