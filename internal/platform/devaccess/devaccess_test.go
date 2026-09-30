package devaccess

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
)

// recorder — исполнитель команд, который только записывает вызовы.
type recorder struct {
	calls []string
}

func (r *recorder) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}

// newEnv создаёт временный корень с заданными файлами и окружение привилегированной операции.
func newEnv(t *testing.T, files map[string]string, info contracts.PlatformInfo) (contracts.PrivilegedEnv, *recorder) {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(p, "/") {
			continue
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &recorder{}
	return contracts.PrivilegedEnv{Root: root, Runner: r, User: "vera", Platform: info}, r
}

// read читает файл внутри корня ("" — если файла нет).
func read(env contracts.PrivilegedEnv, path string) string {
	data, _ := os.ReadFile(filepath.Join(env.Root, path))
	return string(data)
}

// TestApplicable проверяет выбор способа для разных систем.
func TestApplicable(t *testing.T) {
	t.Parallel()

	// first возвращает ID первого подходящего способа.
	first := func(info contracts.PlatformInfo) string {
		for _, a := range All() {
			if a.Applicable(info) {
				return a.Meta().ID
			}
		}
		return ""
	}

	// systemd и elogind+eudev → uaccess; udev без logind → group; mdev → mdev.
	cases := map[string]contracts.PlatformInfo{
		"uaccess": {Logind: "systemd-logind", DeviceManager: "systemd-udevd"},
		"group":   {Logind: "none", DeviceManager: "eudev"},
		"mdev":    {Logind: "none", DeviceManager: "mdev"},
	}
	for want, info := range cases {
		if got := first(info); got != want {
			t.Errorf("%+v: first applicable = %s, want %s", info, got, want)
		}
	}
	if (uaccess{}).Applicable(contracts.PlatformInfo{Logind: "elogind", DeviceManager: "eudev"}) != true {
		t.Error("uaccess must apply to elogind + eudev")
	}
}

// TestUaccessInstallUninstall проверяет файлы и команды способа uaccess на systemd-системе.
func TestUaccessInstallUninstall(t *testing.T) {
	t.Parallel()
	env, rec := newEnv(t, map[string]string{"/etc/modules-load.d/": ""}, contracts.PlatformInfo{Logind: "systemd-logind", DeviceManager: "systemd-udevd"})

	// Установка: правило с uaccess, автозагрузка uinput, modprobe и применение правил.
	if err := (uaccess{}).Install(context.Background(), env); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(read(env, RulesPath), `TAG+="uaccess"`) || read(env, ModulesLoadPath) == "" {
		t.Fatalf("files not written: rules=%q modules=%q", read(env, RulesPath), read(env, ModulesLoadPath))
	}
	want := []string{"modprobe uinput", "udevadm control --reload", "udevadm trigger --subsystem-match=input --subsystem-match=misc", "udevadm settle"}
	if strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v", rec.calls)
	}

	// Установка видна проверкой Installed.
	if !(uaccess{}).Installed(env.Root) {
		t.Fatal("Installed must be true after Install")
	}

	// Удаление: файлов нет.
	if err := (uaccess{}).Uninstall(context.Background(), env); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if read(env, RulesPath) != "" || read(env, ModulesLoadPath) != "" {
		t.Fatal("files must be removed")
	}
}

// TestGroupInstall проверяет способ group: создание группы только при её отсутствии.
func TestGroupInstall(t *testing.T) {
	t.Parallel()

	// Группа input уже есть: groupadd не вызывается, usermod — да.
	env, rec := newEnv(t, map[string]string{"/etc/group": "root:x:0:\ninput:x:104:\n", "/etc/modules-load.d/": ""}, contracts.PlatformInfo{DeviceManager: "eudev"})
	if err := (group{}).Install(context.Background(), env); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(rec.calls, "|")
	if strings.Contains(joined, "groupadd") || !strings.Contains(joined, "usermod -aG input vera") {
		t.Fatalf("calls = %v", rec.calls)
	}
	if !strings.Contains(read(env, RulesPath), `GROUP="input"`) {
		t.Fatal("group rules not written")
	}

	// Без группы input в системе — она создаётся.
	env2, rec2 := newEnv(t, map[string]string{"/etc/group": "root:x:0:\n"}, contracts.PlatformInfo{DeviceManager: "eudev"})
	if err := (group{}).Install(context.Background(), env2); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rec2.calls[0] != "groupadd -r input" {
		t.Fatalf("calls = %v", rec2.calls)
	}
}

// TestMdevAlpine проверяет способ mdev на Alpine: блок в начале mdev.conf и uinput в /etc/modules.
func TestMdevAlpine(t *testing.T) {
	t.Parallel()
	env, rec := newEnv(t, map[string]string{
		MdevConfPath:   "# default rules\nevent[0-9]+ root:input 0640 =input/\n",
		EtcModulesPath: "af_packet\nipv6\n",
		"/etc/group":   "input:x:23:\n",
	}, contracts.PlatformInfo{DeviceManager: "mdev"})

	// Установка: блок mKey первым в mdev.conf, uinput добавлен в /etc/modules, mdev -s.
	if err := (mdev{}).Install(context.Background(), env); err != nil {
		t.Fatalf("Install: %v", err)
	}
	conf := read(env, MdevConfPath)
	if !strings.HasPrefix(conf, markBegin) || !strings.Contains(conf, "uinput root:input 0660") || !strings.Contains(conf, "# default rules") {
		t.Fatalf("mdev.conf = %q", conf)
	}
	if mods := read(env, EtcModulesPath); !strings.HasPrefix(mods, "af_packet\nipv6\n") || !strings.Contains(mods, "\nuinput\n") {
		t.Fatalf("/etc/modules = %q", mods)
	}
	if rec.calls[len(rec.calls)-1] != "mdev -s" || !strings.Contains(strings.Join(rec.calls, "|"), "addgroup vera input") {
		t.Fatalf("calls = %v", rec.calls)
	}

	// Повторная установка не дублирует блок.
	if err := (mdev{}).Install(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if strings.Count(read(env, MdevConfPath), markBegin) != 1 {
		t.Fatal("block duplicated")
	}

	// Удаление возвращает файлы к исходному виду.
	if err := (mdev{}).Uninstall(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if read(env, MdevConfPath) != "# default rules\nevent[0-9]+ root:input 0640 =input/\n" || read(env, EtcModulesPath) != "af_packet\nipv6\n" {
		t.Fatalf("files not restored: %q / %q", read(env, MdevConfPath), read(env, EtcModulesPath))
	}
}

// TestBuiltinUinput проверяет, что для встроенного в ядро uinput автозагрузка не настраивается.
func TestBuiltinUinput(t *testing.T) {
	t.Parallel()
	env, _ := newEnv(t, map[string]string{
		"/proc/sys/kernel/osrelease":              "6.8.0-test\n",
		"/lib/modules/6.8.0-test/modules.builtin": "kernel/drivers/input/misc/uinput.ko\n",
		"/etc/modules-load.d/":                    "",
	}, contracts.PlatformInfo{Logind: "systemd-logind", DeviceManager: "systemd-udevd"})
	if err := (uaccess{}).Install(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if read(env, ModulesLoadPath) != "" {
		t.Fatal("modules-load file must not be written for built-in uinput")
	}
}

// TestInstallRequiresUser проверяет, что способы с группой требуют имя пользователя.
func TestInstallRequiresUser(t *testing.T) {
	t.Parallel()
	env, _ := newEnv(t, nil, contracts.PlatformInfo{DeviceManager: "eudev"})
	env.User = ""
	if err := (group{}).Install(context.Background(), env); err == nil {
		t.Fatal("group install without user must fail")
	}
}
