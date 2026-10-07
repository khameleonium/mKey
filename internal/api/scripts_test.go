package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/scriptfiles"
	"github.com/khameleonium/mKey/internal/registry"
)

// fakeLang — язык скриптов для тестов: файлы *.lua в папке; «ошибка» — текст со словом BAD.
type fakeLang struct{ f scriptfiles.Folder }

func (l fakeLang) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "lua", NameKey: "script_lang.lua", Icon: "lua"}
}
func (l fakeLang) Dir() string { return l.f.Dir }
func (l fakeLang) Ext() string { return l.f.Ext }
func (l fakeLang) Files() ([]contracts.ScriptFile, error) {
	files, err := l.f.Files()
	out := make([]contracts.ScriptFile, len(files))
	for i, f := range files {
		out[i] = contracts.ScriptFile{Lang: "lua", Name: f.Name, Size: f.Size}
	}
	return out, err
}
func (l fakeLang) Read(name string) ([]byte, error) { return l.f.Read(name) }
func (l fakeLang) Write(name string, data []byte) error {
	return l.f.Write(name, data)
}
func (l fakeLang) Delete(name string) error { return l.f.Delete(name) }
func (l fakeLang) Check(code []byte) *contracts.ScriptProblem {
	if strings.Contains(string(code), "BAD") {
		return &contracts.ScriptProblem{Line: 2, Message: "bad"}
	}
	return nil
}

// TestScriptsEndpoints проверяет раздел «Скрипты» через API: языки и файлы, сохранение (с ошибкой
// синтаксиса — сохранено и подсказка), чтение, проверка, недопустимое имя, неизвестный язык,
// удаление.
func TestScriptsEndpoints(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	dir := filepath.Join(t.TempDir(), "scripts")
	ext := registry.NewExtensions()
	if err := ext.Register(contracts.PointScriptLanguage, fakeLang{f: scriptfiles.Folder{Dir: dir, Ext: ".lua"}}); err != nil {
		t.Fatal(err)
	}
	m.svc.ext = ext
	h := m.routes(true)

	// Сохранение верного и неверного скрипта: оба записаны, у неверного — подсказка.
	if code, out := call(t, h, "PUT", "/api/v1/scripts/lua/a.lua", `{"content":"x = 1"}`, nil); code != 200 || out["problem"] != nil {
		t.Fatalf("put: %d %v", code, out)
	}
	code, out := call(t, h, "PUT", "/api/v1/scripts/lua/b.lua", `{"content":"BAD"}`, nil)
	if code != 200 || out["problem"].(map[string]any)["line"] != 2.0 {
		t.Fatalf("put bad: %d %v", code, out)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "b.lua")); string(data) != "BAD" {
		t.Fatalf("not saved: %q", data)
	}

	// Список, чтение, проверка без сохранения.
	if _, out := call(t, h, "GET", "/api/v1/scripts", "", nil); len(out["files"].([]any)) != 2 ||
		out["languages"].([]any)[0].(map[string]any)["highlight"] != "lua" {
		t.Fatalf("list: %v", out)
	}
	if _, out := call(t, h, "GET", "/api/v1/scripts/lua/a.lua", "", nil); out["content"] != "x = 1" {
		t.Fatalf("get: %v", out)
	}
	if _, out := call(t, h, "POST", "/api/v1/scripts/lua/check", `{"content":"BAD"}`, nil); out["problem"] == nil {
		t.Fatalf("check: %v", out)
	}

	// Ошибки: имя с папкой, чужое расширение, нет файла, нет языка.
	for _, c := range []struct {
		method, path, body string
		code               int
		key                string
	}{
		{"PUT", "/api/v1/scripts/lua/x.sh", `{"content":""}`, 400, "api.script_name_bad"},
		{"GET", "/api/v1/scripts/lua/..%2Fup.lua", "", 400, "api.script_name_bad"},
		{"GET", "/api/v1/scripts/lua/none.lua", "", 404, "api.script_not_found"},
		{"GET", "/api/v1/scripts/python/a.py", "", 404, "api.script_lang_unknown"},
	} {
		code, out := call(t, h, c.method, c.path, c.body, nil)
		if code != c.code || out["error"].(map[string]any)["code"] != c.key {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}

	// Удаление.
	if code, _ := call(t, h, "DELETE", "/api/v1/scripts/lua/a.lua", "", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.lua")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}
