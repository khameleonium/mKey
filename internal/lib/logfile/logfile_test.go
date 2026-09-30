package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotation проверяет ротацию по размеру и число хранимых файлов.
func TestRotation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "mkey.log")
	w, err := Open(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Пять записей по 6 байт: каждая вторая вызывает ротацию.
	for _, s := range []string{"aaaaa\n", "bbbbb\n", "ccccc\n", "ddddd\n", "eeeee\n"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	// Текущий файл — последняя запись, .1 и .2 — предыдущие, .3 нет.
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	if read(path) != "eeeee\n" || read(path+".1") != "ddddd\n" || read(path+".2") != "ccccc\n" {
		t.Fatalf("files: %q %q %q", read(path), read(path+".1"), read(path+".2"))
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal(".3 must not exist")
	}

	// Права файла — только владелец.
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 || !strings.HasSuffix(path, "mkey.log") {
		t.Fatalf("perm = %o", fi.Mode().Perm())
	}
}
