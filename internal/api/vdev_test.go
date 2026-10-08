package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// fakeVdevs — виртуальные устройства проектов: pad2 (кнопка South, ось LX) и ошибка у pad3.
type fakeVdevs struct{ contracts.VirtualDeviceManager }

func (fakeVdevs) Resolve(device, control string) (uint16, bool, error) {
	switch {
	case !strings.EqualFold(device, "pad2"):
		return 0, false, contracts.ErrUnknownVirtual
	case control == "South":
		return ev.BtnSouth, false, nil
	case control == "LX":
		return ev.AbsX, true, nil
	}
	return 0, false, contracts.ErrUnknownControl
}

func (fakeVdevs) List() []contracts.VirtualDeviceInfo {
	return []contracts.VirtualDeviceInfo{
		{Name: "pad2", Template: "xbox360", Project: "games", SystemName: "mKey pad2", Node: "/dev/input/event30"},
		{Name: "pad3", Template: "ds4", Project: "other", SystemName: "mKey pad3", Error: "no access"},
	}
}

func (fakeVdevs) Templates() []string { return []string{"xbox360", "ds4"} }

// State знает только pad2: нажата South, левый стик вправо наполовину.
func (fakeVdevs) State(name string) (contracts.VirtualState, error) {
	if !strings.EqualFold(name, "pad2") {
		return contracts.VirtualState{}, contracts.ErrUnknownVirtual
	}
	return contracts.VirtualState{Buttons: []string{"South"}, Axes: map[string]float64{"LX": 0.5}}, nil
}

// TemplateInfo знает состав только xbox360.
func (fakeVdevs) TemplateInfo(id string) (contracts.VirtualTemplateInfo, bool) {
	if id != "xbox360" {
		return contracts.VirtualTemplateInfo{}, false
	}
	return contracts.VirtualTemplateInfo{ID: id, Buttons: []string{"South"}, Axes: []string{"LX"}}, true
}

// TestVirtualDevicesAPI проверяет список виртуальных устройств и сухой прогон с ними.
func TestVirtualDevicesAPI(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.svc.vdevs = fakeVdevs{}
	h := m.routes(true)

	// Список и шаблоны.
	code, out := call(t, h, "GET", "/api/v1/devices/virtual", "", nil)
	devs, _ := out["devices"].([]any)
	if code != 200 || len(devs) != 2 || len(out["templates"].([]any)) != 2 || devs[1].(map[string]any)["error"] != "no access" {
		t.Fatalf("list: %d %v", code, out)
	}
	if info, _ := out["template_info"].([]any); len(info) != 1 || info[0].(map[string]any)["id"] != "xbox360" {
		t.Fatalf("template_info: %v", out["template_info"])
	}

	// Все устройства всех проектов с состоянием: подключено (и сколько привязок, прячет ли),
	// ошибка, выключено (проект выключен); соседние события проекта посчитаны.
	m.svc.projects = &memProjects{files: map[string]string{
		"games": "version: 1\nname: Игры\nevents: [{id: e, trigger: {type: hotkey, keys: '{F8}'}, actions: [{send: '{A}'}]}]\n" +
			"virtual_devices: [{name: pad2, template: xbox360}]\n" +
			"bindings: [{from: '{W}', to: '{pad2.DPadUp}', hide: true}, {from: '{S}', to: '{PAD2.DPadDown}'}, {from: '{Q}', to: '{Space}'}]\n",
		"other": "version: 1\nname: Другое\nevents: []\nvirtual_devices: [{name: pad3, template: ds4}]\n",
		"race":  "version: 1\nname: Гонки\nenabled: false\nevents: []\nvirtual_devices: [{name: wheel, template: wheel}]\n",
	}}
	_, out = call(t, h, "GET", "/api/v1/devices/virtual", "", nil)
	all, _ := out["all"].([]any)
	if len(all) != 3 {
		t.Fatalf("all: %v", out["all"])
	}
	card := func(i int) map[string]any { return all[i].(map[string]any) }
	if c := card(0); c["name"] != "pad2" || c["state"] != "on" || c["node"] != "/dev/input/event30" || c["bindings"] != 2.0 || c["hides"] != true || c["others"] != 1.0 || c["project_name"] != "Игры" {
		t.Errorf("pad2 = %v", c)
	}
	if c := card(1); c["name"] != "pad3" || c["state"] != "error" || c["error"] != "no access" {
		t.Errorf("pad3 = %v", c)
	}
	if c := card(2); c["name"] != "wheel" || c["state"] != "off" || c["system_name"] != "mKey wheel" || c["others"] != 0.0 {
		t.Errorf("wheel = %v", c)
	}

	// Состояние для проверки вживую; не подключено — 404 с понятным кодом.
	if code, out := call(t, h, "GET", "/api/v1/devices/virtual/pad2/state", "", nil); code != 200 || out["axes"].(map[string]any)["LX"] != 0.5 {
		t.Errorf("state: %d %v", code, out)
	}
	if code, out := call(t, h, "GET", "/api/v1/devices/virtual/pad9/state", "", nil); code != 404 || out["error"].(map[string]any)["code"] != "api.virtual_off" {
		t.Errorf("state off: %d %v", code, out)
	}

	// Сухой прогон: кнопка и ось виртуального устройства; неизвестная кнопка — ошибка.
	if code, out := call(t, h, "POST", "/api/v1/send", `{"sequence":"{pad2.South}{pad2.LX=0.5}","dry_run":true}`, nil); code != 200 || out["ok"] != true {
		t.Errorf("dry run: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/send", `{"sequence":"{pad2.Nope}","dry_run":true}`, nil); code != 400 || out["error"].(map[string]any)["code"] != "dsl.unknown_button" {
		t.Errorf("unknown: %d %v", code, out)
	}
}

// fakeEventsValidate — проверка проектов: проект «bad» с ошибкой.
type fakeEventsValidate struct{ contracts.Events }

func (fakeEventsValidate) ValidateProject(p project.Project) error {
	if p.ID == "bad" {
		return &project.Problem{Event: "e", Part: project.PartTrigger, Index: 0, Err: errors.New("broken trigger")}
	}
	return nil
}

// TestEnableValidates проверяет, что проект с ошибкой не включается, а человек видит, что не так.
func TestEnableValidates(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.svc.projects = &memProjects{files: map[string]string{
		"bad":  "version: 1\nname: Bad\nenabled: false\nevents: [{id: e, trigger: {type: manual}}]\n",
		"good": "version: 1\nname: Good\nenabled: false\nevents: [{id: e, trigger: {type: manual}}]\n",
	}}
	m.svc.events = fakeEventsValidate{}
	h := m.routes(true)

	// Проект с ошибкой: 400 с понятным текстом, не включён; выключить можно всегда.
	code, out := call(t, h, "POST", "/api/v1/projects/bad/enable", "", nil)
	if e, _ := out["error"].(map[string]any); code != 400 || e["code"] != "api.project_invalid" || !strings.Contains(e["message"].(string), "broken trigger") {
		t.Errorf("bad: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/v1/projects/bad/disable", "", nil); code != 200 {
		t.Errorf("disable bad: %d", code)
	}
	if code, _ := call(t, h, "POST", "/api/v1/projects/good/enable", "", nil); code != 200 {
		t.Errorf("good: %d", code)
	}
}
