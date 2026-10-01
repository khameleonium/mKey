package pluginsdk_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"mkey/pkg/pluginsdk"
	"mkey/pkg/pluginsdk/plugintest"
)

// newPlugin — плагин для проверки SDK: действие type_text, условие even, триггер every.
func newPlugin() *pluginsdk.Plugin {
	p := pluginsdk.New()
	p.Action(pluginsdk.Type{ID: "type_text", Name: pluginsdk.Text{"ru": "Набрать", "en": "Type"},
		Params: []byte(`{"type":"object","required":["text"],"properties":{"text":{"type":"string"}}}`)},
		func(ctx context.Context, c *pluginsdk.Call) error {
			var a struct{ Text string }
			if err := c.Decode(&a); err != nil {
				return err
			}
			return c.Host.Send(ctx, `{"`+a.Text+`"}`)
		})
	p.Validate("type_text", func(c *pluginsdk.Call) error {
		var a struct{ Text string }
		if err := c.Decode(&a); err != nil || a.Text == "" {
			return errors.New("нужен текст")
		}
		return nil
	})
	p.Condition(pluginsdk.Type{ID: "even"}, func(_ context.Context, c *pluginsdk.Call) (bool, error) {
		var a struct{ N int }
		err := c.Decode(&a)
		return a.N%2 == 0, err
	})
	p.Trigger(pluginsdk.Type{ID: "every"}, func(ctx context.Context, _ *pluginsdk.Call, fire func(map[string]any)) error {
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for n := 1; ; n++ {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				fire(map[string]any{"n": n})
			}
		}
	})
	return p
}

// TestConformance проверяет, что плагин на SDK соответствует протоколу.
func TestConformance(t *testing.T) {
	t.Parallel()
	plugintest.Conformance(t, plugintest.Serve(t, newPlugin()))
}

// TestTypes проверяет действие, проверку параметров, условие, триггер и разрешения.
func TestTypes(t *testing.T) {
	t.Parallel()
	h := plugintest.Serve(t, newPlugin())
	init := h.Initialize("ru", "output.send")
	if len(init.Actions) != 1 || init.Actions[0].Name["ru"] != "Набрать" {
		t.Fatalf("init = %+v", init)
	}
	ctx := context.Background()

	// Действие нажимает через mKey; проверка — понятный текст.
	if err := h.Action(ctx, "type_text", map[string]any{"text": "ok"}); err != nil {
		t.Fatal(err)
	}
	if got := h.Sent(); len(got) != 1 || got[0] != `{"ok"}` {
		t.Fatalf("sent = %v", got)
	}
	if err := h.Validate("action", "type_text", map[string]any{}); err == nil || err.Error() != "нужен текст" {
		t.Fatalf("validate = %v", err)
	}

	// Условие.
	if ok, err := h.Check(ctx, "even", map[string]any{"n": 4}); !ok || err != nil {
		t.Fatalf("even(4) = %v %v", ok, err)
	}

	// Триггер срабатывает, после снятия — нет.
	handle, fires := h.Arm("every", nil)
	if v := <-fires; v["n"] != float64(1) {
		t.Fatalf("fire = %v", v)
	}
	h.Disarm(handle)
	time.Sleep(20 * time.Millisecond)
	for len(fires) > 0 {
		<-fires
	}
	select {
	case v := <-fires:
		t.Fatalf("fired after disarm: %v", v)
	case <-time.After(30 * time.Millisecond):
	}
}

// TestPermissions проверяет, что без разрешения запрос к mKey отклоняется.
func TestPermissions(t *testing.T) {
	t.Parallel()
	h := plugintest.Serve(t, newPlugin())
	h.Initialize("en")
	if err := h.Action(context.Background(), "type_text", map[string]any{"text": "x"}); err == nil {
		t.Fatal("send without output.send must fail")
	}
}
