package input

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// Захват устройств и passthrough (FR-HK-2) с защитой от потери клавиатуры (SEC-1, SEC-3).
//
// Схема: захваченное устройство (EVIOCGRAB) отдаёт события только mKey; mKey передаёт их
// обработчику (модуль hotkeys), который может «съесть» событие горячей клавиши, а всё остальное
// сразу пишет в passthrough-копию устройства (uinput с теми же возможностями) — система видит
// копию как обычную клавиатуру.
//
// Защиты:
//   - захват включается, только когда на устройстве не зажата ни одна клавиша — иначе система
//     «запомнила» бы клавишу зажатой навсегда;
//   - экстренная остановка (по умолчанию Esc+Backspace+Enter одновременно) проверяется раньше
//     любого обработчика: снимает весь захват и отключает его до ResumeGrab;
//   - watchdog: если обработка пачки событий зависла дольше WatchdogMS, захват снимается;
//   - при снятии захвата все клавиши, зажатые на копии, отпускаются, копия уничтожается;
//   - при аварийном завершении процесса ядро само снимает захват и удаляет копию.

// cloneWriter — passthrough-копия устройства: настоящий uinput или фейк в тестах.
type cloneWriter interface {
	// Write отправляет события одним пакетом.
	Write(events ...ev.Event) error
	// Close уничтожает копию.
	Close() error
}

// passthrough — копия захваченного устройства с учётом зажатых на ней клавиш.
type passthrough struct {
	w    cloneWriter
	held map[uint16]bool
}

// write отправляет события в копию и запоминает зажатые клавиши.
func (p *passthrough) write(events []ev.Event) error {
	for _, e := range events {
		if e.Type != ev.EvKey {
			continue
		}
		if e.Value == ev.ValueUp {
			delete(p.held, e.Code)
		} else {
			p.held[e.Code] = true
		}
	}
	return p.w.Write(events...)
}

// close отпускает всё, что зажато на копии, и уничтожает её.
func (p *passthrough) close() error {
	// Отпускаем зажатые клавиши одним пакетом.
	var errs []error
	if len(p.held) > 0 {
		ups := make([]ev.Event, 0, len(p.held)+1)
		for c := range p.held {
			ups = append(ups, ev.Event{Type: ev.EvKey, Code: c, Value: ev.ValueUp})
		}
		errs = append(errs, p.w.Write(append(ups, ev.Sync())...))
		clear(p.held)
	}

	// Уничтожаем копию.
	errs = append(errs, p.w.Close())
	return errors.Join(errs...)
}

// cloneSetup описывает passthrough-копию устройства: те же клавиши, оси и свойства, тот же VID:PID
// (по нему композитор применяет настройки модели), но имя с префиксом mKey — модуль input её не читает.
func cloneSetup(info ev.Info) ev.Setup {
	return ev.Setup{
		Name:  truncate(contracts.VirtualNamePrefix+"passthrough: "+info.Name, 79),
		Phys:  contracts.VirtualPhysPrefix + "passthrough",
		ID:    info.ID,
		Keys:  info.Caps.Codes[ev.EvKey],
		Rels:  info.Caps.Codes[ev.EvRel],
		Abs:   info.Caps.Abs,
		Msc:   info.Caps.Codes[ev.EvMsc],
		Props: info.Caps.Props,
	}
}

// truncate обрезает строку до max байт, не разрывая символ UTF-8.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// handlerBox — обёртка обработчика для atomic.Pointer.
type handlerBox struct {
	h contracts.InputHandler
}

// SetHandler устанавливает синхронный обработчик событий (nil — убрать).
func (m *Module) SetHandler(h contracts.InputHandler) {
	if h == nil {
		m.handler.Store(nil)
		return
	}
	m.handler.Store(&handlerBox{h: h})
}

// SetGrabPolicy задаёт, какие устройства захватывать, и сразу применяет политику.
func (m *Module) SetGrabPolicy(policy func(contracts.InputDevice) bool) {
	m.mu.Lock()
	m.grabPolicy = policy
	m.mu.Unlock()
	m.applyAll()
}

// GrabSuspended сообщает, отключён ли перехват после экстренной остановки.
func (m *Module) GrabSuspended() bool { return m.suspended.Load() }

// ResumeGrab снова разрешает перехват и применяет политику.
func (m *Module) ResumeGrab() {
	m.suspended.Store(false)
	m.log.Info("resumed after emergency stop")
	m.applyAll()
	m.bus.Publish(contracts.TopicResumed, nil)
}

// Inject отправляет события в passthrough-копию захваченного устройства.
func (m *Module) Inject(device string, events ...ev.Event) error {
	// Устройство должно быть открыто и захвачено.
	m.mu.RLock()
	d, ok := m.devices[device]
	m.mu.RUnlock()
	if !ok || !d.grabbed.Load() {
		return contracts.ErrNotGrabbed
	}

	// Завершаем пакет SYN_REPORT и пишем в копию.
	if len(events) == 0 {
		return nil
	}
	if !events[len(events)-1].IsSync() {
		events = append(events, ev.Sync())
	}
	d.cloneMu.Lock()
	defer d.cloneMu.Unlock()
	if d.clone == nil {
		return contracts.ErrNotGrabbed
	}
	return d.clone.write(events)
}

// applyAll применяет политику захвата ко всем открытым устройствам.
func (m *Module) applyAll() {
	m.mu.RLock()
	devs := make(map[string]*openDevice, len(m.devices))
	for p, d := range m.devices {
		devs[p] = d
	}
	m.mu.RUnlock()
	for p, d := range devs {
		m.applyGrab(p, d)
	}
}

// wantGrab сообщает, должно ли устройство быть захвачено по текущей политике.
func (m *Module) wantGrab(d *openDevice) bool {
	if m.suspended.Load() {
		return false
	}
	m.mu.RLock()
	policy := m.grabPolicy
	m.mu.RUnlock()
	return policy != nil && policy(d.desc)
}

// applyGrab включает или снимает захват устройства по политике.
func (m *Module) applyGrab(path string, d *openDevice) {
	switch want := m.wantGrab(d); {
	case want && !d.grabbed.Load() && d.grabbing.CompareAndSwap(false, true):
		// Подготовка захвата может ждать отпускания клавиш — в фоне.
		m.wg.Add(1)
		go m.grab(path, d)
	case !want && d.grabbed.Load():
		m.ungrab(path, d, "policy")
	}
}

// grab ждёт, пока на устройстве отпустят все клавиши, создаёт passthrough-копию, даёт композитору
// её подхватить и захватывает устройство.
func (m *Module) grab(path string, d *openDevice) {
	defer m.wg.Done()
	defer d.grabbing.Store(false)

	// Ждём отпускания всех клавиш (иначе система запомнит их зажатыми).
	if !m.waitReleased(d) {
		return
	}

	// Создаём копию и ждём, пока композитор её подхватит.
	w, err := m.createClone(cloneSetup(d.desc.Info))
	if err != nil {
		m.log.Warn("cannot create passthrough device, grab skipped", "path", path, "err", err)
		return
	}
	if err := m.clk.Sleep(m.ctx, time.Duration(m.cfg.SettleMS)*time.Millisecond); err != nil {
		_ = w.Close()
		return
	}

	// За время ожидания пользователь мог снова нажать клавишу или политика могла измениться.
	if !m.waitReleased(d) {
		_ = w.Close()
		return
	}
	d.cloneMu.Lock()
	d.clone = &passthrough{w: w, held: map[uint16]bool{}}
	d.cloneMu.Unlock()

	// Захватываем.
	if err := d.reader.Grab(); err != nil {
		m.log.Warn("grab failed", "path", path, "err", err)
		m.dropClone(d)
		return
	}
	d.grabbed.Store(true)
	m.log.Info("device grabbed", "path", path, "name", d.desc.Info.Name)
}

// waitReleased ждёт, пока на устройстве не останется зажатых клавиш. Возвращает false,
// если ждать больше не нужно (модуль остановлен, политика изменилась, ошибка).
func (m *Module) waitReleased(d *openDevice) bool {
	for {
		if m.ctx.Err() != nil || !m.wantGrab(d) {
			return false
		}
		pressed, err := d.reader.PressedKeys()
		if err != nil {
			m.log.Warn("cannot read key state, grab skipped", "path", d.desc.Info.Path, "err", err)
			return false
		}
		if len(pressed) == 0 {
			return true
		}
		if err := m.clk.Sleep(m.ctx, 20*time.Millisecond); err != nil {
			return false
		}
	}
}

// ungrab снимает захват: сначала ioctl (события сразу снова идут в систему напрямую),
// затем отпускание зажатого на копии и её уничтожение.
func (m *Module) ungrab(path string, d *openDevice, reason string) {
	if !d.grabbed.Swap(false) {
		return
	}
	if err := d.reader.Ungrab(); err != nil {
		m.log.Warn("ungrab failed", "path", path, "err", err)
	}
	m.log.Info("device released", "path", path, "reason", reason)

	// Копию убираем в фоне: если горутина чтения зависла, она может держать блокировку копии.
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.dropClone(d)
	}()
}

// dropClone отпускает клавиши на копии и уничтожает её.
func (m *Module) dropClone(d *openDevice) {
	d.cloneMu.Lock()
	defer d.cloneMu.Unlock()
	if d.clone == nil {
		return
	}
	if err := d.clone.close(); err != nil {
		m.log.Warn("close passthrough device", "err", err)
	}
	d.clone = nil
}

// forward отправляет события захваченного устройства в его копию.
func (m *Module) forward(d *openDevice, events []ev.Event) {
	d.cloneMu.Lock()
	defer d.cloneMu.Unlock()
	if d.clone == nil {
		return
	}
	if err := d.clone.write(events); err != nil {
		m.log.Debug("passthrough write", "err", err)
	}
}

// checkEmergency отслеживает зажатые клавиши устройства и запускает экстренную остановку,
// когда зажаты все клавиши комбинации. Вызывается из горутины чтения до любых обработчиков.
func (m *Module) checkEmergency(path string, d *openDevice, e ev.Event) {
	// Интересуют только клавиши.
	combo := m.emergency.Load()
	if e.Type != ev.EvKey || combo == nil {
		return
	}
	if e.Value == ev.ValueUp {
		delete(d.down, e.Code)
	} else {
		d.down[e.Code] = true
	}

	// Все клавиши комбинации зажаты — срабатываем один раз, до отпускания.
	all := true
	for _, k := range combo.keys {
		found := false
		for c := range d.down {
			found = found || k.Matches(c)
		}
		all = all && found
	}
	switch {
	case all && !d.emergencyFired:
		d.emergencyFired = true
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.emergencyStop("emergency keys on " + path)
		}()
	case !all:
		d.emergencyFired = false
	}
}

// EmergencyStop — экстренная остановка по команде (значок в трее, веб-интерфейс), см. emergencyStop.
func (m *Module) EmergencyStop(reason string) { m.emergencyStop(reason) }

// emergencyStop снимает весь захват, отключает его до ResumeGrab и сообщает остальным модулям,
// чтобы они остановили макросы и отпустили клавиши (SEC-1).
func (m *Module) emergencyStop(reason string) {
	m.suspended.Store(true)
	m.mu.RLock()
	devs := make(map[string]*openDevice, len(m.devices))
	for p, d := range m.devices {
		devs[p] = d
	}
	m.mu.RUnlock()
	for p, d := range devs {
		m.ungrab(p, d, "emergency")
	}
	m.log.Warn("EMERGENCY STOP: all grabs released, grabbing suspended", "reason", reason)
	m.bus.Publish(contracts.TopicEmergency, nil)
}

// watchdog периодически проверяет, не зависла ли обработка событий захваченных устройств (SEC-3).
func (m *Module) watchdog() {
	defer m.wg.Done()
	period := time.Duration(m.cfg.WatchdogMS) * time.Millisecond / 5
	t := time.NewTicker(max(period, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.checkWatchdog()
		}
	}
}

// checkWatchdog снимает захват со всех устройств, если хоть одно захваченное устройство
// обрабатывает пачку событий дольше WatchdogMS.
func (m *Module) checkWatchdog() {
	limit := time.Duration(m.cfg.WatchdogMS) * time.Millisecond
	now := m.clk.Now().UnixNano()
	m.mu.RLock()
	stuck := ""
	for p, d := range m.devices {
		if busy := d.busySince.Load(); d.grabbed.Load() && busy > 0 && time.Duration(now-busy) > limit {
			stuck = p
			break
		}
	}
	m.mu.RUnlock()
	if stuck != "" {
		m.emergencyStop("watchdog: event processing stuck on " + stuck)
	}
}

// emergencyKeys разбирает сочетание экстренной остановки («^{Esc}^{Backspace}{Enter}») в клавиши.
// Нужно не меньше двух клавиш: одна клавиша срабатывала бы при обычной работе.
func emergencyKeys(combo string) ([]keys.Key, error) {
	refs, err := dsl.ParseHotkey(combo)
	if err != nil {
		return nil, err
	}
	if len(refs) < 2 {
		return nil, errors.New("at least two keys are required")
	}
	out := make([]keys.Key, 0, len(refs))
	for _, r := range refs {
		k, ok := keys.Lookup(r.Name)
		if r.Device != "" || r.Code != nil || !ok {
			return nil, fmt.Errorf("unsupported key %q", r.Name)
		}
		out = append(out, k)
	}
	return out, nil
}

// EmergencyCombo возвращает сочетание экстренной остановки записью зажатием (contracts.InputSource).
func (m *Module) EmergencyCombo() string {
	if c := m.emergency.Load(); c != nil {
		return c.text
	}
	return ""
}

// SetEmergencyCombo меняет сочетание экстренной остановки сразу, без перезапуска (contracts.InputSource).
func (m *Module) SetEmergencyCombo(combo string) error {
	ks, err := emergencyKeys(combo)
	if err != nil {
		return err
	}
	m.emergency.Store(&emergencyCombo{text: combo, keys: ks})
	return nil
}
