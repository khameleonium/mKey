package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetModuleValue проверяет запись значения с сохранением комментариев и созданием файла.
func TestSetModuleValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Файла нет — создаётся.
	p := filepath.Join(dir, "new", "config.yaml")
	if err := SetModuleValue(p, "recorder", "hotkey", "^{Ctrl}^{Alt}{R}"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Modules["recorder"]["hotkey"] != "^{Ctrl}^{Alt}{R}" {
		t.Fatalf("new file: %+v %v", c, err)
	}

	// Существующий файл: комментарии и другие значения сохраняются, значение заменяется.
	p2 := filepath.Join(dir, "config.yaml")
	src := "# мои настройки\nlanguage: ru\nmodules:\n  input:\n    emergency_stop: \"^{Esc}^{Backspace}{Enter}\" # не трогать\n    watchdog_ms: 500\n"
	if err := os.WriteFile(p2, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetModuleValue(p2, "input", "emergency_stop", "^{LCtrl}^{LAlt}{Pause}"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p2)
	text := string(data)
	for _, want := range []string{"# мои настройки", "language: ru", "watchdog_ms: 500", `"^{LCtrl}^{LAlt}{Pause}"`, "# не трогать"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
}
