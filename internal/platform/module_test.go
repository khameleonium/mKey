package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/platform/detect"
	"mkey/internal/registry"
)

// fakeProbe — система во временном каталоге с заданными переменными окружения и программами.
type fakeProbe struct {
	detect.OS
	env      map[string]string
	programs map[string]bool
}

func (f fakeProbe) Getenv(k string) string    { return f.env[k] }
func (f fakeProbe) LookPath(name string) bool { return f.programs[name] }
func (fakeProbe) UID() int                    { return 1000 }

// startPlatform запускает модуль platform на фейковой systemd-системе.
func startPlatform(t *testing.T, programs map[string]bool, tty bool) *Module {
	t.Helper()

	// Корень фейковой системы: systemd, logind, udev.
	root := t.TempDir()
	for _, d := range []string{"/run/systemd/system", "/run/systemd/seats", "/run/udev"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Модуль с подменёнными зависимостями.
	mod := &Module{
		probe: fakeProbe{OS: detect.OS{Root: root}, env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, programs: programs},
		isTTY: func() bool { return tty },
		exec:  func(context.Context, string, []string, bool) error { return nil },
	}

	// Запуск в менеджере.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "ru"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return mod
}

// TestPlatformService проверяет сведения, выбор бэкендов и способ доступа.
func TestPlatformService(t *testing.T) {
	t.Parallel()
	mod := startPlatform(t, map[string]bool{"pkexec": true, "sudo": true}, true)

	// Сведения о системе определены.
	if info := mod.Info(); info.Init != "systemd" || info.Logind != "systemd-logind" || !info.HasPkexec {
		t.Fatalf("Info = %+v", info)
	}

	// Бэкенды: pkexec (графика), sudo (терминал), manual — последним.
	var ids []string
	for _, e := range mod.Elevators() {
		ids = append(ids, e.Meta().ID)
	}
	if len(ids) != 3 || ids[0] != "pkexec" || ids[1] != "sudo" || ids[2] != "manual" {
		t.Fatalf("Elevators = %v", ids)
	}

	// На systemd-системе выбирается uaccess.
	a, err := mod.DeviceAccess()
	if err != nil || a.Meta().ID != "uaccess" {
		t.Fatalf("DeviceAccess = %v, %v", a, err)
	}
}

// pluginElevator — бэкенд «из плагина» для проверки порядка.
type pluginElevator struct{ id string }

func (p pluginElevator) Meta() contracts.ExtensionMeta     { return contracts.ExtensionMeta{ID: p.id} }
func (pluginElevator) Available() bool                     { return true }
func (pluginElevator) Command([]string) string             { return "" }
func (pluginElevator) Run(context.Context, []string) error { return nil }

// TestOrdered проверяет, что бэкенды плагинов встают перед «ручным» вариантом.
func TestOrdered(t *testing.T) {
	t.Parallel()
	all := []contracts.Extension{pluginElevator{"manual"}, pluginElevator{"pkexec"}, pluginElevator{"zz-plugin"}}
	got := ordered(all, []string{"pkexec", "manual"}, "manual")
	if len(got) != 3 || got[0].Meta().ID != "pkexec" || got[1].Meta().ID != "zz-plugin" || got[2].Meta().ID != "manual" {
		t.Fatalf("ordered = %v", got)
	}
}

// TestNoDeviceAccess проверяет ошибку, когда ни один способ не подходит.
func TestNoDeviceAccess(t *testing.T) {
	t.Parallel()
	mod := &Module{info: contracts.PlatformInfo{DeviceManager: "unknown", Logind: "none"}, ext: registry.NewExtensions()}
	if _, err := mod.DeviceAccess(); !errors.Is(err, contracts.ErrUnsupported) {
		t.Fatalf("DeviceAccess err = %v", err)
	}
}
