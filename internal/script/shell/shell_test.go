package shell

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// fakeRC — контекст выполнения с переменными.
type fakeRC struct{ vars map[string]any }

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
func (f fakeRC) Logger() *slog.Logger                                     { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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
