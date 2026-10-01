package engine

import (
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/project"
	"mkey/internal/registry"
)

// testTrigger — вид триггера «test»: запоминает функции срабатывания по ID события.
type testTrigger struct {
	mu    sync.Mutex
	fires map[string]func(contracts.Fire)
}

func (t *testTrigger) Meta() contracts.ExtensionMeta { return contracts.ExtensionMeta{ID: "test"} }

func (t *testTrigger) Validate(project.Trigger) error { return nil }

func (t *testTrigger) Arm(_ context.Context, ref contracts.EventRef, _ project.Trigger, fire func(contracts.Fire)) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fires[ref.Event] = fire
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.fires, ref.Event)
	}, nil
}

// fire вызывает срабатывание события id.
func (t *testTrigger) fire(tst *testing.T, id string, f contracts.Fire) {
	tst.Helper()
	t.mu.Lock()
	fn := t.fires[id]
	t.mu.Unlock()
	if fn == nil {
		tst.Fatalf("event %q is not armed", id)
	}
	fn(f)
}

// memProjects — хранилище проектов в памяти.
type memProjects struct {
	contracts.Projects // остальные методы в тестах не используются
	mu                 sync.Mutex
	ps                 map[string]contracts.ProjectState
}

func (p *memProjects) Dir() string { return "" }
func (p *memProjects) List() []contracts.ProjectState {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contracts.ProjectState
	for _, s := range p.ps {
		out = append(out, s)
	}
	return out
}
func (p *memProjects) Get(id string) (contracts.ProjectState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.ps[id]
	return s, ok
}
func (p *memProjects) SetEnabled(string, bool) error              { return nil }
func (p *memProjects) SetEventEnabled(string, string, bool) error { return nil }
func (p *memProjects) Import(string, []byte) (string, error)      { return "", nil }
func (p *memProjects) put(t *testing.T, id, src string) {
	t.Helper()
	pr, err := project.Parse([]byte(src), id)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	p.mu.Lock()
	p.ps[id] = contracts.ProjectState{Project: pr}
	p.mu.Unlock()
}

// fakeMods — управление модификаторами, считающее вызовы.
type fakeMods struct{ released, restored atomic.Int32 }

func (f *fakeMods) Release() { f.released.Add(1) }
func (f *fakeMods) Restore() { f.restored.Add(1) }

// eventRig — движок с фейковыми устройствами, проектами и триггером test.
type eventRig struct {
	m    *Module
	devs fakeDevices
	trig *testTrigger
	ps   *memProjects
	bus  *bus.Bus
}

// newEventRig создаёт движок с реальными часами и укороченными задержками.
func newEventRig(t *testing.T) *eventRig {
	t.Helper()
	m, devs := newTestModule(clock.Real{}, nil)
	m.rnd = rand.New(rand.NewPCG(1, 2))
	m.cfg.KeyHoldMS, m.cfg.KeyDelayMS = 1, 1
	m.ext = registry.NewExtensions()
	m.bus = bus.New(0)
	m.persist = &persistFile{path: filepath.Join(t.TempDir(), "vars.json")}
	trig := &testTrigger{fires: map[string]func(contracts.Fire){}}
	if err := m.ext.Register(contracts.PointTrigger, trig); err != nil {
		t.Fatal(err)
	}
	if err := m.registerBuiltins(m.ext); err != nil {
		t.Fatal(err)
	}
	ps := &memProjects{ps: map[string]contracts.ProjectState{}}
	m.projects = ps
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return &eventRig{m: m, devs: devs, trig: trig, ps: ps, bus: m.bus.(*bus.Bus)}
}

// load кладёт проект в хранилище и взводит его.
func (r *eventRig) load(t *testing.T, id, src string) {
	t.Helper()
	r.ps.put(t, id, src)
	r.m.reloadProject(id)
}

// clicks считает нажатия левой кнопки мыши.
func (r *eventRig) clicks() int {
	n := 0
	for _, e := range r.devs.mouse.log() {
		if e.Type == ev.EvKey && e.Code == ev.BtnLeft && e.Value == 1 {
			n++
		}
	}
	return n
}

// eventually ждёт выполнения условия. Запас 10 с — для медленных машин под нагрузкой: условия
// точные (конкретные суммы), поэтому длинное ожидание не скрывает ошибок, а только замедляет падение.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s (условие так и не выполнилось за 10 с)", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// running возвращает число выполнений события.
func (r *eventRig) running(id string) int {
	for _, s := range r.m.Statuses() {
		if s.Event == id {
			return s.Running
		}
	}
	return -1
}

// TestAutoclickerToggle — сценарий приёмки: F8 включает и выключает автокликер.
func TestAutoclickerToggle(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	r.load(t, "games", `
variables: { clicks: { type: int, value: 0 } }
events:
  - id: autoclick
    trigger: { type: test }
    actions:
      - repeat:
          while: toggled
          do:
            - send: "{Mouse0}[5]"
            - set_var: { name: clicks, add: 1 }
`)
	on := true

	// Включаем: клики идут.
	r.trig.fire(t, "autoclick", contracts.Fire{Toggle: &on})
	eventually(t, "clicks", func() bool { return r.clicks() >= 5 })

	// Выключаем: цикл останавливается, выполнение завершается, кнопка отпущена.
	r.trig.fire(t, "autoclick", contracts.Fire{Toggle: &on})
	eventually(t, "stop", func() bool { return r.running("autoclick") == 0 })
	if len(r.devs.mouse.Held()) != 0 {
		t.Fatal("mouse button left pressed")
	}
	if v, _ := r.m.Vars("games").Get("clicks"); v.(int64) < 5 {
		t.Fatalf("clicks var = %v", v)
	}

	// Снова включаем и останавливаем через StopAll: переключатель сброшен, следующее нажатие снова включает.
	r.trig.fire(t, "autoclick", contracts.Fire{Toggle: &on})
	eventually(t, "running", func() bool { return r.running("autoclick") == 1 })
	r.m.StopAll()
	eventually(t, "stopped", func() bool { return r.running("autoclick") == 0 })
	r.trig.fire(t, "autoclick", contracts.Fire{Toggle: &on})
	eventually(t, "running again", func() bool { return r.running("autoclick") == 1 })
}

// TestPolicies проверяет политики ignore, queue, parallel и restart (FR-EV-5).
func TestPolicies(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	// Паузы с запасом: все срабатывания должны прийти, пока идёт первое выполнение, даже на
	// медленной машине с проверкой гонок (CI) — иначе итоговые суммы получаются другими.
	r.load(t, "p", `
variables: { n: { type: int, value: 0 } }
events:
  - { id: ignore, trigger: { type: test }, actions: [ { pause: 400 }, { set_var: { name: n, add: 1 } } ] }
  - { id: queue, policy: queue, trigger: { type: test }, actions: [ { pause: 30 }, { set_var: { name: n, add: 10 } } ] }
  - { id: parallel, policy: parallel, max_parallel: 2, trigger: { type: test }, actions: [ { pause: 400 }, { set_var: { name: n, add: 100 } } ] }
  - { id: restart, policy: restart, trigger: { type: test }, actions: [ { pause: 400 }, { set_var: { name: n, add: 1000 } } ] }
`)
	get := func() int64 { v, _ := r.m.Vars("p").Get("n"); return v.(int64) }
	// Если шаг не дождался своей суммы, в журнале теста видно, какая сумма получилась.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("n = %d, running: ignore=%d queue=%d parallel=%d restart=%d", get(),
				r.running("ignore"), r.running("queue"), r.running("parallel"), r.running("restart"))
		}
	})

	// ignore: три быстрых срабатывания — одно выполнение.
	for range 3 {
		r.trig.fire(t, "ignore", contracts.Fire{})
	}
	eventually(t, "ignore", func() bool { return get() == 1 && r.running("ignore") == 0 })

	// queue: три срабатывания — три выполнения по очереди.
	for range 3 {
		r.trig.fire(t, "queue", contracts.Fire{})
	}
	eventually(t, "queue", func() bool { return get() == 31 && r.running("queue") == 0 })

	// parallel с пределом 2: три срабатывания — два выполнения.
	for range 3 {
		r.trig.fire(t, "parallel", contracts.Fire{})
	}
	eventually(t, "parallel", func() bool { return get() == 231 && r.running("parallel") == 0 })

	// restart: второе срабатывание прерывает первое — одно завершённое выполнение.
	r.trig.fire(t, "restart", contracts.Fire{})
	time.Sleep(50 * time.Millisecond)
	r.trig.fire(t, "restart", contracts.Fire{})
	eventually(t, "restart", func() bool { return r.running("restart") == 0 && get() == 1231 })
}

// TestConditionsAndIf проверяет условия события и действие if.
func TestConditionsAndIf(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	r.load(t, "p", `
variables:
  armed: { type: bool, value: false }
  hits: { type: int, value: 0 }
events:
  - id: guarded
    trigger: { type: test }
    conditions: [ { type: variable, name: armed, op: "==", value: true } ]
    actions: [ { set_var: { name: hits, add: 1 } } ]
  - id: branch
    trigger: { type: test }
    actions:
      - if:
          conditions: [ { type: any, of: [ { type: variable, name: hits, op: ">=", value: 1 } ] } ]
          then: [ { set_var: { name: hits, value: 100 } } ]
          else: [ { set_var: { name: armed, value: true } } ]
`)
	vars := r.m.Vars("p")

	// Условие не выполнено — действие не выполняется; ветка else взводит флаг.
	if err := r.m.RunEvent(context.Background(), "p", "guarded"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.RunEvent(context.Background(), "p", "branch"); err != nil {
		t.Fatal(err)
	}
	if v, _ := vars.Get("hits"); v.(int64) != 0 {
		t.Fatalf("hits = %v", v)
	}

	// Теперь условие выполнено; ветка then.
	if err := r.m.RunEvent(context.Background(), "p", "guarded"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.RunEvent(context.Background(), "p", "branch"); err != nil {
		t.Fatal(err)
	}
	if v, _ := vars.Get("hits"); v.(int64) != 100 {
		t.Fatalf("hits = %v", v)
	}
}

// TestHeldAndModifiersAndHotstring проверяет while: held, отпускание модификаторов и замену hotstring.
func TestHeldAndModifiersAndHotstring(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	r.load(t, "p", `
events:
  - { id: held, trigger: { type: test }, actions: [ { repeat: { while: held, do: [ { send: "{Mouse0}[5]" } ] } } ] }
  - { id: abbrev, trigger: { type: test }, actions: [ { send: "{Enter}" } ] }
`)

	// Пока «держат» — кликает; отпустили — остановилось. Модификаторы отпущены и возвращены.
	var held atomic.Bool
	held.Store(true)
	mods := &fakeMods{}
	r.trig.fire(t, "held", contracts.Fire{Held: held.Load, Modifiers: mods})
	eventually(t, "clicks while held", func() bool { return r.clicks() >= 3 })
	held.Store(false)
	eventually(t, "stop on release", func() bool { return r.running("held") == 0 })
	if mods.released.Load() != 1 || mods.restored.Load() != 1 {
		t.Fatalf("modifiers released %d, restored %d", mods.released.Load(), mods.restored.Load())
	}

	// Замена hotstring выполняется перед действиями.
	r.trig.fire(t, "abbrev", contracts.Fire{Vars: map[string]any{"replace_dsl": "{Backspace*2}"}})
	eventually(t, "replacement", func() bool {
		kb := r.devs.kb.log()
		return len(kb) >= 6 && kb[0].Code == ev.KeyBackspace && kb[len(kb)-1].Code == ev.KeyEnter
	})
}

// TestReplaceWithoutActions проверяет, что событие без действий (только замена слова) допустимо
// и замена выполняется.
func TestReplaceWithoutActions(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	r.load(t, "p", `events: [ { id: btw, trigger: { type: test }, actions: [] } ]`)
	r.trig.fire(t, "btw", contracts.Fire{Vars: map[string]any{"replace_dsl": "{Backspace*2}"}})
	eventually(t, "replacement", func() bool {
		kb := r.devs.kb.log()
		return len(kb) >= 4 && kb[0].Code == ev.KeyBackspace
	})
}

// TestReloadKeepsPrevious проверяет, что проект с ошибкой не заменяет рабочую версию.
func TestReloadKeepsPrevious(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	errs, cancel := r.bus.Subscribe(contracts.TopicEngineError)
	defer cancel()
	r.load(t, "p", `events: [ { id: a, trigger: { type: test }, actions: [ { send: "{A}" } ] } ]`)

	// Новая версия с неизвестным действием и с ошибкой в макросе — отвергается.
	for _, bad := range []string{
		`events: [ { id: b, trigger: { type: test }, actions: [ { jump: 1 } ] } ]`,
		`events: [ { id: b, trigger: { type: test }, actions: [ { send: "{Mous0}" } ] } ]`,
		`events: [ { id: b, trigger: { type: nope }, actions: [ { send: "{A}" } ] } ]`,
	} {
		r.load(t, "p", bad)
		select {
		case <-errs:
		case <-time.After(time.Second):
			t.Fatalf("engine error not published for %s", bad)
		}
		if _, ok := r.m.findEvent("p", "a"); !ok {
			t.Fatalf("previous version must stay active after %s", bad)
		}
	}

	// Выключенный проект снимается.
	r.load(t, "p", `enabled: false
events: [ { id: a, trigger: { type: test }, actions: [ { send: "{A}" } ] } ]`)
	if _, ok := r.m.findEvent("p", "a"); ok {
		t.Fatal("disabled project must be deactivated")
	}
}

// TestEmergencyTopic проверяет реакцию на экстренную остановку: всё прервано и отпущено.
func TestEmergencyTopic(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)

	// Движок слушает шину, как в работе.
	projCh, a := r.bus.Subscribe(contracts.TopicProjectsChanged)
	emerCh, b := r.bus.Subscribe(contracts.TopicEmergency)
	resCh, c := r.bus.Subscribe(contracts.TopicResumed)
	r.m.unsub = []func(){a, b, c}
	r.m.wg.Add(1)
	go r.m.listen(projCh, emerCh, resCh)

	r.load(t, "p", `events: [ { id: hold, trigger: { type: test }, actions: [ { send: "^{Mouse0}[3600s]" } ] } ]`)
	r.trig.fire(t, "hold", contracts.Fire{})
	eventually(t, "held", func() bool { return len(r.devs.mouse.Held()) == 1 })
	r.bus.Publish(contracts.TopicEmergency, nil)
	eventually(t, "released", func() bool { return r.running("hold") == 0 && len(r.devs.mouse.Held()) == 0 })

	// Приостановлено: триггер ничего не запускает.
	eventually(t, "suspended", r.m.suspended.Load)
	r.trig.fire(t, "hold", contracts.Fire{})
	time.Sleep(50 * time.Millisecond)
	if r.running("hold") != 0 {
		t.Fatal("triggers must not run events after an emergency stop")
	}

	// После возобновления триггер снова работает.
	r.bus.Publish(contracts.TopicResumed, nil)
	eventually(t, "resumed", func() bool { return !r.m.suspended.Load() })
	r.trig.fire(t, "hold", contracts.Fire{})
	eventually(t, "running after resume", func() bool { return r.running("hold") == 1 })
}

// TestPersistVars проверяет сохранение переменных между перезапусками.
func TestPersistVars(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	src := `
variables: { total: { type: int, value: 0, persist: true } }
events: [ { id: inc, trigger: { type: test }, actions: [ { set_var: { name: total, add: 5 } } ] } ]
`
	r.load(t, "p", src)
	if err := r.m.RunEvent(context.Background(), "p", "inc"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.m.persist.path)
	if err != nil || len(data) == 0 {
		t.Fatalf("vars file: %v", err)
	}

	// «Перезапуск»: новый движок с тем же файлом восстанавливает значение.
	r2 := newEventRig(t)
	r2.m.persist = r.m.persist
	r2.load(t, "p", src)
	if v, _ := r2.m.Vars("p").Get("total"); v.(int64) != 5 {
		t.Fatalf("restored total = %v", v)
	}
}

// TestValidateErrors проверяет понятные ошибки проверки действий и условий.
func TestValidateErrors(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	bad := map[string]string{
		"repeat two modes":  `[{repeat: {times: 2, while: forever, do: [{send: "{A}"}]}}]`,
		"repeat bad while":  `[{repeat: {while: sometimes, do: [{send: "{A}"}]}}]`,
		"nested bad action": `[{repeat: {times: 2, do: [{fly: 1}]}}]`,
		"bad pause":         `[{pause: "soon"}]`,
		"bad stop":          `[{stop: everything}]`,
		"set_var no name":   `[{set_var: {value: 1}}]`,
	}
	for name, actions := range bad {
		p, err := project.Parse([]byte("events: [ { id: a, trigger: { type: test }, actions: "+actions+" } ]"), "p")
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if err := r.m.validateActions(p.Events[0].Actions); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	for name, cond := range map[string]string{
		"unknown op":     `{type: variable, name: x, op: "~="}`,
		"window phase 8": `{type: window, title: "x"}`,
		"bad time":       `{type: time, from: "25:00", to: "1"}`,
	} {
		p, _ := project.Parse([]byte("events: [ { id: a, trigger: { type: test }, conditions: ["+cond+"], actions: [{send: '{A}'}] } ]"), "p")
		if err := r.m.validateConditions(p.Events[0].Conditions); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

// TestValidateProject проверяет проверку проекта целиком, включая выключенные события и триггеры.
func TestValidateProject(t *testing.T) {
	t.Parallel()
	r := newEventRig(t)
	good, _ := project.Parse([]byte(`events: [ { id: a, enabled: false, trigger: { type: timer, every_ms: 1000 }, actions: [ { send: "{A}" } ] } ]`), "p")
	if err := r.m.ValidateProject(good); err != nil {
		t.Fatalf("good project: %v", err)
	}
	for _, src := range []string{
		`events: [ { id: a, enabled: false, trigger: { type: timer, every_ms: 1 }, actions: [ { send: "{A}" } ] } ]`,
		`events: [ { id: a, enabled: false, trigger: { type: nope }, actions: [ { send: "{A}" } ] } ]`,
		`events: [ { id: a, enabled: false, trigger: { type: manual }, actions: [ { send: "{Mous0}" } ] } ]`,
	} {
		p, _ := project.Parse([]byte(src), "p")
		if err := r.m.ValidateProject(p); err == nil {
			t.Errorf("expected error for %s", src)
		}
	}
}
