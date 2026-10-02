package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	"mkey/internal/lib/project"
)

// maxWait — наибольшее время ожидания клавиши в /wait/key.
const maxWait = 10 * time.Minute

// projectInfo — проект в списке /projects.
type projectInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Events  int    `json:"events"`
	Remaps  int    `json:"remaps"`
	Path    string `json:"path"`
	Error   string `json:"error,omitempty"`
}

// handleProjects возвращает список проектов и каталог проектов.
func (m *Module) handleProjects(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	list := m.svc.projects.List()
	out := make([]projectInfo, 0, len(list))
	for _, st := range list {
		out = append(out, projectInfo{
			ID: st.Project.ID, Name: st.Project.Name, Enabled: st.Project.IsEnabled(),
			Events: len(st.Project.Events), Remaps: len(st.Project.Remaps), Path: st.Path, Error: st.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": m.svc.projects.Dir(), "projects": out})
}

// handleProjectToggle включает или выключает проект: POST /projects/{id}/enable|disable. Перед
// включением проект проверяется целиком (ошибка — 400 api.project_invalid, проект не включается).
func (m *Module) handleProjectToggle(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	on, ok := enableFlag(r.PathValue("action"))
	if !ok {
		m.writeError(w, r, http.StatusNotFound, "api.bad_request", map[string]string{"error": "unknown action"})
		return
	}
	// Перед включением — полная проверка: проект с ошибкой не включается (иначе движок молча
	// отказался бы его запускать, а человек думал бы, что он работает).
	id := r.PathValue("id")
	if on && m.svc.events != nil {
		if st, ok := m.svc.projects.Get(id); ok {
			if err := m.svc.events.ValidateProject(st.Project); err != nil {
				m.writeProjectError(w, r, st.Project, err)
				return
			}
		}
	}
	if err := m.svc.projects.SetEnabled(id, on); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleImport импортирует проект: {name, content} → {id, scripts}; новый проект выключен,
// скрипты (lua, shell) возвращаются, чтобы показать их код до включения (SEC-7).
func (m *Module) handleImport(w http.ResponseWriter, r *http.Request) {
	if m.svc.projects == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	id, err := m.svc.projects.Import(req.Name, []byte(req.Content))
	if err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return
	}

	// Скрипты проекта — показать человеку до включения (SEC-7).
	scripts := []project.Script{}
	if p, err := project.Parse([]byte(req.Content), id); err == nil {
		scripts = append(scripts, project.Scripts(p)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "scripts": scripts})
}

// handleEvents возвращает состояние всех событий.
func (m *Module) handleEvents(w http.ResponseWriter, r *http.Request) {
	if m.svc.events == nil {
		m.unavailable(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": m.svc.events.Statuses()})
}

// handleEventAction — POST /events/{project}/{event}/run|enable|disable.
func (m *Module) handleEventAction(w http.ResponseWriter, r *http.Request) {
	proj, ev, action := r.PathValue("project"), r.PathValue("event"), r.PathValue("action")
	switch action {
	case "run":
		// Запуск и ожидание завершения; разрыв соединения прерывает событие.
		if m.svc.events == nil {
			m.unavailable(w, r)
			return
		}
		if err := m.svc.events.RunEvent(r.Context(), proj, ev); err != nil {
			m.writeRunError(w, r, err)
			return
		}
	case "enable", "disable":
		if m.svc.projects == nil {
			m.unavailable(w, r)
			return
		}
		if err := m.svc.projects.SetEventEnabled(proj, ev, action == "enable"); err != nil {
			m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
			return
		}
	default:
		m.writeError(w, r, http.StatusNotFound, "api.bad_request", map[string]string{"error": "unknown action"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleVars возвращает переменные активного проекта.
func (m *Module) handleVars(w http.ResponseWriter, r *http.Request) {
	vars := m.projectVars(w, r)
	if vars == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vars": vars.All()})
}

// handleSetVar задаёт переменную: PUT /vars/{project}/{name} {value}.
func (m *Module) handleSetVar(w http.ResponseWriter, r *http.Request) {
	vars := m.projectVars(w, r)
	if vars == nil {
		return
	}
	var req struct {
		Value any `json:"value"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	if err := vars.Set(r.PathValue("name"), req.Value); err != nil {
		m.writeError(w, r, http.StatusBadRequest, "api.bad_request", map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// projectVars находит переменные проекта из пути; при ошибке отвечает сам и возвращает nil.
func (m *Module) projectVars(w http.ResponseWriter, r *http.Request) contracts.VarStore {
	if m.svc.events == nil {
		m.unavailable(w, r)
		return nil
	}
	vars := m.svc.events.Vars(r.PathValue("project"))
	if vars == nil {
		m.writeError(w, r, http.StatusNotFound, "api.project_inactive", map[string]string{"project": r.PathValue("project")})
		return nil
	}
	return vars
}

// handleWaitKey ждёт нажатия клавиши: {key, timeout_ms}. 200 — нажата, 408 — время вышло.
func (m *Module) handleWaitKey(w http.ResponseWriter, r *http.Request) {
	if m.svc.keyState == nil {
		m.unavailable(w, r)
		return
	}
	var req struct {
		Key       string `json:"key"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if !m.readJSON(w, r, &req) {
		return
	}
	// Клавиша (и кнопки устройств с авто-ID: {UnKey001}).
	k, err := m.svc.keyState.ParseKey(req.Key)
	var de *dsl.Error
	if errors.As(err, &de) {
		m.writeError(w, r, http.StatusBadRequest, de.Code, de.Args)
		return
	}
	if err != nil {
		m.writeError(w, r, http.StatusBadRequest, "dsl.unknown_key", map[string]string{"name": req.Key})
		return
	}

	// Ожидание с ограничением времени; разрыв соединения тоже его прерывает.
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > maxWait {
		timeout = maxWait
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if err := m.svc.keyState.WaitKey(ctx, k); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			m.writeError(w, r, http.StatusRequestTimeout, "api.wait_timeout", nil)
			return
		}
		m.writeRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleResume снова разрешает перехват клавиатуры после экстренной остановки.
func (m *Module) handleResume(w http.ResponseWriter, r *http.Request) {
	if m.svc.keyState == nil {
		m.unavailable(w, r)
		return
	}
	m.svc.keyState.Resume()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// enableFlag переводит "enable"/"disable" в логическое значение.
func enableFlag(action string) (bool, bool) {
	switch action {
	case "enable":
		return true, true
	case "disable":
		return false, true
	}
	return false, false
}
