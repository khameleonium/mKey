package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// scriptAction — вид действия без описания для сухого прогона (как скрипт или плагин).
type scriptAction struct{ ran *bool }

// Meta возвращает метаданные.
func (scriptAction) Meta() contracts.ExtensionMeta { return contracts.ExtensionMeta{ID: "script"} }

// Validate принимает любые параметры.
func (scriptAction) Validate(project.Action) error { return nil }

// Run отмечает запуск — при сухом прогоне его быть не должно.
func (s scriptAction) Run(context.Context, contracts.RunContext, project.Action) error {
	*s.ran = true
	return nil
}

// dryEvent включает проект с одним событием (чтобы были его переменные) и прогоняет событие.
func dryEvent(t *testing.T, r *eventRig, src string) contracts.DryRun {
	t.Helper()
	r.load(t, "p", src)
	p, err := project.Parse([]byte(src), "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.ValidateProject(p); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.DryRun(context.Background(), "p", p.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// rows — таймлайн строками «время уровень вид подробности» для сравнения.
func rows(res contracts.DryRun) string {
	var b strings.Builder
	for _, s := range res.Steps {
		var parts []string
		for _, f := range []string{
			strconv.FormatInt(s.AtMS, 10), strconv.Itoa(s.Depth), s.Kind, s.Key, s.Action, strings.Join(s.Keys, "+"), s.Text,
			condTypes(s.Conditions), strings.Join(s.Points, ";"),
		} {
			if f != "" {
				parts = append(parts, f)
			}
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteString("\n")
	}
	return b.String()
}

// condTypes — виды условий через запятую.
func condTypes(conds []project.Condition) string {
	types := make([]string, len(conds))
	for i, c := range conds {
		types[i] = c.Type
	}
	return strings.Join(types, ",")
}

// TestDryRun проверяет сухой прогон: шаги макроса со временем, повтор (тело один раз, время
// умножено), «Если/Иначе» с одного времени, заметки, действие без описания — строкой, ничего
// не нажато и не выполнено.
func TestDryRun(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	r.m.cfg.KeyHoldMS, r.m.cfg.KeyDelayMS = 20, 10
	ran := false
	if err := r.m.ext.Register(contracts.PointAction, scriptAction{ran: &ran}); err != nil {
		t.Fatal(err)
	}

	res := dryEvent(t, r, `
events:
  - id: a
    trigger: { type: test }
    conditions: [ { type: variable, name: on, op: "==", value: true } ]
    actions:
      - send: '^{Ctrl}{C}~{Ctrl}[100]{"ab"}'
      - repeat: { times: 3, do: [ { tap: F1 }, { pause: 50 } ] }
      - if:
          conditions: [ { type: key_state, key: Shift } ]
          then: [ { pause: 200 } ]
          else: [ { notify: "нет Shift" } ]
      - set_var: { name: n, add: 1 }
      - touch: { x: "50%", y: "80%" }
      - script: "rm -rf /"
      - stop: all
`)
	want := `0 0 note dry.event_conditions variable
0 0 press Ctrl
0 0 tap C
30 0 release Ctrl
30 0 wait
130 0 text ab
190 0 group dry.repeat_times
190 1 tap F1
220 1 wait
430 0 group dry.if key_state
430 1 wait
430 0 group dry.else
430 1 note dry.notify
630 0 note dry.add_var
630 0 touch 50%, 80%
650 0 action script
650 0 note dry.stop_all
`
	if got := rows(res); got != want {
		t.Fatalf("timeline:\n%s\nwant:\n%s", got, want)
	}
	if res.TotalMS != 650 || res.Open || res.Truncated {
		t.Fatalf("result = %+v", res)
	}

	// Ничего не нажато и не выполнено, переменная не изменилась.
	if ran || len(r.devs.kb.log()) != 0 || len(r.devs.mouse.log()) != 0 {
		t.Fatalf("dry run did something: script=%v keyboard=%v", ran, r.devs.kb.log())
	}
	if _, ok := r.m.Vars("p").Get("n"); ok {
		t.Fatal("variable set by dry run")
	}
}

// TestDryRunOpenAndTruncated: повтор «пока включено» — тело один раз и длительность неизвестна;
// очень длинный макрос обрезается.
func TestDryRunOpenAndTruncated(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	res := dryEvent(t, r, `
events:
  - id: a
    trigger: { type: test }
    actions: [ { repeat: { while: toggled, do: [ { mouse_click: Left } ] } } ]
`)
	if !res.Open || len(res.Steps) != 2 || res.Steps[0].Key != "dry.repeat_toggled" || res.Steps[1].Depth != 1 {
		t.Fatalf("open = %+v", res)
	}

	long := strings.Repeat("{A}", maxDrySteps+5)
	res = dryEvent(t, r, "events: [ { id: a, trigger: { type: test }, actions: [ { send: '"+long+"' } ] } ]")
	if !res.Truncated || len(res.Steps) != maxDrySteps {
		t.Fatalf("truncated = %v, steps = %d", res.Truncated, len(res.Steps))
	}
}
