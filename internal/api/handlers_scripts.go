package api

import (
	"errors"
	"io/fs"
	"net/http"
	"slices"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Раздел «Скрипты» окна (FR-UI-1.7): файлы скриптов языков, которые зарегистрировали модули
// (точка contracts.PointScriptLanguage — lua, shell). Сохранение не отказывает из-за ошибки
// синтаксиса: файл записывается, а ошибка возвращается для подсказки (человек может сохранить
// недописанный скрипт).

// registerScriptRoutes добавляет маршруты раздела «Скрипты».
func (m *Module) registerScriptRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/scripts", m.handleScripts)
	mux.HandleFunc("GET /api/v1/scripts/{lang}/{name}", m.handleScriptGet)
	mux.HandleFunc("PUT /api/v1/scripts/{lang}/{name}", m.handleScriptPut)
	mux.HandleFunc("DELETE /api/v1/scripts/{lang}/{name}", m.handleScriptDelete)
	mux.HandleFunc("POST /api/v1/scripts/{lang}/check", m.handleScriptCheck)
}

// scriptLang — язык скриптов для окна.
type scriptLang struct {
	// ID — "lua" или "shell"; Name — название на языке клиента; Highlight — подсветка редактора.
	ID        string `json:"id"`
	Name      string `json:"name"`
	Highlight string `json:"highlight"`
	// Dir — папка файлов; Ext — их расширение.
	Dir string `json:"dir"`
	Ext string `json:"ext"`
}

// languages возвращает зарегистрированные языки скриптов.
func (m *Module) languages() []contracts.ScriptLanguage {
	if m.svc.ext == nil {
		return nil
	}
	var out []contracts.ScriptLanguage
	for _, e := range m.svc.ext.List(contracts.PointScriptLanguage) {
		if l, ok := e.(contracts.ScriptLanguage); ok {
			out = append(out, l)
		}
	}
	return out
}

// language находит язык по ID из адреса; нет — ответ 404 и nil.
func (m *Module) language(w http.ResponseWriter, r *http.Request) contracts.ScriptLanguage {
	id := r.PathValue("lang")
	i := slices.IndexFunc(m.languages(), func(l contracts.ScriptLanguage) bool { return l.Meta().ID == id })
	if i < 0 {
		m.writeError(w, r, http.StatusNotFound, "api.script_lang_unknown", map[string]string{"lang": id})
		return nil
	}
	return m.languages()[i]
}

// writeScriptError отвечает на ошибку работы с файлом: недопустимое имя — 400, нет файла — 404.
func (m *Module) writeScriptError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, contracts.ErrScriptName):
		m.writeError(w, r, http.StatusBadRequest, "api.script_name_bad", map[string]string{"name": r.PathValue("name")})
	case errors.Is(err, fs.ErrNotExist):
		m.writeError(w, r, http.StatusNotFound, "api.script_not_found", map[string]string{"name": r.PathValue("name")})
	default:
		m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
	}
}

// handleScripts — языки и файлы: {languages: [scriptLang], files: [ScriptFile]}.
func (m *Module) handleScripts(w http.ResponseWriter, r *http.Request) {
	tr := m.translator(r)
	langs := []scriptLang{}
	files := []contracts.ScriptFile{}
	for _, l := range m.languages() {
		meta := l.Meta()
		langs = append(langs, scriptLang{ID: meta.ID, Name: tr.T(meta.NameKey), Highlight: meta.Icon, Dir: l.Dir(), Ext: l.Ext()})
		list, err := l.Files()
		if err != nil {
			m.writeError(w, r, http.StatusInternalServerError, "api.internal", map[string]string{"error": err.Error()})
			return
		}
		files = append(files, list...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"languages": langs, "files": files})
}

// handleScriptGet — текст файла: {content}.
func (m *Module) handleScriptGet(w http.ResponseWriter, r *http.Request) {
	l := m.language(w, r)
	if l == nil {
		return
	}
	data, err := l.Read(r.PathValue("name"))
	if err != nil {
		m.writeScriptError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": string(data)})
}

// scriptBody — тело сохранения и проверки.
type scriptBody struct {
	Content string `json:"content"`
}

// handleScriptPut сохраняет файл {content}: {ok: true, problem?} — problem — ошибка синтаксиса
// (файл всё равно сохранён).
func (m *Module) handleScriptPut(w http.ResponseWriter, r *http.Request) {
	l := m.language(w, r)
	if l == nil {
		return
	}
	var body scriptBody
	if !m.readJSON(w, r, &body) {
		return
	}
	if err := l.Write(r.PathValue("name"), []byte(body.Content)); err != nil {
		m.writeScriptError(w, r, err)
		return
	}
	resp := map[string]any{"ok": true}
	if p := l.Check([]byte(body.Content)); p != nil {
		resp["problem"] = p
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleScriptDelete удаляет файл.
func (m *Module) handleScriptDelete(w http.ResponseWriter, r *http.Request) {
	l := m.language(w, r)
	if l == nil {
		return
	}
	if err := l.Delete(r.PathValue("name")); err != nil {
		m.writeScriptError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleScriptCheck проверяет синтаксис {content}, ничего не сохраняя и не выполняя: {problem?}.
func (m *Module) handleScriptCheck(w http.ResponseWriter, r *http.Request) {
	l := m.language(w, r)
	if l == nil {
		return
	}
	var body scriptBody
	if !m.readJSON(w, r, &body) {
		return
	}
	resp := map[string]any{}
	if p := l.Check([]byte(body.Content)); p != nil {
		resp["problem"] = p
	}
	writeJSON(w, http.StatusOK, resp)
}
