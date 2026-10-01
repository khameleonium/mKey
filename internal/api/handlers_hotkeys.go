package api

import (
	"errors"
	"net/http"
	"regexp"

	"mkey/internal/lib/config"
	"mkey/internal/lib/dsl"
)

// Системные сочетания mKey (решение владельца: задаются пользователем): «начать/закончить запись»
// и экстренная остановка. Меняются сразу (без перезапуска) и сохраняются в config.yaml.

// systemHotkeys — системные сочетания записью зажатием.
type systemHotkeys struct {
	// Record — начать/закончить запись ("" — выключено).
	Record *string `json:"record,omitempty"`
	// Emergency — экстренная остановка (не меньше двух клавиш).
	Emergency *string `json:"emergency,omitempty"`
}

// fKeyRe — функциональные клавиши F1…F24: их можно назначить на запись одну, без модификаторов.
var fKeyRe = regexp.MustCompile(`^F\d{1,2}$`)

// handleHotkeysGet возвращает системные сочетания.
func (m *Module) handleHotkeysGet(w http.ResponseWriter, _ *http.Request) {
	out := map[string]string{}
	if m.svc.recorder != nil {
		out["record"] = m.svc.recorder.RecordHotkey()
	}
	if m.svc.input != nil {
		out["emergency"] = m.svc.input.EmergencyCombo()
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHotkeysPut меняет системные сочетания: {record?, emergency?}. Каждое проверяется
// (запись зажатием, не одна обычная клавиша, сочетания не совпадают), применяется сразу
// и сохраняется в config.yaml.
func (m *Module) handleHotkeysPut(w http.ResponseWriter, r *http.Request) {
	var req systemHotkeys
	if !m.readJSON(w, r, &req) {
		return
	}

	// Итоговые сочетания: новые или прежние.
	record, emergency := "", ""
	if m.svc.recorder != nil {
		record = m.svc.recorder.RecordHotkey()
	}
	if m.svc.input != nil {
		emergency = m.svc.input.EmergencyCombo()
	}
	if req.Record != nil {
		record = *req.Record
	}
	if req.Emergency != nil {
		emergency = *req.Emergency
	}

	// Проверки: запись сочетаний, «не одна обычная клавиша», не совпадают друг с другом.
	err := checkHotkey(record, true)
	if err == nil {
		err = checkHotkey(emergency, false)
	}
	if err != nil {
		m.writeHotkeyError(w, r, err)
		return
	}
	if record != "" && canonical(record) == canonical(emergency) {
		m.writeError(w, r, http.StatusBadRequest, "api.hotkey_conflict", nil)
		return
	}

	// Применяем и сохраняем.
	cfgPath := m.cfg.ConfigFile
	if req.Record != nil && m.svc.recorder != nil {
		if err := m.svc.recorder.SetRecordHotkey(record); err != nil {
			m.writeHotkeyError(w, r, err)
			return
		}
		if err := config.SetModuleValue(cfgPath, "recorder", "hotkey", record); err != nil {
			m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Emergency != nil && m.svc.input != nil {
		if err := m.svc.input.SetEmergencyCombo(emergency); err != nil {
			m.writeHotkeyError(w, r, err)
			return
		}
		if err := config.SetModuleValue(cfgPath, "input", "emergency_stop", emergency); err != nil {
			m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
			return
		}
	}
	m.handleHotkeysGet(w, r)
}

// errTooSimple — сочетание из одной обычной клавиши (сработало бы при обычной работе).
var errTooSimple = errors.New("hotkey is too simple")

// checkHotkey проверяет системное сочетание. allowEmpty — можно выключить (""); одна клавиша
// допустима только функциональная (F1…F24) и только там, где можно выключать (запись).
func checkHotkey(combo string, allowEmpty bool) error {
	if combo == "" && allowEmpty {
		return nil
	}
	refs, err := dsl.ParseHotkey(combo)
	if err != nil {
		return err
	}
	if len(refs) == 1 && (!allowEmpty || !fKeyRe.MatchString(refs[0].Name)) {
		return errTooSimple
	}
	return nil
}

// canonical возвращает сочетание в каноническом виде (для сравнения записей с разным регистром).
func canonical(combo string) string {
	nodes, err := dsl.Parse(combo)
	if err != nil {
		return combo
	}
	return dsl.Format(nodes)
}

// writeHotkeyError отвечает понятной ошибкой проверки сочетания.
func (m *Module) writeHotkeyError(w http.ResponseWriter, r *http.Request, err error) {
	var de *dsl.Error
	switch {
	case errors.As(err, &de):
		m.writeErrorDetails(w, r, http.StatusBadRequest, de.Code, de.Args, map[string]any{"pos": de.Pos, "args": de.Args})
	case errors.Is(err, errTooSimple):
		m.writeError(w, r, http.StatusBadRequest, "api.hotkey_too_simple", nil)
	default:
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
	}
}
