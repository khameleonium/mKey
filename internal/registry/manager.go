package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/khameleonium/mKey/internal/contracts"
)

// State — состояние модуля в менеджере.
type State string

// Возможные состояния модуля.
const (
	// StatePending — модуль зарегистрирован, но ещё не инициализирован.
	StatePending State = "pending"
	// StateDisabled — необязательный модуль отключён в конфиге и не запускался.
	StateDisabled State = "disabled"
	// StateInitialized — Init прошёл успешно, Start ещё не вызывался.
	StateInitialized State = "initialized"
	// StateRunning — Start прошёл успешно, модуль работает.
	StateRunning State = "running"
	// StateFailed — Init или Start завершились ошибкой; модуль остановлен.
	StateFailed State = "failed"
	// StateStopped — модуль остановлен штатно.
	StateStopped State = "stopped"
)

// Entry — модуль в составе программы.
type Entry struct {
	// Module — сам модуль.
	Module contracts.Module
	// Core — обязательный модуль: его ошибка останавливает запуск программы.
	Core bool
}

// Status — текущее состояние модуля (для диагностики, GUI и тестов).
type Status struct {
	// ID — идентификатор модуля.
	ID string
	// Core — обязательный ли модуль.
	Core bool
	// State — текущее состояние.
	State State
	// Err — последняя ошибка модуля, если была.
	Err error
}

// Options — зависимости менеджера модулей.
type Options struct {
	// Logger — базовый логгер; каждому модулю выдаётся дочерний с полем module.
	Logger *slog.Logger
	// Translator — переводчик сообщений, общий для всех модулей.
	Translator contracts.Translator
	// Bus — шина событий. Если nil, менеджер требует её явно (ошибка в New).
	Bus contracts.Bus
	// Services — реестр сервисов. Если nil, создаётся новый.
	Services *Services
	// Extensions — реестр точек расширения. Если nil, создаётся новый.
	Extensions *Extensions
	// Enabled сообщает, включён ли необязательный модуль. nil — включены все.
	Enabled func(id string) bool
	// Config возвращает секцию конфига модуля. nil — всем модулям пустые секции.
	Config func(id string) contracts.ConfigSection
}

// Manager управляет жизненным циклом модулей: Init → Start → Stop.
type Manager struct {
	// opts — зависимости, переданные при создании.
	opts Options
	// mu защищает состояния модулей.
	mu sync.Mutex
	// items — модули в порядке регистрации вместе с их состоянием.
	items []*item
}

// item — модуль и его состояние внутри менеджера.
type item struct {
	entry Entry
	state State
	err   error
}

// NewManager проверяет список модулей и создаёт менеджер.
// Ошибка возвращается при пустом или повторяющемся ID модуля и при отсутствии обязательных зависимостей.
func NewManager(opts Options, entries []Entry) (*Manager, error) {
	// Проверяем обязательные зависимости и подставляем значения по умолчанию.
	if opts.Logger == nil || opts.Translator == nil || opts.Bus == nil {
		return nil, errors.New("module manager: logger, translator and bus are required")
	}
	if opts.Services == nil {
		opts.Services = NewServices()
	}
	if opts.Extensions == nil {
		opts.Extensions = NewExtensions()
	}

	// Проверяем модули: ID непустой и уникальный.
	seen := make(map[string]bool, len(entries))
	items := make([]*item, 0, len(entries))
	for _, e := range entries {
		if e.Module == nil {
			return nil, errors.New("module manager: nil module")
		}
		id := e.Module.ID()
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("module manager: module %T has empty id", e.Module)
		}
		if seen[id] {
			return nil, fmt.Errorf("module manager: duplicate module id %q", id)
		}
		seen[id] = true
		items = append(items, &item{entry: e, state: StatePending})
	}
	// Ядро само публикует сведения о модулях, чтобы их могли показать API и диагностика.
	m := &Manager{opts: opts, items: items}
	if err := contracts.ProvideService[contracts.Modules](opts.Services, m); err != nil {
		return nil, fmt.Errorf("module manager: %w", err)
	}
	return m, nil
}

// ModuleStatuses возвращает состояния модулей в виде контракта contracts.Modules.
func (m *Manager) ModuleStatuses() []contracts.ModuleStatus {
	statuses := m.Statuses()
	out := make([]contracts.ModuleStatus, len(statuses))
	for i, s := range statuses {
		out[i] = contracts.ModuleStatus{ID: s.ID, Core: s.Core, State: string(s.State)}
		if s.Err != nil {
			out[i].Error = s.Err.Error()
		}
	}
	return out
}

// Services возвращает реестр сервисов менеджера.
func (m *Manager) Services() *Services { return m.opts.Services }

// Extensions возвращает реестр точек расширения менеджера.
func (m *Manager) Extensions() *Extensions { return m.opts.Extensions }

// Bus возвращает шину событий, общую для всех модулей.
func (m *Manager) Bus() contracts.Bus { return m.opts.Bus }

// Start инициализирует и запускает все включённые модули.
//
// Ошибка необязательного модуля записывается в его статус, модуль останавливается,
// запуск продолжается. Ошибка обязательного модуля останавливает всё, что уже
// инициализировано, и возвращается вызывающему.
func (m *Manager) Start(ctx context.Context) error {
	// Этап 1: Init всех включённых модулей по порядку регистрации.
	for _, it := range m.items {
		if !it.entry.Core && m.opts.Enabled != nil && !m.opts.Enabled(it.entry.Module.ID()) {
			m.setState(it, StateDisabled, nil)
			continue
		}
		err := safeCall(func() error { return it.entry.Module.Init(ctx, m.hostFor(it)) })
		if err := m.handleResult(ctx, it, "init", err, StateInitialized); err != nil {
			return err
		}
	}

	// Этап 2: Start всех успешно инициализированных модулей в том же порядке.
	for _, it := range m.items {
		if m.state(it) != StateInitialized {
			continue
		}
		err := safeCall(func() error { return it.entry.Module.Start(ctx) })
		if err := m.handleResult(ctx, it, "start", err, StateRunning); err != nil {
			return err
		}
	}
	return nil
}

// Stop останавливает все инициализированные и работающие модули в обратном порядке.
// Возвращает объединение всех ошибок остановки.
func (m *Manager) Stop(ctx context.Context) error {
	var errs []error

	// Идём с конца: модули, запущенные позже, могут зависеть от запущенных раньше.
	for i := len(m.items) - 1; i >= 0; i-- {
		it := m.items[i]
		st := m.state(it)
		if st != StateInitialized && st != StateRunning {
			continue
		}
		if err := safeCall(func() error { return it.entry.Module.Stop(ctx) }); err != nil {
			errs = append(errs, fmt.Errorf("stop module %q: %w", it.entry.Module.ID(), err))
			m.setState(it, StateStopped, err)
			continue
		}
		m.setState(it, StateStopped, nil)
	}
	return errors.Join(errs...)
}

// Statuses возвращает состояния всех модулей в порядке регистрации.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.items))
	for _, it := range m.items {
		out = append(out, Status{ID: it.entry.Module.ID(), Core: it.entry.Core, State: it.state, Err: it.err})
	}
	return out
}

// handleResult обрабатывает результат Init/Start модуля it.
// При успехе переводит модуль в состояние ok. При ошибке необязательного модуля — останавливает
// только его; при ошибке обязательного — останавливает все модули и возвращает ошибку.
func (m *Manager) handleResult(ctx context.Context, it *item, phase string, err error, ok State) error {
	id := it.entry.Module.ID()

	// Успех: фиксируем новое состояние.
	if err == nil {
		m.setState(it, ok, nil)
		return nil
	}

	// Ошибка: логируем и пытаемся освободить ресурсы сбойного модуля (Stop обязан это выдерживать).
	wrapped := fmt.Errorf("%s module %q: %w", phase, id, err)
	m.opts.Logger.Error("module failed", "module", id, "phase", phase, "core", it.entry.Core, "err", err)
	if stopErr := safeCall(func() error { return it.entry.Module.Stop(ctx) }); stopErr != nil {
		m.opts.Logger.Error("module stop after failure", "module", id, "err", stopErr)
	}
	m.setState(it, StateFailed, wrapped)

	// Необязательный модуль: продолжаем без него.
	if !it.entry.Core {
		return nil
	}

	// Обязательный модуль: останавливаем всё уже поднятое и прерываем запуск.
	if stopErr := m.Stop(ctx); stopErr != nil {
		return errors.Join(wrapped, stopErr)
	}
	return wrapped
}

// hostFor собирает Host для конкретного модуля.
func (m *Manager) hostFor(it *item) contracts.Host {
	// Определяем секцию конфига модуля (пустую, если провайдер конфига не задан).
	id := it.entry.Module.ID()
	var cfg contracts.ConfigSection = RawConfig(nil)
	if m.opts.Config != nil {
		if c := m.opts.Config(id); c != nil {
			cfg = c
		}
	}

	// Собираем хост с логгером, помеченным ID модуля.
	return &host{
		logger:     m.opts.Logger.With("module", id),
		config:     cfg,
		translator: m.opts.Translator,
		services:   m.opts.Services,
		extensions: m.opts.Extensions,
		bus:        m.opts.Bus,
	}
}

// state возвращает текущее состояние модуля под блокировкой.
func (m *Manager) state(it *item) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return it.state
}

// setState меняет состояние и ошибку модуля под блокировкой.
func (m *Manager) setState(it *item, s State, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it.state = s
	it.err = err
}

// safeCall вызывает f и превращает панику в ошибку, чтобы сбой модуля не ронял программу (NFR-7).
func safeCall(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f()
}

// host — реализация contracts.Host для одного модуля.
type host struct {
	logger     *slog.Logger
	config     contracts.ConfigSection
	translator contracts.Translator
	services   contracts.ServiceRegistry
	extensions contracts.ExtensionRegistry
	bus        contracts.Bus
}

// Logger возвращает логгер модуля.
func (h *host) Logger() *slog.Logger { return h.logger }

// Config возвращает секцию конфига модуля.
func (h *host) Config() contracts.ConfigSection { return h.config }

// I18n возвращает переводчик.
func (h *host) I18n() contracts.Translator { return h.translator }

// Services возвращает реестр сервисов.
func (h *host) Services() contracts.ServiceRegistry { return h.services }

// Extensions возвращает реестр точек расширения.
func (h *host) Extensions() contracts.ExtensionRegistry { return h.extensions }

// Bus возвращает шину событий.
func (h *host) Bus() contracts.Bus { return h.bus }
