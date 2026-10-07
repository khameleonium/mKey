package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/config"
	"github.com/khameleonium/mKey/internal/lib/mkrec"
)

// Запись и воспроизведение ввода (этап 6): команды `mkey rec`, `mkey play` и окно программы.

// registerRecRoutes добавляет маршруты записи и воспроизведения.
func (m *Module) registerRecRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/recordings", m.handleRecordings)
	mux.HandleFunc("DELETE /api/v1/recordings/{name}", m.handleRecordingDelete)
	mux.HandleFunc("GET /api/v1/recordings/status", m.handleRecordingStatus)
	mux.HandleFunc("POST /api/v1/recordings/start", m.handleRecordingStart)
	mux.HandleFunc("POST /api/v1/recordings/stop", m.handleRecordingStop)
	mux.HandleFunc("POST /api/v1/recordings/wait", m.handleRecordingWait)
	mux.HandleFunc("POST /api/v1/play", m.handlePlay)
	mux.HandleFunc("POST /api/v1/recordings/{name}/convert", m.handleRecordingConvert)
	mux.HandleFunc("GET /api/v1/settings/hotkeys", m.handleHotkeysGet)
	mux.HandleFunc("PUT /api/v1/settings/hotkeys", m.handleHotkeysPut)
	mux.HandleFunc("GET /api/v1/settings/recording", m.handleRecordSettingsGet)
	mux.HandleFunc("PUT /api/v1/settings/recording", m.handleRecordSettingsPut)
	mux.HandleFunc("GET /api/v1/settings/timing", m.handleTimingGet)
	mux.HandleFunc("PUT /api/v1/settings/timing", m.handleTimingPut)
}

// handleTimingGet возвращает интервалы нажатий по умолчанию (contracts.Timing).
func (m *Module) handleTimingGet(w http.ResponseWriter, r *http.Request) {
	if m.svc.timing == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, m.svc.timing.Timing())
}

// handleTimingPut меняет интервалы нажатий: действуют сразу и сохраняются в config.yaml
// (modules.engine). Значение вне пределов — 400 api.timing_bad.
func (m *Module) handleTimingPut(w http.ResponseWriter, r *http.Request) {
	if m.svc.timing == nil {
		m.unavailable(w, r)
		return
	}
	var req contracts.Timing
	if !m.readJSON(w, r, &req) {
		return
	}

	// Применяем и сохраняем в config.yaml одной записью.
	if err := m.svc.timing.SetTiming(req); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.timing_bad", map[string]string{"error": err.Error()})
		return
	}
	tm := m.svc.timing.Timing()
	if err := config.SetModuleValues(m.cfg.ConfigFile, "engine", []config.KeyValue{
		{Key: "key_hold_ms", Value: tm.KeyHoldMS}, {Key: "key_delay_ms", Value: tm.KeyDelayMS},
		{Key: "layout_switch_ms", Value: tm.LayoutSwitchMS},
	}); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tm)
}

// handleRecordSettingsGet возвращает настройки записи по умолчанию.
func (m *Module) handleRecordSettingsGet(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, m.svc.recorder.RecordSettings())
}

// handleRecordSettingsPut меняет настройки записи по умолчанию: применяются со следующей записи
// и сохраняются в config.yaml (modules.recorder). Неверные — 400 api.record_settings_bad.
func (m *Module) handleRecordSettingsPut(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	var req contracts.RecordSettings
	if !m.readJSON(w, r, &req) {
		return
	}

	// Применяем и сохраняем в config.yaml одной записью.
	if err := m.svc.recorder.SetRecordSettings(req); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.record_settings_bad", map[string]string{"error": err.Error()})
		return
	}
	s := m.svc.recorder.RecordSettings()
	if err := config.SetModuleValues(m.cfg.ConfigFile, "recorder", []config.KeyValue{
		{Key: "kinds", Value: s.Kinds}, {Key: "moves", Value: s.Moves}, {Key: "merge_moves_ms", Value: s.MergeMovesMS},
		{Key: "center_pointer", Value: s.CenterPointer}, {Key: "coalesce_ms", Value: s.CoalesceMS},
	}); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// handleRecordings возвращает сохранённые записи и идущую запись (если есть).
func (m *Module) handleRecordings(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	list, err := m.svc.recorder.Recordings()
	if err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	// Ошибки в файлах — понятным текстом на языке клиента.
	tr := m.translator(r)
	for i := range list {
		if p := list[i].Problem; p != nil {
			p.Message = problemMessage(tr, *p)
		}
	}
	resp := map[string]any{"recordings": list, "dir": m.placePath(contracts.PlaceRecordings)}
	if cur, ok := m.svc.recorder.Recording(); ok {
		resp["current"] = cur
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRecordingStatus возвращает идущую запись: {recording: true, info} или {recording: false}.
func (m *Module) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	info, ok := m.svc.recorder.Recording()
	writeJSON(w, http.StatusOK, map[string]any{"recording": ok, "info": info})
}

// handleRecordingStart начинает запись: {name, kinds}.
func (m *Module) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	var req contracts.RecordOptions
	if !m.readJSON(w, r, &req) {
		return
	}
	info, err := m.svc.recorder.StartRecording(req)
	if err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRecordingStop заканчивает запись: {cut} — сочетание остановки, которое нужно вырезать ("^{Ctrl}{C}").
func (m *Module) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Cut string `json:"cut"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	info, err := m.svc.recorder.StopRecording(req.Cut)
	if err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRecordingWait ждёт окончания идущей записи (сочетанием, из окна или другой командой).
func (m *Module) handleRecordingWait(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	info, err := m.svc.recorder.WaitRecording(r.Context())
	if err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRecordingDelete удаляет запись.
func (m *Module) handleRecordingDelete(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	if err := m.svc.recorder.DeleteRecording(r.PathValue("name")); err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRecordingConvert превращает запись в блоки: {simplify} → {project} (ID нового выключенного проекта).
func (m *Module) handleRecordingConvert(w http.ResponseWriter, r *http.Request) {
	if m.svc.recorder == nil {
		m.unavailable(w, r)
		return
	}
	var req contracts.ConvertOptions
	if !m.readJSON(w, r, &req) {
		return
	}
	id, err := m.svc.recorder.ConvertRecording(r.PathValue("name"), req)
	if err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": id})
}

// handlePlay воспроизводит запись и ждёт окончания: {name, speed, repeat, skip_moves}.
// Разрыв соединения (Ctrl+C в терминале) останавливает воспроизведение.
func (m *Module) handlePlay(w http.ResponseWriter, r *http.Request) {
	if m.svc.player == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Name string `json:"name"`
		contracts.PlayOptions
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	if err := m.svc.player.Play(r.Context(), req.Name, req.PlayOptions); err != nil {
		m.writeRecError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// maxProblemText — сколько символов строки файла показывать в сообщении об ошибке
// (строка с множеством действий может быть очень длинной).
const maxProblemText = 60

// problemMessage — понятное сообщение об ошибке в файле записи на языке tr:
// «строка 8 («0.100 0 ~{Hh}»): неизвестная клавиша «Hh»».
func problemMessage(tr contracts.Translator, p contracts.RecordingProblem) string {
	reason := tr.T("rec.problem."+p.Code, contracts.Arg{Name: "arg", Value: p.Arg})
	if p.Line == 0 {
		return reason
	}
	text := p.Text
	if r := []rune(text); len(r) > maxProblemText {
		text = string(r[:maxProblemText]) + "…"
	}
	return tr.T("rec.problem.at", contracts.Arg{Name: "line", Value: p.Line}, contracts.Arg{Name: "text", Value: text},
		contracts.Arg{Name: "reason", Value: reason})
}

// writeRecError переводит ошибку записи или воспроизведения в ответ API.
// Ошибка в файле записи (api.recording_bad) — с номером строки и понятным описанием в details.
func (m *Module) writeRecError(w http.ResponseWriter, r *http.Request, err error) {
	var bad *mkrec.Problem
	switch {
	case errors.As(err, &bad):
		p := contracts.RecordingProblem{Line: bad.Line, Text: bad.Text, Code: bad.Code, Arg: bad.Arg}
		p.Message = problemMessage(m.translator(r), p)
		m.writeErrorDetails(w, r, http.StatusBadRequest, "api.recording_bad", map[string]string{"problem": p.Message}, p)
	case errors.Is(err, contracts.ErrRecordingNotFound):
		m.writeError(w, r, http.StatusNotFound, "api.recording_not_found", nil)
	case errors.Is(err, contracts.ErrRecordingEmpty):
		m.writeError(w, r, http.StatusBadRequest, "api.recording_empty", nil)
	case errors.Is(err, contracts.ErrAlreadyRecording):
		m.writeError(w, r, http.StatusConflict, "api.already_recording", nil)
	case errors.Is(err, contracts.ErrNotRecording):
		m.writeError(w, r, http.StatusConflict, "api.not_recording", nil)
	case errors.Is(err, context.Canceled):
		m.writeError(w, r, http.StatusConflict, "api.stopped", nil)
	case errors.Is(err, contracts.ErrOutputUnavailable):
		m.writeError(w, r, http.StatusServiceUnavailable, "api.output_unavailable", map[string]string{"error": err.Error()})
	default:
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
	}
}
