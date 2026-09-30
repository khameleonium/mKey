package setup

import (
	"context"
	"os"
	"slices"
	"testing"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// fakeProbe — фейковая система для проверок.
type fakeProbe struct {
	kernel   string
	exists   map[string]bool
	openErrs map[string]error
	devices  []ev.ProcDevice
}

func (f fakeProbe) KernelRelease() (string, error)        { return f.kernel, nil }
func (f fakeProbe) Exists(path string) bool               { return f.exists[path] }
func (f fakeProbe) CanOpen(path string, _ bool) error     { return f.openErrs[path] }
func (f fakeProbe) ProcDevices() ([]ev.ProcDevice, error) { return f.devices, nil }
func (fakeProbe) Executable() (string, error)             { return "/usr/bin/mkey", nil }

// fakeSession и fakePlatform — сервисы других модулей.
type fakeSession struct{ info contracts.SessionInfo }

func (f fakeSession) Info() contracts.SessionInfo { return f.info }

type fakePlatform struct {
	info      contracts.PlatformInfo
	elevators []contracts.Elevator
	installed bool
}

func (f fakePlatform) Info() contracts.PlatformInfo    { return f.info }
func (f fakePlatform) Elevators() []contracts.Elevator { return f.elevators }
func (f fakePlatform) DeviceAccess() (contracts.DeviceAccess, error) {
	return fakeAccess{installed: f.installed}, nil
}

type fakeAccess struct{ installed bool }

func (fakeAccess) Meta() contracts.ExtensionMeta                            { return contracts.ExtensionMeta{ID: "uaccess"} }
func (fakeAccess) Applicable(contracts.PlatformInfo) bool                   { return true }
func (fakeAccess) RequiresRelogin() bool                                    { return false }
func (fakeAccess) Install(context.Context, contracts.PrivilegedEnv) error   { return nil }
func (fakeAccess) Uninstall(context.Context, contracts.PrivilegedEnv) error { return nil }
func (f fakeAccess) Installed(string) bool                                  { return f.installed }

// fakeElevator записывает запущенную команду.
type fakeElevator struct {
	id   string
	argv *[]string
}

func (f fakeElevator) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: f.id, NameKey: "platform.elevator." + f.id}
}
func (fakeElevator) Available() bool         { return true }
func (fakeElevator) Command([]string) string { return "" }
func (f fakeElevator) Run(_ context.Context, argv []string) error {
	*f.argv = argv
	return nil
}

// byID возвращает проверку с заданным ID.
func byID(t *testing.T, checks []contracts.Check, id string) contracts.Check {
	t.Helper()
	i := slices.IndexFunc(checks, func(c contracts.Check) bool { return c.ID == id })
	if i < 0 {
		t.Fatalf("check %q not found in %+v", id, checks)
	}
	return checks[i]
}

// devices — две физические клавиатуры и собственное устройство mKey.
var devices = []ev.ProcDevice{
	{Name: "USB Keyboard", Handlers: []string{"kbd", "event3"}},
	{Name: "USB Mouse", Handlers: []string{"mouse0", "event4"}},
	{Name: "mKey Keyboard", Handlers: []string{"kbd", "event9"}},
	{Name: "Power Button", Handlers: []string{"kbd"}},
}

// TestHealthySystem проверяет систему, где у пользователя уже есть все права.
func TestHealthySystem(t *testing.T) {
	t.Parallel()
	var argv []string
	m := &Module{
		probe:   fakeProbe{kernel: "6.8.0-45-generic", exists: map[string]bool{"/dev/uinput": true}, devices: devices},
		session: fakeSession{contracts.SessionInfo{Type: "wayland", Compositor: "kde"}},
		platform: fakePlatform{
			info:      contracts.PlatformInfo{Init: "systemd", RuntimeDir: "/run/user/1000/mkey"},
			elevators: []contracts.Elevator{fakeElevator{id: "pkexec", argv: &argv}},
			installed: true,
		},
	}
	checks := m.Run(context.Background())

	// Всё в порядке, устройства посчитаны без собственного mKey и без устройства без evdev.
	if !Healthy(checks) {
		t.Fatalf("checks = %+v", checks)
	}
	if c := byID(t, checks, "input_devices"); c.Status != contracts.CheckOK || c.Args["total"] != "2" {
		t.Fatalf("input_devices = %+v", c)
	}
	if c := byID(t, checks, "elevator"); c.ArgKeys["method"] != "platform.elevator.pkexec" {
		t.Fatalf("elevator = %+v", c)
	}
}

// TestNoAccess проверяет систему без прав: проверки провалены и предлагают исправление.
func TestNoAccess(t *testing.T) {
	t.Parallel()
	m := &Module{probe: fakeProbe{
		kernel:   "5.4.0",
		exists:   map[string]bool{"/dev/uinput": true},
		openErrs: map[string]error{"/dev/uinput": os.ErrPermission, "/dev/input/event3": os.ErrPermission, "/dev/input/event4": os.ErrPermission},
		devices:  devices,
	}}
	checks := m.Run(context.Background())

	// uinput и устройства недоступны, у обеих проверок есть исправление.
	if Healthy(checks) {
		t.Fatal("system without access must not be healthy")
	}
	for _, id := range []string{"uinput", "input_devices"} {
		if c := byID(t, checks, id); c.Status != contracts.CheckFail || c.Fix != contracts.FixDeviceAccess {
			t.Errorf("%s = %+v", id, c)
		}
	}

	// Ядро 5.4 — минимально допустимое. Без модулей session/platform их проверки пропущены.
	if c := byID(t, checks, "kernel"); c.Status != contracts.CheckOK {
		t.Errorf("kernel = %+v", c)
	}
	if slices.ContainsFunc(checks, func(c contracts.Check) bool { return c.ID == "session" || c.ID == "rules" }) {
		t.Error("session/platform checks must be skipped without their modules")
	}
}

// TestMissingUinputAndOldKernel проверяет отсутствие модуля uinput и слишком старое ядро.
func TestMissingUinputAndOldKernel(t *testing.T) {
	t.Parallel()
	m := &Module{probe: fakeProbe{kernel: "4.19.0", exists: map[string]bool{}}}
	checks := m.Run(context.Background())
	if c := byID(t, checks, "uinput"); c.MessageKey != "setup.check.uinput.missing" || c.Fix == "" {
		t.Errorf("uinput = %+v", c)
	}
	if c := byID(t, checks, "kernel"); c.Status != contracts.CheckFail {
		t.Errorf("kernel = %+v", c)
	}
}

// TestFix проверяет, что исправление запускает `mkey privileged install-rules` через бэкенд.
func TestFix(t *testing.T) {
	t.Parallel()
	var argv []string
	m := &Module{probe: fakeProbe{}}
	if err := m.Fix(context.Background(), contracts.FixDeviceAccess, fakeElevator{id: "sudo", argv: &argv}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{"/usr/bin/mkey", "privileged", "install-rules"}) {
		t.Fatalf("argv = %v", argv)
	}
	if err := m.Fix(context.Background(), "unknown", fakeElevator{argv: &argv}); err == nil {
		t.Fatal("unknown fix must fail")
	}
}

// TestParseKernel проверяет разбор версии ядра.
func TestParseKernel(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]int{"6.8.0-45-generic": {6, 8}, "5.4": {5, 4}, "5.10rc1": {5, 10}, "junk": {0, 0}} {
		if ma, mi := parseKernel(in); ma != want[0] || mi != want[1] {
			t.Errorf("parseKernel(%q) = %d.%d", in, ma, mi)
		}
	}
}
