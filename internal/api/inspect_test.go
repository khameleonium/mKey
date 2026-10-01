package api

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
	"mkey/internal/lib/devmap"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// fakeInspector — инспектор с заданными устройствами; поиск — по имени файла или части названия;
// режим авто-ID хранится в mode.
type fakeInspector struct {
	devs []contracts.DeviceDetails
	mode *string
}

// Label — авто-ID только у BTN_TRIGGER_HAPPY3 устройства event9.
func (f fakeInspector) Label(path string, _, code uint16) string {
	if path == "/dev/input/event9" && code == 0x2c2 {
		return "UnKey001"
	}
	return ""
}

// ResolveKey знает две кнопки: UnKey.001 (BTN_TRIGGER_HAPPY3) и UnKey.002 (KEY_CALC).
func (f fakeInspector) ResolveKey(device, button string) (contracts.DeviceKey, error) {
	if device == "UnKey" && button == "001" {
		return contracts.DeviceKey{Key: keys.Key{Name: "UnKey001", Type: ev.EvKey, Code: 0x2c2}, Device: "UnKey"}, nil
	}
	if device == "UnKey" && button == "002" {
		return contracts.DeviceKey{Key: keys.Key{Name: "UnKey002", Type: ev.EvKey, Code: ev.KeyCalc}, Device: "UnKey"}, nil
	}
	return contracts.DeviceKey{}, errors.New("unknown")
}

// Rename отказывает имени «Enter» (имя клавиши), остальные принимает.
func (f fakeInspector) Rename(_, _, name string) error {
	if name == "Enter" {
		return &devmap.NameError{Code: devmap.NameKey, Name: name}
	}
	return nil
}

// NamesFor — имена только у устройства «Геймпад» (он же UnKey).
func (f fakeInspector) NamesFor(devices []string) []devmap.Names {
	for _, d := range devices {
		if strings.EqualFold(d, "Геймпад") || strings.EqualFold(d, "UnKey") {
			return []devmap.Names{{Match: devmap.NamesMatch{Vid: "0079", Pid: "0011"}, Name: "Геймпад", Buttons: map[string]string{"BTN_TRIGGER": "Старт"}}}
		}
	}
	return nil
}

// DeviceOf — авто-ID только у event9.
func (f fakeInspector) DeviceOf(path string) string {
	if path == "/dev/input/event9" {
		return "UnKey"
	}
	return ""
}

// AutoIDMode возвращает режим.
func (f fakeInspector) AutoIDMode() string { return *f.mode }

// SetAutoIDMode меняет режим (известны smart, all, unusual).
func (f fakeInspector) SetAutoIDMode(mode string) error {
	if mode != "smart" && mode != "all" && mode != "unusual" {
		return errors.New("unknown auto-id mode")
	}
	*f.mode = mode
	return nil
}

// Devices возвращает все устройства.
func (f fakeInspector) Devices() []contracts.DeviceDetails { return f.devs }

// Find ищет, как настоящий инспектор (упрощённо).
func (f fakeInspector) Find(ref string) []contracts.DeviceDetails {
	var out []contracts.DeviceDetails
	for _, d := range f.devs {
		if strings.HasSuffix(d.Info.Path, "/"+ref) || strings.Contains(strings.ToLower(d.Info.Name), strings.ToLower(ref)) {
			out = append(out, d)
		}
	}
	return out
}

// TestDeviceInspect проверяет /devices/inspect: одно устройство, не найдено, несколько.
func TestDeviceInspect(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	dev := func(path, name string) contracts.DeviceDetails {
		return contracts.DeviceDetails{InputDevice: contracts.InputDevice{Info: ev.Info{Path: path, Name: name}},
			Keys: []contracts.DeviceControl{{Code: ev.BtnLeft, Kernel: "BTN_LEFT", Name: "Mouse0"}}}
	}
	pad := dev("/dev/input/event9", "USB Gamepad")
	pad.Kinds = []ev.Kind{ev.KindGamepad}
	mode := "smart"
	m.svc.inspect = fakeInspector{devs: []contracts.DeviceDetails{dev("/dev/input/event6", "USB Mouse"), pad}, mode: &mode}
	h := m.routes(true)
	ru := map[string]string{"Accept-Language": "ru"}

	// Одно устройство — подробности.
	code, out := call(t, h, "GET", "/api/v1/devices/inspect?ref=event6", "", ru)
	d, _ := out["device"].(map[string]any)
	if code != 200 || d == nil || d["info"].(map[string]any)["name"] != "USB Mouse" || len(d["keys"].([]any)) != 1 {
		t.Fatalf("one: %d %v", code, out)
	}

	// Не найдено — 404 с понятным текстом.
	code, out = call(t, h, "GET", "/api/v1/devices/inspect?ref=nope", "", ru)
	if e, _ := out["error"].(map[string]any); code != 404 || e["code"] != "api.device_not_found" || !strings.Contains(e["message"].(string), "«nope»") {
		t.Errorf("not found: %d %v", code, out)
	}

	// Несколько — 409 со списком в сообщении и в details.
	code, out = call(t, h, "GET", "/api/v1/devices/inspect?ref=usb", "", ru)
	e, _ := out["error"].(map[string]any)
	if msg, _ := e["message"].(string); code != 409 || !strings.Contains(msg, "event6   USB Mouse") || !strings.Contains(msg, "event9   USB Gamepad (геймпад)") || len(e["details"].(map[string]any)["candidates"].([]any)) != 2 {
		t.Errorf("ambiguous: %d %v", code, out)
	}

	// Без инспектора — модуль недоступен.
	m.svc.inspect = nil
	if code, _ := call(t, m.routes(true), "GET", "/api/v1/devices/inspect?ref=event6", "", nil); code != 503 {
		t.Errorf("unavailable: %d", code)
	}
}

// TestDeviceSettings проверяет режим авто-ID: чтение, смена с сохранением в config.yaml, ошибка.
func TestDeviceSettings(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	mode := "smart"
	m.svc.inspect = fakeInspector{mode: &mode}
	h := m.routes(true)

	// Чтение и смена режима.
	if _, out := call(t, h, "GET", "/api/v1/settings/devices", "", nil); out["auto_ids"] != "smart" {
		t.Fatalf("get: %v", out)
	}
	if code, out := call(t, h, "PUT", "/api/v1/settings/devices", `{"auto_ids":"all"}`, nil); code != 200 || out["auto_ids"] != "all" || mode != "all" {
		t.Fatalf("put: %d %v", code, out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	if !strings.Contains(string(data), `auto_ids: "all"`) {
		t.Errorf("config:\n%s", data)
	}

	// Неизвестный режим — 400, режим и файл не меняются.
	if code, _ := call(t, h, "PUT", "/api/v1/settings/devices", `{"auto_ids":"some"}`, nil); code != 400 || mode != "all" {
		t.Errorf("bad mode: %d %s", code, mode)
	}
}

// TestLabelEntry проверяет монитор нажатий: кнопка без имени — авто-ID, если он есть.
func TestLabelEntry(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	mode := "smart"
	m.svc.inspect = fakeInspector{mode: &mode}
	ie := func(dev string, code uint16) contracts.InputEvent {
		return contracts.InputEvent{Device: dev, Event: ev.Event{Type: ev.EvKey, Code: code, Value: 1}}
	}
	for _, c := range []struct {
		dev, name, want string
		code            uint16
	}{
		{"/dev/input/event9", "#706", "UnKey001", 0x2c2}, // авто-ID есть
		{"/dev/input/event6", "#706", "#706", 0x2c2},     // у этого устройства авто-ID нет
		{"/dev/input/event9", "A", "A", ev.KeyA},         // обычное имя не меняется
	} {
		entry := watchEntry{Name: c.name}
		m.labelEntry(&entry, ie(c.dev, c.code))
		if entry.Name != c.want || entry.Labeled != (c.want == "UnKey001") {
			t.Errorf("%s %s: name = %q, want %q", c.dev, c.name, entry.Name, c.want)
		}
	}
}

// TestDryRunDeviceKey проверяет сухой прогон с кнопками устройства с авто-ID: клавишу (KEY_CALC)
// можно нажать, кнопку джойстика — пока нет; без инспектора — «устройство не найдено».
func TestDryRunDeviceKey(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	mode := "smart"
	m.svc.inspect = fakeInspector{mode: &mode}
	code, out := call(t, m.routes(true), "POST", "/api/v1/send", `{"sequence":"{UnKey002}","dry_run":true}`, nil)
	if code != 200 || out["ok"] != true {
		t.Fatalf("dry run: %d %v", code, out)
	}
	code, out = call(t, m.routes(true), "POST", "/api/v1/send", `{"sequence":"{UnKey001}","dry_run":true}`, nil)
	if e, _ := out["error"].(map[string]any); code != 400 || e["code"] != "dsl.cannot_send" {
		t.Errorf("joystick button: %d %v", code, out)
	}
	m.svc.inspect = nil
	code, out = call(t, m.routes(true), "POST", "/api/v1/send", `{"sequence":"{UnKey001}","dry_run":true}`, nil)
	if e, _ := out["error"].(map[string]any); code != 400 || e["code"] != "dsl.unknown_device" {
		t.Errorf("no inspector: %d %v", code, out)
	}
}

// TestDeviceRename проверяет переименование через API: успех и понятная ошибка имени.
func TestDeviceRename(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	mode := "smart"
	m.svc.inspect = fakeInspector{mode: &mode}
	h := m.routes(true)
	if code, out := call(t, h, "POST", "/api/v1/devices/rename", `{"device":"UnKey","name":"Sega"}`, nil); code != 200 || out["ok"] != true {
		t.Fatalf("rename: %d %v", code, out)
	}
	code, out := call(t, h, "POST", "/api/v1/devices/rename", `{"device":"UnKey","name":"Enter"}`, map[string]string{"Accept-Language": "ru"})
	e, _ := out["error"].(map[string]any)
	if msg, _ := e["message"].(string); code != 400 || e["code"] != "api.rename_key" || !strings.Contains(msg, "«Enter» — это имя клавиши") {
		t.Errorf("bad name: %d %v", code, out)
	}
}

// TestProjectExport проверяет сохранение проекта в файл: имена кнопок устройств проекта дописываются
// в раздел devices (старые записи других моделей остаются, комментарии — тоже); проект без кнопок
// устройств — как есть; нет проекта — 404.
func TestProjectExport(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	mode := "smart"
	m.svc.inspect = fakeInspector{mode: &mode}
	m.svc.projects = &memProjects{files: map[string]string{
		"china": "# Мой геймпад\nversion: 1\nname: Китайский геймпад\nevents:\n  - id: a\n    trigger: {type: hotkey, keys: \"{Геймпад.Старт}\"}\n" +
			"devices:\n  - match: {vid: \"1234\", pid: \"5678\"}\n    name: Руль\n",
		"plain": "version: 1\nname: P\nevents: []\n",
	}}
	h := m.routes(true)
	get := func(id string) (int, string, string) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/projects/"+id+"/export", nil))
		return rr.Code, rr.Body.String(), rr.Header().Get("Content-Disposition")
	}

	// Имена дописаны, прежняя модель и комментарий остались, файл — с именем проекта.
	code, body, disp := get("china")
	if code != 200 || !strings.Contains(disp, "china.mkey.yaml") || !strings.Contains(body, "# Мой геймпад") ||
		!strings.Contains(body, "BTN_TRIGGER: Старт") || !strings.Contains(body, "name: Руль") {
		t.Fatalf("export: %d %s\n%s", code, disp, body)
	}
	p, err := project.Parse([]byte(body), "china")
	if err != nil || len(p.Devices) != 2 || project.Check(p) != nil {
		t.Errorf("exported file: %+v %v", p.Devices, err)
	}

	// Без кнопок устройств — как есть; нет проекта — 404.
	if code, body, _ := get("plain"); code != 200 || body != "version: 1\nname: P\nevents: []\n" {
		t.Errorf("plain: %d %q", code, body)
	}
	if code, _, _ := get("nope"); code != 404 {
		t.Errorf("missing: %d", code)
	}

	// Поиск упоминаний устройств в тексте.
	refs := deviceRefs(`keys: "{Геймпад.Старт}" send: "{UnKey2001}{unkey001}" lua: {file: script.lua}`)
	if strings.Join(refs, ",") != "Геймпад,script,UnKey2,UnKey" {
		t.Errorf("refs = %v", refs)
	}
}
