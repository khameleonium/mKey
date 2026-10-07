package api

import (
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/registry"
)

// TestPlaces проверяет /places: порядок по Order, перевод названий, наличие на диске,
// объединение мест с одинаковым путём и папку в ответе /recordings.
func TestPlaces(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	dir := t.TempDir()

	// Места: папка, которая есть, файл, которого нет, и две папки скриптов с одним путём;
	// регистрируются не по порядку.
	ext := registry.NewExtensions()
	_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceLog, NameKey: "place.log", Provider: "test"}, P: dir + "/nope.log", N: 90,
	})
	_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceRecordings, NameKey: "place.recordings", Provider: "test"}, P: dir, Dir: true, N: 30,
	})
	_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceLuaScripts, NameKey: "place.lua_scripts", Provider: "test"}, P: dir + "/scripts", Dir: true, N: 40,
	})
	_ = ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceShellScripts, NameKey: "place.shell_scripts", Provider: "test"}, P: dir + "/scripts", Dir: true, N: 41,
	})
	m.svc.ext = ext
	h := m.routes(true)

	// Порядок, названия на языке клиента, наличие.
	code, out := call(t, h, "GET", "/api/v1/places", "", map[string]string{"Accept-Language": "ru"})
	places, _ := out["places"].([]any)
	if code != 200 || len(places) != 3 {
		t.Fatalf("places: %d %v", code, out)
	}
	first, scripts, second := places[0].(map[string]any), places[1].(map[string]any), places[2].(map[string]any)
	if first["id"] != "recordings" || first["name"] != "Записи" || first["exists"] != true || first["is_dir"] != true {
		t.Errorf("first: %v", first)
	}
	// Скрипты Lua и bash в одной папке — одна строка с обоими названиями.
	if scripts["name"] != "Скрипты Lua, Скрипты bash" || len(scripts["ids"].([]any)) != 2 {
		t.Errorf("scripts: %v", scripts)
	}
	if second["id"] != "log" || second["exists"] != false || second["path"] != dir+"/nope.log" {
		t.Errorf("second: %v", second)
	}

	// Папка записей — и в ответе /recordings (если модуль записи есть).
	rec := &fakeRecorder{}
	m.svc.recorder, m.svc.player = rec, rec
	if _, out := call(t, m.routes(true), "GET", "/api/v1/recordings", "", nil); out["dir"] != dir {
		t.Errorf("recordings dir: %v", out["dir"])
	}
}

// TestShortPath проверяет замену домашней папки на «~».
func TestShortPath(t *testing.T) {
	t.Parallel()
	cases := []struct{ path, home, want string }{
		{"/home/u/.config/mkey", "/home/u", "~/.config/mkey"},
		{"/home/u", "/home/u", "~"},
		{"/home/user2/x", "/home/u", "/home/user2/x"},
		{"/etc/x", "", "/etc/x"},
		{"/x", "/", "/x"},
	}
	for _, c := range cases {
		if got := shortPath(c.path, c.home); got != c.want {
			t.Errorf("shortPath(%q, %q) = %q, want %q", c.path, c.home, got, c.want)
		}
	}
}
