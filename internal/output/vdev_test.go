package output

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/clock"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// vdevModule — модуль с фейковой фабрикой (запоминает описания и приёмники) и проектами.
type vdevModule struct {
	*Module
	mu      sync.Mutex
	setups  map[string]ev.Setup
	writers map[string]*fakeWriter
}

// newVdevModule создаёт модуль с фейковой фабрикой и нулевым прогревом.
func newVdevModule(projects contracts.Projects) *vdevModule {
	v := &vdevModule{setups: map[string]ev.Setup{}, writers: map[string]*fakeWriter{}}
	v.Module = newModule(func(s ev.Setup) (eventWriter, error) {
		v.mu.Lock()
		defer v.mu.Unlock()
		w := &fakeWriter{}
		v.setups[s.Name], v.writers[s.Name] = s, w
		return w, nil
	}, clock.NewFake(time.Unix(0, 0)))
	v.cfg.SettleMS = 0
	v.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	v.projects = projects
	return v
}

// pad создаёт устройство по шаблону напрямую (без проектов).
func (v *vdevModule) pad(t *testing.T, name, template string) (*vdevice, *fakeWriter) {
	t.Helper()
	d, err := v.createVirtual(wanted{spec: project.VirtualDevice{Name: name, Template: template}, project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	return d, v.writers[contracts.VirtualNamePrefix+name]
}

// lastPacket — последний отправленный пакет без SYN_REPORT.
func lastPacket(w *fakeWriter) []ev.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.packets[len(w.packets)-1]
	return p[:len(p)-1]
}

// TestXbox360 проверяет шаблон Xbox 360: VID:PID, замену X/Y как у xpad, курки и крестовину осями,
// оси из макросов, нажатое в кодах макросов и сброс всего.
func TestXbox360(t *testing.T) {
	t.Parallel()
	v := newVdevModule(nil)
	d, w := v.pad(t, "pad2", "xbox360")
	ctx := context.Background()
	s := v.setups["mKey pad2"]
	if s.ID.Vendor != 0x045e || s.ID.Product != 0x028e || s.ID.Bustype != ev.BusUSB || s.Phys != "mkey/vdev/pad2" || len(s.FF) != 1 {
		t.Fatalf("setup = %+v", s)
	}

	// West (X) — код BTN_X = 0x133, как у xpad; South — как есть.
	cases := []struct {
		code uint16
		want ev.Event
	}{
		{ev.BtnWest, ev.Event{Type: ev.EvKey, Code: 0x133, Value: 1}},
		{ev.BtnSouth, ev.Event{Type: ev.EvKey, Code: ev.BtnSouth, Value: 1}},
		{ev.BtnTl2, ev.Event{Type: ev.EvAbs, Code: ev.AbsZ, Value: 255}},
		{ev.BtnDpadUp, ev.Event{Type: ev.EvAbs, Code: ev.AbsHat0y, Value: -1}},
	}
	for _, c := range cases {
		if err := d.Press(ctx, c.code); err != nil {
			t.Fatal(err)
		}
		if p := lastPacket(w); len(p) != 1 || p[0].Type != c.want.Type || p[0].Code != c.want.Code || p[0].Value != c.want.Value {
			t.Errorf("press %s: %v", ev.CodeName(ev.EvKey, c.code), p)
		}
	}

	// Нажатое — в кодах макросов; отпускание курка — ось в покой.
	if h := d.Held(); len(h) != 4 || h[0] != ev.BtnSouth {
		t.Errorf("held = %v", h)
	}
	_ = d.Release(ctx, ev.BtnTl2)
	if p := lastPacket(w); p[0].Code != ev.AbsZ || p[0].Value != 0 {
		t.Errorf("release LT: %v", p)
	}

	// Оси: −1…1 на весь диапазон, курок 0…1, выход за пределы обрезается.
	for _, c := range []struct {
		code uint16
		v    float64
		want int32
	}{{ev.AbsX, -1, -32768}, {ev.AbsX, 1, 32767}, {ev.AbsX, 0, 0}, {ev.AbsX, 5, 32767}, {ev.AbsZ, 0.5, 128}, {ev.AbsZ, -1, 0}} {
		_ = d.SetAxis(ctx, c.code, c.v)
		if p := lastPacket(w); p[0].Value != c.want {
			t.Errorf("SetAxis(%s, %v) = %d, want %d", ev.CodeName(ev.EvAbs, c.code), c.v, p[0].Value, c.want)
		}
	}

	// Сброс: кнопки отпущены, все оси в покое, нажатого нет.
	if err := d.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	p := lastPacket(w)
	if len(d.Held()) != 0 || len(p) != len(s.Abs) {
		t.Errorf("release all: held %v, last packet %v", d.Held(), p)
	}

	// Кнопки, которой нет у устройства, — понятная ошибка.
	if err := d.Press(ctx, ev.BtnTrigger); !errors.Is(err, contracts.ErrUnknownControl) {
		t.Errorf("unknown button: %v", err)
	}
}

// TestDS4 проверяет шаблон DualShock 4: курок — и осью, и кнопкой, оси стиков 0…255 с центром 128.
func TestDS4(t *testing.T) {
	t.Parallel()
	v := newVdevModule(nil)
	d, w := v.pad(t, "ds", "ds4")
	ctx := context.Background()
	_ = d.Press(ctx, ev.BtnTl2)
	if p := lastPacket(w); len(p) != 2 || p[0].Code != ev.AbsZ || p[1].Code != ev.BtnTl2 || p[1].Value != 1 {
		t.Errorf("LT: %v", p)
	}
	_ = d.SetAxis(ctx, ev.AbsX, 0)
	if p := lastPacket(w); p[0].Value != 128 {
		t.Errorf("center = %d", p[0].Value)
	}
}

// fakeProjects — проекты с виртуальными устройствами.
type fakeProjects struct {
	contracts.Projects
	mu   sync.Mutex
	list []contracts.ProjectState
}

// List возвращает проекты.
func (f *fakeProjects) List() []contracts.ProjectState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list
}

// projectWith — проект с виртуальными устройствами.
func projectWith(id string, on bool, vd ...project.VirtualDevice) contracts.ProjectState {
	return contracts.ProjectState{Project: project.Project{ID: id, Enabled: &on, VirtualDevices: vd}}
}

// TestReconcile проверяет создание и удаление устройств по проектам: включён — есть, выключен —
// нет; изменение пересоздаёт; одно имя в двух проектах и неверный шаблон — ошибки в списке;
// поиск кнопок и осей по имени.
func TestReconcile(t *testing.T) {
	t.Parallel()
	pad := project.VirtualDevice{Name: "pad2", Template: "xbox360"}
	projects := &fakeProjects{list: []contracts.ProjectState{
		projectWith("games", true, pad, project.VirtualDevice{Name: "bad", Template: "nope"}),
		projectWith("other", true, project.VirtualDevice{Name: "PAD2", Template: "ds4"}),
		projectWith("off", false, project.VirtualDevice{Name: "joy", Template: "joystick"}),
	}}
	v := newVdevModule(projects)
	v.reconcile()

	// Создан только pad2 из включённого проекта; ошибки — у bad и у второго PAD2.
	list := v.List()
	got := map[string]contracts.VirtualDeviceInfo{}
	for _, i := range list {
		got[i.Name] = i
	}
	if len(list) != 3 || len(v.vdevs) != 1 || got["pad2"].Project != "games" || got["pad2"].Error != "" || got["pad2"].SystemName != "mKey pad2" {
		t.Fatalf("list = %+v", list)
	}
	if !strings.Contains(got["bad"].Error, "unknown virtual device template") || got["PAD2"].Project != "other" || !strings.Contains(got["PAD2"].Error, `project "games"`) {
		t.Errorf("errors: %+v", list)
	}

	// Поиск: устройство и его кнопки и оси.
	if d, err := v.Device("PAD2"); err != nil || d.Name() != "mKey pad2" {
		t.Errorf("Device: %v %v", d, err)
	}
	if _, err := v.Device("nope"); !errors.Is(err, contracts.ErrUnknownVirtual) {
		t.Errorf("unknown device: %v", err)
	}
	for _, c := range []struct {
		ctl  string
		code uint16
		axis bool
		ok   bool
	}{{"South", ev.BtnSouth, false, true}, {"X", ev.BtnWest, false, true}, {"LT", ev.BtnTl2, false, true},
		{"LX", ev.AbsX, true, true}, {"DPadUp", ev.BtnDpadUp, false, true}, {"BTN_TRIGGER", 0, false, false}, {"Nope", 0, false, false}} {
		code, axis, err := v.Resolve("pad2", c.ctl)
		if (err == nil) != c.ok || (c.ok && (code != c.code || axis != c.axis)) {
			t.Errorf("Resolve(pad2, %s) = %d %v %v", c.ctl, code, axis, err)
		}
	}

	// Проект выключили — устройство закрыто и исчезло.
	projects.mu.Lock()
	projects.list = []contracts.ProjectState{projectWith("games", false, pad)}
	projects.mu.Unlock()
	v.reconcile()
	if len(v.vdevs) != 0 || !v.writers["mKey pad2"].closed || len(v.List()) != 0 {
		t.Errorf("after disable: %v", v.List())
	}
}

// TestValidate проверяет описание устройства: шаблон, зарезервированные имена, свой набор кнопок.
func TestValidate(t *testing.T) {
	t.Parallel()
	v := newVdevModule(nil)
	for _, c := range []struct {
		spec project.VirtualDevice
		ok   bool
	}{
		{project.VirtualDevice{Name: "pad2", Template: "xbox360"}, true},
		{project.VirtualDevice{Name: "pad3", Template: "custom", Buttons: []string{"South", "BTN_TRIGGER_HAPPY1"}, Axes: map[string]project.AxisRange{"LX": {Min: -100, Max: 100}}}, true},
		{project.VirtualDevice{Name: "Keyboard", Template: "keyboard"}, false},
		{project.VirtualDevice{Name: "pad4", Template: "nope"}, false},
		{project.VirtualDevice{Name: "pad5", Template: "xbox360", Buttons: []string{"South"}}, false},
		{project.VirtualDevice{Name: "pad6", Template: "custom"}, false},
		{project.VirtualDevice{Name: "pad7", Template: "custom", Buttons: []string{"Nope"}}, false},
		{project.VirtualDevice{Name: "pad8", Template: "custom", Axes: map[string]project.AxisRange{"LX": {Min: 5, Max: 5}}}, false},
	} {
		if err := v.Validate(c.spec); (err == nil) != c.ok {
			t.Errorf("Validate(%+v) = %v", c.spec, err)
		}
	}
}

// TestTemplateInfo проверяет состав шаблонов для окна: имена кнопок и осей, как в макросах.
func TestTemplateInfo(t *testing.T) {
	t.Parallel()
	m := &Module{}
	info, ok := m.TemplateInfo("xbox360")
	if !ok {
		t.Fatal("xbox360 not found")
	}
	got := strings.Join(info.Buttons, ",") + " | " + strings.Join(info.Axes, ",")
	for _, want := range []string{"South", "West", "North", "LB", "Start", "LT", "RT", "DPadUp", "| LX,LY,LT,RX,RY,RT,DPadX,DPadY"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	if _, ok := m.TemplateInfo("custom"); ok {
		t.Error("custom has no fixed controls")
	}
	if _, ok := m.TemplateInfo("nope"); ok {
		t.Error("unknown template")
	}
}

// TestWheel проверяет руль: модель (VID:PID), обратную связь, имена руля, поворот −1…1 на 0…65535,
// педали (0 — отпущена = 255, 1 — до упора = 0), крестовину и сброс педалей в «отпущено».
func TestWheel(t *testing.T) {
	t.Parallel()
	v := newVdevModule(nil)
	d, w := v.pad(t, "wheel", "wheel")
	ctx := context.Background()
	s := v.setups["mKey wheel"]
	if s.ID.Vendor != 0x046d || s.ID.Product != 0xc24f || len(s.Keys) != 25 || len(s.FF) < 10 || s.Abs[ev.AbsZ].Value != 255 {
		t.Fatalf("setup = %+v", s)
	}

	// Имена: поворот, газ, кнопка-лепесток; общие имена крестовины тоже работают.
	for _, c := range []struct {
		name string
		code uint16
		axis bool
	}{{"Wheel", ev.AbsX, true}, {"gas", ev.AbsZ, true}, {"Brake", ev.AbsRz, true}, {"ShiftUp", ev.BtnJoystick + 4, false}, {"DPadUp", ev.BtnDpadUp, false}} {
		code, axis, err := v.ResolveIn(project.VirtualDevice{Name: "wheel", Template: "wheel"}, c.name)
		if err != nil || code != c.code || axis != c.axis {
			t.Errorf("%s = %#x %v %v", c.name, code, axis, err)
		}
	}

	// Поворот и педали.
	for _, c := range []struct {
		code  uint16
		value float64
		want  int32
	}{{ev.AbsX, -1, 0}, {ev.AbsX, 0, 32768}, {ev.AbsX, 1, 65535}, {ev.AbsZ, 0, 255}, {ev.AbsZ, 1, 0}, {ev.AbsRz, 0.5, 128}} {
		if err := d.SetAxis(ctx, c.code, c.value); err != nil {
			t.Fatal(err)
		}
		if got := lastPacket(w)[0].Value; got != c.want {
			t.Errorf("axis %#x = %v → %d, want %d", c.code, c.value, got, c.want)
		}
	}

	// Сброс: педали — «отпущено» (255), руль — в центр.
	if err := d.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	for _, e := range lastPacket(w) {
		if e.Type == ev.EvAbs && e.Value != s.Abs[e.Code].Value {
			t.Errorf("reset %#x = %d", e.Code, e.Value)
		}
	}
}

// TestFlightstick проверяет лётный джойстик: 56 кнопок (Button1 — гашетка, Button56 — последняя
// TRIGGER_HAPPY), РУД 0…1 от минимума, 4 шляпки осями и первую — ещё и кнопками.
func TestFlightstick(t *testing.T) {
	t.Parallel()
	v := newVdevModule(nil)
	d, w := v.pad(t, "stick", "flightstick")
	ctx := context.Background()
	s := v.setups["mKey stick"]
	if len(s.Keys) != 56 || s.Keys[55] != ev.BtnTriggerHappy40 || len(s.Abs) != 16 {
		t.Fatalf("setup: keys %d abs %d", len(s.Keys), len(s.Abs))
	}
	spec := project.VirtualDevice{Name: "stick", Template: "flightstick"}
	for name, want := range map[string]uint16{"Trigger": ev.BtnJoystick, "Button1": ev.BtnJoystick, "Button17": ev.BtnTriggerHappy1, "button56": ev.BtnTriggerHappy40, "Throttle": ev.AbsThrottle, "Hat4Y": ev.AbsHat3y} {
		if code, _, err := v.ResolveIn(spec, name); err != nil || code != want {
			t.Errorf("%s = %#x %v", name, code, err)
		}
	}
	if err := d.SetAxis(ctx, ev.AbsThrottle, 1); err != nil || lastPacket(w)[0].Value != 65535 {
		t.Fatalf("throttle = %v %v", lastPacket(w), err)
	}
	if err := d.Press(ctx, ev.BtnDpadLeft); err != nil || lastPacket(w)[0] != (ev.Event{Type: ev.EvAbs, Code: ev.AbsHat0x, Value: -1}) {
		t.Fatalf("hat1 left = %v %v", lastPacket(w), err)
	}
	if err := d.Press(ctx, ev.BtnTriggerHappy40); err != nil || lastPacket(w)[0].Code != ev.BtnTriggerHappy40 {
		t.Fatalf("button56 = %v %v", lastPacket(w), err)
	}

	// Состав для окна: свои имена.
	info, ok := v.TemplateInfo("flightstick")
	if !ok || info.Axes[0] != "StickX" || info.Buttons[0] != "Trigger" || !slices.Contains(info.Buttons, "Button56") || !slices.Contains(info.Buttons, "DPadUp") {
		t.Fatalf("info = %+v", info)
	}
}
