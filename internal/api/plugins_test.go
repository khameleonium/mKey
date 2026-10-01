package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
)

// fakePlugins — один плагин io.test.a; включение меняет список включённых.
type fakePlugins struct {
	contracts.Plugins
	active []string
}

func (f *fakePlugins) List() []contracts.PluginInfo {
	return []contracts.PluginInfo{{ID: "io.test.a", Kind: "process", State: contracts.PluginOff}}
}
func (f *fakePlugins) Dir() string      { return "/plugins" }
func (f *fakePlugins) Active() []string { return f.active }
func (f *fakePlugins) SetActive(id string, on bool) error {
	if id != "io.test.a" {
		return contracts.ErrPluginNotFound
	}
	f.active = nil
	if on {
		f.active = []string{id}
	}
	return nil
}
func (f *fakePlugins) Install(src string) (contracts.PluginInfo, error) {
	if src == "/exists" {
		return contracts.PluginInfo{}, contracts.ErrPluginExists
	}
	return contracts.PluginInfo{ID: "io.test.b"}, nil
}

// TestPluginsAPI проверяет список, включение с записью в config.yaml и ошибки.
func TestPluginsAPI(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	pl := &fakePlugins{}
	m.svc.plugins = pl
	h := m.routes(true)

	// Список и папка.
	if code, out := call(t, h, "GET", "/api/v1/plugins", "", nil); code != 200 || out["dir"] != "/plugins" {
		t.Fatalf("list: %d %v", code, out)
	}

	// Включение — список включённых в config.yaml; неизвестный — 404.
	if code, out := call(t, h, "POST", "/api/v1/plugins/io.test.a/enable", "{}", nil); code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	if !strings.Contains(string(data), "active: [io.test.a]") {
		t.Fatalf("config:\n%s", data)
	}
	if code, out := call(t, h, "POST", "/api/v1/plugins/nope/enable", "{}", nil); code != 404 || out["error"].(map[string]any)["code"] != "api.plugin_not_found" {
		t.Fatalf("unknown: %d %v", code, out)
	}

	// Установка: занятый ID — 409.
	if code, out := call(t, h, "POST", "/api/v1/plugins/install", `{"path":"/exists"}`, nil); code != 409 || out["error"].(map[string]any)["code"] != "api.plugin_exists" {
		t.Fatalf("exists: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/plugins/install", `{"path":"/new"}`, nil); code != 200 || out["id"] != "io.test.b" {
		t.Fatalf("install: %d %v", code, out)
	}
}
