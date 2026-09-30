package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
)

// maxQueued — наибольшая очередь срабатываний для политики queue.
const maxQueued = 10

// errStopSelf — действие stop: self завершает выполнение события без ошибки.
var errStopSelf = errors.New("stop")

// projectRuntime — загруженный и «взведённый» проект.
type projectRuntime struct {
	// p — проект.
	p project.Project
	// vars — переменные проекта.
	vars *varStore
	// events — события по ID.
	events map[string]*eventRuntime
	// ctx живёт, пока проект взведён; cancel прерывает все его выполнения.
	ctx    context.Context
	cancel context.CancelFunc
	// disarms снимают все триггеры проекта.
	disarms []func()
}

// eventRuntime — состояние одного события.
type eventRuntime struct {
	ref  contracts.EventRef
	ev   project.Event
	proj *projectRuntime
	// toggled — состояние переключателя события (для while: toggled).
	toggled atomic.Bool

	// mu защищает running, queued, lastFire и cancels.
	mu       sync.Mutex
	running  int
	queued   int
	lastFire contracts.Fire
	cancels  map[*run]context.CancelFunc
}

// reloadAll перестраивает все проекты (при старте).
func (m *Module) reloadAll() {
	if m.projects == nil {
		return
	}
	for _, st := range m.projects.List() {
		m.reloadProject(st.Project.ID)
	}
}

// reloadProject перестраивает проект по его текущему состоянию в хранилище: выключенный или
// удалённый — снимается; новый — проверяется целиком и взводится; при ошибке остаётся прежний.
func (m *Module) reloadProject(id string) {
	st, ok := m.projects.Get(id)

	// Проекта нет или он выключен — снимаем.
	if !ok || !st.Project.IsEnabled() {
		m.evMu.Lock()
		old := m.runtimes[id]
		delete(m.runtimes, id)
		m.evMu.Unlock()
		if old != nil {
			m.teardown(old)
			m.log.Info("project deactivated", "project", id)
		}
		return
	}

	// Тот же проект уже взведён (повторное уведомление) — ничего не делаем.
	m.evMu.Lock()
	cur := m.runtimes[id]
	m.evMu.Unlock()
	if cur != nil && reflect.DeepEqual(cur.p, st.Project) {
		return
	}

	// Проверяем и взводим новую версию; ошибка — прежняя версия продолжает работать.
	rt, err := m.build(st.Project)
	if err != nil {
		m.log.Warn("project rejected, previous version kept", "project", id, "err", err)
		m.bus.Publish(contracts.TopicEngineError, contracts.ProjectError{ID: id, Error: err.Error()})
		return
	}

	// Заменяем прежнюю версию новой.
	m.evMu.Lock()
	old := m.runtimes[id]
	m.runtimes[id] = rt
	m.evMu.Unlock()
	if old != nil {
		m.teardown(old)
	}
	m.log.Info("project active", "project", id, "events", len(rt.events))
}

// ValidateProject проверяет проект целиком, ничего не взводя: триггеры, условия и действия всех
// событий, включая выключенные (для проверки перед сохранением из GUI).
func (m *Module) ValidateProject(p project.Project) error {
	if err := project.Check(p); err != nil {
		return err
	}
	return m.validate(p, true)
}

// validate проверяет условия, действия и (при withTriggers) триггеры событий проекта.
// Выключенные события проверяются, только когда проверяется весь проект (withTriggers).
func (m *Module) validate(p project.Project, withTriggers bool) error {
	if err := m.validateConditions(p.ActiveWhen); err != nil {
		return fmt.Errorf("active_when: %w", err)
	}
	for _, e := range p.Events {
		if !e.IsEnabled() && !withTriggers {
			continue
		}
		if withTriggers {
			for i, t := range e.AllTriggers() {
				ext, ok := m.ext.Get(contracts.PointTrigger, t.Type)
				trig, isTrigger := ext.(contracts.TriggerType)
				if !ok || !isTrigger {
					err := fmt.Errorf("unknown trigger type %q (available: %s)", t.Type, m.available(contracts.PointTrigger))
					return &project.Problem{Event: e.ID, Part: project.PartTrigger, Index: i, Err: err}
				}
				if err := trig.Validate(t); err != nil {
					return &project.Problem{Event: e.ID, Part: project.PartTrigger, Index: i, Kind: t.Type, Err: err}
				}
			}
		}
		if err := m.validateConditions(e.Conditions); err != nil {
			return inEvent(e.ID, err)
		}
		if err := m.validateActions(e.Actions); err != nil {
			return inEvent(e.ID, err)
		}
	}
	return nil
}

// build проверяет все условия и действия проекта и взводит триггеры включённых событий.
func (m *Module) build(p project.Project) (*projectRuntime, error) {
	// Проверка условий и действий всех включённых событий до взведения чего-либо.
	if err := m.validate(p, false); err != nil {
		return nil, err
	}

	// Переменные: прежние значения (при перезагрузке) и сохранённые переживают правку файла.
	var previous map[string]any
	m.evMu.Lock()
	if old := m.runtimes[p.ID]; old != nil {
		previous = old.vars.All()
	}
	m.evMu.Unlock()
	saved := m.persist.load()[p.ID]
	vars, err := newVarStore(p.Variables, previous, saved, func() { m.savePersisted() })
	if err != nil {
		return nil, err
	}

	// Взводим триггеры; ошибка любого — снимаем уже взведённые.
	rt := &projectRuntime{p: p, vars: vars, events: map[string]*eventRuntime{}}
	rt.ctx, rt.cancel = context.WithCancel(m.root)
	for _, e := range p.Events {
		er := &eventRuntime{
			ref: contracts.EventRef{Project: p.ID, Event: e.ID, Name: e.Name},
			ev:  e, proj: rt, cancels: map[*run]context.CancelFunc{},
		}
		rt.events[e.ID] = er
		if !e.IsEnabled() {
			continue
		}
		for _, t := range e.AllTriggers() {
			tt, ok := m.ext.Get(contracts.PointTrigger, t.Type)
			trig, isTrigger := tt.(contracts.TriggerType)
			if !ok || !isTrigger {
				m.teardown(rt)
				return nil, fmt.Errorf("event %q: unknown trigger type %q", e.ID, t.Type)
			}
			disarm, err := trig.Arm(rt.ctx, er.ref, t, func(f contracts.Fire) { m.onFire(er, f) })
			if err != nil {
				m.teardown(rt)
				return nil, fmt.Errorf("event %q: %w", e.ID, err)
			}
			rt.disarms = append(rt.disarms, disarm)
		}
	}
	return rt, nil
}

// teardown снимает триггеры проекта и прерывает его выполнения (их клавиши отпускаются).
func (m *Module) teardown(rt *projectRuntime) {
	for _, d := range rt.disarms {
		d()
	}
	rt.disarms = nil
	rt.cancel()
}

// onFire обрабатывает срабатывание триггера по политике повторного запуска (FR-EV-5).
// Вызывается из потоков модулей-источников и не блокируется.
func (m *Module) onFire(er *eventRuntime, f contracts.Fire) {
	// После экстренной остановки триггеры ничего не запускают до `mkey resume`.
	if m.suspended.Load() {
		return
	}

	// Переключатель: состояние ведёт сам движок (после StopAll следующее нажатие снова включает).
	if f.Toggle != nil {
		on := !er.toggled.Load()
		er.toggled.Store(on)
		if !on {
			return
		}
	}

	// Политика повторного запуска.
	er.mu.Lock()
	switch er.ev.Policy {
	case project.PolicyRestart:
		for _, cancel := range er.cancels {
			cancel()
		}
	case project.PolicyQueue:
		if er.running > 0 {
			if er.queued < maxQueued {
				er.queued++
				er.lastFire = f
			}
			er.mu.Unlock()
			return
		}
	case project.PolicyParallel:
		limit := er.ev.MaxParallel
		if limit <= 0 {
			limit = 4
		}
		if er.running >= limit {
			er.mu.Unlock()
			return
		}
	default: // ignore
		if er.running > 0 {
			er.mu.Unlock()
			return
		}
	}
	er.running++
	er.mu.Unlock()

	// Выполнение в отдельной горутине.
	go m.runLoop(er, f)
}

// runLoop выполняет событие и, для политики queue, накопленные срабатывания.
func (m *Module) runLoop(er *eventRuntime, f contracts.Fire) {
	for {
		// Начало и итог выполнения — на шину (GUI показывает, что сейчас работает).
		m.bus.Publish(contracts.TopicEventStarted, er.ref)
		err := m.runEvent(er.proj.ctx, er, f)
		fin := contracts.EventFinished{EventRef: er.ref}
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errStopSelf) {
			fin.Error = err.Error()
		}
		m.bus.Publish(contracts.TopicEventFinished, fin)
		if err != nil {
			m.logRunError(er, err)
		}

		// Следующее срабатывание из очереди или завершение.
		er.mu.Lock()
		if er.queued > 0 && er.proj.ctx.Err() == nil {
			er.queued--
			f = er.lastFire
			er.mu.Unlock()
			continue
		}
		er.running--
		er.mu.Unlock()
		return
	}
}

// logRunError записывает ошибку выполнения события (остановка — не ошибка).
func (m *Module) logRunError(er *eventRuntime, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, errStopSelf) {
		return
	}
	m.log.Warn("event failed", "project", er.ref.Project, "event", er.ref.Event, "err", err)
}

// runEvent выполняет событие один раз: условия, отпускание модификаторов, замена hotstring, действия.
func (m *Module) runEvent(parent context.Context, er *eventRuntime, f contracts.Fire) error {
	// Раннер события: всё зажатое действиями отпускается в конце.
	ctx, r, finish := m.newRun(parent)
	defer finish()
	er.mu.Lock()
	er.cancels[r] = r.cancel
	er.mu.Unlock()
	defer func() {
		er.mu.Lock()
		delete(er.cancels, r)
		er.mu.Unlock()
	}()
	rc := &runCtx{m: m, er: er, run: r, fire: f, log: m.log.With("project", er.ref.Project, "event", er.ref.Event)}

	// Условия проекта (active_when) и события.
	ok, err := rc.Check(ctx, er.proj.p.ActiveWhen)
	if err == nil && ok {
		ok, err = rc.Check(ctx, er.ev.Conditions)
	}
	if err != nil || !ok {
		return err
	}

	// Модификаторы горячей клавиши не должны смешиваться с нажатиями макроса (FR-HK-3).
	if f.Modifiers != nil && er.ev.ReleaseModifiers != "never" {
		f.Modifiers.Release()
		defer f.Modifiers.Restore()
	}

	// Замена hotstring перед действиями.
	if s, ok := f.Vars["replace_dsl"].(string); ok && s != "" {
		if err := rc.Send(ctx, s); err != nil {
			return err
		}
	}

	// Действия.
	err = rc.RunActions(ctx, er.ev.Actions)
	if errors.Is(err, errStopSelf) {
		return nil
	}
	return err
}

// findEvent возвращает событие взведённого проекта.
func (m *Module) findEvent(projectID, eventID string) (*eventRuntime, bool) {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	rt := m.runtimes[projectID]
	if rt == nil {
		return nil, false
	}
	er, ok := rt.events[eventID]
	return er, ok
}

// Statuses возвращает состояние всех событий всех проектов (contracts.Events).
func (m *Module) Statuses() []contracts.EventStatus {
	var out []contracts.EventStatus
	if m.projects == nil {
		return out
	}
	for _, st := range m.projects.List() {
		for _, e := range st.Project.Events {
			s := contracts.EventStatus{
				EventRef: contracts.EventRef{Project: st.Project.ID, Event: e.ID, Name: e.Name},
				Enabled:  st.Project.IsEnabled() && e.IsEnabled(),
			}
			for _, t := range e.AllTriggers() {
				s.Triggers = append(s.Triggers, t.Type)
			}
			if er, ok := m.findEvent(st.Project.ID, e.ID); ok {
				er.mu.Lock()
				s.Running = er.running
				er.mu.Unlock()
				s.Toggled = er.toggled.Load()
			}
			out = append(out, s)
		}
	}
	return out
}

// RunEvent запускает событие вручную и ждёт завершения (contracts.Events.Run).
func (m *Module) RunEvent(ctx context.Context, projectID, eventID string) error {
	er, ok := m.findEvent(projectID, eventID)
	if !ok {
		return fmt.Errorf("%s/%s: %w", projectID, eventID, contracts.ErrEventInactive)
	}
	err := m.runEvent(ctx, er, contracts.Fire{})
	if errors.Is(err, errStopSelf) {
		return nil
	}
	return err
}

// Vars возвращает переменные взведённого проекта (nil, если проект не активен).
func (m *Module) Vars(projectID string) contracts.VarStore {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	if rt := m.runtimes[projectID]; rt != nil {
		return rt.vars
	}
	return nil
}

// resetToggles выключает все переключатели (после StopAll циклы while: toggled не возобновятся сами).
func (m *Module) resetToggles() {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	for _, rt := range m.runtimes {
		for _, er := range rt.events {
			er.toggled.Store(false)
		}
	}
}

// savePersisted записывает сохраняемые переменные всех проектов.
func (m *Module) savePersisted() {
	all := m.persist.load()
	m.evMu.Lock()
	for id, rt := range m.runtimes {
		if p := rt.vars.persisted(); len(p) > 0 {
			all[id] = p
		}
	}
	m.evMu.Unlock()
	if err := m.persist.save(all); err != nil {
		m.log.Warn("save variables", "err", err)
	}
}

// validateActions проверяет список действий по реестру видов действий.
// Ошибка — *project.Problem с номером блока (ID события заполняет вызывающий код, см. inEvent).
func (m *Module) validateActions(actions []project.Action) error {
	for i, a := range actions {
		at, err := m.actionType(a.Type)
		if err != nil {
			return &project.Problem{Part: project.PartAction, Index: i, Err: err}
		}
		if err := at.Validate(a); err != nil {
			return &project.Problem{Part: project.PartAction, Index: i, Kind: a.Type, Err: err}
		}
	}
	return nil
}

// inEvent дописывает ID события в ошибку проверки условий или действий события.
// Для вложенных списков («Повторять», «Если») место остаётся у внешнего блока, а внутренняя
// ошибка сохраняется в цепочке (её поле найдёт errors.As).
func inEvent(eventID string, err error) error {
	var p *project.Problem
	if errors.As(err, &p) && p.Event == "" {
		p.Event = eventID
		return p
	}
	return &project.Problem{Event: eventID, Index: -1, Err: err}
}

// validateConditions проверяет список условий по реестру видов условий.
// Ошибка — *project.Problem с номером условия (ID события заполняет вызывающий код).
func (m *Module) validateConditions(conds []project.Condition) error {
	for i, c := range conds {
		ct, err := m.conditionType(c.Type)
		if err != nil {
			return &project.Problem{Part: project.PartCondition, Index: i, Err: err}
		}
		if err := ct.Validate(c); err != nil {
			return &project.Problem{Part: project.PartCondition, Index: i, Kind: c.Type, Err: err}
		}
	}
	return nil
}

// actionType находит вид действия в реестре.
func (m *Module) actionType(typ string) (contracts.ActionType, error) {
	ext, ok := m.ext.Get(contracts.PointAction, typ)
	at, isAction := ext.(contracts.ActionType)
	if !ok || !isAction {
		return nil, fmt.Errorf("unknown action %q (available: %s)", typ, m.available(contracts.PointAction))
	}
	return at, nil
}

// conditionType находит вид условия в реестре.
func (m *Module) conditionType(typ string) (contracts.ConditionType, error) {
	ext, ok := m.ext.Get(contracts.PointCondition, typ)
	ct, isCond := ext.(contracts.ConditionType)
	if !ok || !isCond {
		return nil, fmt.Errorf("unknown condition %q (available: %s)", typ, m.available(contracts.PointCondition))
	}
	return ct, nil
}

// available перечисляет зарегистрированные виды точки расширения (для сообщений об ошибках).
func (m *Module) available(point contracts.ExtensionPoint) string {
	var ids []string
	for _, e := range m.ext.List(point) {
		ids = append(ids, e.Meta().ID)
	}
	slices.Sort(ids)
	return strings.Join(ids, ", ")
}

// runCtx — контекст одного выполнения события (contracts.RunContext).
type runCtx struct {
	m    *Module
	er   *eventRuntime
	run  *run
	fire contracts.Fire
	log  *slog.Logger
}

// Event возвращает выполняемое событие.
func (rc *runCtx) Event() contracts.EventRef { return rc.er.ref }

// Send выполняет макрос в раннере события (зажатое остаётся зажатым до конца события).
func (rc *runCtx) Send(ctx context.Context, src string) error {
	return rc.m.execSource(ctx, rc.run, src)
}

// RunActions выполняет действия по порядку.
func (rc *runCtx) RunActions(ctx context.Context, actions []project.Action) error {
	for _, a := range actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		at, err := rc.m.actionType(a.Type)
		if err != nil {
			return err
		}
		if err := at.Run(ctx, rc, a); err != nil {
			return err
		}
	}
	return nil
}

// Check вычисляет условия: все должны выполняться.
func (rc *runCtx) Check(ctx context.Context, conds []project.Condition) (bool, error) {
	for _, c := range conds {
		ct, err := rc.m.conditionType(c.Type)
		if err != nil {
			return false, err
		}
		ok, err := ct.Check(ctx, rc, c)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// Toggled сообщает состояние переключателя события.
func (rc *runCtx) Toggled() bool { return rc.er.toggled.Load() }

// Held сообщает, удерживается ли клавиша триггера.
func (rc *runCtx) Held() bool { return rc.fire.Held != nil && rc.fire.Held() }

// Fire возвращает срабатывание, запустившее выполнение.
func (rc *runCtx) Fire() contracts.Fire { return rc.fire }

// Vars возвращает переменные проекта.
func (rc *runCtx) Vars() contracts.VarStore { return rc.er.proj.vars }

// Logger возвращает логгер события.
func (rc *runCtx) Logger() *slog.Logger { return rc.log }

// Проверки на этапе компиляции.
var (
	_ contracts.RunContext = (*runCtx)(nil)
	_ contracts.VarStore   = (*varStore)(nil)
)
