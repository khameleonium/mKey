package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/layout"
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
}

// Module — модуль выполнения макросов, реализует contracts.SequenceRunner.
type Module struct {
	// clk — часы для пауз.
	clk clock.Clock
	// rnd — генератор для случайных пауз [a..b].
	rnd *rand.Rand
	// log — логгер модуля; cfg — настройки.
	log *slog.Logger
	cfg Config
	// devices — виртуальные устройства; layouts — раскладки (nil, если модуль десктопа отключён).
	devices contracts.VirtualDevices
	layouts contracts.LayoutProvider

	// mu защищает runs.
	mu sync.Mutex
	// runs — выполняющиеся сейчас макросы.
	runs map[*run]struct{}
}

// run — один выполняющийся макрос.
type run struct {
	// cancel прерывает макрос.
	cancel context.CancelFunc
	// mu защищает held.
	mu sync.Mutex
	// held — что зажал этот макрос: устройство → коды в порядке нажатия.
	held map[string][]uint16
}

// New создаёт модуль с настоящими часами.
func New() *Module {
	return newModule(clock.Real{}, rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x6d6b)))
}

// newModule создаёт модуль с заданными часами и генератором случайных чисел (для тестов).
func newModule(clk clock.Clock, rnd *rand.Rand) *Module {
	return &Module{
		clk:  clk,
		rnd:  rnd,
		cfg:  Config{KeyHoldMS: 20, KeyDelayMS: 10, LayoutSwitchMS: 60},
		runs: map[*run]struct{}{},
	}
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
	if lp, err := contracts.LookupService[contracts.LayoutProvider](host.Services()); err == nil {
		m.layouts = lp
	}
	return contracts.ProvideService[contracts.SequenceRunner](host.Services(), m)
}

// Start ничего не делает: макросы запускаются по запросу.
func (m *Module) Start(context.Context) error { return nil }

// Stop прерывает все макросы (их клавиши отпускаются).
func (m *Module) Stop(context.Context) error {
	m.StopAll()
	return nil
}

// Running возвращает число выполняющихся макросов.
func (m *Module) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runs)
}

// StopAll прерывает все выполняющиеся макросы.
func (m *Module) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for r := range m.runs {
		r.cancel()
	}
}

// Run разбирает, компилирует и выполняет макрос.
func (m *Module) Run(ctx context.Context, src string) error {
	// Разбор и компиляция: ошибки в тексте макроса возвращаются как *dsl.Error.
	nodes, err := dsl.Parse(src)
	if err != nil {
		return err
	}
	steps, err := dsl.Compile(nodes, dsl.DefaultResolver{})
	if err != nil {
		return err
	}

	// Регистрируем раннер, чтобы StopAll мог его прервать.
	ctx, cancel := context.WithCancel(ctx)
	r := &run{cancel: cancel, held: map[string][]uint16{}}
	m.mu.Lock()
	m.runs[r] = struct{}{}
	m.mu.Unlock()

	// Что бы ни случилось, отпускаем всё зажатое и снимаем раннер с учёта (FR-DSL-5).
	defer func() {
		if err := m.releaseAll(r); err != nil {
			m.log.Error("release after macro", "err", err)
		}
		cancel()
		m.mu.Lock()
		delete(m.runs, r)
		m.mu.Unlock()
	}()

	// Выполняем план.
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
		hold = m.ms(m.cfg.KeyHoldMS)
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
		if err := m.clk.Sleep(ctx, m.ms(m.cfg.KeyDelayMS)); err != nil {
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

// releaseAll отпускает всё, что зажал раннер, в обратном порядке. Работает и после отмены.
func (m *Module) releaseAll(r *run) error {
	// Забираем список зажатого под блокировкой.
	r.mu.Lock()
	held := r.held
	r.held = map[string][]uint16{}
	r.mu.Unlock()

	// Отпускаем без контекста отмены: это очистка, она должна выполниться всегда.
	var errs []error
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
	return nil, fmt.Errorf("engine: unknown device %q", name)
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
		if err := m.clk.Sleep(ctx, m.ms(m.cfg.KeyDelayMS)); err != nil {
			return err
		}
	}
	return nil
}

// pause возвращает длительность паузы: фиксированную или случайную в [min, max].
func (m *Module) pause(minMS, maxMS int64) time.Duration {
	ms := minMS
	if maxMS > minMS {
		ms += m.rnd.Int64N(maxMS - minMS + 1)
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
			if err := m.clk.Sleep(ctx, m.ms(m.cfg.LayoutSwitchMS)); err != nil {
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
)
