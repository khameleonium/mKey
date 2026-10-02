package api

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"

	"mkey/internal/contracts"
	"mkey/internal/lib/devmap"
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
	mux.HandleFunc("GET /api/v1/projects/{id}/export", m.handleProjectExport)
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
	mux.HandleFunc("POST /api/v1/projects/{id}/events/{event}/dry_run", m.handleDryRun)
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

// handleProjectExport отдаёт файл проекта для сохранения и обмена (ADR-0027): в раздел devices
// дописываются имена кнопок устройств, которые встречаются в проекте ({Геймпад.Старт}), — у того,
// кто загрузит проект, такое же устройство получит эти имена. Комментарии файла сохраняются.
func (m *Module) handleProjectExport(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	id := r.PathValue("id")
	_, ok := m.svc.projects.Get(id)
	raw, err := m.svc.projects.Raw(id)
	if !ok || err != nil {
		m.writeError(w, r, http.StatusNotFound, "api.project_not_found", map[string]string{"project": id})
		return
	}

	// Имена кнопок устройств проекта (если инспектор работает).
	if m.svc.inspect != nil {
		if names := m.svc.inspect.NamesFor(deviceRefs(string(raw))); len(names) > 0 {
			if out, err := withDeviceNames(raw, names); err == nil {
				raw = out
			} else {
				m.log.Warn("cannot add device names to the exported project", "project", id, "err", err)
			}
		}
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": id + project.FileSuffix}))
	_, _ = w.Write(raw)
}

// deviceRefRe и joinedRefRe — упоминания кнопок устройств в тексте проекта: «Устройство.Кнопка»
// ({Геймпад.Старт}, UnKey2.001) и слитная запись авто-ID (UnKey001, UnKey2001).
var (
	deviceRefRe = regexp.MustCompile(`([\p{L}\p{N}_]+)\.[\p{L}\p{N}_]+`)
	joinedRefRe = regexp.MustCompile(`(?i)\bunkey[0-9]+\b`)
)

// deviceRefs находит в тексте проекта возможные имена устройств; лишние (например, «script» из
// «script.lua») отсеет инспектор — у него нет таких устройств.
func deviceRefs(text string) []string {
	var out []string
	for _, m := range deviceRefRe.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	for _, s := range joinedRefRe.FindAllString(text, -1) {
		if dev, _, ok := devmap.SplitJoined(s); ok {
			out = append(out, dev)
		}
	}
	return out
}

// withDeviceNames записывает имена в раздел devices файла проекта: записи тех же моделей
// заменяются, остальные остаются; комментарии файла сохраняются.
func withDeviceNames(raw []byte, names []devmap.Names) ([]byte, error) {
	// Файл как дерево YAML.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("project is not a YAML map")
	}
	root := doc.Content[0]

	// Прежний раздел devices: модели, которых нет среди новых имён, сохраняются.
	var merged []devmap.Names
	key := func(n devmap.Names) string {
		return strings.ToLower(n.Match.Vid + ":" + n.Match.Pid + ":" + n.Match.Name)
	}
	fresh := map[string]bool{}
	for _, n := range names {
		fresh[key(n)] = true
	}
	idx := -1
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "devices" {
			idx = i
			var old []devmap.Names
			if err := root.Content[i+1].Decode(&old); err == nil {
				for _, n := range old {
					if !fresh[key(n)] {
						merged = append(merged, n)
					}
				}
			}
		}
	}
	merged = append(merged, names...)

	// Новый раздел на месте прежнего или в конце файла.
	var value yaml.Node
	if err := value.Encode(merged); err != nil {
		return nil, err
	}
	if idx >= 0 {
		root.Content[idx+1] = &value
	} else {
		k := &yaml.Node{Kind: yaml.ScalarNode, Value: "devices",
			HeadComment: "Имена кнопок устройств этого проекта: mKey подставит их такому же устройству."}
		root.Content = append(root.Content, k, &value)
	}

	// Запись с отступом в два пробела.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// handleProjectCreate создаёт выключенный проект: {id, template} (шаблон необязателен) →
// {id, scripts} — скрипты шаблона показываются до включения (SEC-7).
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

	// Скрипты шаблона (шаблоны плагинов пишут сторонние авторы) — показать до включения (SEC-7).
	scripts := []project.Script{}
	if p, err := project.Parse(content, id); err == nil && len(content) > 0 {
		scripts = append(scripts, project.Scripts(p)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "scripts": scripts})
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
		// Встроенный — по ключам перевода; из плагина — по текстам на языках.
		name, desc := tr.T("template."+t.ID+".name"), tr.T("template."+t.ID+".description")
		if len(t.Names) > 0 {
			name, desc = cmp.Or(pickLang(t.Names, tr.Lang()), t.ID), pickLang(t.Descriptions, tr.Lang())
		}
		out = append(out, tpl{ID: t.ID, Name: name, Description: desc, Content: t.Content})
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

// handleDryRun — сухой прогон события (FR-UI-6): POST /projects/{id}/events/{event}/dry_run
// {project} — проект в том виде, в каком он сейчас в окне (можно несохранённый). Отвечает
// таймлайном contracts.DryRun; проект с ошибкой — 400 с её описанием.
func (m *Module) handleDryRun(w http.ResponseWriter, r *http.Request) {
	if m.svc.dryRun == nil {
		m.unavailable(w, r)
		return
	}

	// Проект из окна: полная проверка, как при сохранении.
	p, _, ok := m.decodeProject(w, r, r.PathValue("id"))
	if !ok || !m.validateFull(w, r, p) {
		return
	}

	// Событие и его прогон.
	i := slices.IndexFunc(p.Events, func(e project.Event) bool { return e.ID == r.PathValue("event") })
	if i < 0 {
		m.writeError(w, r, http.StatusNotFound, "api.bad_request", map[string]string{"error": "unknown event " + r.PathValue("event")})
		return
	}
	res, err := m.svc.dryRun.DryRun(r.Context(), p.ID, p.Events[i])
	if err != nil {
		m.writeRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
