package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriterAndUndo проверяет запись файлов и блоков с учётом в манифесте и полное удаление.
func TestWriterAndUndo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m, err := Load(filepath.Join(dir, "data", FileName))
	if err != nil {
		t.Fatal(err)
	}
	w := Writer{M: m, Owner: "test"}

	// Файл mKey и блок в чужом файле.
	file := filepath.Join(dir, "apps", "mkey.desktop")
	conf := filepath.Join(dir, "sway", "config")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("bindsym x exec y\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFile(file, []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.SetBlock(conf, "#", []string{"exec mkey daemon"}); err != nil {
		t.Fatal(err)
	}

	// Манифест на диске перечисляет обе записи; права чужого файла сохранены.
	again, err := Load(m.Path())
	if err != nil || len(again.List()) != 2 {
		t.Fatalf("reloaded manifest = %+v, %v", again, err)
	}
	if fi, _ := os.Stat(conf); fi.Mode().Perm() != 0o640 {
		t.Fatalf("conf perm = %o", fi.Mode().Perm())
	}

	// Undo удаляет файл и блок, чужой файл возвращается к исходному виду.
	if errs := again.Undo(nil); len(errs) != 0 {
		t.Fatalf("Undo: %v", errs)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("file must be removed")
	}
	if data, _ := os.ReadFile(conf); string(data) != "bindsym x exec y\n" {
		t.Fatalf("conf = %q", data)
	}
	if len(again.List()) != 0 {
		t.Fatalf("entries left: %+v", again.List())
	}
}

// TestUndoKeep проверяет выборочное удаление (например, «сохранить настройки»).
func TestUndoKeep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m, _ := Load(filepath.Join(dir, FileName))
	w := Writer{M: m, Owner: "a"}
	keep := filepath.Join(dir, "keep.txt")
	drop := filepath.Join(dir, "drop.txt")
	_ = w.WriteFile(keep, []byte("k"), 0o600)
	_ = w.WriteFile(drop, []byte("d"), 0o600)
	m.Undo(func(e Entry) bool { return e.Path == keep })
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("kept file removed")
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Fatal("dropped file remains")
	}
}
