package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/api"
	"mkey/internal/i18n"
)

// TestFormatAPIError проверяет сообщение об ошибке в макросе со стрелкой под ошибочным символом.
func TestFormatAPIError(t *testing.T) {
	t.Parallel()
	cat, _ := i18n.LoadCatalog()
	tr := i18n.New(cat, "ru")
	err := &apiError{Message: "неизвестная клавиша", Details: map[string]any{"pos": map[string]any{"line": 2.0, "col": 3.0}}}
	got := formatAPIError(tr, err, "{A}\n^{Mous0}")
	want := "Ошибка в макросе (строка 2, символ 3): неизвестная клавиша\n\n  ^{Mous0}\n    ^"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// Ошибка без позиции — только сообщение.
	if got := formatAPIError(tr, &apiError{Message: "нет доступа"}, ""); got != "нет доступа" {
		t.Fatalf("got %q", got)
	}
}

// TestClientNotRunning проверяет, что отсутствие демона распознаётся как errNotRunning.
func TestClientNotRunning(t *testing.T) {
	t.Parallel()
	c := newClient(t.TempDir(), "en")
	if err := c.do(context.Background(), "GET", "/api/v1/status", nil, nil); !errors.Is(err, errNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

// TestClientAPIError проверяет разбор ошибки API и передачу языка.
func TestClientAPIError(t *testing.T) {
	t.Parallel()

	// Фейковый демон на Unix-сокете: отвечает ошибкой на языке из Accept-Language.
	dir := t.TempDir()
	l, err := net.Listen("unix", filepath.Join(dir, api.SocketFile))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"dsl.unknown_key","message":"lang=` + r.Header.Get("Accept-Language") + `"}}`))
	}))
	srv.Listener = l
	srv.Start()
	defer srv.Close()

	// Клиент получает *apiError с кодом, статусом и сообщением.
	err = newClient(dir, "ru").do(context.Background(), "POST", "/api/v1/send", map[string]string{"sequence": "x"}, nil)
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != "dsl.unknown_key" || ae.Status != 400 || !strings.Contains(ae.Message, "lang=ru") {
		t.Fatalf("err = %v", err)
	}
	_ = os.Remove(filepath.Join(dir, api.SocketFile))
}

// TestLifecycle проверяет повторный вызов Shutdown.
func TestLifecycle(t *testing.T) {
	t.Parallel()
	l := &lifecycle{done: make(chan struct{})}
	l.Shutdown()
	l.Shutdown()
	select {
	case <-l.done:
	default:
		t.Fatal("done must be closed")
	}
}
