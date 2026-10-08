package api

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/registry"
)

// fakeBuilder — сборка: проект «bad» — нет записи, остальные — файл в папке dir.
type fakeBuilder struct{ dir string }

func (b fakeBuilder) Build(_ context.Context, req contracts.BuildRequest) (contracts.BuildResult, error) {
	if req.Project == "bad" {
		return contracts.BuildResult{}, fmt.Errorf("%w: бег.mkrec", contracts.ErrBuildMissing)
	}
	path := req.Output
	if path == "" {
		path = filepath.Join(b.dir, "Игра")
	}
	if err := os.WriteFile(path, []byte("MACRO"), 0o755); err != nil {
		return contracts.BuildResult{}, err
	}
	return contracts.BuildResult{Path: path, Size: 5, Files: []string{"recordings/бег.mkrec"}}, nil
}

// TestBuildAPI: сборка в папку собранных с адресом скачивания, скачивание только оттуда и по имени,
// ошибки сборки и пути — понятные коды.
func TestBuildAPI(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	dir := t.TempDir()
	ext := registry.NewExtensions()
	_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{M: contracts.ExtensionMeta{ID: contracts.PlaceBuilds}, P: dir, Dir: true})
	m.svc.ext, m.svc.builder = ext, fakeBuilder{dir: dir}
	h := m.routes(true)

	// Сборка в папку по умолчанию.
	code, out := call(t, h, "POST", "/api/v1/projects/game/build", `{"mode":"events"}`, nil)
	if code != 200 || out["download"] != "/api/v1/builds/Игра" || out["size"] != 5.0 {
		t.Fatalf("build: %d %v", code, out)
	}

	// Скачивание собранного файла; чужие пути — нет.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/builds/%D0%98%D0%B3%D1%80%D0%B0", nil))
	if body, _ := io.ReadAll(rec.Body); rec.Code != 200 || string(body) != "MACRO" || rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download: %d %q", rec.Code, body)
	}
	for _, p := range []string{"/api/v1/builds/..%2Fx", "/api/v1/builds/.hidden", "/api/v1/builds/nope"} {
		if code, _ := call(t, h, "GET", p, "", nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}

	// По пути — без адреса скачивания; относительный путь и ошибка сборки — понятные коды.
	if code, out := call(t, h, "POST", "/api/v1/projects/game/build", `{"output":"`+filepath.Join(t.TempDir(), "m")+`"}`, nil); code != 200 || out["download"] != nil {
		t.Errorf("output: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/projects/game/build", `{"output":"rel/m"}`, nil); code != 400 || out["error"].(map[string]any)["code"] != "api.build_output" {
		t.Errorf("relative: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/projects/bad/build", `{}`, nil); code != 400 || out["error"].(map[string]any)["code"] != "api.build_missing" {
		t.Errorf("missing: %d %v", code, out)
	}
}
