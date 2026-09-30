package project

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// sample — проект из SPEC §8 (без пиксельного события и условия окна).
const sample = `
version: 1
name: "Игры"
variables:
  clicks: { type: int, value: 0, persist: true }
events:
  - id: autoclick
    name: "Автокликер на F8"
    trigger:
      type: hotkey
      keys: "{F8}"
      on: toggle
      consume: true
    actions:
      - repeat:
          while: toggled
          do:
            - send: "{Mouse0}[50]"
            - set_var: { name: clicks, add: 1 }
  - id: long_hold
    name: "Удержание ЛКМ"
    trigger: { type: hotkey, keys: "{Ctrl+Alt+H}", consume: true }
    conditions:
      - type: any
        of:
          - { type: variable, name: clicks, op: ">=", value: 0 }
    actions:
      - send: "^{Mouse0}[1500]~{Mouse0}"
`

// TestParseSample проверяет разбор проекта из ТЗ.
func TestParseSample(t *testing.T) {
	t.Parallel()
	p, err := Parse([]byte(sample), "games")
	if err != nil {
		t.Fatal(err)
	}

	// Проект, переменная, события.
	if p.ID != "games" || p.Name != "Игры" || !p.IsEnabled() || p.Variables["clicks"].Type != "int" || len(p.Events) != 2 {
		t.Fatalf("project = %+v", p)
	}

	// Триггер: вид и параметры.
	tr := p.Events[0].AllTriggers()
	if len(tr) != 1 || tr[0].Type != "hotkey" || tr[0].Params["keys"] != "{F8}" || tr[0].Params["consume"] != true {
		t.Fatalf("trigger = %+v", tr)
	}

	// Действие repeat и вложенные действия.
	a := p.Events[0].Actions[0]
	if a.Type != "repeat" {
		t.Fatalf("action = %+v", a)
	}
	var rep struct {
		While string `json:"while"`
		Do    any    `json:"do"`
	}
	if err := Decode(a.Value, &rep); err != nil || rep.While != "toggled" {
		t.Fatalf("repeat = %+v, %v", rep, err)
	}
	inner, err := DecodeActions(rep.Do)
	if err != nil || len(inner) != 2 || inner[0].Type != "send" || inner[1].Type != "set_var" {
		t.Fatalf("inner = %+v, %v", inner, err)
	}

	// Вложенные условия группы any.
	cond := p.Events[1].Conditions[0]
	sub, err := DecodeConditions(cond.Params["of"])
	if cond.Type != "any" || err != nil || len(sub) != 1 || sub[0].Type != "variable" || sub[0].Params["op"] != ">=" {
		t.Fatalf("conditions = %+v / %+v, %v", cond, sub, err)
	}
}

// TestParseErrors проверяет структурные ошибки.
func TestParseErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown key":     "events: []\ntitel: x\n",
		"missing id":      "events:\n  - trigger: {type: manual}\n    actions: [{send: '{A}'}]\n",
		"duplicate id":    "events:\n  - {id: a, trigger: {type: manual}, actions: [{send: '{A}'}]}\n  - {id: a, trigger: {type: manual}, actions: [{send: '{A}'}]}\n",
		"no trigger":      "events:\n  - {id: a, actions: [{send: '{A}'}]}\n",
		"no actions":      "events:\n  - {id: a, trigger: {type: manual}, actions: []}\n",
		"trigger no type": "events:\n  - {id: a, trigger: {keys: '{A}'}, actions: [{send: '{A}'}]}\n",
		"action two keys": "events:\n  - {id: a, trigger: {type: manual}, actions: [{send: '{A}', pause: 1}]}\n",
		"bad policy":      "events:\n  - {id: a, policy: sometimes, trigger: {type: manual}, actions: [{send: '{A}'}]}\n",
		"bad var type":    "variables: {x: {type: list, value: 1}}\nevents: []\n",
		"newer version":   "version: 7\nevents: []\n",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src), "p"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// TestRoundTripYAML проверяет, что проект записывается обратно в YAML в той же форме.
func TestRoundTripYAML(t *testing.T) {
	t.Parallel()
	p, err := Parse([]byte(sample), "games")
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out, "games")
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if len(again.Events) != 2 || again.Events[0].Actions[0].Type != "repeat" || !strings.Contains(string(out), "type: hotkey") {
		t.Fatalf("round trip lost data:\n%s", out)
	}
}

// TestIDFromFile проверяет получение идентификатора проекта из имени файла.
func TestIDFromFile(t *testing.T) {
	t.Parallel()
	if id, ok := IDFromFile("games.mkey.yaml"); !ok || id != "games" {
		t.Errorf("IDFromFile = %q, %v", id, ok)
	}
	for _, n := range []string{"games.yaml", ".mkey.yaml", "notes.txt"} {
		if _, ok := IDFromFile(n); ok {
			t.Errorf("%s must not be a project file", n)
		}
	}
}
