package api

import (
	"strings"
	"testing"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// fakeInspector — инспектор с двумя устройствами; поиск — по имени файла или части названия.
type fakeInspector struct{ devs []contracts.DeviceDetails }

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
	m.svc.inspect = fakeInspector{devs: []contracts.DeviceDetails{dev("/dev/input/event6", "USB Mouse"), pad}}
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
