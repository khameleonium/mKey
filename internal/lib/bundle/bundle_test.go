package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample — содержимое для проверок: проект, запись и скрипты.
func sample() Contents {
	return Contents{
		Manifest: Manifest{Format: Format, Name: "Игра", Project: "game", Mode: ModeOnce, Event: "go", Version: "v1", Creator: "c"},
		Project:  []byte("# комментарий\nversion: 1\nname: Игра\n"),
		Files: map[string][]byte{
			RecordingsDir + "/бег.mkrec": []byte("mkrec 1\n"),
			LuaDir + "/a.lua":            []byte("print(1)"),
			ShellDir + "/b.sh":           []byte("echo 1"),
		},
	}
}

// TestRoundTrip: программа + содержимое → файл; программа — без изменений в начале, содержимое
// читается обратно; повторная сборка из собранного файла заменяет содержимое, а не копит его.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	exe := []byte("\x7fELF program bytes")
	var out bytes.Buffer
	if err := Write(&out, bytes.NewReader(exe), int64(len(exe)), sample()); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), exe) {
		t.Fatal("program bytes changed")
	}
	c, err := Read(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil || c.Manifest.Name != "Игра" || string(c.Project) != string(sample().Project) || len(c.Files) != 3 {
		t.Fatalf("read = %+v, %v", c, err)
	}

	// Пересборка: другое содержимое, программа та же.
	again := sample()
	again.Manifest.Mode, again.Manifest.Event = ModeEvents, ""
	again.Files = nil
	var out2 bytes.Buffer
	if err := Write(&out2, bytes.NewReader(out.Bytes()), int64(out.Len()), again); err != nil {
		t.Fatal(err)
	}
	c2, err := Read(bytes.NewReader(out2.Bytes()), int64(out2.Len()))
	if err != nil || c2.Manifest.Mode != ModeEvents || len(c2.Files) != 0 || !bytes.HasPrefix(out2.Bytes(), exe) || bytes.Count(out2.Bytes(), []byte(magic)) != 1 {
		t.Fatalf("rebuild = %+v, %v", c2, err)
	}
}

// TestNoBundleAndDamage: обычная программа — ErrNoBundle; испорченный хвост или пути — ошибки.
func TestNoBundleAndDamage(t *testing.T) {
	t.Parallel()
	plain := []byte("plain program without a bundle, long enough")
	if _, err := Read(bytes.NewReader(plain), int64(len(plain))); !errors.Is(err, ErrNoBundle) {
		t.Fatalf("plain = %v", err)
	}

	// Неверный путь файла и неверные сведения не записываются.
	bad := sample()
	bad.Files["../../etc/passwd"] = []byte("x")
	if err := Write(&bytes.Buffer{}, bytes.NewReader(plain), int64(len(plain)), bad); err == nil {
		t.Fatal("unsafe path written")
	}
	once := sample()
	once.Manifest.Event = ""
	if err := Write(&bytes.Buffer{}, bytes.NewReader(plain), int64(len(plain)), once); err == nil {
		t.Fatal("once without event written")
	}

	// Хвост с неверной длиной.
	var out bytes.Buffer
	_ = Write(&out, bytes.NewReader(plain), int64(len(plain)), sample())
	data := out.Bytes()
	data[len(data)-1] = 0x7f
	if _, err := Read(bytes.NewReader(data), int64(len(data))); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("damaged = %v", err)
	}
}

// TestFileAndExtract: чтение из файла на диске и раскладка по папкам (скрипт bash — исполняемый).
func TestFileAndExtract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := []byte("program")
	f, err := os.Create(filepath.Join(dir, "macro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(f, bytes.NewReader(exe), int64(len(exe)), sample()); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	c, err := ReadFile(filepath.Join(dir, "macro"))
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{RecordingsDir: filepath.Join(dir, "rec"), LuaDir: filepath.Join(dir, "lua"), ShellDir: filepath.Join(dir, "sh")}
	if err := Extract(c, dirs); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "rec", "бег.mkrec")); err != nil || string(b) != "mkrec 1\n" {
		t.Fatalf("recording = %q %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(dir, "sh", "b.sh")); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("shell script = %v %v", st, err)
	}
}
