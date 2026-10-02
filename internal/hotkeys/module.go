package hotkeys

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
)

// ModuleID — идентификатор модуля.
const ModuleID = "hotkeys"

// modifierCodes — все коды модификаторов (для правила «лишних модификаторов нет»).
var modifierCodes = []uint16{
	ev.KeyLeftctrl, ev.KeyRightctrl, ev.KeyLeftshift, ev.KeyRightshift,
	ev.KeyLeftalt, ev.KeyRightalt, ev.KeyLeftmeta, ev.KeyRightmeta,
}

// Module — модуль горячих клавиш.
type Module struct {
	// log — логгер; input — источник событий; projects, layouts и inspect — необязательные сервисы
	// (inspect — авто-ID устройств: кнопки вида {UnKey001}, FR-DEV-2).
	log      *slog.Logger
	input    contracts.InputSource
	projects contracts.Projects
	layouts  contracts.LayoutProvider
	inspect  contracts.Inspector
	// devs и vdm — устройства mKey и виртуальные устройства проектов (цели привязок, FR-VD-3);
	// bindCh — очередь действий привязок для фонового обработчика.
	devs   contracts.VirtualDevices
	vdm    contracts.VirtualDeviceManager
	bindCh chan bindAction
	// unsub — отписки от шины.
	unsub []func()
	// ctx живёт до Stop; stop отменяет его; wg ждёт фоновые горутины.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	// layout — текущая раскладка (string) для распознавания hotstrings.
	layout atomic.Value

	// mu защищает всё состояние ниже.
	mu sync.Mutex
	// down — зажатые клавиши по устройствам (после переназначения).
	down map[string]map[uint16]bool
	// suppressed — «съеденные» клавиши по устройствам: их повторы и отпускание тоже не пропускаются.
	suppressed map[string]map[uint16]bool
	// hotkeys, sequences, hotstrings — зарегистрированные триггеры.
	hotkeys    map[*hotkey]struct{}
	sequences  map[*sequence]struct{}
	hotstrings map[*hotstring]struct{}
	// remaps — переназначения из включённых проектов; bindings — привязки (FR-VD-3).
	remaps   []remapRule
	bindings []*binding
	// bindClosed — очередь привязок закрыта (модуль остановлен): действия больше не отправляются.
	bindClosed bool
	// absRanges — диапазоны осей устройств-источников привязок (заполняются при первом движении).
	absRanges map[axisKey]absRange
	// waiters — ожидающие нажатия клавиши (`mkey wait key`).
	waiters map[*waiter]struct{}
	// history — последние нажатия (для последовательностей).
	history []press
	// typed — последние набранные символы (для hotstrings).
	typed []rune
}

// press — одно нажатие в истории: устройство, код, время.
type press struct {
	device string
	code   uint16
	at     time.Time
}

// waiter — ожидание нажатия клавиши.
type waiter struct {
	key contracts.DeviceKey
	ch  chan struct{}
}

// New создаёт модуль.
func New() *Module {
	return &Module{
		down:       map[string]map[uint16]bool{},
		suppressed: map[string]map[uint16]bool{},
		hotkeys:    map[*hotkey]struct{}{},
		sequences:  map[*sequence]struct{}{},
		hotstrings: map[*hotstring]struct{}{},
		waiters:    map[*waiter]struct{}{},
		bindCh:     make(chan bindAction, bindQueue),
		absRanges:  map[axisKey]absRange{},
	}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init получает сервисы, регистрирует виды триггеров и публикует состояние клавиш.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Источник событий обязателен; проекты (для переназначений) и раскладки — нет.
	m.log = host.Logger()
	in, err := contracts.LookupService[contracts.InputSource](host.Services())
	if err != nil {
		return fmt.Errorf("%s: input: %w", ModuleID, err)
	}
	m.input = in
	m.projects, _ = contracts.LookupService[contracts.Projects](host.Services())
	m.layouts, _ = contracts.LookupService[contracts.LayoutProvider](host.Services())
	m.inspect, _ = contracts.LookupService[contracts.Inspector](host.Services())
	m.devs, _ = contracts.LookupService[contracts.VirtualDevices](host.Services())
	m.vdm, _ = contracts.LookupService[contracts.VirtualDeviceManager](host.Services())
	m.layout.Store("us")

	// Виды триггеров.
	for _, t := range []contracts.TriggerType{hotkeyType{m}, sequenceType{m}, hotstringType{m}} {
		if err := host.Extensions().Register(contracts.PointTrigger, t); err != nil {
			return err
		}
	}

	// Подписки: изменения проектов (переназначения), отключение устройств (сброс их клавиш) и
	// новые авто-ID (перехват для «съедаемых» кнопок {UnKey001} считается по устройству).
	projCh, unsub1 := host.Bus().Subscribe(contracts.TopicProjectsChanged)
	remCh, unsub2 := host.Bus().Subscribe(contracts.TopicInputDeviceRemoved)
	autoCh, unsub3 := host.Bus().Subscribe(contracts.TopicAutoIDsChanged)
	m.unsub = []func(){unsub1, unsub2, unsub3}
	m.ctx, m.stop = context.WithCancel(context.Background())
	m.wg.Add(2)
	go m.listen(m.ctx, projCh, remCh, autoCh)
	go m.bindWorker()

	return contracts.ProvideService[contracts.KeyState](host.Services(), m)
}

// Start ставит модуль обработчиком событий ввода и начинает следить за раскладкой.
func (m *Module) Start(context.Context) error {
	m.input.SetHandler(m)
	m.reloadRemaps()
	if m.layouts != nil {
		m.wg.Add(1)
		go m.watchLayout()
	}
	return nil
}

// Stop снимает обработчик и захват, останавливает фоновые горутины.
func (m *Module) Stop(context.Context) error {
	if m.input != nil {
		m.input.SetHandler(nil)
		m.input.SetGrabPolicy(nil)
	}

	// Очередь привязок закрывается: обработчик отпустит всё, что нажато привязками, и завершится.
	m.mu.Lock()
	if !m.bindClosed {
		m.bindings, m.bindClosed = nil, true
		close(m.bindCh)
	}
	m.mu.Unlock()
	for _, u := range m.unsub {
		u()
	}
	if m.stop != nil {
		m.stop()
	}
	m.wg.Wait()
	return nil
}

// listen обрабатывает события шины до остановки.
func (m *Module) listen(ctx context.Context, projects, removed, autoIDs <-chan contracts.Event) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-projects:
			if !ok {
				return
			}
			m.reloadRemaps()
		case e, ok := <-removed:
			if !ok {
				return
			}
			if d, ok := e.Payload.(contracts.InputDevice); ok {
				m.mu.Lock()
				delete(m.down, d.Info.Path)
				delete(m.suppressed, d.Info.Path)
				m.mu.Unlock()
			}
		case _, ok := <-autoIDs:
			if !ok {
				return
			}
			m.mu.Lock()
			m.updateGrab()
			m.mu.Unlock()
		}
	}
}

// watchLayout раз в секунду обновляет текущую раскладку для распознавания hotstrings
// (запрос к рабочему столу нельзя делать в потоке ввода). Пока hotstrings нет, рабочий стол
// не спрашивается: раскладка нужна только им, а лишний запрос раз в секунду — лишняя работа в простое.
func (m *Module) watchLayout() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		// Спрашиваем раскладку с таймаутом, чтобы зависший рабочий стол не держал модуль.
		m.mu.Lock()
		need := len(m.hotstrings) > 0
		m.mu.Unlock()
		if need {
			ctx, cancel := context.WithTimeout(m.ctx, 500*time.Millisecond)
			if info, err := m.layouts.Layouts(ctx); err == nil && info.Current != "" {
				m.layout.Store(info.Current)
			}
			cancel()
		}

		// Ждём следующего раза или остановки.
		select {
		case <-t.C:
		case <-m.ctx.Done():
			return
		}
	}
}

// HandleInput — синхронный обработчик событий всех устройств (contracts.InputHandler).
func (m *Module) HandleInput(device string, e *ev.Event, grabbed bool) bool {
	// Оси и мышь — только привязкам (FR-VD-3); остальное интересует только клавиши и кнопки.
	if e.Type == ev.EvAbs || e.Type == ev.EvRel {
		if m.input.GrabSuspended() {
			return false
		}
		m.mu.Lock()
		hide := len(m.bindings) > 0 && m.feedAxis(device, e)
		m.mu.Unlock()
		return hide && grabbed
	}
	if e.Type != ev.EvKey {
		return false
	}

	// После экстренной остановки mKey приостановлен: состояние клавиш ведём (для `mkey wait key`
	// и условий), но ни переназначений, ни срабатываний, ни «съеденных» нажатий нет (SEC-1).
	suspended := m.input.GrabSuspended()

	// Вся обработка под блокировкой; срабатывания вызываются после её снятия.
	// Привязки смотрят на исходный код кнопки (до переназначения).
	var fires []func()
	orig := e.Code
	m.mu.Lock()
	if !suspended {
		m.applyRemap(device, e)
	}
	drop := false
	switch e.Value {
	case ev.ValueDown:
		m.setDown(device, e.Code, true)
		if !suspended {
			drop = m.onDown(device, e.Code, &fires)
			drop = m.feedBindings(device, orig, true) || drop
			m.feedSequence(device, e.Code, &fires)
			m.feedHotstring(e.Code, &fires)
		}
		m.notifyWaiters(device, e.Code)
		if drop {
			m.suppress(device, e.Code, true)
		}
	case ev.ValueRepeat:
		drop = m.suppressed[device][e.Code]
	case ev.ValueUp:
		m.setDown(device, e.Code, false)
		drop = m.suppressed[device][e.Code]
		m.suppress(device, e.Code, false)
		// Отпускание привязкам — всегда (и после экстренной остановки): иначе кнопка цели
		// осталась бы нажатой.
		m.feedBindings(device, orig, false)
		if !suspended {
			m.onUp(device, e.Code, &fires)
		}
	}
	m.mu.Unlock()

	// Срабатывания — вне блокировки: колбэки могут обращаться к модулю (Held, Modifiers).
	for _, f := range fires {
		f()
	}
	return drop && grabbed
}

// setDown отмечает клавишу зажатой или отпущенной на устройстве.
func (m *Module) setDown(device string, code uint16, down bool) {
	if m.down[device] == nil {
		m.down[device] = map[uint16]bool{}
	}
	if down {
		m.down[device][code] = true
	} else {
		delete(m.down[device], code)
	}
}

// suppress отмечает клавишу «съеденной» или снимает отметку.
func (m *Module) suppress(device string, code uint16, on bool) {
	if m.suppressed[device] == nil {
		m.suppressed[device] = map[uint16]bool{}
	}
	if on {
		m.suppressed[device][code] = true
	} else {
		delete(m.suppressed[device], code)
	}
}

// anyDown сообщает, зажата ли клавиша k на любом устройстве (под блокировкой).
func (m *Module) anyDown(k contracts.DeviceKey) bool {
	for device, codes := range m.down {
		for c := range codes {
			if m.matches(k, device, c) {
				return true
			}
		}
	}
	return false
}

// matches сообщает, что нажатие code на устройстве device — это клавиша k: код совпадает, а если
// у k указано устройство ({UnKey2.001}), то и авто-ID устройства.
func (m *Module) matches(k contracts.DeviceKey, device string, code uint16) bool {
	if !k.Matches(code) {
		return false
	}
	return k.Device == "" || (m.inspect != nil && m.inspect.DeviceOf(device) == k.Device)
}

// ParseKey разбирает имя клавиши, как в макросах (contracts.KeyState): "A", "{Mouse0}",
// кнопки устройств с авто-ID — "UnKey001", "UnKey2.001", "UnKey.A".
func (m *Module) ParseKey(name string) (contracts.DeviceKey, error) {
	ks, err := m.parseChord(braced(name))
	if err != nil {
		return contracts.DeviceKey{}, err
	}
	if len(ks) != 1 {
		return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrBadHotkey)
	}
	return ks[0], nil
}

// IsDown сообщает, зажата ли клавиша сейчас (contracts.KeyState).
func (m *Module) IsDown(k contracts.DeviceKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.anyDown(k)
}

// WaitKey ждёт нажатия клавиши k или отмены ctx.
func (m *Module) WaitKey(ctx context.Context, k contracts.DeviceKey) error {
	w := &waiter{key: k, ch: make(chan struct{})}
	m.mu.Lock()
	m.waiters[w] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.waiters, w)
		m.mu.Unlock()
	}()
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notifyWaiters будит ожидающих нажатия клавиши code на устройстве device (под блокировкой).
func (m *Module) notifyWaiters(device string, code uint16) {
	for w := range m.waiters {
		if m.matches(w.key, device, code) {
			close(w.ch)
			delete(m.waiters, w)
		}
	}
}

// Resume возобновляет работу после экстренной остановки.
func (m *Module) Resume() { m.input.ResumeGrab() }

// Suspended сообщает, приостановлен ли mKey после экстренной остановки.
func (m *Module) Suspended() bool { return m.input.GrabSuspended() }

// updateGrab пересчитывает, какие устройства нужно захватить: те, у которых есть клавиши,
// «съедаемые» горячими клавишами или переназначаемые (под блокировкой).
func (m *Module) updateGrab() {
	// Коды, ради которых нужен захват, и фильтры устройств.
	// autoID — авто-ID устройства клавиши ({UnKey2.001}): захватывается только оно.
	// typ — тип событий кода (0 — EV_KEY; у привязок осей — EV_ABS или EV_REL).
	type need struct {
		typ    uint16
		codes  []uint16
		device string
		autoID string
	}
	var needs []need
	for h := range m.hotkeys {
		if h.consume {
			main := h.chord[len(h.chord)-1]
			codes := []uint16{main.Code}
			if main.AnySide {
				codes = append(codes, sidePair(main.Code))
			}
			needs = append(needs, need{codes: codes, device: h.device, autoID: main.Device})
		}
	}
	for _, r := range m.remaps {
		needs = append(needs, need{codes: []uint16{r.from.Code}, device: r.device, autoID: r.from.Device})
	}
	for _, b := range m.bindings {
		if b.Hide {
			codes := []uint16{b.From.Code}
			if b.From.AnySide {
				codes = append(codes, sidePair(b.From.Code))
			}
			needs = append(needs, need{typ: b.From.Type, codes: codes, autoID: b.From.Device})
		}
	}
	if len(needs) == 0 {
		m.input.SetGrabPolicy(nil)
		return
	}

	// Политика: у устройства есть хоть один такой код (и оно подходит под фильтр).
	m.input.SetGrabPolicy(func(d contracts.InputDevice) bool {
		for _, n := range needs {
			if n.device != "" && !deviceMatches(d.Info, n.device) {
				continue
			}
			if n.autoID != "" && (m.inspect == nil || m.inspect.DeviceOf(d.Info.Path) != n.autoID) {
				continue
			}
			typ := n.typ
			if typ == 0 {
				typ = ev.EvKey
			}
			for _, c := range n.codes {
				if d.Info.Caps.Has(typ, c) {
					return true
				}
			}
		}
		return false
	})
}

// deviceMatches сообщает, подходит ли устройство под фильтр: часть имени или "vid:pid".
func deviceMatches(info ev.Info, filter string) bool {
	return strings.Contains(strings.ToLower(info.Name), strings.ToLower(filter)) || info.ID.String() == strings.ToLower(filter)
}

// sidePair возвращает парный модификатор другой стороны (или тот же код).
func sidePair(code uint16) uint16 {
	pairs := map[uint16]uint16{
		ev.KeyLeftctrl: ev.KeyRightctrl, ev.KeyLeftshift: ev.KeyRightshift,
		ev.KeyLeftalt: ev.KeyRightalt, ev.KeyLeftmeta: ev.KeyRightmeta,
	}
	if p, ok := pairs[code]; ok {
		return p
	}
	return code
}

// isModifier сообщает, является ли код модификатором.
func isModifier(code uint16) bool { return slices.Contains(modifierCodes, code) }

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module       = (*Module)(nil)
	_ contracts.InputHandler = (*Module)(nil)
	_ contracts.KeyState     = (*Module)(nil)
)
