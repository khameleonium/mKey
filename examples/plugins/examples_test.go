// Package examples проверяет примеры плагинов не на Go (python-webhook) тестовым mKey.
package examples

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"mkey/pkg/pluginsdk/plugintest"
)

// TestPythonWebhook проверяет пример на Python: протокол, проверку адреса и вебхук.
func TestPythonWebhook(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	plugintest.Conformance(t, plugintest.Start(t, "./python-webhook/main.py"))

	h := plugintest.Start(t, "./python-webhook/main.py")
	init := h.Initialize("ru", "net")
	if len(init.Actions) != 1 || len(init.Triggers) != 1 {
		t.Fatalf("init = %+v", init)
	}
	if err := h.Validate("action", "http_request", map[string]any{"url": "ftp://x"}); err == nil {
		t.Fatal("bad url accepted")
	}

	// Вебхук: запрос на адрес — срабатывание с телом запроса.
	handle, fires := h.Arm("http_webhook", map[string]any{"port": 18097, "path": "/go"})
	var resp *http.Response
	var err error
	for range 50 {
		resp, err = http.Post("http://127.0.0.1:18097/go", "text/plain", strings.NewReader("привет"))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	select {
	case v := <-fires:
		if v["body"] != "привет" {
			t.Fatalf("vars = %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("webhook did not fire")
	}
	h.Disarm(handle)
}
