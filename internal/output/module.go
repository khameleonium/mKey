package output

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
)

// ModuleID — идентификатор модуля.
const ModuleID = "output"

// Config — настройки модуля из секции modules.output.
type Config struct {
	// UInputPath — путь к интерфейсу uinput (по умолчанию /dev/uinput).
	UInputPath string `json:"uinput_path"`
	// SettleMS — сколько миллисекунд ждать после создания устройства, прежде чем отправлять события.
	SettleMS int `json:"settle_ms"`
	// MaxEventsPerSecond — предел событий в секунду на устройство (SEC-4); 0 — без ограничения.
	MaxEventsPerSecond int `json:"max_events_per_second"`
}

// creator создаёт uinput-устройство по описанию (подменяется в тестах).
type creator func(ev.Setup) (eventWriter, error)

// Module — модуль виртуальных устройств, реализует contracts.VirtualDevices.
type Module struct {
	// create — фабрика uinput-устройств.
	create creator
	// clk — часы.
	clk clock.Clock
	// log — логгер модуля.
	log *slog.Logger
	// cfg — настройки.
	cfg Config

	// mu защищает устройства и последнюю ошибку.
	mu sync.Mutex
	// keyboard и mouse — созданные устройства (nil, пока не созданы).
	keyboard, mouse *device
	// pointer — указатель с абсолютными координатами (создаётся при первом MovePointer).
	pointer *device
	// lastErr — последняя ошибка создания устройств.
	lastErr error

	// Виртуальные устройства проектов (FR-VD-1): созданные по имени (в нижнем регистре), какие
	// должны существовать (из включённых проектов) и почему какие-то не созданы.
	vdevs    map[string]*vdevice
	want     map[string]wanted
	vdevErrs map[string]string
	// conflicts — устройства, не созданные из-за имени, занятого другим проектом.
	conflicts []contracts.VirtualDeviceInfo
	// projects — проекты (модуль store; nil — виртуальных устройств проектов нет); bus — шина.
	projects  contracts.Projects
	bus       contracts.Bus
	unsub     func()
	watchDone chan struct{}
	// clones — счётчик временных копий устройств (для уникального физического пути).
	clones atomic.Int64
}

// New создаёт модуль, работающий с настоящим uinput (путь берётся из настроек).
func New() *Module {
	m := newModule(nil, clock.Real{})
	m.create = func(s ev.Setup) (eventWriter, error) {
		return ev.CreateUInput(m.cfg.UInputPath, s)
	}
	return m
}

// newModule создаёт модуль с заданной фабрикой устройств и часами (для тестов).
func newModule(create creator, clk clock.Clock) *Module {
	return &Module{create: create, clk: clk, cfg: Config{UInputPath: ev.DefaultUInputPath, SettleMS: 500, MaxEventsPerSecond: 2000},
		vdevs: map[string]*vdevice{}, want: map[string]wanted{}, vdevErrs: map[string]string{}}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и публикует сервис виртуальных устройств.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Читаем настройки поверх значений по умолчанию.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// Проекты (их виртуальные устройства) и шаблоны устройств — в точку расширения (для окна).
	m.projects, _ = contracts.LookupService[contracts.Projects](host.Services())
	m.bus = host.Bus()
	for _, id := range templateOrder {
		if err := host.Extensions().Register(contracts.PointDeviceTemplate, templateMeta{id: id}); err != nil {
			return err
		}
	}

	// Публикуем сервисы для других модулей.
	if err := contracts.ProvideService[contracts.VirtualDeviceManager](host.Services(), m); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.VirtualDevices](host.Services(), m)
}

// Start создаёт виртуальные устройства заранее, чтобы к первому макросу композитор их уже подхватил.
// Отсутствие прав не считается ошибкой модуля: он остаётся в состоянии «недоступно».
func (m *Module) Start(context.Context) error {
	if err := m.ensure(); err != nil {
		m.log.Warn("virtual devices unavailable", "err", err)
	}

	// Виртуальные устройства включённых проектов — сейчас и при каждом изменении проектов.
	m.reconcile()
	if m.bus != nil {
		var events <-chan contracts.Event
		events, m.unsub = m.bus.Subscribe(contracts.TopicProjectsChanged)
		m.watchDone = make(chan struct{})
		go m.watchProjects(events)
	}
	return nil
}

// Stop отпускает всё зажатое и уничтожает виртуальные устройства.
func (m *Module) Stop(context.Context) error {
	// Перестаём следить за проектами.
	if m.unsub != nil {
		m.unsub()
		<-m.watchDone
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Закрываем устройства (и виртуальные устройства проектов), собирая ошибки.
	var errs []error
	for key, d := range m.vdevs {
		errs = append(errs, d.close())
		delete(m.vdevs, key)
	}
	for _, d := range []*device{m.keyboard, m.mouse, m.pointer} {
		if d != nil {
			errs = append(errs, d.close())
		}
	}
	m.keyboard, m.mouse, m.pointer = nil, nil, nil
	return errors.Join(errs...)
}

// Keyboard возвращает виртуальную клавиатуру, создавая её при необходимости.
func (m *Module) Keyboard() (contracts.VirtualDevice, error) {
	if err := m.ensure(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyboard, nil
}

// Mouse возвращает виртуальную мышь, создавая её при необходимости.
func (m *Module) Mouse() (contracts.VirtualDevice, error) {
	if err := m.ensure(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mouse, nil
}

// Status возвращает доступность виртуального ввода.
func (m *Module) Status() contracts.OutputStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keyboard != nil && m.mouse != nil {
		return contracts.OutputStatus{Available: true}
	}
	st := contracts.OutputStatus{}
	if m.lastErr != nil {
		st.Error = m.lastErr.Error()
	}
	return st
}

// ReleaseAll отпускает все зажатые клавиши на всех устройствах (SEC-2).
func (m *Module) ReleaseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, d := range []*device{m.keyboard, m.mouse, m.pointer} {
		if d != nil {
			errs = append(errs, d.ReleaseAll())
		}
	}
	for _, d := range m.vdevs {
		errs = append(errs, d.ReleaseAll())
	}
	return errors.Join(errs...)
}

// CenterPointer ставит указатель в центр рабочего стола через «mKey Pointer» (создаётся при
// первом использовании: обычным макросам он не нужен).
func (m *Module) CenterPointer(ctx context.Context) error {
	// Устройство создаётся один раз.
	m.mu.Lock()
	if m.pointer == nil {
		setup := pointerSetup()
		w, err := m.create(setup)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("%w: %s: %w", contracts.ErrOutputUnavailable, setup.Name, err)
		}
		m.pointer = newDevice(setup.Name, w, m.clk, time.Duration(m.cfg.SettleMS)*time.Millisecond, m.cfg.MaxEventsPerSecond)
		m.log.Info("virtual device created", "name", setup.Name)
	}
	p := m.pointer
	m.mu.Unlock()

	// Ядро отбрасывает абсолютные значения, равные прошлым значениям того же устройства
	// (drivers/input/input.c, input_handle_abs_event), — повторная постановка в центр
	// не дошла бы до композитора, если курсор с тех пор сдвинули обычной мышью. Поэтому сначала
	// соседнее значение (та же точка экрана: одна единица оси — доли пикселя), затем середина.
	if err := p.Emit(ctx,
		ev.Event{Type: ev.EvAbs, Code: ev.AbsX, Value: pointerCenter - 1},
		ev.Event{Type: ev.EvAbs, Code: ev.AbsY, Value: pointerCenter - 1},
	); err != nil {
		return err
	}
	return p.Emit(ctx,
		ev.Event{Type: ev.EvAbs, Code: ev.AbsX, Value: pointerCenter},
		ev.Event{Type: ev.EvAbs, Code: ev.AbsY, Value: pointerCenter},
	)
}

// ensure создаёт недостающие устройства. Возвращает ошибку, обёрнутую в ErrOutputUnavailable.
func (m *Module) ensure() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	settle := time.Duration(m.cfg.SettleMS) * time.Millisecond

	// Создаём клавиатуру и мышь, если их ещё нет.
	for _, slot := range []struct {
		dev   **device
		setup ev.Setup
	}{
		{&m.keyboard, keyboardSetup()},
		{&m.mouse, mouseSetup()},
	} {
		if *slot.dev != nil {
			continue
		}
		w, err := m.create(slot.setup)
		if err != nil {
			m.lastErr = err
			return fmt.Errorf("%w: %s: %w", contracts.ErrOutputUnavailable, slot.setup.Name, err)
		}
		*slot.dev = newDevice(slot.setup.Name, w, m.clk, settle, m.cfg.MaxEventsPerSecond)
		m.log.Info("virtual device created", "name", slot.setup.Name)
	}
	m.lastErr = nil
	return nil
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module         = (*Module)(nil)
	_ contracts.VirtualDevices = (*Module)(nil)
)
