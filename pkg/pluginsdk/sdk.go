package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/khameleonium/mKey/internal/lib/jsonrpc"
)

// APIVersion — мажорная версия API плагинов, которую реализует этот пакет.
const APIVersion = 1

// Text — текст на языках: "ru" → «Привет», "en" → "Hello".
type Text map[string]string

// Type — описание вида: ID (как его пишут в проекте), название и описание на языках, категория
// в палитре конструктора и JSON Schema параметров (по ней окно строит форму блока).
type Type struct {
	ID          string          `json:"id"`
	Name        Text            `json:"name,omitempty"`
	Description Text            `json:"description,omitempty"`
	Category    string          `json:"category,omitempty"`
	Icon        string          `json:"icon,omitempty"`
	Params      json.RawMessage `json:"params_schema,omitempty"`
}

// EventRef — событие проекта, которое выполняется.
type EventRef struct {
	Project string `json:"project"`
	Event   string `json:"event"`
	Name    string `json:"name,omitempty"`
}

// Call — один вызов вида: его ID, параметры из проекта, событие, значения от триггера и
// доступ к mKey.
type Call struct {
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
	Event  EventRef        `json:"event"`
	Vars   map[string]any  `json:"vars"`
	// Host — запросы к mKey (нажатия, переменные, уведомления).
	Host *Host `json:"-"`
}

// Decode разбирает параметры вида в v (структуру с json-тегами).
func (c *Call) Decode(v any) error {
	if len(c.Params) == 0 || string(c.Params) == "null" {
		return nil
	}
	return json.Unmarshal(c.Params, v)
}

// Функции видов.
type (
	// RunFunc выполняет действие; ctx отменяется, когда mKey останавливает событие.
	RunFunc func(ctx context.Context, c *Call) error
	// CheckFunc вычисляет условие (mKey ждёт не больше 2 с).
	CheckFunc func(ctx context.Context, c *Call) (bool, error)
	// ArmFunc следит за триггером, пока ctx не отменён (триггер сняли или mKey остановился),
	// и вызывает fire при каждом срабатывании (vars — значения для действий события).
	ArmFunc func(ctx context.Context, c *Call, fire func(vars map[string]any)) error
	// ValidateFunc проверяет параметры; текст ошибки увидит человек (язык — Plugin.Lang).
	ValidateFunc func(c *Call) error
)

// Plugin — плагин: его виды и соединение с mKey.
type Plugin struct {
	// mu защищает всё ниже.
	mu         sync.Mutex
	actions    map[string]RunFunc
	conditions map[string]CheckFunc
	triggers   map[string]ArmFunc
	validators map[string]ValidateFunc
	types      struct{ Actions, Conditions, Triggers []Type }
	// armed — отмена следящих триггеров по handle.
	armed map[string]context.CancelFunc
	// lang и permissions — из initialize.
	lang        string
	permissions []string
	host        *Host
}

// New создаёт плагин без видов.
func New() *Plugin {
	p := &Plugin{
		actions: map[string]RunFunc{}, conditions: map[string]CheckFunc{}, triggers: map[string]ArmFunc{},
		validators: map[string]ValidateFunc{}, armed: map[string]context.CancelFunc{},
	}
	p.types.Actions, p.types.Conditions, p.types.Triggers = []Type{}, []Type{}, []Type{}
	return p
}

// Action добавляет действие.
func (p *Plugin) Action(t Type, run RunFunc) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.actions[t.ID] = run
	p.types.Actions = append(p.types.Actions, t)
	return p
}

// Condition добавляет условие.
func (p *Plugin) Condition(t Type, check CheckFunc) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conditions[t.ID] = check
	p.types.Conditions = append(p.types.Conditions, t)
	return p
}

// Trigger добавляет триггер.
func (p *Plugin) Trigger(t Type, arm ArmFunc) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggers[t.ID] = arm
	p.types.Triggers = append(p.types.Triggers, t)
	return p
}

// Validate задаёт проверку параметров вида typeID (любого: действия, условия, триггера).
func (p *Plugin) Validate(typeID string, fn ValidateFunc) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.validators[typeID] = fn
	return p
}

// Lang возвращает язык человека ("ru", "en") — для текстов ошибок.
func (p *Plugin) Lang() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lang
}

// Run работает с mKey через stdin/stdout до команды shutdown или закрытия stdin.
// Ошибка — соединение оборвалось не по команде mKey.
func (p *Plugin) Run() error {
	return p.Serve(os.Stdin, os.Stdout)
}

// Serve работает с mKey через r и w (для тестов; обычно — Run).
func (p *Plugin) Serve(r io.Reader, w io.Writer) error {
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	conn := jsonrpc.New(r, w, func(ctx context.Context, method string, params json.RawMessage, _ bool) (any, error) {
		if method == "shutdown" {
			stop()
			return nil, nil
		}
		return p.handle(ctx, method, params)
	})
	p.mu.Lock()
	p.host = &Host{conn: conn}
	p.mu.Unlock()

	// Ждём команду завершения или конец потока.
	var err error
	select {
	case <-done:
	case <-conn.Done():
		if e := conn.Err(); !errors.Is(e, jsonrpc.ErrClosed) {
			err = e
		}
	}

	// Следящие триггеры — остановить; ответ на shutdown и другие ответы — успеть отправить
	// (не дольше секунды: mKey ждёт завершения 2 с).
	p.mu.Lock()
	for _, cancel := range p.armed {
		cancel()
	}
	p.mu.Unlock()
	flushed := make(chan struct{})
	go func() { conn.Wait(); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(time.Second):
	}
	conn.Close()
	return err
}

// request — параметры вызовов mKey → плагин.
type request struct {
	Call
	Handle string `json:"handle"`
}

// handle выполняет вызов mKey (docs/plugins.md, «Методы mKey → плагин»).
func (p *Plugin) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var req request
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%v", err)
		}
	}
	p.mu.Lock()
	req.Host = p.host
	p.mu.Unlock()

	switch method {
	case "initialize":
		// Язык и разрешения; ответ — виды.
		var init struct {
			Lang        string   `json:"lang"`
			Permissions []string `json:"permissions"`
			PluginAPI   int      `json:"plugin_api"`
		}
		_ = json.Unmarshal(params, &init)
		if init.PluginAPI != 0 && init.PluginAPI != APIVersion {
			return nil, jsonrpc.Errorf(jsonrpc.CodeFailed, "plugin API %d is not supported (plugin needs %d)", init.PluginAPI, APIVersion)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.lang, p.permissions = init.Lang, init.Permissions
		return map[string]any{"actions": p.types.Actions, "conditions": p.types.Conditions, "triggers": p.types.Triggers}, nil

	case "ping":
		return nil, nil

	case "action.validate", "condition.validate", "trigger.validate":
		// Своя проверка, если задана; иначе — параметры принимаются.
		p.mu.Lock()
		fn := p.validators[req.Type]
		p.mu.Unlock()
		if fn == nil {
			return nil, nil
		}
		return nil, fn(&req.Call)

	case "action.run":
		p.mu.Lock()
		run := p.actions[req.Type]
		p.mu.Unlock()
		if run == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown action %q", req.Type)
		}
		return nil, run(ctx, &req.Call)

	case "condition.check":
		p.mu.Lock()
		check := p.conditions[req.Type]
		p.mu.Unlock()
		if check == nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown condition %q", req.Type)
		}
		ok, err := check(ctx, &req.Call)
		return map[string]bool{"result": ok}, err

	case "trigger.arm":
		return nil, p.arm(req)

	case "trigger.disarm":
		p.mu.Lock()
		if cancel, ok := p.armed[req.Handle]; ok {
			cancel()
			delete(p.armed, req.Handle)
		}
		p.mu.Unlock()
		return nil, nil
	}
	return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "method %q not found", method)
}

// arm запускает слежение за триггером в своей горутине до снятия.
func (p *Plugin) arm(req request) error {
	p.mu.Lock()
	armFn := p.triggers[req.Type]
	host := p.host
	if armFn == nil {
		p.mu.Unlock()
		return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown trigger %q", req.Type)
	}
	if old, ok := p.armed[req.Handle]; ok {
		old()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.armed[req.Handle] = cancel
	p.mu.Unlock()

	// Слежение; ошибка — в журнал mKey (взвод уже подтверждён).
	handle := req.Handle
	call := req.Call
	go func() {
		fire := func(vars map[string]any) {
			_ = host.conn.Notify("trigger.fire", map[string]any{"handle": handle, "vars": vars})
		}
		if err := armFn(ctx, &call, fire); err != nil && ctx.Err() == nil {
			_ = host.Log(context.Background(), "error", fmt.Sprintf("trigger %s: %v", call.Type, err))
		}
	}()
	return nil
}

// Host — запросы плагина к mKey. Методы, требующие разрешений (docs/plugins.md), без них
// возвращают ошибку с кодом -32001.
type Host struct{ conn *jsonrpc.Conn }

// Send нажимает клавиши макросом mKey ("{Ctrl}{C}", docs/dsl.md); разрешение output.send.
func (h *Host) Send(ctx context.Context, macro string) error {
	return h.conn.Call(ctx, "mkey.send", map[string]string{"macro": macro}, nil)
}

// GetVar возвращает переменную проекта; разрешение vars.read.
func (h *Host) GetVar(ctx context.Context, project, name string) (any, error) {
	var res struct {
		Value any `json:"value"`
	}
	err := h.conn.Call(ctx, "mkey.vars.get", map[string]string{"project": project, "name": name}, &res)
	return res.Value, err
}

// SetVar меняет переменную проекта; разрешение vars.write.
func (h *Host) SetVar(ctx context.Context, project, name string, value any) error {
	return h.conn.Call(ctx, "mkey.vars.set", map[string]any{"project": project, "name": name, "value": value}, nil)
}

// Notify показывает уведомление; разрешение notify.
func (h *Host) Notify(ctx context.Context, title, body string) error {
	return h.conn.Call(ctx, "mkey.notify", map[string]string{"title": title, "body": body}, nil)
}

// Log пишет строку в журнал mKey (level: "info", "warn", "error").
func (h *Host) Log(ctx context.Context, level, message string) error {
	return h.conn.Call(ctx, "mkey.log", map[string]string{"level": level, "message": message}, nil)
}
