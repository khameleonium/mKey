package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// fakeRC — контекст выполнения с переменными; журнал — в log (если задан).
type fakeRC struct {
	vars map[string]any
	log  io.Writer
}

func (f fakeRC) Event() contracts.EventRef {
	return contracts.EventRef{Project: "games", Event: "click"}
}
func (f fakeRC) Send(context.Context, string) error                       { return nil }
func (f fakeRC) RunActions(context.Context, []project.Action) error       { return nil }
func (f fakeRC) Check(context.Context, []project.Condition) (bool, error) { return true, nil }
func (f fakeRC) Toggled() bool                                            { return false }
func (f fakeRC) Held() bool                                               { return false }
func (f fakeRC) Fire() contracts.Fire                                     { return contracts.Fire{} }
func (f fakeRC) Vars() contracts.VarStore                                 { return fakeVars(f.vars) }
func (f fakeRC) Logger() *slog.Logger {
	if f.log == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return slog.New(slog.NewTextHandler(f.log, nil))
}

// fakeVars — переменные только для чтения.
type fakeVars map[string]any

func (v fakeVars) Get(n string) (any, bool)  { x, ok := v[n]; return x, ok }
func (v fakeVars) Set(string, any) error     { return nil }
func (v fakeVars) Add(string, float64) error { return nil }
func (v fakeVars) All() map[string]any       { return maps.Clone(v) }

// newAction создаёт действие с каталогом скриптов dir.
func newAction(dir string) shellAction {
	return shellAction{m: &Module{cfg: Config{ScriptsDir: dir, TimeoutMS: 5000, Shell: "sh"}, socket: "/run/x/mkey.sock"}}
}

// TestEnvAndOutput проверяет окружение скрипта: событие, сокет и переменные.
func TestEnvAndOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	a := newAction(dir)
	code := `echo "$MKEY_PROJECT/$MKEY_EVENT $MKEY_SOCKET $MKEY_VAR_CLICKS $MKEY_VAR_MY_FLAG" > ` + out
	if err := a.Run(context.Background(), fakeRC{vars: map[string]any{"clicks": int64(7), "my-flag": true}}, project.Action{Value: code}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if strings.TrimSpace(string(data)) != "games/click /run/x/mkey.sock 7 true" {
		t.Fatalf("env = %q", data)
	}
}

// TestOutputLogged: вывод скрипта попадает в журнал построчно — stdout сведениями, stderr
// предупреждениями, последняя строка без перевода строки тоже.
func TestOutputLogged(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	a := newAction(t.TempDir())
	if err := a.Run(context.Background(), fakeRC{log: &log}, project.Action{Value: "echo out-line; echo err-line >&2; printf tail"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`level=INFO msg="shell: out-line"`, `level=WARN msg="shell: err-line"`, `level=INFO msg="shell: tail"`} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log has no %s:\n%s", want, log.String())
		}
	}
}

// TestFileAndExitCode проверяет запуск файла из каталога скриптов и ошибку при ненулевом коде выхода.
func TestFileAndExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fail.sh"), []byte("exit 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAction(dir)
	err := a.Run(context.Background(), fakeRC{}, project.Action{Value: map[string]any{"file": "../../fail.sh"}})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
}

// TestCancelKillsGroup проверяет, что остановка прерывает скрипт вместе с дочерними процессами.
func TestCancelKillsGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-alive")
	a := newAction(dir)

	// Скрипт запускает дочерний процесс, который через 1 с создал бы файл, и ждёт.
	code := `(sleep 1; touch ` + marker + `) & sleep 30`
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	err := a.Run(ctx, fakeRC{}, project.Action{Value: code})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 3*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}

	// Дочерний процесс тоже убит — файл не появился.
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child process survived the stop")
	}
}

// TestTimeout проверяет ограничение времени.
func TestTimeout(t *testing.T) {
	t.Parallel()
	a := newAction(t.TempDir())
	err := a.Run(context.Background(), fakeRC{}, project.Action{Value: map[string]any{"code": "sleep 30", "timeout_ms": 100}})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v", err)
	}
}

// TestValidate проверяет параметры.
func TestValidate(t *testing.T) {
	t.Parallel()
	a := newAction("")
	if err := a.Validate(project.Action{Value: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"code": "a", "file": "b"}, 5} {
		if err := a.Validate(project.Action{Value: bad}); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

// TestBackgroundProgram: программа, запущенная скриптом в фоне («… &»), держит вывод скрипта
// открытым. Действие не ждёт её: завершается сразу после скрипта без ошибки, а программа
// продолжает работать (раньше действие висело до таймаута, а таймаут убивал программу).
func TestBackgroundProgram(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	a := newAction(dir)
	start := time.Now()
	if err := a.Run(context.Background(), fakeRC{}, project.Action{Value: "sleep 30 & echo $! > " + pidFile}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("action waited for the background program: %v", took)
	}
	data, err := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file: %q %v", data, err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("background program was killed: %v", err)
	}
}

// TestLineLog проверяет журнал вывода: строки по переводу строки, хвост без перевода строки —
// при завершении, слишком длинная строка — обрезанной, без её хвоста.
func TestLineLog(t *testing.T) {
	t.Parallel()
	var got []string
	l := &lineLog{log: func(s string) { got = append(got, s) }}
	long := strings.Repeat("x", maxLogLine+500)
	for _, chunk := range []string{"one\ntw", "o\n", long[:1500], long[1500:] + "\nthree", "\n", "tail"} {
		if n, err := l.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	l.stop()
	_, _ = l.Write([]byte("after stop\n"))
	want := []string{"one", "two", strings.Repeat("x", maxLogLine) + "…", "three", "tail"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", got)
	}
}

// TestBackgroundProgramWritesLater: фоновая программа, которая пишет в вывод уже после
// завершения действия, продолжает работать (вывод дочитывается, SIGPIPE она не получает).
func TestBackgroundProgramWritesLater(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark")
	a := newAction(dir)
	code := "(sleep 1; echo late; echo late >&2; echo ok > " + mark + ") &"
	if err := a.Run(context.Background(), fakeRC{}, project.Action{Value: code}); err != nil {
		t.Fatalf("run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(mark); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("background program died after writing to the output")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
