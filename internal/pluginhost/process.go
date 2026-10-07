package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/jsonrpc"
)

// Тайминги плагина-процесса (ADR-0029 п. 7).
const (
	// initTimeout — сколько ждать ответа на initialize (запуск интерпретатора Python — до секунды).
	initTimeout = 5 * time.Second
	// shortTimeout — проверки параметров, условия, взвод триггеров, ping, shutdown.
	shortTimeout = 2 * time.Second
	// pingEvery — как часто проверять, что плагин отвечает.
	pingEvery = 30 * time.Second
	// maxBackoff — предел паузы перед перезапуском.
	maxBackoff = 30 * time.Second
	// crashWindow и maxCrashes — три падения за минуту отключают плагин.
	crashWindow = time.Minute
	maxCrashes  = 3
	// maxLog — журнал плагина больше 1 МиБ при запуске начинается заново.
	maxLog = 1 << 20
)

// typeInfo — вид из ответа плагина на initialize.
type typeInfo struct {
	ID           string          `json:"id"`
	Name         Text            `json:"name"`
	Description  Text            `json:"description"`
	Category     string          `json:"category"`
	Icon         string          `json:"icon"`
	ParamsSchema json.RawMessage `json:"params_schema"`
}

// initResult — ответ на initialize: виды плагина.
type initResult struct {
	Actions    []typeInfo `json:"actions"`
	Conditions []typeInfo `json:"conditions"`
	Triggers   []typeInfo `json:"triggers"`
}

// UnmarshalJSON принимает строку (один текст на все языки) или объект языков.
func (t *Text) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*t = Text{"en": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*t = m
	return nil
}

// armed — взведённый триггер плагина: что взводить заново после перезапуска и кого звать.
type armed struct {
	typ    string
	params map[string]any
	event  contracts.EventRef
	fire   func(contracts.Fire)
}

// plugin — найденный плагин и его работа.
type plugin struct {
	h      *Module
	man    Manifest
	dir    string
	system bool

	// mu защищает всё ниже.
	mu sync.Mutex
	// active — включён человеком; state и err — состояние (contracts.Plugin*) и причина.
	active bool
	state  string
	err    string
	// conn — соединение с работающим процессом (nil — не работает); lua — загруженный Lua-плагин.
	conn *jsonrpc.Conn
	lua  contracts.LuaPlugin
	// types — зарегистрированные виды по точкам.
	types map[contracts.ExtensionPoint][]string
	// triggers — взведённые триггеры по handle; nextHandle — номер следующего.
	triggers   map[string]*armed
	nextHandle int
	// stop и done — остановка надзора и его завершение (nil — не запущен).
	stop context.CancelFunc
	done chan struct{}
}

// info возвращает сведения о плагине для окна и команд.
func (p *plugin) info() contracts.PluginInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return contracts.PluginInfo{
		ID: p.man.ID, Version: p.man.Version, Kind: p.man.Kind, Name: p.man.Name, Description: p.man.Description,
		Dir: p.dir, System: p.system, Permissions: p.man.Permissions, Active: p.active, State: p.state, Error: p.err,
		Actions: p.types[contracts.PointAction], Conditions: p.types[contracts.PointCondition], Triggers: p.types[contracts.PointTrigger],
		Templates: p.types[contracts.PointProjectTemplate],
	}
}

// setState меняет состояние и причину.
func (p *plugin) setState(state, why string) {
	p.mu.Lock()
	p.state, p.err = state, why
	p.mu.Unlock()
}

// running возвращает соединение с работающим плагином или понятную ошибку.
func (p *plugin) running() (*jsonrpc.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil, fmt.Errorf("plugin %q is not running (%s)", p.man.ID, p.state)
	}
	return p.conn, nil
}

// call вызывает метод плагина; timeout > 0 — с ограничением времени.
func (p *plugin) call(ctx context.Context, timeout time.Duration, method string, params, result any) error {
	conn, err := p.running()
	if err != nil {
		return err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	err = conn.Call(ctx, method, params, result)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("plugin %q did not answer %s in %s", p.man.ID, method, timeout)
	}
	return err
}

// start запускает надзор за плагином (если ещё не запущен).
func (p *plugin) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.stop, p.done = cancel, make(chan struct{})
	p.state, p.err = contracts.PluginStarting, ""
	go p.supervise(ctx, p.done)
}

// halt останавливает плагин и ждёт завершения надзора.
func (p *plugin) halt() {
	p.mu.Lock()
	stop, done := p.stop, p.done
	p.stop, p.done = nil, nil
	p.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

// supervise запускает процесс и перезапускает его при падениях: пауза 1, 2, 4… с; три падения
// за минуту — плагин отключается (FR-PLG-3).
func (p *plugin) supervise(ctx context.Context, done chan struct{}) {
	defer close(done)
	var crashes []time.Time
	backoff := time.Second
	for {
		// Один запуск процесса до его завершения или остановки.
		began := time.Now()
		err := p.session(ctx)
		if ctx.Err() != nil {
			p.setState(contracts.PluginOff, "")
			return
		}

		// Падение: учёт, отключение после частых падений, пауза перед перезапуском.
		now := time.Now()
		crashes = slices.DeleteFunc(crashes, func(c time.Time) bool { return now.Sub(c) >= crashWindow })
		crashes = append(crashes, now)
		why := "stopped"
		if err != nil {
			why = err.Error()
		}
		p.h.log.Warn("plugin crashed", "plugin", p.man.ID, "err", why)
		if len(crashes) >= maxCrashes {
			p.setState(contracts.PluginFailed, why)
			p.h.unregister(p)
			p.h.notifyFailed(p, why)
			return
		}
		if now.Sub(began) > crashWindow {
			backoff = time.Second
		}
		p.setState(contracts.PluginRestarting, why)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			p.setState(contracts.PluginOff, "")
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session запускает процесс, знакомится с ним (initialize), регистрирует виды, взводит триггеры
// и ждёт его завершения, остановки или молчания на ping. Возвращает причину завершения.
func (p *plugin) session(ctx context.Context) error {
	// Журнал (stderr плагина) и процесс в своей группе — чтобы остановить и его дочерние процессы.
	logf, err := p.h.openLog(p.man.ID)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	path, err := p.man.entryPath(p.dir)
	if err != nil {
		return err
	}
	cmd := exec.Command(path, p.man.Args...)
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(), "MKEY_PLUGIN_ID="+p.man.ID, "MKEY_PLUGIN_DIR="+p.dir)
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.man.Entry, err)
	}
	exited := make(chan error, 1)
	conn := jsonrpc.New(stdout, stdin, p.handle)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		conn.Close()
		p.mu.Lock()
		p.conn = nil
		p.mu.Unlock()
	}()
	kill := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}

	// Знакомство: версии и виды плагина.
	var res initResult
	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	err = conn.Call(ictx, "initialize", map[string]any{
		"plugin_api": APIVersion, "mkey_version": p.h.version, "lang": p.h.tr.Lang(), "permissions": p.man.Permissions,
	}, &res)
	cancel()
	if err != nil {
		kill()
		return fmt.Errorf("initialize: %w", err)
	}

	// Работает: виды — в реестры, триггеры — взвести заново.
	p.mu.Lock()
	p.conn = conn
	p.state, p.err = contracts.PluginRunning, ""
	p.mu.Unlock()
	p.h.register(p, res)
	p.rearm(ctx)
	p.h.log.Info("plugin started", "plugin", p.man.ID, "actions", len(res.Actions), "conditions", len(res.Conditions), "triggers", len(res.Triggers))

	// Ждём: остановку, завершение процесса или молчание на ping.
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			// Остановка: просим завершиться, через 2 с — принудительно.
			sctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
			_ = conn.Call(sctx, "shutdown", nil, nil)
			cancel()
			_ = stdin.Close()
			select {
			case <-exited:
			case <-time.After(shortTimeout):
				kill()
			}
			return nil
		case err := <-exited:
			if err == nil {
				err = errors.New("process exited")
			}
			return err
		case <-ping.C:
			if err := p.call(ctx, shortTimeout, "ping", nil, nil); err != nil {
				kill()
				return fmt.Errorf("not responding: %w", err)
			}
		}
	}
}

// rearm взводит у работающего плагина все триггеры, взведённые движком (после запуска и перезапуска).
func (p *plugin) rearm(ctx context.Context) {
	p.mu.Lock()
	list := make(map[string]*armed, len(p.triggers))
	for h, a := range p.triggers {
		list[h] = a
	}
	p.mu.Unlock()
	for h, a := range list {
		params := map[string]any{"type": a.typ, "params": a.params, "handle": h, "event": a.event}
		if err := p.call(ctx, shortTimeout, "trigger.arm", params, nil); err != nil {
			p.h.log.Warn("plugin trigger not armed", "plugin", p.man.ID, "type", a.typ, "err", err)
		}
	}
}

// openLog открывает журнал плагина на дозапись; больше maxLog — начинается заново.
func (m *Module) openLog(id string) (*os.File, error) {
	if err := os.MkdirAll(m.cfg.LogDir, 0o700); err != nil {
		return nil, err
	}
	path := m.logPath(id)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxLog {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}

// logPath — файл журнала плагина.
func (m *Module) logPath(id string) string { return filepath.Join(m.cfg.LogDir, id+".log") }
