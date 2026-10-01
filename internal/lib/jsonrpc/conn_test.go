package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// pair соединяет два Conn каналами в памяти (как процесс и хост).
func pair(t *testing.T, ha, hb Handler) (*Conn, *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a, b := New(ar, aw, ha), New(br, bw, hb)
	t.Cleanup(func() {
		_ = aw.Close()
		_ = bw.Close()
		a.Close()
		b.Close()
	})
	return a, b
}

// TestCallBothWays проверяет вызовы в обе стороны, ошибки, уведомления и «метода нет».
func TestCallBothWays(t *testing.T) {
	t.Parallel()
	notes := make(chan string, 1)
	a, b := pair(t, func(_ context.Context, method string, params json.RawMessage, notify bool) (any, error) {
		if notify {
			notes <- method + string(params)
			return nil, nil
		}
		if method == "fail" {
			return nil, errors.New("сломалось")
		}
		return map[string]string{"echo": string(params)}, nil
	}, func(_ context.Context, method string, _ json.RawMessage, _ bool) (any, error) {
		if method == "ping" {
			return nil, nil
		}
		return nil, Errorf(CodeMethodNotFound, "no %s", method)
	})
	ctx := context.Background()

	// b вызывает a: ответ с результатом; ошибка обработчика — CodeFailed с текстом.
	var out map[string]string
	if err := b.Call(ctx, "echo", []int{1}, &out); err != nil || out["echo"] != "[1]" {
		t.Fatalf("echo: %v %v", out, err)
	}
	var e *Error
	if err := b.Call(ctx, "fail", nil, nil); !errors.As(err, &e) || e.Code != CodeFailed || e.Message != "сломалось" {
		t.Fatalf("fail: %v", err)
	}

	// a вызывает b: пустой результат; неизвестный метод — CodeMethodNotFound.
	if err := a.Call(ctx, "ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Call(ctx, "nope", nil, nil); !errors.As(err, &e) || e.Code != CodeMethodNotFound {
		t.Fatalf("nope: %v", err)
	}

	// Уведомление доходит, ответа нет.
	if err := b.Notify("fire", map[string]int{"h": 1}); err != nil {
		t.Fatal(err)
	}
	if got := <-notes; got != `fire{"h":1}` {
		t.Fatalf("notify: %s", got)
	}
}

// TestCancel проверяет отмену: свой Call возвращает ctx.Err(), обработчик другой стороны
// получает отменённый контекст.
func TestCancel(t *testing.T) {
	t.Parallel()
	cancelled := make(chan struct{})
	_, b := pair(t, func(ctx context.Context, _ string, _ json.RawMessage, _ bool) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := b.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call: %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not cancelled")
	}
}

// TestClosed проверяет закрытие: конец потока закрывает соединение, ожидающий вызов получает ошибку.
func TestClosed(t *testing.T) {
	t.Parallel()
	r, w := io.Pipe()
	c := New(r, io.Discard, nil)
	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "x", nil, nil) }()
	time.Sleep(10 * time.Millisecond)
	_ = w.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not return")
	}
	<-c.Done()
}

// TestBadLine проверяет ответ на строку, которая не JSON.
func TestBadLine(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	r, w := io.Pipe()
	c := New(r, &out, nil)
	_, _ = w.Write([]byte("не json\n"))
	_ = w.Close()
	<-c.Done()
	if !strings.Contains(out.String(), "-32700") {
		t.Fatalf("out = %s", out.String())
	}
}
