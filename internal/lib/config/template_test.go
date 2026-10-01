package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testTemplate — маленький шаблон настроек для проверки дополнения.
const testTemplate = `# Настройки
version: 1

# Язык.
language: ""

modules:
  recorder:
    # Сочетание записи.
    hotkey: "^{LCtrl}^{RAlt}{Space}"
    # Движения мыши.
    moves: true
`

// TestComplete проверяет дополнение файла настроек по шаблону: нет файла — шаблон; не хватает
// ключей — дописываются с пояснениями, значения пользователя и его лишние ключи сохраняются;
// всего хватает — файл не трогается.
func TestComplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Файла нет — записывается шаблон как есть.
	p := filepath.Join(dir, "a", "config.yaml")
	if changed, err := Complete(p, []byte(testTemplate)); err != nil || !changed {
		t.Fatalf("new: %v %v", changed, err)
	}
	if data, _ := os.ReadFile(p); string(data) != testTemplate {
		t.Fatalf("new file:\n%s", data)
	}

	// Старый файл: значение пользователя остаётся, недостающее дописывается с пояснением.
	p2 := filepath.Join(dir, "config.yaml")
	old := "modules:\n  recorder:\n    hotkey: \"{F9}\"\n  hotkeys:\n    enabled: false\n"
	if err := os.WriteFile(p2, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := Complete(p2, []byte(testTemplate)); err != nil || !changed {
		t.Fatalf("old: %v %v", changed, err)
	}
	data, _ := os.ReadFile(p2)
	text := string(data)
	for _, want := range []string{`hotkey: "{F9}"`, "# Сочетание записи.", "# Движения мыши.", "moves: true", "enabled: false", `language: ""`} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "RAlt") {
		t.Errorf("user value replaced:\n%s", text)
	}

	// Всё есть — файл не меняется.
	if changed, err := Complete(p2, []byte(testTemplate)); err != nil || changed {
		t.Fatalf("complete: %v %v", changed, err)
	}

	// Ошибка в файле — ошибка, файл не трогается.
	p3 := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(p3, []byte("modules: [\n"), 0o600)
	if _, err := Complete(p3, []byte(testTemplate)); err == nil {
		t.Fatal("broken file accepted")
	}
}
