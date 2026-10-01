package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/buildinfo"
	"mkey/internal/lib/dsl"
)

// maxBody — наибольший размер тела запроса (1 МиБ).
const maxBody = 1 << 20

// routes собирает маршруты API. trusted — канал Unix-сокета (без статических файлов GUI).
func (m *Module) routes(trusted bool) http.Handler {
	mux := http.NewServeMux()

	// Состояние и сведения.
	mux.HandleFunc("GET /api/v1/status", m.handleStatus)
	mux.HandleFunc("GET /api/v1/devices", m.handleDevices)
	mux.HandleFunc("GET /api/v1/devices/inspect", m.handleDeviceInspect)
	mux.HandleFunc("GET /api/v1/doctor", m.handleDoctor)
	mux.HandleFunc("GET /api/v1/layouts", m.handleLayouts)

	// Макросы.
	mux.HandleFunc("POST /api/v1/send", m.handleSend)
	mux.HandleFunc("POST /api/v1/dsl/parse", m.handleParse)
	mux.HandleFunc("POST /api/v1/dsl/format", m.handleFormat)
	mux.HandleFunc("POST /api/v1/stop", m.handleStop)
	mux.HandleFunc("POST /api/v1/panic", m.handlePanic)
	mux.HandleFunc("POST /api/v1/emergency", m.handleEmergency)

	// Проекты, события, переменные (этап 3).
	mux.HandleFunc("GET /api/v1/projects", m.handleProjects)
	mux.HandleFunc("POST /api/v1/projects/import", m.handleImport)
	mux.HandleFunc("POST /api/v1/projects/{id}/{action}", m.handleProjectToggle)
	mux.HandleFunc("GET /api/v1/events", m.handleEvents)
	mux.HandleFunc("POST /api/v1/events/{project}/{event}/{action}", m.handleEventAction)
	mux.HandleFunc("GET /api/v1/vars/{project}", m.handleVars)
	mux.HandleFunc("PUT /api/v1/vars/{project}/{name}", m.handleSetVar)
	mux.HandleFunc("POST /api/v1/wait/key", m.handleWaitKey)
	mux.HandleFunc("POST /api/v1/resume", m.handleResume)
	mux.HandleFunc("GET /api/v1/registry", m.handleRegistry)

	// Веб-интерфейс (этап 5).
	m.registerGUIRoutes(mux)
	m.registerRecRoutes(mux)

	// Управление демоном.
	mux.HandleFunc("POST /api/v1/shutdown", m.handleShutdown)

	// Веб-интерфейс — только через TCP.
	if !trusted && m.static != nil {
		mux.Handle("GET /", http.FileServerFS(m.static))
	}
	return mux
}

// statusResponse — ответ /status.
type statusResponse struct {
	Version   string                   `json:"version"`
	Commit    string                   `json:"commit"`
	PID       int                      `json:"pid"`
	StartedAt *time.Time               `json:"started_at,omitempty"`
	Port      int                      `json:"port"`
	Running   int                      `json:"running"`
	Modules   []contracts.ModuleStatus `json:"modules,omitempty"`
	Input     *contracts.InputStatus   `json:"input,omitempty"`
	Output    *contracts.OutputStatus  `json:"output,omitempty"`
	// GrabSuspended — перехват отключён после экстренной остановки (включить: mkey resume).
	GrabSuspended bool `json:"grab_suspended"`
	// Playing — сколько идёт воспроизведений записей; Recording — идущая запись (если есть).
	Playing   int                      `json:"playing"`
	Recording *contracts.RecordingInfo `json:"recording,omitempty"`
}

// handleStatus возвращает состояние демона и модулей.
func (m *Module) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := statusResponse{Version: buildinfo.Version, Commit: buildinfo.Commit, PID: os.Getpid(), Port: m.port}

	// Каждое поле заполняется, только если соответствующий модуль работает.
	if m.svc.life != nil {
		t := m.svc.life.StartedAt()
		resp.StartedAt = &t
	}
	if m.svc.runner != nil {
		resp.Running = m.svc.runner.Running()
	}
	if m.svc.modules != nil {
		resp.Modules = m.svc.modules.ModuleStatuses()
	}
	if m.svc.input != nil {
		st := m.svc.input.Status()
		resp.Input = &st
	}
	if m.svc.devices != nil {
		st := m.svc.devices.Status()
		resp.Output = &st
	}
	if m.svc.keyState != nil {
		resp.GrabSuspended = m.svc.keyState.Suspended()
	}
	if m.svc.player != nil {
		resp.Playing = m.svc.player.Playing()
	}
	if m.svc.recorder != nil {
		if info, ok := m.svc.recorder.Recording(); ok {
			resp.Recording = &info
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDevices возвращает физические устройства ввода.
func (m *Module) handleDevices(w http.ResponseWriter, r *http.Request) {
	if m.svc.input == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": m.svc.input.Devices(), "status": m.svc.input.Status()})
}

// handleDeviceInspect возвращает подробности устройства (?ref= путь, event6, постоянное имя
// или часть названия, FR-DEV-1): {device}. Не нашлось — 404 api.device_not_found;
// подходит несколько — 409 api.device_ambiguous со списком {path, name} в details.candidates.
func (m *Module) handleDeviceInspect(w http.ResponseWriter, r *http.Request) {
	if m.svc.inspect == nil {
		m.unavailable(w, r)
		return
	}
	ref := r.URL.Query().Get("ref")
	found := m.svc.inspect.Find(ref)
	switch len(found) {
	case 0:
		m.writeError(w, r, http.StatusNotFound, "api.device_not_found", map[string]string{"ref": ref})
	case 1:
		writeJSON(w, http.StatusOK, map[string]any{"device": found[0]})
	default:
		// Несколько: перечисляем, чтобы человек выбрал точнее.
		type candidate struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		tr := m.translator(r)
		list := make([]candidate, 0, len(found))
		lines := make([]string, 0, len(found))
		for _, d := range found {
			list = append(list, candidate{Path: d.Info.Path, Name: d.Info.Name})
			kinds := make([]string, 0, len(d.Kinds))
			for _, k := range d.Kinds {
				kinds = append(kinds, tr.T("device.kind."+string(k)))
			}
			line := fmt.Sprintf("  %-8s %s", filepath.Base(d.Info.Path), d.Info.Name)
			if len(kinds) > 0 {
				line += " (" + strings.Join(kinds, ", ") + ")"
			}
			lines = append(lines, line)
		}
		m.writeErrorDetails(w, r, http.StatusConflict, "api.device_ambiguous",
			map[string]string{"ref": ref, "list": strings.Join(lines, "\n")}, map[string]any{"candidates": list})
	}
}

// doctorCheck — проверка с уже переведённым сообщением.
type doctorCheck struct {
	contracts.Check
	Message string `json:"message"`
}

// handleDoctor выполняет диагностику и возвращает проверки с сообщениями на языке клиента.
func (m *Module) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if m.svc.doctor == nil {
		m.unavailable(w, r)
		return
	}
	tr := m.translator(r)
	checks := m.svc.doctor.Run(r.Context())
	out := make([]doctorCheck, len(checks))
	for i, c := range checks {
		out[i] = doctorCheck{Check: c, Message: tr.T(c.MessageKey, checkArgs(tr, c)...)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": out})
}

// handleLayouts возвращает раскладки клавиатуры пользователя.
func (m *Module) handleLayouts(w http.ResponseWriter, r *http.Request) {
	if m.svc.layouts == nil {
		m.unavailable(w, r)
		return
	}
	info, err := m.svc.layouts.Layouts(r.Context())
	if err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// sendRequest — тело /send.
type sendRequest struct {
	// Sequence — текст макроса.
	Sequence string `json:"sequence"`
	// DryRun — только проверить макрос, ничего не нажимая.
	DryRun bool `json:"dry_run"`
}

// handleSend выполняет макрос и отвечает после его завершения. Разрыв соединения
// (например, Ctrl+C в `mkey send`) прерывает макрос.
func (m *Module) handleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if !m.readJSON(w, r, &req) {
		return
	}

	// Разбор и проверка зажатий (^ дважды, ~ без ^) — до выполнения.
	nodes, err := dsl.Parse(req.Sequence)
	if err == nil {
		err = dsl.CheckHolds(nodes)
	}
	if err != nil {
		m.writeRunError(w, r, err)
		return
	}

	// Сухой прогон: компиляция без выполнения.
	if req.DryRun {
		steps, err := dsl.Compile(nodes, dsl.DefaultResolver{})
		if err != nil {
			m.writeRunError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "steps": steps})
		return
	}

	// Выполнение.
	if m.svc.runner == nil {
		m.unavailable(w, r)
		return
	}
	start := time.Now()
	if err := m.svc.runner.Run(r.Context(), req.Sequence); err != nil {
		m.writeRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duration_ms": time.Since(start).Milliseconds()})
}

// textRequest — тело /dsl/parse и /dsl/format.
type textRequest struct {
	Text string `json:"text"`
}

// handleParse возвращает дерево разбора макроса (для конструктора блоков GUI).
func (m *Module) handleParse(w http.ResponseWriter, r *http.Request) {
	var req textRequest
	if !m.readJSON(w, r, &req) {
		return
	}
	nodes, err := dsl.Parse(req.Text)
	if err == nil {
		err = dsl.CheckHolds(nodes)
	}
	if err != nil {
		m.writeRunError(w, r, err)
		return
	}
	if nodes == nil {
		nodes = []dsl.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// handleFormat возвращает макрос в каноническом виде.
func (m *Module) handleFormat(w http.ResponseWriter, r *http.Request) {
	var req textRequest
	if !m.readJSON(w, r, &req) {
		return
	}
	nodes, err := dsl.Parse(req.Text)
	if err != nil {
		m.writeRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": dsl.Format(nodes)})
}

// handleStop прерывает все выполняющиеся макросы.
func (m *Module) handleStop(w http.ResponseWriter, r *http.Request) {
	if m.svc.runner == nil {
		m.unavailable(w, r)
		return
	}
	n := m.svc.runner.Running()
	m.svc.runner.StopAll()
	if m.svc.player != nil {
		n += m.svc.player.StopPlayback()
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": n})
}

// handlePanic — экстренная остановка: прервать все макросы и отпустить все клавиши на всех устройствах.
func (m *Module) handlePanic(w http.ResponseWriter, r *http.Request) {
	if m.svc.runner != nil {
		m.svc.runner.StopAll()
	}
	if m.svc.player != nil {
		m.svc.player.StopPlayback()
	}
	if m.svc.devices != nil {
		if err := m.svc.devices.ReleaseAll(); err != nil {
			m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleEmergency выполняет экстренную остановку, как сочетание Esc+Backspace+Enter (SEC-1):
// mKey приостанавливается до POST /resume.
func (m *Module) handleEmergency(w http.ResponseWriter, r *http.Request) {
	if m.svc.input == nil {
		m.unavailable(w, r)
		return
	}
	m.svc.input.EmergencyStop("api")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleShutdown просит демон завершиться (ответ отправляется до остановки).
func (m *Module) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if m.svc.life == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	go m.svc.life.Shutdown()
}

// readJSON читает тело запроса в v; при ошибке отвечает 400 и возвращает false.
func (m *Module) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return false
	}
	return true
}

// writeRunError переводит ошибку макроса в ответ: ошибка в тексте — 400, остановка — 409,
// нет виртуального ввода — 503, прочее — 500.
func (m *Module) writeRunError(w http.ResponseWriter, r *http.Request, err error) {
	var de *dsl.Error
	switch {
	case errors.As(err, &de):
		m.writeErrorDetails(w, r, http.StatusBadRequest, de.Code, de.Args, map[string]any{"pos": de.Pos, "args": de.Args})
	case errors.Is(err, context.Canceled):
		m.writeError(w, r, http.StatusConflict, "api.stopped", nil)
	case errors.Is(err, contracts.ErrEventInactive):
		m.writeError(w, r, http.StatusNotFound, "api.event_inactive", nil)
	case errors.Is(err, contracts.ErrOutputUnavailable):
		m.writeError(w, r, http.StatusServiceUnavailable, "api.output_unavailable", map[string]string{"error": err.Error()})
	default:
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
	}
}

// unavailable отвечает 503: нужный модуль отключён или не запущен.
func (m *Module) unavailable(w http.ResponseWriter, r *http.Request) {
	m.writeError(w, r, http.StatusServiceUnavailable, "api.unavailable", nil)
}

// errorBody — единый формат ошибки API.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

// writeError отвечает ошибкой с переведённым сообщением.
func (m *Module) writeError(w http.ResponseWriter, r *http.Request, status int, code string, args map[string]string) {
	m.writeErrorDetails(w, r, status, code, args, nil)
}

// writeErrorDetails отвечает ошибкой с переведённым сообщением и подробностями.
func (m *Module) writeErrorDetails(w http.ResponseWriter, r *http.Request, status int, code string, args map[string]string, details any) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = m.translator(r).T(code, toArgs(args)...)
	body.Error.Details = details
	writeJSON(w, status, body)
}

// translator возвращает переводчик на язык клиента из Accept-Language (первый язык списка).
func (m *Module) translator(r *http.Request) contracts.Translator {
	lang := r.Header.Get("Accept-Language")
	if i := strings.IndexAny(lang, ",;-_"); i >= 0 {
		lang = lang[:i]
	}
	return m.tr.WithLang(strings.ToLower(strings.TrimSpace(lang)))
}

// toArgs превращает карту параметров в параметры перевода.
func toArgs(args map[string]string) []contracts.Arg {
	out := make([]contracts.Arg, 0, len(args))
	for k, v := range args {
		out = append(out, contracts.Arg{Name: k, Value: v})
	}
	return out
}

// checkArgs собирает параметры проверки диагностики, переводя параметры-ключи.
func checkArgs(tr contracts.Translator, c contracts.Check) []contracts.Arg {
	args := toArgs(c.Args)
	for k, key := range c.ArgKeys {
		args = append(args, contracts.Arg{Name: k, Value: tr.T(key)})
	}
	return args
}

// writeJSON отправляет ответ в JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
