package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/clock"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/layout"
	"github.com/khameleonium/mKey/internal/lib/paths"
)

// ModuleID — идентификатор модуля.
const ModuleID = "engine"

// wheelHiRes — единиц высокого разрешения в одном щелчке колеса (как у настоящих мышей).
const wheelHiRes = 120

// Config — настройки модуля из секции modules.engine.
type Config struct {
	// KeyHoldMS — сколько держать клавишу при обычном нажатии {A}.
	KeyHoldMS int `json:"key_hold_ms"`
	// KeyDelayMS — пауза после каждого нажатия и символа текста.
	KeyDelayMS int `json:"key_delay_ms"`
	// LayoutSwitchMS — пауза после переключения раскладки, чтобы система успела его применить.
	LayoutSwitchMS int `json:"layout_switch_ms"`
	// VarsFile — файл сохраняемых переменных (по умолчанию ~/.local/state/mkey/vars.json).
	VarsFile string `json:"vars_file"`
}

// Module — модуль выполнения макросов, реализует contracts.SequenceRunner.
type Module struct {
	// clk — часы для пауз.
	clk clock.Clock
	// rnd — генератор для случайных пауз [a..b]; rndMu защищает его: паузы берут одновременно
	// несколько макросов, а rand.Rand нельзя использовать из нескольких горутин.
	rndMu sync.Mutex
	rnd   *rand.Rand
	// log — логгер модуля; cfg — настройки.
	log *slog.Logger
	cfg Config
	// devices — виртуальные устройства; layouts — раскладки (nil, если модуль десктопа отключён).
	devices contracts.VirtualDevices
	layouts contracts.LayoutProvider

	// mu защищает runs.
	mu sync.Mutex
	// tmu защищает интервалы нажатий в cfg (KeyHoldMS, KeyDelayMS, LayoutSwitchMS): их меняет окно.
	tmu sync.RWMutex
	// runs — выполняющиеся сейчас макросы и события.
	runs map[*run]struct{}

	// Сервисы для событий (любой, кроме ext и bus, может отсутствовать).
	ext      contracts.ExtensionRegistry
	bus      contracts.Bus
	projects contracts.Projects
	keyState contracts.KeyState
	// inspect — авто-ID устройств: отправка кнопок {UnKey001} (nil — модуль отключён).
	inspect contracts.Inspector
	// vdevs — виртуальные устройства проектов ({pad2.South}); nil — модуль вывода их не даёт.
	vdevs contracts.VirtualDeviceManager
	// devOut — нажатия «от имени» физических устройств ({Sega.Start}); nil — модуль ввода выключен.
	devOut contracts.DeviceOutput
	// screen — размер экрана для касаний в пикселях (nil — модуль desktop выключен).
	screen   contracts.ScreenInfo
	notifier contracts.Notifier
	// tr — переводчик уведомлений; blindOnce — предупреждение «раскладка не видна» — один раз
	// за работу демона.
	tr        contracts.Translator
	blindOnce sync.Once
	// root живёт до Stop: от него наследуются контексты проектов.
	root       context.Context
	rootCancel context.CancelFunc
	// unsub — отписки от шины; wg ждёт горутину обработки шины.
	unsub []func()
	wg    sync.WaitGroup
	// evMu защищает runtimes; runtimes — взведённые проекты по ID.
	evMu     sync.Mutex
	runtimes map[string]*projectRuntime
	// persist — файл сохраняемых переменных.
	persist *persistFile
	// suspended — mKey приостановлен после экстренной остановки: триггеры не запускают события
	// до `mkey resume` (ручной запуск и `mkey send` работают).
	suspended atomic.Bool
}

// run — один выполняющийся макрос.
type run struct {
	// cancel прерывает макрос.
	cancel context.CancelFunc
	// mu защищает held и axes.
	mu sync.Mutex
	// held — что зажал этот макрос: устройство → коды в порядке нажатия.
	held map[string][]uint16
	// axes — оси виртуальных устройств, которые сдвинул макрос: в конце они возвращаются в покой.
	axes map[string][]uint16
	// touched — сенсорные экраны, которых касается макрос: в конце палец отрывается.
	touched map[string]bool
}

// New создаёт модуль с настоящими часами.
func New() *Module {
	return newModule(clock.Real{}, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x6d6b)))
}

// newModule создаёт модуль с заданными часами и генератором случайных чисел (для тестов).
func newModule(clk clock.Clock, rnd *rand.Rand) *Module {
	m := &Module{
		clk:      clk,
		rnd:      rnd,
		cfg:      Config{KeyHoldMS: 20, KeyDelayMS: 10, LayoutSwitchMS: 60},
		runs:     map[*run]struct{}{},
		runtimes: map[string]*projectRuntime{},
		persist:  &persistFile{path: filepath.Join(paths.State(os.Getenv), "vars.json")},
	}
	m.root, m.rootCancel = context.WithCancel(context.Background())
	return m
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init получает виртуальные устройства и раскладки и публикует сервис выполнения макросов.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Настройки поверх значений по умолчанию.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// Виртуальные устройства обязательны; раскладки — нет (без них считается раскладка "us").
	vd, err := contracts.LookupService[contracts.VirtualDevices](host.Services())
	if err != nil {
		return fmt.Errorf("%s: virtual devices: %w", ModuleID, err)
	}
	m.devices = vd
	m.layouts, _ = contracts.LookupService[contracts.LayoutProvider](host.Services())
	m.projects, _ = contracts.LookupService[contracts.Projects](host.Services())
	m.keyState, _ = contracts.LookupService[contracts.KeyState](host.Services())
	m.inspect, _ = contracts.LookupService[contracts.Inspector](host.Services())
	m.vdevs, _ = contracts.LookupService[contracts.VirtualDeviceManager](host.Services())
	m.notifier, _ = contracts.LookupService[contracts.Notifier](host.Services())
	m.tr = host.I18n()
	m.devOut, _ = contracts.LookupService[contracts.DeviceOutput](host.Services())
	m.screen, _ = contracts.LookupService[contracts.ScreenInfo](host.Services())
	if m.cfg.VarsFile != "" {
		m.persist.path = m.cfg.VarsFile
	}

	// Встроенные виды триггеров, условий и действий.
	m.ext = host.Extensions()
	m.bus = host.Bus()
	if err := m.registerBuiltins(m.ext); err != nil {
		return err
	}

	// Подписки: изменения проектов и экстренная остановка.
	projCh, u1 := m.bus.Subscribe(contracts.TopicProjectsChanged)
	emerCh, u2 := m.bus.Subscribe(contracts.TopicEmergency)
	resCh, u3 := m.bus.Subscribe(contracts.TopicResumed)
	extCh, u4 := m.bus.Subscribe(contracts.TopicExtensionsChanged)
	m.unsub = []func(){u1, u2, u3, u4}
	m.wg.Add(1)
	go m.listen(projCh, emerCh, resCh, extCh)

	// Сервисы.
	if err := contracts.ProvideService[contracts.SequenceRunner](host.Services(), m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.TimingSettings](host.Services(), m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.DryRunner](host.Services(), m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.ActionConverter](host.Services(), m); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.Events](host.Services(), m)
}

// Start взводит все включённые проекты (все модули уже зарегистрировали свои виды действий).
func (m *Module) Start(context.Context) error {
	m.reloadAll()
	return nil
}

// Stop снимает все проекты, прерывает все макросы (их клавиши отпускаются) и сохраняет переменные.
func (m *Module) Stop(context.Context) error {
	for _, u := range m.unsub {
		u()
	}
	m.wg.Wait()
	m.evMu.Lock()
	runtimes := m.runtimes
	m.runtimes = map[string]*projectRuntime{}
	m.evMu.Unlock()
	for _, rt := range runtimes {
		m.teardown(rt)
	}
	m.StopAll()
	m.rootCancel()
	return nil
}

// listen обрабатывает события шины: перезагрузка изменённых проектов, экстренная остановка,
// возобновление работы и смена набора видов (плагины).
func (m *Module) listen(projects, emergency, resumed, extensions <-chan contracts.Event) {
	defer m.wg.Done()
	for {
		select {
		case e, ok := <-projects:
			if !ok {
				return
			}
			ids, _ := e.Payload.([]string)
			for _, id := range ids {
				m.reloadProject(id)
			}
		case _, ok := <-emergency:
			if !ok {
				return
			}
			// Экстренная остановка (SEC-1): приостановиться, всё прервать и отпустить.
			m.log.Warn("emergency stop: stopping all macros, triggers suspended until resume")
			m.suspended.Store(true)
			m.StopAll()
			if err := m.devices.ReleaseAll(); err != nil {
				m.log.Error("release all after emergency", "err", err)
			}
		case _, ok := <-resumed:
			if !ok {
				return
			}
			m.log.Info("triggers resumed")
			m.suspended.Store(false)
		case _, ok := <-extensions:
			if !ok {
				return
			}
			// Появились или пропали виды плагинов: проекты, которые не взводились из-за
			// неизвестного вида, пробуем снова (взведённые и неизменные не трогаются).
			m.reloadAll()
		}
	}
}

// Running возвращает число выполняющихся макросов.
func (m *Module) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runs)
}

// StopAll прерывает все выполняющиеся макросы и события и выключает переключатели.
func (m *Module) StopAll() {
	m.resetToggles()
	m.mu.Lock()
	defer m.mu.Unlock()
	for r := range m.runs {
		r.cancel()
	}
}

// Run разбирает, компилирует и выполняет макрос как отдельный раннер: всё, что он зажал,
// отпускается по его завершению (FR-DSL-5).
func (m *Module) Run(ctx context.Context, src string) error {
	// Проверяем макрос до регистрации раннера: ошибка в тексте ничего не нажимает.
	steps, err := m.compileSource(src)
	if err != nil {
		return err
	}
	ctx, r, finish := m.newRun(ctx)
	defer finish()
	return m.exec(ctx, r, steps)
}

// compileSource разбирает и компилирует макрос; ошибки — *dsl.Error. Кнопки устройств
// с авто-ID ({UnKey001}) находит инспектор, если он есть.
func (m *Module) compileSource(src string) ([]dsl.Step, error) {
	nodes, err := dsl.Parse(src)
	if err != nil {
		return nil, err
	}
	return dsl.Compile(nodes, m.resolver())
}

// resolver — распознаватель клавиш макросов: виртуальные устройства проектов ({pad2.South}),
// затем кнопки физических устройств с авто-ID ({UnKey001}), затем обычные клавиши.
func (m *Module) resolver() dsl.DeviceResolver {
	// Кнопки, которых нет у клавиатуры и мыши mKey, — «от имени» устройства (нужны модуль ввода
	// и инспектор, который знает, какое устройство как называется).
	r := dsl.DeviceResolver{Physical: m.devOut != nil && m.inspect != nil}
	if m.inspect != nil {
		r.Lookup = func(device, button string) (uint16, string, error) {
			k, err := m.inspect.ResolveKey(device, button)
			return k.Code, k.Name, err
		}
	}
	if m.vdevs != nil {
		r.Virtual = func(device, control string) (uint16, bool, bool, error) {
			code, axis, err := m.vdevs.Resolve(device, control)
			if errors.Is(err, contracts.ErrUnknownVirtual) {
				return 0, false, false, nil
			}
			if err != nil {
				return 0, false, true, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", device, "button", control)
			}
			return code, axis, true, nil
		}
	}
	return r
}

// newRun регистрирует раннер, чтобы StopAll мог его прервать. finish отпускает всё,
// что раннер зажал, и снимает его с учёта; его нужно вызвать всегда.
func (m *Module) newRun(parent context.Context) (context.Context, *run, func()) {
	ctx, cancel := context.WithCancel(parent)
	r := &run{cancel: cancel, held: map[string][]uint16{}, axes: map[string][]uint16{}, touched: map[string]bool{}}
	m.mu.Lock()
	m.runs[r] = struct{}{}
	m.mu.Unlock()
	return ctx, r, func() {
		if err := m.releaseAll(r); err != nil {
			m.log.Error("release after macro", "err", err)
		}
		cancel()
		m.mu.Lock()
		delete(m.runs, r)
		m.mu.Unlock()
	}
}

// execSource выполняет макрос в рамках существующего раннера r: зажатые клавиши остаются
// зажатыми для следующих действий события и отпускаются по завершению события.
func (m *Module) execSource(ctx context.Context, r *run, src string) error {
	steps, err := m.compileSource(src)
	if err != nil {
		return err
	}
	return m.exec(ctx, r, steps)
}

// exec выполняет шаги по порядку.
func (m *Module) exec(ctx context.Context, r *run, steps []dsl.Step) error {
	for _, s := range steps {
		// Отмена проверяется перед каждым шагом.
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.step(ctx, r, s); err != nil {
			return err
		}
	}
	return nil
}

// step выполняет один шаг плана.
func (m *Module) step(ctx context.Context, r *run, s dsl.Step) error {
	switch s.Kind {
	case dsl.StepPress:
		for _, t := range s.Targets {
			if err := m.press(ctx, r, t); err != nil {
				return err
			}
		}
		return nil
	case dsl.StepRelease:
		for _, t := range slices.Backward(s.Targets) {
			if err := m.release(ctx, r, t.Device, t.Code); err != nil {
				return err
			}
		}
		return nil
	case dsl.StepReleaseAll:
		return m.releaseAll(r)
	case dsl.StepAxis:
		return m.axis(ctx, r, s)
	case dsl.StepTouch, dsl.StepSwipe:
		return m.touch(ctx, r, s)
	case dsl.StepTap:
		return m.tap(ctx, r, s)
	case dsl.StepWait:
		return m.clk.Sleep(ctx, m.pause(s.MinMS, s.MaxMS))
	case dsl.StepText:
		return m.typeText(ctx, r, s)
	case dsl.StepMove:
		return m.move(ctx, s.DX, s.DY)
	case dsl.StepWheel:
		return m.wheel(ctx, s)
	case dsl.StepLoop:
		for range s.Count {
			if err := m.exec(ctx, r, s.Body); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("engine: unknown step %q", s.Kind)
}

// tap нажимает и отпускает сочетание s.Count раз с паузами между нажатиями.
func (m *Module) tap(ctx context.Context, r *run, s dsl.Step) error {
	// Удержание: заданное в макросе или по умолчанию.
	hold := time.Duration(s.HoldMS) * time.Millisecond
	if s.HoldMS == 0 {
		hold = m.ms(m.Timing().KeyHoldMS)
	}

	for range s.Count {
		// Зажимаем клавиши по порядку, держим, отпускаем в обратном порядке.
		for _, t := range s.Targets {
			if err := m.press(ctx, r, t); err != nil {
				return err
			}
		}
		if err := m.clk.Sleep(ctx, hold); err != nil {
			return err
		}
		for _, t := range slices.Backward(s.Targets) {
			if err := m.release(ctx, r, t.Device, t.Code); err != nil {
				return err
			}
		}

		// Пауза после нажатия (и между повторами).
		if err := m.clk.Sleep(ctx, m.ms(m.Timing().KeyDelayMS)); err != nil {
			return err
		}
	}
	return nil
}

// press зажимает клавишу и запоминает её в раннере.
func (m *Module) press(ctx context.Context, r *run, t dsl.Target) error {
	dev, err := m.device(t.Device)
	if err != nil {
		return err
	}
	if err := dev.Press(ctx, t.Code); err != nil {
		return err
	}
	r.mu.Lock()
	r.held[t.Device] = append(r.held[t.Device], t.Code)
	r.mu.Unlock()
	return nil
}

// release отпускает клавишу и убирает её из раннера.
func (m *Module) release(ctx context.Context, r *run, device string, code uint16) error {
	dev, err := m.device(device)
	if err != nil {
		return err
	}
	if err := dev.Release(ctx, code); err != nil {
		return err
	}
	r.mu.Lock()
	if i := slices.Index(r.held[device], code); i >= 0 {
		r.held[device] = slices.Delete(r.held[device], i, i+1)
	}
	r.mu.Unlock()
	return nil
}

// releaseAll отпускает всё, что зажал раннер, в обратном порядке, и возвращает в покой сдвинутые
// им оси виртуальных устройств. Работает и после отмены.
func (m *Module) releaseAll(r *run) error {
	// Забираем списки зажатого, сдвинутых осей и касаний под блокировкой.
	r.mu.Lock()
	held, axes, touched := r.held, r.axes, r.touched
	r.held, r.axes, r.touched = map[string][]uint16{}, map[string][]uint16{}, map[string]bool{}
	r.mu.Unlock()

	// Пальцы, оставшиеся на сенсорных экранах, — оторвать.
	var errs []error
	for device := range touched {
		if dev, err := m.device(device); err == nil {
			if ts, ok := dev.(contracts.TouchSetter); ok {
				if err := ts.TouchUp(context.Background()); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}

	// Оси, сдвинутые макросом, — в покой (центр стиков, отпущенные курки).
	for device, codes := range axes {
		dev, err := m.device(device)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if setter, ok := dev.(contracts.AxisSetter); ok {
			for _, c := range codes {
				if err := setter.SetAxis(context.Background(), c, 0); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}

	// Отпускаем без контекста отмены: это очистка, она должна выполниться всегда.
	for device, codes := range held {
		dev, err := m.device(device)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, c := range slices.Backward(codes) {
			if err := dev.Release(context.Background(), c); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// device возвращает виртуальное устройство по имени из плана.
func (m *Module) device(name string) (contracts.VirtualDevice, error) {
	switch name {
	case dsl.DeviceKeyboard:
		return m.devices.Keyboard()
	case dsl.DeviceMouse:
		return m.devices.Mouse()
	}
	if id, ok := dsl.IsPhysical(name); ok {
		return m.physicalOutput(id)
	}
	if m.vdevs != nil {
		return m.vdevs.Device(name)
	}
	return nil, fmt.Errorf("engine: unknown device %q", name)
}

// physicalOutput возвращает устройство вывода «от имени» физического устройства id (авто-ID или
// имя, данное человеком, без учёта регистра): его копию (contracts.DeviceOutput). Устройство не
// подключено — ошибка с contracts.ErrDeviceGone.
func (m *Module) physicalOutput(id string) (contracts.VirtualDevice, error) {
	if m.devOut == nil || m.inspect == nil {
		return nil, fmt.Errorf("engine: device %q: pressing its buttons is not available", id)
	}
	for _, d := range m.inspect.Devices() {
		if strings.EqualFold(d.AutoID, id) || (d.DeviceName != "" && strings.EqualFold(d.DeviceName, id)) {
			return m.devOut.DeviceOutput(d.Info.Path)
		}
	}
	return nil, fmt.Errorf("%w: %s", contracts.ErrDeviceGone, id)
}

// axis ставит оси виртуального устройства в положение s.Value и запоминает их, чтобы в конце
// макроса вернуть в покой.
func (m *Module) axis(ctx context.Context, r *run, s dsl.Step) error {
	for _, t := range s.Targets {
		dev, err := m.device(t.Device)
		if err != nil {
			return err
		}
		setter, ok := dev.(contracts.AxisSetter)
		if !ok {
			return fmt.Errorf("engine: %s has no axes", dev.Name())
		}
		if err := setter.SetAxis(ctx, t.Code, s.Value); err != nil {
			return err
		}
		r.mu.Lock()
		if !slices.Contains(r.axes[t.Device], t.Code) {
			r.axes[t.Device] = append(r.axes[t.Device], t.Code)
		}
		r.mu.Unlock()
	}
	return nil
}

// move сдвигает курсор мыши на (dx, dy).
func (m *Module) move(ctx context.Context, dx, dy int32) error {
	mouse, err := m.devices.Mouse()
	if err != nil {
		return err
	}
	return mouse.Emit(ctx,
		ev.Event{Type: ev.EvRel, Code: ev.RelX, Value: dx},
		ev.Event{Type: ev.EvRel, Code: ev.RelY, Value: dy},
	)
}

// wheel прокручивает колесо s.Count щелчков. Как настоящая мышь, отправляет и обычные щелчки,
// и события высокого разрешения (их используют современные приложения).
func (m *Module) wheel(ctx context.Context, s dsl.Step) error {
	mouse, err := m.devices.Mouse()
	if err != nil {
		return err
	}

	// Коды осей: вертикальная (DY) или горизонтальная (DX) прокрутка.
	code, hiRes, value := ev.RelWheel, ev.RelWheelHiRes, s.DY
	if s.DX != 0 {
		code, hiRes, value = ev.RelHwheel, ev.RelHwheelHiRes, s.DX
	}

	// Щелчки с паузой между ними.
	for range s.Count {
		if err := mouse.Emit(ctx,
			ev.Event{Type: ev.EvRel, Code: uint16(code), Value: value},
			ev.Event{Type: ev.EvRel, Code: uint16(hiRes), Value: value * wheelHiRes},
		); err != nil {
			return err
		}
		if err := m.clk.Sleep(ctx, m.ms(m.Timing().KeyDelayMS)); err != nil {
			return err
		}
	}
	return nil
}

// pause возвращает длительность паузы: фиксированную или случайную в [min, max].
func (m *Module) pause(minMS, maxMS int64) time.Duration {
	ms := minMS
	if maxMS > minMS {
		m.rndMu.Lock()
		ms += m.rnd.Int64N(maxMS - minMS + 1)
		m.rndMu.Unlock()
	}
	return time.Duration(ms) * time.Millisecond
}

// ms переводит миллисекунды из настроек в time.Duration.
func (m *Module) ms(v int) time.Duration { return time.Duration(v) * time.Millisecond }

// typeText набирает текст по текущей раскладке пользователя (docs/dsl.md, «Набор текста»).
func (m *Module) typeText(ctx context.Context, r *run, s dsl.Step) error {
	kb, err := m.devices.Keyboard()
	if err != nil {
		return err
	}

	// Узнаём раскладки: без модуля десктопа считаем, что включена только "us".
	info := contracts.LayoutInfo{Current: "us", Available: []string{"us"}}
	if m.layouts != nil {
		if got, err := m.layouts.Layouts(ctx); err == nil && got.Current != "" {
			info = got
		} else if err != nil {
			m.log.Warn("keyboard layout unknown, assuming us", "err", err)
		}
	}
	// Раскладка не видна: печатаем нажатиями клавиш как есть, без переключений.
	if info.Blind {
		return m.typeBlind(ctx, r, kb, s, info)
	}
	current, ok := layout.Get(info.Current)
	if !ok {
		current, _ = layout.Get("us")
	}

	// Если по ходу пришлось переключить раскладку — в конце возвращаем исходную.
	original := info.Current
	defer func() {
		if current.Name != original && info.CanSwitch {
			if err := m.layouts.Switch(context.WithoutCancel(ctx), original); err != nil {
				m.log.Warn("restore keyboard layout", "err", err)
			}
		}
	}()

	// Набираем символ за символом; "\r\n" — один Enter.
	runes := []rune(s.Text)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			continue
		}

		// Управляющие символы — отдельные клавиши.
		if code, special := controlKey(c); special {
			if err := m.tapStroke(ctx, r, kb, layout.Stroke{Code: code}); err != nil {
				return err
			}
			continue
		}

		// Ищем символ в текущей раскладке, иначе — в другой доступной с переключением.
		stroke, found := current.Find(c)
		if !found {
			next, st, ok := m.findElsewhere(c, info, current.Name)
			if !ok {
				return &dsl.Error{Pos: s.Pos, Code: dsl.ErrUntypeable, Args: map[string]string{
					"char": string(c), "layouts": strings.Join(info.Available, ", "),
				}}
			}
			if err := m.layouts.Switch(ctx, next.Name); err != nil {
				return fmt.Errorf("switch keyboard layout to %s: %w", next.Name, err)
			}
			if err := m.clk.Sleep(ctx, m.ms(m.Timing().LayoutSwitchMS)); err != nil {
				return err
			}
			current, stroke = next, st
		}
		if err := m.tapStroke(ctx, r, kb, stroke); err != nil {
			return err
		}
	}
	return nil
}

// findElsewhere ищет символ в других раскладках пользователя, если раскладку можно переключить.
func (m *Module) findElsewhere(c rune, info contracts.LayoutInfo, currentName string) (*layout.Layout, layout.Stroke, bool) {
	if !info.CanSwitch || m.layouts == nil {
		return nil, layout.Stroke{}, false
	}
	for _, name := range info.Available {
		l, ok := layout.Get(name)
		if !ok || l.Name == currentName {
			continue
		}
		if st, ok := l.Find(c); ok {
			return l, st, true
		}
	}
	return nil, layout.Stroke{}, false
}

// typeBlind набирает текст, когда mKey не видит раскладку (LayoutInfo.Blind): каждый символ —
// нажатие клавиши, на которой он находится в одной из известных раскладок (сначала — указанных
// в настройках), без переключения раскладки. Что получится на экране, зависит от раскладки,
// включённой у человека, — об этом mKey один раз предупреждает (журнал и уведомление).
func (m *Module) typeBlind(ctx context.Context, r *run, kb contracts.VirtualDevice, s dsl.Step, info contracts.LayoutInfo) error {
	m.blindOnce.Do(func() {
		m.log.Warn("keyboard layout is not visible: typing text as plain key presses", "layouts", info.Available)
		if m.notifier != nil && m.tr != nil {
			_ = m.notifier.Notify(context.WithoutCancel(ctx), "mKey", m.tr.T("engine.layout_blind"))
		}
	})

	// Таблицы по порядку: из настроек, затем остальные встроенные.
	var tables []*layout.Layout
	for _, name := range append(slices.Clone(info.Available), layout.Names()...) {
		if l, ok := layout.Get(name); ok && !slices.Contains(tables, l) {
			tables = append(tables, l)
		}
	}

	// Символ за символом; "\r\n" — один Enter.
	runes := []rune(s.Text)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			continue
		}
		if code, special := controlKey(c); special {
			if err := m.tapStroke(ctx, r, kb, layout.Stroke{Code: code}); err != nil {
				return err
			}
			continue
		}
		found := false
		for _, l := range tables {
			if st, ok := l.Find(c); ok {
				if err := m.tapStroke(ctx, r, kb, st); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return &dsl.Error{Pos: s.Pos, Code: dsl.ErrUntypeable, Args: map[string]string{
				"char": string(c), "layouts": strings.Join(layout.Names(), ", "),
			}}
		}
	}
	return nil
}

// tapStroke нажимает клавишу символа. Shift зажимается, только если он нужен символу и ещё
// не зажат: зажатый макросом Shift действует на символ, как палец на клавише.
func (m *Module) tapStroke(ctx context.Context, r *run, kb contracts.VirtualDevice, st layout.Stroke) error {
	shift := dsl.Target{Device: dsl.DeviceKeyboard, Code: ev.KeyLeftshift}
	needShift := st.Shift && !slices.ContainsFunc(kb.Held(), func(c uint16) bool {
		return c == ev.KeyLeftshift || c == ev.KeyRightshift
	})

	// Shift (если нужен) и сама клавиша.
	if needShift {
		if err := m.press(ctx, r, shift); err != nil {
			return err
		}
	}
	key := dsl.Step{Kind: dsl.StepTap, Targets: []dsl.Target{{Device: dsl.DeviceKeyboard, Code: st.Code}}, Count: 1}
	if err := m.tap(ctx, r, key); err != nil {
		return err
	}
	if needShift {
		return m.release(ctx, r, dsl.DeviceKeyboard, ev.KeyLeftshift)
	}
	return nil
}

// controlKey сопоставляет управляющие символы текста клавишам: перевод строки — Enter, табуляция — Tab, \b — Backspace.
func controlKey(c rune) (uint16, bool) {
	switch c {
	case '\n', '\r':
		return ev.KeyEnter, true
	case '\t':
		return ev.KeyTab, true
	case '\b':
		return ev.KeyBackspace, true
	}
	return 0, false
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module         = (*Module)(nil)
	_ contracts.SequenceRunner = (*Module)(nil)
	_ contracts.Events         = (*Module)(nil)
)
