package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
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
		if entry.Name != c.want {
			t.Errorf("%s %s: name = %q, want %q", c.dev, c.name, entry.Name, c.want)
		}
	}
}
