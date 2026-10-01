package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// Эндпоинты для веб-интерфейса (этап 5): редактирование проектов, шаблоны, поток новостей,
// захват клавиши, исправление прав, удаление программы, журнал.

// streamTopics — темы шины, которые получает веб-интерфейс в потоке новостей.
var streamTopics = map[string]bool{
	contracts.TopicProjectsChanged:    true,
	contracts.TopicProjectError:       true,
	contracts.TopicEngineError:        true,
	contracts.TopicEventStarted:       true,
	contracts.TopicEventFinished:      true,
	contracts.TopicEmergency:          true,
	contracts.TopicResumed:            true,
	contracts.TopicInputDeviceAdded:   true,
	contracts.TopicInputDeviceRemoved: true,
	contracts.TopicInputAccessChanged: true,
	contracts.TopicRecordingStarted:   true,
	contracts.TopicRecordingStopped:   true,
	contracts.TopicAutoIDsChanged:     true,
}

// registerGUIRoutes добавляет маршруты веб-интерфейса.
func (m *Module) registerGUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}", m.handleProjectGet)
	mux.HandleFunc("PUT /api/v1/projects/{id}", m.handleProjectPut)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", m.handleProjectDelete)
	mux.HandleFunc("POST /api/v1/projects", m.handleProjectCreate)
	mux.HandleFunc("POST /api/v1/projects/validate", m.handleProjectValidate)
	mux.HandleFunc("GET /api/v1/templates", m.handleTemplates)
	mux.HandleFunc("GET /api/v1/stream", m.handleStream)
	mux.HandleFunc("POST /api/v1/capture/key", m.handleCaptureKey)
	mux.HandleFunc("GET /api/v1/input/watch", m.handleWatch)
	mux.HandleFunc("POST /api/v1/doctor/fix", m.handleDoctorFix)
	mux.HandleFunc("POST /api/v1/uninstall", m.handleUninstall)
	mux.HandleFunc("GET /api/v1/logs", m.handleLogs)
	mux.HandleFunc("GET /api/v1/places", m.handlePlaces)
	mux.HandleFunc("POST /api/v1/dsl/to_actions", m.handleDSLToActions)
	mux.HandleFunc("POST /api/v1/actions/to_dsl", m.handleActionsToDSL)
}

// handleDSLToActions разбирает макрос на отдельные действия (блоки конструктора): {text} → {actions}.
func (m *Module) handleDSLToActions(w http.ResponseWriter, r *http.Request) {
	if m.svc.convert == nil {
		m.unavailable(w, r)
		return
	}
	var req textRequest
	if !m.readJSON(w, r, &req) {
		return
	}
	actions, err := m.svc.convert.DSLToActions(req.Text)
	if err != nil {
		m.writeRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// handleActionsToDSL склеивает действия в один макрос: {actions} → {text}.
// Действия, которые нельзя записать макросом, дают ошибку 400.
func (m *Module) handleActionsToDSL(w http.ResponseWriter, r *http.Request) {
	if m.svc.convert == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Actions []project.Action `json:"actions"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	text, err := m.svc.convert.ActionsToDSL(req.Actions)
	if err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.not_macro", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}

// handleProjectGet возвращает проект: структуру (для конструктора) и текст файла (для редактора YAML).
func (m *Module) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	id := r.PathValue("id")
	st, ok := m.svc.projects.Get(id)
	if !ok {
		m.writeError(w, r, http.StatusNotFound, "api.project_not_found", map[string]string{"project": id})
		return
	}
	raw, _ := m.svc.projects.Raw(id)
	writeJSON(w, http.StatusOK, map[string]any{"project": st.Project, "raw": string(raw), "path": st.Path, "error": st.Error})
}

// projectBody — тело сохранения и проверки: структура проекта или текст YAML.
type projectBody struct {
	Project *project.Project `json:"project"`
	Raw     *string          `json:"raw"`
}

// decodeProject разбирает тело запроса в проект; текст YAML проверяется разбором.
func (m *Module) decodeProject(w http.ResponseWriter, r *http.Request, id string) (project.Project, *string, bool) {
	var body projectBody
	if !m.readJSON(w, r, &body) {
		return project.Project{}, nil, false
	}
	switch {
	case body.Raw != nil:
		p, err := project.Parse([]byte(*body.Raw), id)
		if err != nil {
			m.writeProjectError(w, r, p, err)
			return project.Project{}, nil, false
		}
		return p, body.Raw, true
	case body.Project != nil:
		p := *body.Project
		p.ID = id
		return p, nil, true
	}
	m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": "project or raw is required"})
	return project.Project{}, nil, false
}

// validateFull проверяет проект движком (если он есть) и отвечает ошибкой 400; true — проект верен.
func (m *Module) validateFull(w http.ResponseWriter, r *http.Request, p project.Project) bool {
	var err error
	if m.svc.events == nil {
		err = project.Check(p)
	} else {
		err = m.svc.events.ValidateProject(p)
	}
	if err != nil {
		m.writeProjectError(w, r, p, err)
		return false
	}
	return true
}

// handleProjectPut сохраняет проект после полной проверки: PUT /projects/{id} {project} или {raw}.
func (m *Module) handleProjectPut(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	id := r.PathValue("id")
	p, raw, ok := m.decodeProject(w, r, id)
	if !ok || !m.validateFull(w, r, p) {
		return
	}

	// Текст сохраняется как есть (с комментариями), структура — через YAML.
	var err error
	if raw != nil {
		err = m.svc.projects.SaveRaw(id, []byte(*raw))
	} else {
		err = m.svc.projects.Save(id, p)
	}
	if err != nil {
		m.writeProjectError(w, r, p, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleProjectValidate проверяет проект, не сохраняя: {ok: true} или ошибка 400 с текстом.
func (m *Module) handleProjectValidate(w http.ResponseWriter, r *http.Request) {
	p, _, ok := m.decodeProject(w, r, "draft")
	if !ok || !m.validateFull(w, r, p) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleProjectCreate создаёт выключенный проект: {id, template} (шаблон необязателен).
func (m *Module) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Template string `json:"template"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}

	// Содержимое из шаблона (если указан).
	var content []byte
	if req.Template != "" {
		for _, t := range m.svc.projects.Templates() {
			if t.ID == req.Template {
				content = []byte(t.Content)
			}
		}
		if content == nil {
			m.writeError(w, r, http.StatusNotFound, "api.bad_request", map[string]string{"error": "unknown template"})
			return
		}
	}
	id, err := m.svc.projects.Create(req.ID, content)
	if err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleProjectDelete удаляет проект.
func (m *Module) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	if err := m.svc.projects.Delete(r.PathValue("id")); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTemplates возвращает шаблоны проектов с названиями на языке клиента.
func (m *Module) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	tr := m.translator(r)
	type tpl struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	var out []tpl
	for _, t := range m.svc.projects.Templates() {
		out = append(out, tpl{ID: t.ID, Name: tr.T("template." + t.ID + ".name"), Description: tr.T("template." + t.ID + ".description"), Content: t.Content})
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// handleStream — поток новостей для веб-интерфейса (Server-Sent Events): события шины
// «проекты изменились», «событие началось/закончилось», ошибки, экстренная остановка и т.п.
func (m *Module) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || m.bus == nil {
		m.unavailable(w, r)
		return
	}

	// Заголовки потока.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": mkey stream\n\n")
	flusher.Flush()

	// Подписка на шину до разрыва соединения; раз в 15 с — комментарий, чтобы соединение не закрылось.
	ch, cancel := m.bus.Subscribe("*")
	defer cancel()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !streamTopics[e.Topic] {
				continue
			}
			data, err := json.Marshal(streamPayload(e.Payload))
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Topic, data)
			flusher.Flush()
		}
	}
}

// streamPayload сокращает данные событий для потока: для устройств — только путь и имя.
func streamPayload(p any) any {
	if d, ok := p.(contracts.InputDevice); ok {
		return map[string]string{"path": d.Info.Path, "name": d.Info.Name}
	}
	return p
}

// capturedKey — нажатая клавиша для «Нажмите клавишу…» (FR-UI-3, FR-DEV-2).
type capturedKey struct {
	// Name — имя клавиши mKey ("Mouse0", "F8"), для сочетания — запись зажатием "^{Ctrl}^{Alt}{H}";
	// у клавиши без имени — сырой код ("#30"), который тоже понимает DSL.
	Name string `json:"name"`
	// Code — код evdev (первой клавиши сочетания); Kernel — имя кода в ядре ("BTN_TRIGGER_HAPPY3").
	Code   uint16 `json:"code"`
	Kernel string `json:"kernel"`
	// Device и DeviceName — устройство, на котором нажата клавиша.
	Device     string `json:"device"`
	DeviceName string `json:"device_name"`
}

// handleCaptureKey ждёт нажатия клавиши или кнопки: {timeout_ms, combo} → capturedKey; 408 — время вышло.
// combo = true — сочетание: собираются все клавиши, зажатые до первого отпускания ("^{Ctrl}^{Alt}{H}").
func (m *Module) handleCaptureKey(w http.ResponseWriter, r *http.Request) {
	if m.svc.input == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		TimeoutMS int  `json:"timeout_ms"`
		Combo     bool `json:"combo"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > time.Minute {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Читаем нажатия (не повторы) на любом устройстве.
	events, unsub := m.svc.input.Subscribe(64)
	defer unsub()
	var first *capturedKey
	var names []string
	for {
		select {
		case <-ctx.Done():
			m.writeError(w, r, http.StatusRequestTimeout, "api.wait_timeout", nil)
			return
		case e, ok := <-events:
			if !ok {
				m.unavailable(w, r)
				return
			}
			if e.Event.Type != ev.EvKey {
				continue
			}

			// Отпускание: сочетание собрано (или это отпускание клавиши, зажатой до начала захвата).
			if e.Event.Value == ev.ValueUp {
				if first != nil {
					first.Name = hotkeyText(names)
					writeJSON(w, http.StatusOK, first)
					return
				}
				continue
			}
			if e.Event.Value != ev.ValueDown {
				continue
			}

			// Нажатие: имя клавиши; первое нажатие запоминаем вместе с устройством.
			name, ok := keys.NameOf(e.Event.Code)
			if !ok {
				name = "#" + strconv.Itoa(int(e.Event.Code))
			}
			if first == nil {
				first = &capturedKey{Code: e.Event.Code, Kernel: ev.CodeName(ev.EvKey, e.Event.Code), Device: e.Device}
				for _, d := range m.svc.input.Devices() {
					if d.Info.Path == e.Device {
						first.DeviceName = d.Info.Name
					}
				}
			}
			if req.Combo {
				name = anySide(name)
			}
			if !slices.Contains(names, name) {
				names = append(names, name)
			}

			// Одиночная клавиша готова сразу.
			if !req.Combo {
				first.Name = name
				writeJSON(w, http.StatusOK, first)
				return
			}
		}
	}
}

// hotkeyText записывает сочетание зажатием: все клавиши, кроме последней, зажаты, последняя нажата
// ("^{Ctrl}^{Alt}{H}"); одна клавиша — "{F8}".
func hotkeyText(names []string) string {
	var b strings.Builder
	for i, n := range names {
		if i < len(names)-1 {
			b.WriteString("^")
		}
		b.WriteString("{" + n + "}")
	}
	return b.String()
}

// anySide заменяет левый/правый модификатор на модификатор без стороны: в горячей клавише
// "Ctrl+H" срабатывает от любого Ctrl. Правый Alt (AltGr) остаётся отдельной клавишей.
func anySide(name string) string {
	switch name {
	case "LCtrl", "RCtrl":
		return "Ctrl"
	case "LShift", "RShift":
		return "Shift"
	case "LAlt":
		return "Alt"
	case "LSuper", "RSuper":
		return "Super"
	}
	return name
}

// handleDoctorFix выдаёт доступ к устройствам лучшим способом повышения прав (обычно окно pkexec).
// Ответ: {ok: true} или {manual_command: "..."} — если команду нужно выполнить вручную.
func (m *Module) handleDoctorFix(w http.ResponseWriter, r *http.Request) {
	if m.svc.doctor == nil || m.svc.platform == nil {
		m.unavailable(w, r)
		return
	}
	elevators := m.svc.platform.Elevators()
	if len(elevators) == 0 {
		m.unavailable(w, r)
		return
	}
	err := m.svc.doctor.Fix(r.Context(), contracts.FixDeviceAccess, elevators[0])
	var manual *contracts.ManualActionError
	switch {
	case errors.As(err, &manual):
		writeJSON(w, http.StatusOK, map[string]any{"manual_command": manual.Command})
	case err != nil:
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "method": m.translator(r).T(elevators[0].Meta().NameKey)})
	}
}

// handleUninstall запускает удаление mKey отдельным процессом (он же остановит этот демон): {keep_config}.
func (m *Module) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeepConfig bool `json:"keep_config"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	mode := "--all"
	if req.KeepConfig {
		mode = "--keep-config"
	}

	// Отдельная сессия: процесс удаления переживёт остановку демона.
	cmd := exec.Command(exe, "uninstall", "--yes", mode, "--lang", m.translator(r).Lang())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
		return
	}
	_ = cmd.Process.Release()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogs возвращает последние строки журнала демона: ?lines=N (по умолчанию 200, не больше 2000).
func (m *Module) handleLogs(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("lines"))
	if err != nil || n <= 0 {
		n = 200
	}
	n = min(n, 2000)

	// Читаем файл целиком построчно и оставляем последние n строк (файл ограничен ротацией, 5 МиБ).
	f, err := os.Open(m.cfg.LogFile)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []string{}})
		return
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}
