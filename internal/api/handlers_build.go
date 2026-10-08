package api

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Самостоятельный файл макроса (FR-BUILD-1, ADR-0042): сборка из окна и командой `mkey build`,
// скачивание собранного файла окном.

// buildErrors — ошибки сборки и их коды для окна и терминала (тексты — в i18n).
var buildErrors = []struct {
	err  error
	code string
}{
	{contracts.ErrBuildMissing, "api.build_missing"},
	{contracts.ErrBuildPlugin, "api.build_plugin"},
	{contracts.ErrBuildEvent, "api.build_event"},
	{contracts.ErrBuildProject, "api.build_project"},
}

// handleBuild собирает файл макроса из проекта {id}: {mode, event, output} → BuildResult
// (+ download — адрес скачивания, если файл в папке собранных). Ошибки — 400 с кодом из buildErrors
// и подробностью в detail.
func (m *Module) handleBuild(w http.ResponseWriter, r *http.Request) {
	if m.svc.builder == nil {
		m.unavailable(w, r)
		return
	}
	var req contracts.BuildRequest
	if !m.readJSON(w, r, &req) {
		return
	}
	req.Project = r.PathValue("id")

	// Путь назначения — только полный (относительный неясно от чего считать).
	if req.Output != "" && !filepath.IsAbs(req.Output) {
		m.writeError(w, r, http.StatusBadRequest, "api.build_output", map[string]string{"path": req.Output})
		return
	}

	// Сборка; ошибка — понятный код.
	res, err := m.svc.builder.Build(r.Context(), req)
	if err != nil {
		code := "api.bad_request"
		for _, e := range buildErrors {
			if errors.Is(err, e.err) {
				code = e.code
				break
			}
		}
		m.writeError(w, r, http.StatusBadRequest, code, map[string]string{"detail": err.Error()})
		return
	}
	out := map[string]any{"path": res.Path, "size": res.Size, "files": res.Files}
	if filepath.Dir(res.Path) == m.placePath(contracts.PlaceBuilds) {
		out["download"] = "/api/v1/builds/" + filepath.Base(res.Path)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBuildFile отдаёт собранный файл из папки собранных макросов (только оттуда и только по
// имени — без «/» и «..»), чтобы окно могло его скачать.
func (m *Module) handleBuildFile(w http.ResponseWriter, r *http.Request) {
	dir := m.placePath(contracts.PlaceBuilds)
	name := r.PathValue("name")
	if dir == "" || name == "" || name != filepath.Base(name) || name[0] == '.' {
		m.writeError(w, r, http.StatusNotFound, "api.not_found", nil)
		return
	}
	path := filepath.Join(dir, name)
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		m.writeError(w, r, http.StatusNotFound, "api.not_found", nil)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeFile(w, r, path)
}
