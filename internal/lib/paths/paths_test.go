package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// TestXDG проверяет выбор каталогов по переменным окружения.
func TestXDG(t *testing.T) {
	t.Parallel()
	env := map[string]string{"HOME": "/home/vera", "XDG_STATE_HOME": "/var/state", "XDG_CONFIG_HOME": "relative"}
	getenv := func(k string) string { return env[k] }
	if got := State(getenv); got != "/var/state/mkey" {
		t.Errorf("State = %s", got)
	}
	if got := Config(getenv); got != "/home/vera/.config/mkey" {
		t.Errorf("Config with relative XDG = %s (relative values must be ignored)", got)
	}
	if got := Data(getenv); got != "/home/vera/.local/share/mkey" {
		t.Errorf("Data = %s", got)
	}
}

// TestEnsurePrivateDir проверяет создание каталога 0700 и исправление слишком открытых прав.
func TestEnsurePrivateDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run", "mkey")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("perm = %o", fi.Mode().Perm())
	}
}
