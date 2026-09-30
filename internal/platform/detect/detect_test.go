package detect

import (
	"os"
	"testing"
)

// fakeProbe — система, описанная списком существующих путей, файлов и программ.
type fakeProbe struct {
	paths    map[string]bool
	files    map[string]string
	programs map[string]bool
	env      map[string]string
}

func (f fakeProbe) Exists(path string) bool { return f.paths[path] || f.files[path] != "" }

func (f fakeProbe) ReadFile(path string) ([]byte, error) {
	if s, ok := f.files[path]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (f fakeProbe) LookPath(name string) bool { return f.programs[name] }
func (f fakeProbe) Getenv(name string) string { return f.env[name] }
func (fakeProbe) UID() int                    { return 1000 }

// TestDetectSystems проверяет определение для характерных систем из матрицы NFR-6.
func TestDetectSystems(t *testing.T) {
	t.Parallel()

	// Таблица систем: маркеры → ожидаемые init, logind, менеджер устройств, пакетный менеджер.
	cases := []struct {
		name                         string
		probe                        fakeProbe
		init, logind, devmgr, pkgmgr string
	}{
		{
			name: "linux mint (systemd)",
			probe: fakeProbe{
				paths:    map[string]bool{"/run/systemd/system": true, "/run/systemd/seats": true, "/run/udev": true},
				files:    map[string]string{"/etc/os-release": "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\nPRETTY_NAME=\"Linux Mint 22.3\"\n"},
				programs: map[string]bool{"pkexec": true, "sudo": true},
				env:      map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			},
			init: "systemd", logind: "systemd-logind", devmgr: "systemd-udevd", pkgmgr: "apt",
		},
		{
			name: "void (runit + elogind + eudev)",
			probe: fakeProbe{
				paths: map[string]bool{"/run/runit": true, "/run/systemd/seats": true, "/run/udev": true},
				files: map[string]string{"/etc/os-release": "ID=\"void\"\n"},
			},
			init: "runit", logind: "elogind", devmgr: "eudev", pkgmgr: "xbps",
		},
		{
			name: "alpine (openrc + mdev)",
			probe: fakeProbe{
				paths: map[string]bool{"/run/openrc": true},
				files: map[string]string{"/etc/os-release": "ID=alpine\n", "/proc/sys/kernel/hotplug": "/sbin/mdev\n"},
			},
			init: "openrc", logind: "none", devmgr: "mdev", pkgmgr: "apk",
		},
		{
			name: "devuan (sysvinit)",
			probe: fakeProbe{
				paths: map[string]bool{"/run/udev": true, "/run/systemd/seats": true},
				files: map[string]string{"/proc/1/comm": "init\n", "/etc/os-release": "ID=devuan\nID_LIKE=debian\n"},
			},
			init: "sysvinit", logind: "elogind", devmgr: "eudev", pkgmgr: "apt",
		},
		{
			name: "artix (openrc)",
			probe: fakeProbe{
				paths:    map[string]bool{"/run/openrc": true, "/run/systemd/seats": true, "/run/udev": true},
				files:    map[string]string{"/etc/os-release": "ID=artix\nID_LIKE=arch\n"},
				programs: map[string]bool{"systemd-udevd": true},
			},
			init: "openrc", logind: "elogind", devmgr: "systemd-udevd", pkgmgr: "pacman",
		},
	}

	// Прогоняем все системы.
	for _, c := range cases {
		got := Detect(c.probe)
		if got.Init != c.init || got.Logind != c.logind || got.DeviceManager != c.devmgr || got.PackageManager != c.pkgmgr {
			t.Errorf("%s: got init=%s logind=%s devmgr=%s pkg=%s", c.name, got.Init, got.Logind, got.DeviceManager, got.PackageManager)
		}
	}
}

// TestRuntimeDir проверяет выбор каталога времени выполнения и запасной вариант.
func TestRuntimeDir(t *testing.T) {
	t.Parallel()
	if d, fb := RuntimeDir(func(string) string { return "/run/user/1000" }, 1000); d != "/run/user/1000/mkey" || fb {
		t.Errorf("RuntimeDir = %s, %v", d, fb)
	}
	if d, fb := RuntimeDir(func(string) string { return "" }, 1000); d != "/tmp/mkey-1000" || !fb {
		t.Errorf("fallback RuntimeDir = %s, %v", d, fb)
	}
}
