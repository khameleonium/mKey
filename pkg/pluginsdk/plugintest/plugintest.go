// Package plugintest — тестовый mKey для проверки плагинов (FR-PLG-9): запускает плагин (файл
// или pluginsdk.Plugin в том же процессе), говорит с ним по протоколу, как настоящий mKey, и
// запоминает его запросы (нажатия, переменные, уведомления). Conformance проверяет
// соответствие протоколу.
//
//	func TestPlugin(t *testing.T) {
//		h := plugintest.Start(t, "./my-plugin")
//		plugintest.Conformance(t, h)
//		h.Initialize("ru", "output.send")
//		if err := h.Action(context.Background(), "hello", nil); err != nil { t.Fatal(err) }
//		if got := h.Sent(); len(got) != 1 { t.Fatalf("sent %v", got) }
//	}
package plugintest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"mkey/internal/lib/jsonrpc"
	"mkey/pkg/pluginsdk"
)

// Init — ответ плагина на initialize: его виды.
type Init struct {
	Actions    []pluginsdk.Type `json:"actions"`
	Conditions []pluginsdk.Type `json:"conditions"`
	Triggers   []pluginsdk.Type `json:"triggers"`
}

// Host — тестовый mKey.
type Host struct {
	t    testing.TB
	conn *jsonrpc.Conn
	cmd  *exec.Cmd
	// exited закрывается, когда процесс плагина завершился (nil — плагин в этом процессе).
	exited chan struct{}

	// mu защищает всё ниже.
	mu     sync.Mutex
	perms  []string
	sent   []string
	notes  []string
	vars   map[string]any
	fires  map[string]chan map[string]any
	nextID int
}

// Start запускает исполняемый файл плагина path с аргументами args. Плагин останавливается
// в конце теста.
func Start(t testing.TB, path string, args ...string) *Host {
	t.Helper()
	cmd := exec.Command(path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	h := newHost(t, stdout, stdin)
	h.cmd, h.exited = cmd, make(chan struct{})
	go func() { _ = cmd.Wait(); close(h.exited) }()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case <-h.exited:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return h
}

// Serve запускает плагин p в этом же процессе (через каналы в памяти).
func Serve(t testing.TB, p *pluginsdk.Plugin) *Host {
	t.Helper()
	pr, hw := io.Pipe()
	hr, pw := io.Pipe()
	go func() { _ = p.Serve(pr, pw); _ = pw.Close() }()
	h := newHost(t, hr, hw)
	t.Cleanup(func() { _ = hw.Close() })
	return h
}

// newHost создаёт тестовый mKey поверх потоков плагина.
func newHost(t testing.TB, r io.Reader, w io.Writer) *Host {
	h := &Host{t: t, vars: map[string]any{}, fires: map[string]chan map[string]any{}}
	h.conn = jsonrpc.New(r, w, h.serve)
	return h
}

// serve отвечает на запросы плагина как mKey, проверяя разрешения.
func (h *Host) serve(_ context.Context, method string, params json.RawMessage, _ bool) (any, error) {
	var a struct {
		Handle  string         `json:"handle"`
		Vars    map[string]any `json:"vars"`
		Macro   string         `json:"macro"`
		Project string         `json:"project"`
		Name    string         `json:"name"`
		Value   any            `json:"value"`
		Title   string         `json:"title"`
		Body    string         `json:"body"`
		Level   string         `json:"level"`
		Message string         `json:"message"`
	}
	if err := json.Unmarshal(params, &a); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	need := func(perm string) error {
		if !slices.Contains(h.perms, perm) {
			return jsonrpc.Errorf(jsonrpc.CodeForbidden, "permission %q is not declared", perm)
		}
		return nil
	}
	switch method {
	case "trigger.fire":
		ch, ok := h.fires[a.Handle]
		if !ok {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown trigger handle %q", a.Handle)
		}
		select {
		case ch <- a.Vars:
		default:
		}
		return nil, nil
	case "mkey.log":
		h.t.Logf("plugin %s: %s", a.Level, a.Message)
		return nil, nil
	case "mkey.send":
		if err := need("output.send"); err != nil {
			return nil, err
		}
		h.sent = append(h.sent, a.Macro)
		return nil, nil
	case "mkey.vars.get":
		if err := need("vars.read"); err != nil {
			return nil, err
		}
		return map[string]any{"value": h.vars[a.Project+"/"+a.Name]}, nil
	case "mkey.vars.set":
		if err := need("vars.write"); err != nil {
			return nil, err
		}
		h.vars[a.Project+"/"+a.Name] = a.Value
		return nil, nil
	case "mkey.notify":
		if err := need("notify"); err != nil {
			return nil, err
		}
		h.notes = append(h.notes, a.Title+": "+a.Body)
		return nil, nil
	}
	return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "method %q not found", method)
}

// call вызывает метод плагина с таймаутом timeout.
func (h *Host) call(ctx context.Context, timeout time.Duration, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return h.conn.Call(ctx, method, params, result)
}

// Initialize знакомится с плагином (язык lang, разрешения perms) и возвращает его виды; ошибка
// или ответ дольше 5 с — провал теста.
func (h *Host) Initialize(lang string, perms ...string) Init {
	h.t.Helper()
	h.mu.Lock()
	h.perms = perms
	h.mu.Unlock()
	var res Init
	if err := h.call(context.Background(), 5*time.Second, "initialize", map[string]any{
		"plugin_api": pluginsdk.APIVersion, "mkey_version": "test", "lang": lang, "permissions": perms,
	}, &res); err != nil {
		h.t.Fatalf("initialize: %v", err)
	}
	return res
}

// Action выполняет действие typ с параметрами params.
func (h *Host) Action(ctx context.Context, typ string, params any) error {
	return h.conn.Call(ctx, "action.run", map[string]any{"type": typ, "params": params, "event": map[string]string{"project": "test", "event": "test"}}, nil)
}

// Validate проверяет параметры вида: kind — "action", "condition" или "trigger".
func (h *Host) Validate(kind, typ string, params any) error {
	err := h.call(context.Background(), 2*time.Second, kind+".validate", map[string]any{"type": typ, "params": params}, nil)
	var e *jsonrpc.Error
	if errors.As(err, &e) && e.Code == jsonrpc.CodeMethodNotFound {
		return nil
	}
	return err
}

// Check вычисляет условие typ (не дольше 2 с, как в mKey).
func (h *Host) Check(ctx context.Context, typ string, params any) (bool, error) {
	var res struct {
		Result bool `json:"result"`
	}
	err := h.call(ctx, 2*time.Second, "condition.check", map[string]any{"type": typ, "params": params, "event": map[string]string{"project": "test", "event": "test"}}, &res)
	return res.Result, err
}

// Arm взводит триггер typ; срабатывания приходят в канал (значения vars).
func (h *Host) Arm(typ string, params any) (string, <-chan map[string]any) {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	handle := "t" + strconv.Itoa(h.nextID)
	ch := make(chan map[string]any, 16)
	h.fires[handle] = ch
	h.mu.Unlock()
	if err := h.call(context.Background(), 2*time.Second, "trigger.arm", map[string]any{"type": typ, "params": params, "handle": handle}, nil); err != nil {
		h.t.Fatalf("trigger.arm %s: %v", typ, err)
	}
	return handle, ch
}

// Disarm снимает триггер.
func (h *Host) Disarm(handle string) {
	h.t.Helper()
	if err := h.call(context.Background(), 2*time.Second, "trigger.disarm", map[string]any{"handle": handle}, nil); err != nil {
		h.t.Fatalf("trigger.disarm: %v", err)
	}
}

// Sent возвращает макросы, которые плагин просил нажать.
func (h *Host) Sent() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.sent)
}

// Notes возвращает уведомления плагина («заголовок: текст»).
func (h *Host) Notes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.notes)
}

// Var возвращает переменную, которую плагин записал (mkey.vars.set).
func (h *Host) Var(project, name string) any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vars[project+"/"+name]
}

// Conformance проверяет соответствие протоколу: initialize отвечает за 5 с, ID видов непусты
// и уникальны, схемы параметров — JSON-объекты, ping отвечает, неизвестный метод — -32601,
// shutdown отвечает и (для процесса) плагин завершается за 2 с. Вызывать на свежем Host.
func Conformance(t *testing.T, h *Host) {
	t.Helper()

	// initialize: виды и схемы.
	res := h.Initialize("en")
	seen := map[string]bool{}
	for _, list := range [][]pluginsdk.Type{res.Actions, res.Conditions, res.Triggers} {
		for _, ty := range list {
			if ty.ID == "" || seen[ty.ID] {
				t.Errorf("type id %q is empty or used twice", ty.ID)
			}
			seen[ty.ID] = true
			if len(ty.Params) > 0 {
				var schema map[string]any
				if err := json.Unmarshal(ty.Params, &schema); err != nil {
					t.Errorf("type %s: params_schema is not a JSON object: %v", ty.ID, err)
				}
			}
		}
	}

	// ping и неизвестный метод.
	if err := h.call(context.Background(), 2*time.Second, "ping", nil, nil); err != nil {
		t.Errorf("ping: %v", err)
	}
	var e *jsonrpc.Error
	if err := h.call(context.Background(), 2*time.Second, "no.such.method", nil, nil); !errors.As(err, &e) || e.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("unknown method: want error -32601, got %v", err)
	}

	// shutdown: ответ и завершение процесса.
	if err := h.call(context.Background(), 2*time.Second, "shutdown", nil, nil); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if h.exited != nil {
		select {
		case <-h.exited:
		case <-time.After(2 * time.Second):
			t.Error("plugin did not exit within 2 s after shutdown")
		}
	}
}
