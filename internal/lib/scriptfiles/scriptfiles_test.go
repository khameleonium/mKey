package scriptfiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestFolder проверяет файлы скриптов: запись создаёт папку и заменяет файл целиком, список —
// только файлы языка по имени, недопустимые имена отклоняются, удаление.
func TestFolder(t *testing.T) {
	t.Parallel()
	f := Folder{Dir: filepath.Join(t.TempDir(), "scripts"), Ext: ".lua"}

	// Папки нет — пустой список.
	if files, err := f.Files(); err != nil || len(files) != 0 {
		t.Fatalf("empty: %v %v", files, err)
	}

	// Запись и перезапись; чужие файлы не попадают в список.
	for _, c := range []struct{ name, data string }{{"b.lua", "x = 1"}, {"a.lua", "y = 2"}, {"b.lua", "x = 3"}} {
		if err := f.Write(c.name, []byte(c.data)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(f.Dir, "run.sh"), []byte("echo"), 0o600)
	files, err := f.Files()
	if err != nil || len(files) != 2 || files[0].Name != "a.lua" || files[1].Name != "b.lua" || files[1].Size != 5 {
		t.Fatalf("files = %+v %v", files, err)
	}
	if data, err := f.Read("b.lua"); err != nil || string(data) != "x = 3" {
		t.Fatalf("read = %q %v", data, err)
	}

	// Недопустимые имена — ErrName, и ничего не записано.
	for _, bad := range []string{"", ".hidden.lua", "../up.lua", "dir/x.lua", "x.sh", ".lua", "a\nb.lua"} {
		if err := f.Write(bad, []byte("1")); !errors.Is(err, ErrName) {
			t.Errorf("Write(%q) = %v", bad, err)
		}
		if _, err := f.Read(bad); !errors.Is(err, ErrName) {
			t.Errorf("Read(%q) = %v", bad, err)
		}
	}

	// Удаление; удалённого нет.
	if err := f.Delete("a.lua"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read("a.lua"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read deleted = %v", err)
	}
}
