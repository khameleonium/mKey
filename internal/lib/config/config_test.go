package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDefaults проверяет настройки по умолчанию при отсутствии файла.
func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || c.Version != CurrentVersion || !c.Enabled("recorder") || c.Section("engine") != nil {
		t.Fatalf("defaults = %+v, %v", c, err)
	}
}

// TestParse проверяет язык, включение модулей и секции настроек.
func TestParse(t *testing.T) {
	t.Parallel()
	src := `
version: 1
language: ru
modules:
  desktop:
    enabled: false
  engine:
    key_hold_ms: 30
    key_delay_ms: 5
`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.Language != "ru" || c.Enabled("desktop") || !c.Enabled("engine") {
		t.Fatalf("config = %+v", c)
	}
	if got := string(c.Section("engine")); got != `{"key_delay_ms":5,"key_hold_ms":30}` {
		t.Fatalf("engine section = %s", got)
	}
	if c.Section("desktop") != nil {
		t.Fatal("section with only enabled must be empty")
	}
}

// TestParseErrors проверяет опечатки в ключах, новую версию и пустой файл.
func TestParseErrors(t *testing.T) {
	t.Parallel()
	if _, err := Parse([]byte("languge: ru\n")); err == nil {
		t.Error("unknown key must fail")
	}
	if _, err := Parse([]byte("version: 99\n")); err == nil {
		t.Error("newer version must fail")
	}
	if c, err := Parse(nil); err != nil || c.Version != CurrentVersion {
		t.Errorf("empty file: %+v, %v", c, err)
	}

	// Файл на диске.
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte("language: en\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.Language != "en" {
		t.Errorf("Load = %+v, %v", c, err)
	}
}
