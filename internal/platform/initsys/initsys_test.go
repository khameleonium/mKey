package initsys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/setup/manifest"
)

// runner записывает команды и отвечает успехом, если active.
type runner struct {
	calls  []string
	active bool
}

func (r *runner) Run(_ context.Context, name string, args ...string) error {
	cmd := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, cmd)
	if strings.Contains(cmd, "is-active") && !r.active {
		return errors.New("inactive")
	}
	return nil
}

// newEnv создаёт окружение во временном каталоге.
func newEnv(t *testing.T, session contracts.SessionInfo, init string, r *runner) (contracts.AutostartEnv, *manifest.Manifest) {
	t.Helper()
	home := t.TempDir()
	m, _ := manifest.Load(filepath.Join(home, "manifest.json"))
	return contracts.AutostartEnv{
		Home: home, ConfigHome: filepath.Join(home, ".config"), Exe: "/home/u/.local/bin/mkey",
		Session: session, Platform: contracts.PlatformInfo{Init: init}, Runner: r,
		Files: manifest.Writer{M: m, Owner: "autostart"},
	}, m
}

// TestSelect проверяет выбор способа для разных окружений.
func TestSelect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		session contracts.SessionInfo
		init    string
		active  bool
		file    string
		want    string
	}{
		{"kde", contracts.SessionInfo{Type: "wayland", Desktop: "KDE", Compositor: "kde"}, "systemd", true, "", "xdg"},
		{"xfce on void", contracts.SessionInfo{Type: "x11", Desktop: "XFCE", Compositor: "xfce"}, "runit", false, "", "xdg"},
		{"hyprland with systemd session", contracts.SessionInfo{Type: "wayland", Desktop: "Hyprland", Compositor: "hyprland"}, "systemd", true, "hypr/hyprland.conf", "systemd-user"},
		{"sway on alpine", contracts.SessionInfo{Type: "wayland", Compositor: "sway"}, "openrc", false, "sway/config", "sway"},
		{"sway without config", contracts.SessionInfo{Type: "wayland", Compositor: "sway"}, "openrc", false, "", "manual"},
		{"labwc creates autostart", contracts.SessionInfo{Type: "wayland", Desktop: "labwc", Compositor: "labwc"}, "runit", false, "", "labwc"},
		{"console", contracts.SessionInfo{Type: "tty"}, "systemd", false, "", "manual"},
	}
	for _, c := range cases {
		env, _ := newEnv(t, c.session, c.init, &runner{active: c.active})
		if c.file != "" {
			p := filepath.Join(env.ConfigHome, c.file)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte("# user config\n"), 0o644)
		}
		if got := Select(All(), env).Meta().ID; got != c.want {
			t.Errorf("%s: Select = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestXDGInstallUninstall проверяет файл автозапуска и манифест.
func TestXDGInstallUninstall(t *testing.T) {
	t.Parallel()
	env, m := newEnv(t, contracts.SessionInfo{Type: "wayland", Desktop: "KDE", Compositor: "kde"}, "systemd", &runner{})
	a := xdg{}
	if err := a.Install(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a.Target(env))
	if !a.Installed(env) || !strings.Contains(string(data), `Exec="/home/u/.local/bin/mkey" daemon`) || len(m.List()) != 1 {
		t.Fatalf("installed = %v, data = %q, manifest = %+v", a.Installed(env), data, m.List())
	}
	if err := a.Uninstall(context.Background(), env); err != nil || a.Installed(env) || len(m.List()) != 0 {
		t.Fatalf("uninstall: %v, installed %v, manifest %+v", err, a.Installed(env), m.List())
	}
}

// TestSystemdInstall проверяет файл службы и команды systemctl.
func TestSystemdInstall(t *testing.T) {
	t.Parallel()
	r := &runner{active: true}
	env, _ := newEnv(t, contracts.SessionInfo{Type: "wayland", Compositor: "hyprland"}, "systemd", r)
	a := systemdUser{}
	if err := a.Install(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.calls, "|")
	if !strings.Contains(got, "systemctl --user enable mkey.service") || !strings.Contains(got, "systemctl --user start mkey.service") {
		t.Fatalf("calls = %v", r.calls)
	}
	data, _ := os.ReadFile(a.Target(env))
	if !strings.Contains(string(data), "Restart=on-failure") {
		t.Fatalf("unit = %q", data)
	}
	if err := a.Uninstall(context.Background(), env); err != nil || a.Installed(env) {
		t.Fatalf("uninstall: %v", err)
	}
}

// TestCompositorBlock проверяет блок в конфиге Sway и его удаление без следов.
func TestCompositorBlock(t *testing.T) {
	t.Parallel()
	env, _ := newEnv(t, contracts.SessionInfo{Type: "wayland", Compositor: "sway"}, "openrc", &runner{})
	conf := filepath.Join(env.ConfigHome, "sway", "config")
	_ = os.MkdirAll(filepath.Dir(conf), 0o755)
	_ = os.WriteFile(conf, []byte("# user config\n"), 0o644)
	a := Select(All(), env)
	if a.Meta().ID != "sway" {
		t.Fatalf("selected %s", a.Meta().ID)
	}
	if err := a.Install(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(conf)
	if !strings.Contains(string(data), `exec "/home/u/.local/bin/mkey" daemon`) || !a.Installed(env) {
		t.Fatalf("config = %q", data)
	}
	if err := a.Uninstall(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(conf); string(data) != "# user config\n" {
		t.Fatalf("config after uninstall = %q", data)
	}
}

// TestManual проверяет ручной способ: команда для пользователя.
func TestManual(t *testing.T) {
	t.Parallel()
	env, _ := newEnv(t, contracts.SessionInfo{}, "", &runner{})
	err := manual{}.Install(context.Background(), env)
	var me *contracts.ManualActionError
	if !errors.As(err, &me) || me.Command != `"/home/u/.local/bin/mkey" daemon` {
		t.Fatalf("manual = %v", err)
	}
}
