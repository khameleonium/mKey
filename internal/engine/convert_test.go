package engine

import (
	"encoding/json"
	"testing"

	"mkey/internal/lib/project"
)

// TestDSLRoundTrip проверяет разбор макроса на действия и обратную сборку (FR-UI-4).
func TestDSLRoundTrip(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	cases := []struct {
		src, actions, back string
	}{
		{`{F8}`, `[{"type":"tap","value":"F8"}]`, `{F8}`},
		{`^{Shift}{"Привет"}~{Shift}`, `[{"type":"key_down","value":"Shift"},{"type":"type_text","value":"Привет"},{"type":"key_up","value":"Shift"}]`, `^{Shift}{"Привет"}~{Shift}`},
		{`{Mouse0}[50]`, `[{"type":"tap","value":"Mouse0"},{"type":"pause","value":50}]`, `{Mouse0}[50]`},
		{`{Space 500}[100..300]`, `[{"type":"hold","value":{"key":"Space","ms":500}},{"type":"pause","value":{"max_ms":300,"min_ms":100}}]`, `{Space 500}[100..300]`},
		{`{Move +10 -5}{Click right}{Wheel down 3}`, `[{"type":"mouse_move","value":{"dx":10,"dy":-5}},{"type":"mouse_click","value":"Right"},{"type":"wheel","value":{"count":3,"direction":"Down"}}]`, `{Move +10 -5}{Click Right}{Wheel Down 3}`},
		// Повтор и группы остаются макросом; соседние фрагменты склеиваются.
		{`{A*3}({B}[10])*2{C}`, `[{"type":"send","value":"{A*3}({B}[10])*2"},{"type":"tap","value":"C"}]`, `{A*3}({B}[10])*2{C}`},
		{`{Ctrl+C}`, `[{"type":"tap","value":"Ctrl+C"}]`, `{Ctrl+C}`},
	}
	for _, c := range cases {
		// Макрос → действия.
		acts, err := r.m.DSLToActions(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		got, _ := json.Marshal(acts)
		if string(got) != c.actions {
			t.Errorf("%s:\n got %s\nwant %s", c.src, got, c.actions)
		}

		// Действия (как их пришлёт GUI в JSON) → макрос.
		var back []project.Action
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatal(err)
		}
		text, err := r.m.ActionsToDSL(back)
		if err != nil || text != c.back {
			t.Errorf("%s: back = %q, %v; want %q", c.src, text, err, c.back)
		}
	}

	// Ошибка в макросе и действия, которые нельзя записать макросом.
	if _, err := r.m.DSLToActions(`{Mous0}`); err == nil {
		t.Error("expected parse error")
	}
	if _, err := r.m.ActionsToDSL([]project.Action{{Type: "notify", Value: "x"}}); err == nil {
		t.Error("expected not-a-macro error")
	}
	if _, err := r.m.ActionsToDSL([]project.Action{{Type: "tap", Value: "Mous0"}}); err == nil {
		t.Error("expected bad key error")
	}
}
