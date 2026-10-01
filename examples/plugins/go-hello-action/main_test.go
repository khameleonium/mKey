package main

import (
	"context"
	"testing"

	"mkey/pkg/pluginsdk/plugintest"
)

// TestPlugin проверяет пример: соответствие протоколу и приветствие.
func TestPlugin(t *testing.T) {
	t.Parallel()
	plugintest.Conformance(t, plugintest.Serve(t, newPlugin()))

	h := plugintest.Serve(t, newPlugin())
	h.Initialize("ru", "output.send")
	if err := h.Action(context.Background(), "hello", map[string]any{"name": "Вера"}); err != nil {
		t.Fatal(err)
	}
	if got := h.Sent(); len(got) != 1 || got[0] != `{"Hello, Вера!"}` {
		t.Fatalf("sent = %v", got)
	}
}
