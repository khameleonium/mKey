package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"mkey/internal/bus"
	"mkey/internal/contracts"
)

// tarball — архив .tar.gz с одним файлом.
func tarball(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// fakeLife запоминает перезапуск.
type fakeLife struct {
	contracts.Lifecycle
	exe chan string
}

// Restart сообщает, какую программу запустить.
func (f *fakeLife) Restart(exe string) { f.exe <- exe }

// newRig — модуль с поддельным GitHub: выпуск 9.9.9 с архивом и суммами (badSum — неверная сумма).
func newRig(t *testing.T, badSum bool) (*Module, *fakeLife, string) {
	t.Helper()
	home := t.TempDir()
	exe := filepath.Join(home, ".local", "bin", "mkey")
	_ = os.MkdirAll(filepath.Dir(exe), 0o700)
	_ = os.WriteFile(exe, []byte("old"), 0o755)

	archive := fmt.Sprintf("mkey_9.9.9_linux_%s.tar.gz", runtime.GOARCH)
	tgz := tarball(t, "mkey", []byte("new"))
	sum := sha256.Sum256(tgz)
	sums := hex.EncodeToString(sum[:]) + "  " + archive + "\n"
	if badSum {
		sums = "0000  " + archive + "\n"
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v9.9.9","html_url":"https://x/r","assets":[{"name":%q,"browser_download_url":"%s/a"},{"name":"checksums.txt","browser_download_url":"%s/s"}]}`, archive, srv.URL, srv.URL)
		case "/a":
			_, _ = w.Write(tgz)
		case "/s":
			_, _ = io.WriteString(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	life := &fakeLife{exe: make(chan string, 1)}
	m := New()
	m.log, m.bus = slog.New(slog.NewTextHandler(io.Discard, nil)), bus.New(4)
	m.cfg.Repo, m.apiBase, m.exe, m.home, m.version, m.gui = "o/r", srv.URL, exe, home, "1.0.0", true
	m.life = life
	return m, life, exe
}

// TestApply проверяет обновление: новая версия найдена, архив проверен, программа заменена
// (старая — mkey.old), перезапуск новой программой.
func TestApply(t *testing.T) {
	t.Parallel()
	m, life, exe := newRig(t, false)
	info, err := m.Check(context.Background())
	if err != nil || !info.Available || info.Latest != "9.9.9" || !info.CanApply {
		t.Fatalf("check: %+v %v", info, err)
	}
	if _, err := m.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("old = %q", b)
	}
	if got := <-life.exe; got != exe {
		t.Fatalf("restart %s", got)
	}
}

// TestBadChecksum проверяет, что при неверной сумме программа не меняется.
func TestBadChecksum(t *testing.T) {
	t.Parallel()
	m, _, exe := newRig(t, true)
	if _, err := m.Apply(context.Background()); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("exe changed: %q", b)
	}
}

// TestCannotApply проверяет отказ для установки пакетом и сборки разработчика.
func TestCannotApply(t *testing.T) {
	t.Parallel()
	m, _, _ := newRig(t, false)
	m.exe = "/usr/bin/mkey"
	if _, err := m.Apply(context.Background()); !errors.Is(err, contracts.ErrCannotUpdate) || m.Info().Reason != contracts.UpdatePackage {
		t.Fatalf("package: %v %+v", err, m.Info())
	}
	m2, _, _ := newRig(t, false)
	m2.version = "dev"
	if info, _ := m2.Check(context.Background()); info.CanApply || info.Reason != contracts.UpdateDev {
		t.Fatalf("dev: %+v", info)
	}
}

// TestNoReleases проверяет, что отсутствие выпусков — не ошибка, а «новых версий нет».
func TestNoReleases(t *testing.T) {
	t.Parallel()
	m, _, _ := newRig(t, false)
	m.cfg.Repo = "o/none"
	info, err := m.Check(context.Background())
	if err != nil || info.Available || info.Latest != "" {
		t.Fatalf("check: %+v %v", info, err)
	}
}
