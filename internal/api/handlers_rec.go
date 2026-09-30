package api

import (
	"context"
	"errors"
	"net/http"

	"mkey/internal/contracts"
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
	mux.HandleFunc("GET /api/v1/settings/hotkeys", m.handleHotkeysGet)
	mux.HandleFunc("PUT /api/v1/settings/hotkeys", m.handleHotkeysPut)
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
	resp := map[string]any{"recordings": list}
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

// writeRecError переводит ошибку записи или воспроизведения в ответ API.
func (m *Module) writeRecError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, contracts.ErrRecordingNotFound):
		m.writeError(w, r, http.StatusNotFound, "api.recording_not_found", nil)
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
