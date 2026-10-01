package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/lib/buildinfo"

	"mkey/internal/setup/manifest"
)

// newTestEnv создаёт окружение с временным домашним каталогом и «запущенной программой».
func newTestEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	exe := filepath.Join(home, "Загрузки", "mkey")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	return NewEnv(func(k string) string { return env[k] }, filepath.Join(home, "run"), exe)
}

// TestInstallAndUndo проверяет установку файлов и полное удаление по манифесту.
func TestInstallAndUndo(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, err := manifest.Load(e.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}

	// Установка: программа, иконка, ярлык.
	if err := Files(e, manifest.Writer{M: m, Owner: "install"}); err != nil {
		t.Fatal(err)
	}
	if !e.Installed() {
		t.Fatal("must be installed")
	}
	// Ярлык в меню открывает окно — он есть только в полной сборке.
	data, _ := os.ReadFile(e.DesktopPath())
	if got := strings.Contains(string(data), "Exec=\""+e.BinPath()+"\" gui"); got != buildinfo.GUI {
		t.Fatalf("desktop entry = %q (GUI build = %v)", data, buildinfo.GUI)
	}
	if fi, _ := os.Stat(e.BinPath()); fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary perm = %o", fi.Mode().Perm())
	}

	// Удаление по манифесту: файлов не осталось.
	if errs := m.Undo(nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, p := range []string{e.BinPath(), e.DesktopPath(), e.IconPath()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains", p)
		}
	}
}

// TestSystemInstall проверяет установку пакетом: программа вне домашней папки не копируется,
// программой считается сам запущенный файл.
func TestSystemInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	e := NewEnv(func(k string) string { return env[k] }, filepath.Join(home, "run"), "/usr/bin/mkey")
	if !e.System() || e.BinPath() != "/usr/bin/mkey" {
		t.Fatalf("system = %v, bin = %s", e.System(), e.BinPath())
	}
	if newTestEnv(t).System() {
		t.Fatal("a file in the home folder is not a system install")
	}
	m, _ := manifest.Load(e.ManifestPath())
	if err := Files(e, manifest.Writer{M: m, Owner: "install"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "mkey")); !os.IsNotExist(err) {
		t.Fatal("program copied into ~/.local/bin")
	}
}

// TestRemoveData проверяет удаление данных: полное и с сохранением настроек.
func TestRemoveData(t *testing.T) {
	t.Parallel()
	for _, keep := range []bool{false, true} {
		e := newTestEnv(t)
		for _, p := range []string{
			filepath.Join(e.Config, "projects", "a.mkey.yaml"),
			filepath.Join(e.Data, "recordings", "r.mkrec"),
			filepath.Join(e.Data, manifest.FileName),
			filepath.Join(e.State, "mkey.log"),
			filepath.Join(e.Runtime, "token"),
		} {
			_ = os.MkdirAll(filepath.Dir(p), 0o700)
			_ = os.WriteFile(p, []byte("x"), 0o600)
		}
		if errs := RemoveData(e, keep); len(errs) != 0 {
			t.Fatal(errs)
		}
		exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
		if exists(e.State) || exists(e.Runtime) || exists(filepath.Join(e.Data, manifest.FileName)) {
			t.Errorf("keep=%v: state, runtime or manifest remains", keep)
		}
		if exists(filepath.Join(e.Config, "projects")) != keep || exists(filepath.Join(e.Data, "recordings")) != keep {
			t.Errorf("keep=%v: config/recordings presence mismatch", keep)
		}
	}
}
