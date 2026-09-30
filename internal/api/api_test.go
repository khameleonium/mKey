package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/lib/dsl"
)

// fakeRunner — исполнитель макросов, записывающий запуски.
type fakeRunner struct {
	mu      sync.Mutex
	runs    []string
	err     error
	stopped int
}

func (f *fakeRunner) Run(_ context.Context, src string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := dsl.Parse(src); err != nil {
		return err
	}
	f.runs = append(f.runs, src)
	return f.err
}
func (f *fakeRunner) StopAll()     { f.mu.Lock(); f.stopped++; f.mu.Unlock() }
func (f *fakeRunner) Running() int { return 0 }

// newTestModule создаёт модуль API с фейковым исполнителем и переводчиком.
func newTestModule(t *testing.T) (*Module, *fakeRunner) {
	t.Helper()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	m := New(fstest.MapFS{"index.html": {Data: []byte("<h1>mKey</h1>")}})
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.tr = i18n.New(cat, "en")
	m.svc.runner = runner
	return m, runner
}

// call выполняет запрос к обработчику и разбирает JSON-ответ.
func call(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestSendAndErrors проверяет выполнение макроса и формат ошибок с переводом.
func TestSendAndErrors(t *testing.T) {
	t.Parallel()
	m, runner := newTestModule(t)
	h := m.routes(true)

	// Правильный макрос выполняется.
	code, out := call(t, h, "POST", "/api/v1/send", `{"sequence":"{A}"}`, nil)
	if code != 200 || out["ok"] != true || len(runner.runs) != 1 {
		t.Fatalf("send: %d %v runs=%v", code, out, runner.runs)
	}

	// Ошибка в макросе: 400, код, позиция, сообщение на языке клиента.
	code, out = call(t, h, "POST", "/api/v1/send", `{"sequence":"{Mous0}"}`, map[string]string{"Accept-Language": "ru-RU,ru;q=0.9"})
	e := out["error"].(map[string]any)
	if code != 400 || e["code"] != dsl.ErrUnknownKeyHint || !strings.Contains(e["message"].(string), "Mouse0") || !strings.Contains(e["message"].(string), "клавиша") {
		t.Fatalf("dsl error: %d %v", code, out)
	}
	pos := e["details"].(map[string]any)["pos"].(map[string]any)
	if pos["line"] != 1.0 || pos["col"] != 2.0 {
		t.Fatalf("pos = %v", pos)
	}

	// Сухой прогон ничего не выполняет, но возвращает план.
	code, out = call(t, h, "POST", "/api/v1/send", `{"sequence":"{A}[10]","dry_run":true}`, nil)
	if code != 200 || len(out["steps"].([]any)) != 2 || len(runner.runs) != 1 {
		t.Fatalf("dry run: %d %v", code, out)
	}

	// Остановленный макрос — 409; неверный JSON — 400.
	runner.err = context.Canceled
	if code, _ := call(t, h, "POST", "/api/v1/send", `{"sequence":"{A}"}`, nil); code != http.StatusConflict {
		t.Fatalf("stopped: %d", code)
	}
	if code, out := call(t, h, "POST", "/api/v1/send", `{"seq":1}`, nil); code != 400 || out["error"].(map[string]any)["code"] != "api.bad_request" {
		t.Fatalf("bad json: %d %v", code, out)
	}
}

// TestParseFormatStop проверяет разбор, форматирование, остановку и отсутствующие сервисы.
func TestParseFormatStop(t *testing.T) {
	t.Parallel()
	m, runner := newTestModule(t)
	h := m.routes(true)

	// Разбор возвращает дерево, форматирование — канонический текст.
	if code, out := call(t, h, "POST", "/api/v1/dsl/parse", `{"text":"^{shift}{a}"}`, nil); code != 200 || len(out["nodes"].([]any)) != 2 {
		t.Fatalf("parse: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/dsl/format", `{"text":"^{shift} {a}  [1000]"}`, nil); code != 200 || out["text"] != "^{Shift}{A}[1s]" {
		t.Fatalf("format: %d %v", code, out)
	}

	// Остановка вызывает StopAll; экстренная — тоже.
	call(t, h, "POST", "/api/v1/stop", "", nil)
	call(t, h, "POST", "/api/v1/panic", "", nil)
	if runner.stopped != 2 {
		t.Fatalf("stopped = %d", runner.stopped)
	}

	// Без модуля ввода — 503.
	if code, _ := call(t, h, "GET", "/api/v1/devices", "", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("devices without input: %d", code)
	}
}

// TestSecurity проверяет защиту TCP-канала (SEC-5).
func TestSecurity(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.port, m.token = 17420, "secret-token"
	h := m.secure(m.routes(false))
	good := map[string]string{"Authorization": "Bearer secret-token"}
	host := func(extra map[string]string) map[string]string {
		out := map[string]string{"Host": "127.0.0.1:17420"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// httptest.NewRequest ставит Host из URL — задаём адрес явно.
	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, bytes.NewReader(nil))
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Без токена API недоступен, с токеном — доступен.
	if rec := do("GET", "/api/v1/status", host(nil)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do("GET", "/api/v1/status", host(good)); rec.Code != 200 {
		t.Fatalf("with token: %d", rec.Code)
	}
	if rec := do("GET", "/api/v1/status", host(map[string]string{"Authorization": "Bearer wrong"})); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}

	// Чужой Host (DNS rebinding) — отказ даже с токеном.
	if rec := do("GET", "/api/v1/status", map[string]string{"Host": "evil.example:17420", "Authorization": "Bearer secret-token"}); rec.Code != http.StatusForbidden {
		t.Fatalf("evil host: %d", rec.Code)
	}

	// Изменяющий запрос с чужого сайта (CSRF) — отказ; со своей страницы — пропускается.
	if rec := do("POST", "/api/v1/stop", host(map[string]string{"Origin": "http://evil.example", "Authorization": "Bearer secret-token"})); rec.Code != http.StatusForbidden {
		t.Fatalf("evil origin: %d", rec.Code)
	}
	if rec := do("POST", "/api/v1/stop", host(map[string]string{"Origin": "http://localhost:17420", "Authorization": "Bearer secret-token"})); rec.Code != 200 {
		t.Fatalf("own origin: %d", rec.Code)
	}

	// Вход по ссылке: cookie с HttpOnly и SameSite=Strict, затем API работает по cookie.
	rec := do("GET", "/?t=secret-token", host(nil))
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d %v", rec.Code, cookies)
	}
	if rec := do("GET", "/api/v1/status", host(map[string]string{"Cookie": SessionCookie + "=secret-token"})); rec.Code != 200 {
		t.Fatalf("cookie auth: %d", rec.Code)
	}

	// Страница интерфейса открывается без токена.
	if rec := do("GET", "/", host(nil)); rec.Code != 200 || !strings.Contains(rec.Body.String(), "mKey") {
		t.Fatalf("static: %d %q", rec.Code, rec.Body.String())
	}
}

// TestStartStopUnixSocket проверяет запуск на Unix-сокете, файлы с правами 0600 и уборку при остановке.
func TestStartStopUnixSocket(t *testing.T) {
	t.Parallel()
	m, runner := newTestModule(t)
	dir := filepath.Join(t.TempDir(), "rt")
	m.cfg = Config{Port: 0, RuntimeDir: dir}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Сокет и токен доступны только владельцу.
	for _, f := range []string{SocketFile, TokenFile, InfoFile} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v perm %o", f, err, fi.Mode().Perm())
		}
	}

	// Запрос через сокет выполняет макрос без токена (доверенный канал).
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, SocketFile))
	}}}
	resp, err := client.Post("http://mkey/api/v1/send", "application/json", strings.NewReader(`{"sequence":"{B}"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(runner.runs) != 1 {
		t.Fatalf("send via socket: %d, runs %v", resp.StatusCode, runner.runs)
	}

	// Остановка удаляет файлы.
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, SocketFile)); !os.IsNotExist(err) {
		t.Fatal("socket must be removed")
	}
}

// Проверка на этапе компиляции, что фейк реализует контракт.
var _ contracts.SequenceRunner = (*fakeRunner)(nil)
