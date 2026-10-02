package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestMigrate проверяет миграцию: копия старого файла, шаг, новая версия, комментарии сохранены;
// текущая версия — ничего не делается; более новая — ошибка.
func TestMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	old := "# мои настройки\nversion: 1\nmodules:\n  recorder:\n    kinds: [keyboard]\n"
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	// Шаг v1 → v2: переименовать modules.recorder.kinds в devices (пример).
	steps := map[int]Step{1: func(root *yaml.Node) error {
		_, mods := lookup(root, "modules")
		k, _ := lookup(mods.Content[1], "kinds")
		k.Value = "devices"
		return nil
	}}
	backup, err := migrate(p, 2, steps)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != old || !strings.HasSuffix(backup, ".v1.bak") {
		t.Fatalf("backup %s = %q", backup, b)
	}
	data, _ := os.ReadFile(p)
	for _, want := range []string{"# мои настройки", "version: 2", "devices: [keyboard]"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no %q in:\n%s", want, data)
		}
	}

	// Повторно — версия текущая, ничего не делается; новее программы — ошибка.
	if b, err := migrate(p, 2, steps); err != nil || b != "" {
		t.Fatalf("second: %q %v", b, err)
	}
	if _, err := migrate(p, 1, nil); err == nil {
		t.Fatal("newer file accepted")
	}

	// Файла нет — ничего не делается.
	if b, err := Migrate(filepath.Join(dir, "none.yaml")); err != nil || b != "" {
		t.Fatalf("missing: %q %v", b, err)
	}
}

// TestNoMoveMerging проверяет шаг v1 → v2: прежние значения по умолчанию склейки движений
// (8 и 4 мс) становятся 0, выбранные пользователем значения и комментарии остаются.
func TestNoMoveMerging(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, merge, coalesce string }{
		{"modules:\n  recorder:\n    merge_moves_ms: 8 # склейка\n    coalesce_ms: 4\n", "merge_moves_ms: 0 # склейка", "coalesce_ms: 0"},
		{"modules:\n  recorder:\n    merge_moves_ms: 20\n    coalesce_ms: 0\n", "merge_moves_ms: 20", "coalesce_ms: 0"},
		{"modules:\n  api:\n    port: 1\n", "", ""},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte("version: 1\n"+tc.in), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Migrate(p); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		for _, want := range []string{"version: 2", tc.merge, tc.coalesce} {
			if !strings.Contains(string(data), want) {
				t.Errorf("no %q in:\n%s", want, data)
			}
		}
	}
}
