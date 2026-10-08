package hotkeys

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
)

// Привязки «физический ввод → виртуальный выход» (FR-VD-3, ADR-0028): раздел bindings включённых
// проектов. Поток чтения устройств только замечает нажатие кнопки или движение оси источника (и,
// если привязка прячет его, «съедает» его, как горячая клавиша с consume); само действие на
// виртуальном устройстве выполняет фоновый обработчик по очереди — так поток ввода никогда не ждёт
// записи в uinput, а порядок сохраняется.
//
// Виды привязок (настройки — contracts.CompileBinding):
//   - кнопка → кнопка, кнопка → ось (положение value, плавно — ramp_ms; с latch — «рычаг»:
//     отпустили — ось остаётся, где была);
//   - ось → ось (invert, deadzone, sensitivity), ось → кнопка (порог threshold);
//   - мышь → ось (движение мыши наклоняет стик, остановилась — стик возвращается в центр; со steer —
//     «как руль»: движение поворачивает ось, она держит положение или плавно возвращается за
//     recenter_ms), мышь → кнопка (движение или колесо в сторону threshold держит кнопку нажатой).

// bindQueue — размер очереди действий привязок: с запасом на быстрые нажатия и движения осей.
const bindQueue = 4096

// Тайминги фонового обработчика.
const (
	// bindTick — шаг плавных движений (ramp_ms) и опроса мыши: 10 мс — плавно для глаза и игры
	// (100 обновлений в секунду), но без лишней нагрузки. Таймер работает, только пока что-то движется.
	bindTick = 10 * time.Millisecond
	// mouseFull — сколько единиц движения мыши за шаг bindTick дают полный наклон стика при
	// чувствительности 1 (≈5000 единиц в секунду — быстрый, но обычный взмах мышью).
	mouseFull = 50.0
	// mouseRelease — через сколько после последнего движения мыши отпускается кнопка «мышь → кнопка»
	// (один щелчок колеса — короткое нажатие).
	mouseRelease = 60 * time.Millisecond
	// steerFull — сколько единиц движения мыши поворачивают руль (steer) от центра до упора при
	// чувствительности 1: ≈ движение мыши на 5–10 см — руль можно держать точно, не упираясь в стол.
	steerFull = 1000.0
	// mouseDecay — во сколько раз за шаг уменьшается наклон стика, когда мышь не движется: стик
	// возвращается в центр за ~50 мс, но не дёргается между отчётами мыши (мышь 125 Гц шлёт их раз в 8 мс).
	mouseDecay = 0.5
	// hysteresis — ось → кнопка: кнопка отпускается, когда наклон опустится ниже 80% порога
	// (иначе кнопка «дребезжит» на границе).
	hysteresis = 0.8
)

// binding — проверенная привязка.
type binding struct {
	contracts.CompiledBinding
}

// bindAction — действие для фонового обработчика: нажатие или отпускание кнопки-источника (down),
// новое положение оси-источника (axis, value — после обработки −1…1 или движение мыши);
// reset — отпустить всё, что нажато привязками (привязки поменялись или mKey останавливается).
type bindAction struct {
	b     *binding
	down  bool
	axis  bool
	value float64
	reset bool
}

// axisKey — ось конкретного устройства (для учёта кнопок, держащих одну ось).
type axisKey struct {
	device string
	code   uint16
}

// ramp — плавное движение оси цели: от from к to за время dur от start.
type ramp struct {
	from, to float64
	start    time.Time
	dur      time.Duration
	b        *binding
}

// mouseState — состояние привязки «мышь → …»: накопленное движение за шаг, текущий наклон,
// время последнего движения (для кнопки).
type mouseState struct {
	acc  float64
	cur  float64
	last time.Time
}

// bindState — состояние фонового обработчика (только его горутина): нажатые привязки; для каждой
// оси цели — нажатые кнопки по порядку (ось в положении последней нажатой), текущее положение и
// плавное движение; состояние привязок мыши.
type bindState struct {
	pressed map[*binding]bool
	axes    map[axisKey][]*binding
	cur     map[axisKey]float64
	ramps   map[axisKey]*ramp
	mouse   map[*binding]*mouseState
	touched map[axisKey]*binding
}

// newBindState создаёт пустое состояние.
func newBindState() *bindState {
	return &bindState{
		pressed: map[*binding]bool{}, axes: map[axisKey][]*binding{}, cur: map[axisKey]float64{},
		ramps: map[axisKey]*ramp{}, mouse: map[*binding]*mouseState{}, touched: map[axisKey]*binding{},
	}
}

// busy сообщает, что нужен таймер: идёт плавное движение или мышь двигает цель.
func (st *bindState) busy() bool {
	if len(st.ramps) > 0 {
		return true
	}
	for b, s := range st.mouse {
		if s.acc != 0 || st.pressed[b] || s.cur != 0 && (!b.Steer || b.Recenter > 0) {
			return true
		}
	}
	return false
}

// ParseBindingSource разбирает источник привязки: кнопку или ось (contracts.KeyState).
func (m *Module) ParseBindingSource(name string) (contracts.DeviceKey, error) {
	// Кнопка — как в горячих клавишах.
	k, keyErr := m.ParseKey(name)
	if keyErr == nil {
		return k, nil
	}

	// Ось: "LX", "MouseX" — любого устройства; "Устройство.Ось" — именно его.
	ref := strings.Trim(strings.TrimSpace(name), "{}")
	if dev, axis, ok := strings.Cut(ref, "."); ok {
		if m.inspect == nil {
			return contracts.DeviceKey{}, keyErr
		}
		if a, err := m.inspect.ResolveAxis(dev, axis); err == nil {
			return a, nil
		}
		return contracts.DeviceKey{}, keyErr
	}
	if a, ok := keys.LookupAxis(ref); ok {
		return contracts.DeviceKey{Key: a}, nil
	}
	if a, ok := keys.LookupRel(ref); ok {
		return contracts.DeviceKey{Key: a}, nil
	}
	return contracts.DeviceKey{}, keyErr
}

// compileBindings разбирает привязки включённого проекта; ошибочные пропускаются с предупреждением
// (проект с ними не включится — его проверяет движок; здесь — защита от гонок при перезагрузке).
func (m *Module) compileBindings(p contracts.ProjectState) []*binding {
	var out []*binding
	for i, b := range p.Project.Bindings {
		src, err := m.ParseBindingSource(b.From)
		if err == nil && src.Type == ev.EvKey {
			// Источник-сочетание ({Ctrl}{A}) привязкой не бывает: одна кнопка.
			var chord []contracts.DeviceKey
			if chord, err = m.parseChord(braced(b.From)); err == nil && len(chord) != 1 {
				err = dsl.NewError(dsl.Pos{}, dsl.ErrBadHotkey)
			}
		}
		var c contracts.CompiledBinding
		if err == nil {
			c, err = contracts.CompileBinding(b, src, p.Project.VirtualDevices, m.vdm)
		}
		if err != nil {
			m.log.Warn("invalid binding skipped", "project", p.Project.ID, "index", i+1, "err", err)
			continue
		}
		out = append(out, &binding{CompiledBinding: c})
	}
	return out
}

// feedBindings передаёт нажатие или отпускание code на устройстве device привязкам-кнопкам (под m.mu).
// Возвращает true, если нажатие нужно спрятать от системы (hide).
func (m *Module) feedBindings(device string, code uint16, down bool) bool {
	hide := false
	if m.bindClosed {
		return false
	}
	for _, b := range m.bindings {
		if b.From.Type != ev.EvKey || !m.matches(b.From, device, code) {
			continue
		}
		m.bindCh <- bindAction{b: b, down: down}
		hide = hide || b.Hide
	}
	return hide
}

// feedAxis передаёт движение оси (EV_ABS) или мыши (EV_REL) привязкам-осям (под m.mu). Положение
// стика переводится в −1…1 по диапазону оси устройства. Очередь полна — движение пропускается
// (следующее его заменит), чтобы поток ввода не ждал. true — событие нужно спрятать (hide).
func (m *Module) feedAxis(device string, e *ev.Event) bool {
	hide := false
	if m.bindClosed {
		return false
	}
	for _, b := range m.bindings {
		if b.From.Type != e.Type || !m.matches(b.From, device, e.Code) {
			continue
		}
		v := float64(e.Value)
		if e.Type == ev.EvAbs {
			v = m.normalizeAbs(device, e.Code, e.Value, b.Invert)
		}
		select {
		case m.bindCh <- bindAction{b: b, axis: true, value: v}:
		default:
		}
		hide = hide || b.Hide
	}
	return hide
}

// absRange — диапазон оси устройства и то, что ось односторонняя (курок: покой — минимум).
type absRange struct {
	info     ev.AbsInfo
	oneSided bool
}

// normalizeAbs переводит значение оси устройства в −1…1 (односторонняя — в 0…1), invert —
// переворачивает (двусторонняя: влево ↔ вправо, курок: отпущен ↔ нажат). Диапазон оси
// запоминается при первом движении (под m.mu).
func (m *Module) normalizeAbs(device string, code uint16, value int32, invert bool) float64 {
	// Диапазон: из кэша или из сведений об устройстве.
	key := axisKey{device: device, code: code}
	r, ok := m.absRanges[key]
	if !ok {
		r = m.lookupRange(device, code)
		m.absRanges[key] = r
	}
	span := float64(r.info.Maximum - r.info.Minimum)
	if span <= 0 {
		return 0
	}

	// Положение: доля хода 0…1, у двусторонней оси — −1…1 от середины.
	f := float64(value-r.info.Minimum) / span
	f = max(0, min(1, f))
	if invert {
		f = 1 - f
	}
	if r.oneSided {
		return f
	}
	return f*2 - 1
}

// lookupRange находит диапазон оси устройства. Курком (односторонней осью) считаются газ, тормоз,
// дроссель, а также ABS_Z/ABS_RZ с минимумом 0 у геймпада с кнопкой South — так их отдаёт
// драйвер xpad; у прочих джойстиков ABS_Z — обычная двусторонняя ось (поворот ручки).
func (m *Module) lookupRange(device string, code uint16) absRange {
	for _, d := range m.input.Devices() {
		if d.Info.Path != device {
			continue
		}
		info := d.Info.Caps.Abs[code]
		one := code == ev.AbsGas || code == ev.AbsBrake || code == ev.AbsThrottle
		if (code == ev.AbsZ || code == ev.AbsRz) && info.Minimum == 0 && d.Info.Caps.Has(ev.EvKey, ev.BtnSouth) {
			one = true
		}
		return absRange{info: info, oneSided: one}
	}
	return absRange{}
}

// bindWorker выполняет действия привязок по очереди, пока очередь не закрыта; в конце отпускает всё.
// Пока что-то движется плавно (ramp_ms, мышь), раз в bindTick обновляет положение осей.
func (m *Module) bindWorker() {
	defer m.wg.Done()
	st := newBindState()
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		// Таймер — только когда нужен.
		var tick <-chan time.Time
		switch {
		case st.busy() && ticker == nil:
			ticker = time.NewTicker(bindTick)
			tick = ticker.C
		case st.busy():
			tick = ticker.C
		case ticker != nil:
			ticker.Stop()
			ticker = nil
		}

		// Следующее действие или шаг таймера.
		select {
		case a, ok := <-m.bindCh:
			if !ok {
				m.applyBinding(st, bindAction{reset: true})
				return
			}
			m.applyBinding(st, a)
		case now := <-tick:
			m.stepBindings(st, now)
		}
	}
}

// applyBinding выполняет одно действие привязки.
func (m *Module) applyBinding(st *bindState, a bindAction) {
	ctx := context.Background()

	// Сброс: всё нажатое привязками — отпустить, отклонённые оси — в покой.
	if a.reset {
		for b := range st.pressed {
			if !b.To.Axis {
				m.bindTarget(ctx, b.To, false, 0)
			}
		}
		for k := range st.touched {
			if st.cur[k] != 0 || st.ramps[k] != nil {
				m.bindTarget(ctx, contracts.BindingTarget{Device: k.device, Code: k.code, Axis: true}, true, 0)
			}
		}
		*st = *newBindState()
		return
	}

	// Источник — ось или мышь.
	b := a.b
	if a.axis {
		if b.From.Type == ev.EvRel {
			m.feedMouse(ctx, st, b, a.value)
		} else {
			m.applyAxis(ctx, st, b, a.value)
		}
		return
	}

	// Источник — кнопка: повторное нажатие или отпускание ненажатого — пропускаем.
	if st.pressed[b] == a.down {
		return
	}
	if a.down {
		st.pressed[b] = true
	} else {
		delete(st.pressed, b)
	}

	// Кнопка: нажать или отпустить.
	if !b.To.Axis {
		m.bindTarget(ctx, b.To, a.down, 0)
		return
	}

	// Ось: положение последней нажатой кнопки этой оси; отпустили все — покой.
	k := axisKey{device: b.To.Device, code: b.To.Code}
	stack := slices.DeleteFunc(st.axes[k], func(x *binding) bool { return x == b })
	if a.down {
		stack = append(stack, b)
	}

	// «Рычаг» (latch) отпустили, и других нажатых кнопок у оси нет: ось остаётся, где её застало
	// отпускание (плавное движение останавливается).
	if !a.down && b.Latch && len(stack) == 0 {
		delete(st.axes, k)
		delete(st.ramps, k)
		return
	}
	value, ramped := 0.0, b
	if len(stack) > 0 {
		ramped = stack[len(stack)-1]
		value = ramped.To.Value
		st.axes[k] = stack
	} else {
		delete(st.axes, k)
	}
	m.moveAxis(ctx, st, k, b, value, ramped.Ramp, time.Now())
}

// moveAxis ставит ось цели k в value: сразу или плавно за dur (ramp_ms).
func (m *Module) moveAxis(ctx context.Context, st *bindState, k axisKey, b *binding, value float64, dur time.Duration, now time.Time) {
	st.touched[k] = b
	if dur <= 0 {
		delete(st.ramps, k)
		st.cur[k] = value
		m.bindTarget(ctx, b.To, true, value)
		return
	}
	st.ramps[k] = &ramp{from: st.cur[k], to: value, start: now, dur: dur, b: b}
}

// applyAxis передаёт положение оси-источника v (−1…1 или 0…1) цели: оси — с настройками,
// кнопке — по порогу.
func (m *Module) applyAxis(ctx context.Context, st *bindState, b *binding, v float64) {
	v = shape(v, b)

	// Ось → ось.
	if b.To.Axis {
		k := axisKey{device: b.To.Device, code: b.To.Code}
		m.moveAxis(ctx, st, k, b, max(-1, min(1, v*b.Sensitivity)), 0, time.Time{})
		return
	}

	// Ось → кнопка: нажать за порогом, отпустить ниже 80% порога.
	on := st.pressed[b]
	t := b.Threshold
	reach := v * math.Copysign(1, t)
	switch {
	case !on && reach >= math.Abs(t):
		st.pressed[b] = true
		m.bindTarget(ctx, b.To, true, 0)
	case on && reach < math.Abs(t)*hysteresis:
		delete(st.pressed, b)
		m.bindTarget(ctx, b.To, false, 0)
	}
}

// shape применяет к положению оси мёртвую зону (малые отклонения — центр, остальное растягивается
// на весь ход) и кривую отклика (|v|^curve с тем же знаком; переворот уже сделан в normalizeAbs).
func shape(v float64, b *binding) float64 {
	if dz := b.Deadzone; dz > 0 {
		a := math.Abs(v)
		if a < dz {
			return 0
		}
		v = math.Copysign((a-dz)/(1-dz), v)
	}
	if b.Curve > 0 && b.Curve != 1 {
		v = math.Copysign(math.Pow(math.Abs(v), b.Curve), v)
	}
	return v
}

// feedMouse копит движение мыши d для привязки «мышь → …»: стик наклоняется на шаге таймера,
// кнопка нажимается сразу при движении в сторону порога.
func (m *Module) feedMouse(ctx context.Context, st *bindState, b *binding, d float64) {
	if b.Invert {
		d = -d
	}
	s := st.mouse[b]
	if s == nil {
		s = &mouseState{}
		st.mouse[b] = s
	}

	// Мышь → кнопка: движение в сторону порога держит кнопку нажатой.
	if !b.To.Axis {
		if d*b.Threshold > 0 {
			s.last = time.Now()
			if !st.pressed[b] {
				st.pressed[b] = true
				m.bindTarget(ctx, b.To, true, 0)
			}
		}
		return
	}

	// Мышь → ось: движение копится до шага таймера (руль — запоминает, когда мышь двигалась).
	s.acc += d
	s.last = time.Now()
	st.touched[axisKey{device: b.To.Device, code: b.To.Code}] = b
}

// stepBindings — шаг таймера: плавные движения осей и привязки мыши.
func (m *Module) stepBindings(st *bindState, now time.Time) {
	ctx := context.Background()

	// Плавные движения: положение по доле прошедшего времени; дошли — движение закончено.
	for k, r := range st.ramps {
		f := float64(now.Sub(r.start)) / float64(r.dur)
		v := r.to
		if f < 1 {
			v = r.from + (r.to-r.from)*max(0, f)
		} else {
			delete(st.ramps, k)
		}
		st.cur[k] = v
		m.bindTarget(ctx, r.b.To, true, v)
	}

	// Мышь: наклон стика — по движению за шаг (нет движения — плавно к центру); кнопка
	// отпускается, когда мышь перестала двигаться.
	for b, s := range st.mouse {
		if !b.To.Axis {
			if st.pressed[b] && now.Sub(s.last) >= mouseRelease {
				delete(st.pressed, b)
				m.bindTarget(ctx, b.To, false, 0)
			}
			continue
		}
		var next float64
		switch {
		// Руль: движение поворачивает ось от текущего положения; мышь стоит — ось держит
		// положение или возвращается в центр со скоростью «из упора за recenter_ms».
		case b.Steer && s.acc != 0:
			next = max(-1, min(1, s.cur+s.acc*b.Sensitivity/steerFull))
		case b.Steer && b.Recenter > 0 && now.Sub(s.last) >= bindTick:
			step := float64(bindTick) / float64(b.Recenter)
			next = s.cur - math.Copysign(min(step, math.Abs(s.cur)), s.cur)
		case b.Steer:
			next = s.cur

		// Стик: наклон по движению за шаг, нет движения — плавно к центру.
		case s.acc != 0:
			next = max(-1, min(1, s.acc*b.Sensitivity/mouseFull))
		default:
			next = s.cur * mouseDecay
			if math.Abs(next) < 0.02 {
				next = 0
			}
		}
		s.acc = 0
		if next != s.cur {
			s.cur = next
			st.cur[axisKey{device: b.To.Device, code: b.To.Code}] = next
			m.bindTarget(ctx, b.To, true, next)
		}
	}
}

// bindTarget отправляет нажатие (down) или отпускание кнопки цели t, либо ставит ось цели в value.
func (m *Module) bindTarget(ctx context.Context, t contracts.BindingTarget, down bool, value float64) {
	dev, err := m.bindDevice(t.Device)
	if err != nil {
		m.log.Warn("binding target unavailable", "device", t.Device, "err", err)
		return
	}
	switch {
	case t.Axis:
		if s, ok := dev.(contracts.AxisSetter); ok {
			err = s.SetAxis(ctx, t.Code, value)
		}
	case down:
		err = dev.Press(ctx, t.Code)
	default:
		err = dev.Release(ctx, t.Code)
	}
	if err != nil {
		m.log.Warn("binding output failed", "device", t.Device, "err", err)
	}
}

// bindDevice возвращает устройство цели: клавиатура или мышь mKey, иначе виртуальное устройство проекта.
func (m *Module) bindDevice(name string) (contracts.VirtualDevice, error) {
	switch {
	case name == dsl.DeviceKeyboard && m.devs != nil:
		return m.devs.Keyboard()
	case name == dsl.DeviceMouse && m.devs != nil:
		return m.devs.Mouse()
	case m.vdm != nil:
		return m.vdm.Device(name)
	}
	return nil, contracts.ErrOutputUnavailable
}
