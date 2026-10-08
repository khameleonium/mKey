//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/lib/bundle"
)

// buildMkey собирает консольную программу mkey во временную папку (как `make build-cli`).
func buildMkey(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "mkey")
	cmd := exec.Command("go", "build", "-tags", "nogui", "-o", exe, "./cmd/mkey")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return exe
}

// pack дописывает к программе exe проект project (режим mode, событие event) и записывает файл
// макроса out с правами на запуск.
func pack(t *testing.T, exe, out, project, mode, event string, files map[string][]byte) {
	t.Helper()
	src, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = bundle.Write(&buf, bytes.NewReader(src), int64(len(src)), bundle.Contents{
		Manifest: bundle.Manifest{Format: bundle.Format, Name: "Проверка", Project: "check", Mode: mode, Event: event},
		Project:  []byte(project),
		Files:    files,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolated — окружение запуска: своя «домашняя» папка, без настоящих устройств (только для
// проверок), файл-результат для скрипта.
func isolated(t *testing.T, result string) []string {
	home := t.TempDir()
	return append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home+"/c", "XDG_DATA_HOME="+home+"/d",
		"XDG_STATE_HOME="+home+"/s", "MKEY_FAKE_BACKENDS=1", "MKEY_TEST_OUT="+result, "LANG=en_US.UTF-8")
}

// TestStandaloneMacro проверяет самостоятельный файл макроса (FR-BUILD-1, ADR-0042) без настоящих
// устройств: «выполнить и выйти» выполняет событие (скрипт bash из файла внутри пишет результат) и
// завершается с кодом 0; «работать, как проект» работает до Ctrl+C; --info показывает сведения.
func TestStandaloneMacro(t *testing.T) {
	exe := buildMkey(t)
	dir := t.TempDir()
	project := "version: 1\nname: Проверка\nenabled: false\nevents:\n" +
		"  - id: go\n    trigger: {type: manual}\n    actions:\n      - send: '{A}'\n      - shell: {file: write.sh}\n"
	script := map[string][]byte{bundle.ShellDir + "/write.sh": []byte("#!/bin/sh\necho done > \"$MKEY_TEST_OUT\"\n")}

	// «Выполнить и выйти»: скрипт из файла внутри выполнен.
	once := filepath.Join(dir, "once")
	pack(t, exe, once, project, bundle.ModeOnce, "go", script)
	result := filepath.Join(dir, "result.txt")
	cmd := exec.Command(once)
	cmd.Env = isolated(t, result)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("once: %v\n%s", err, out)
	}
	if b, err := os.ReadFile(result); err != nil || strings.TrimSpace(string(b)) != "done" {
		t.Fatalf("result = %q, %v", b, err)
	}

	// Сведения о файле.
	info := exec.Command(once, "--info")
	info.Env = isolated(t, result)
	if out, err := info.Output(); err != nil || !strings.Contains(string(out), "Проверка") || !strings.Contains(string(out), "write.sh") {
		t.Fatalf("info: %v\n%s", err, out)
	}

	// «Работать, как проект»: не выходит сам, Ctrl+C — выход с кодом 0.
	events := filepath.Join(dir, "events")
	pack(t, exe, events, project, bundle.ModeEvents, "", nil)
	run := exec.Command(events)
	run.Env = isolated(t, result)
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("events mode exited by itself: %v", err)
	case <-time.After(2 * time.Second):
	}
	_ = run.Process.Signal(syscall.SIGINT)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("events mode after Ctrl+C: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = run.Process.Kill()
		t.Fatal("events mode did not stop on Ctrl+C")
	}
}
