package output

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Виртуальные устройства проектов (FR-VD-1): шаблоны и устройство-геймпад поверх device.
//
// Шаблон описывает, каким устройство видят система и игры (VID:PID, кнопки, оси, вибрация), и как
// понятные имена из макросов превращаются в события этого устройства. Например, у Xbox 360 (драйвер
// xpad) левая кнопка X приходит кодом BTN_X = 0x133, курки — осями ABS_Z/ABS_RZ, крестовина — осями
// ABS_HAT0X/Y; игры и Steam ждут именно этого. Поэтому {pad2.West} у шаблона xbox360 — 0x133,
// а {pad2.LT} — ось ABS_Z до упора.

// vtemplate — шаблон виртуального устройства.
type vtemplate struct {
	// id — имя шаблона в проекте ("xbox360").
	id string
	// build — описание устройства для uinput (без имени и phys: их подставляет менеджер).
	build func(v project.VirtualDevice) (ev.Setup, error)
	// remap — кнопка из макросов → код устройства (Xbox: West → BTN_X).
	remap map[uint16]uint16
	// buttonAxes — кнопки, которые устройство передаёт осью (курки и крестовина Xbox).
	buttonAxes map[uint16]axisPress
	// oneSided — оси «от нуля» (курки): в макросах 0…1; остальные — −1…1.
	oneSided map[uint16]bool
	// inverted — оси «от нуля», у которых покой — максимум (педали руля): 0 в макросах — максимум.
	inverted map[uint16]bool
	// controls — свои имена кнопок и осей шаблона ({wheel.Gas}); пусто — общие имена геймпада.
	controls []control
}

// axisPress — кнопка, нажатие которой — положение оси: ось, значение при нажатии и нужна ли ещё
// и сама кнопка (у DualShock курки — и кнопка, и ось).
type axisPress struct {
	axis  uint16
	value int32
	key   bool
}

// stick — параметры стика и других осей с центром; trigger — курка; hat — крестовины.
var (
	stick   = ev.AbsInfo{Minimum: -32768, Maximum: 32767, Fuzz: 16, Flat: 128}
	stick8  = ev.AbsInfo{Minimum: 0, Maximum: 255, Value: 128}
	trigger = ev.AbsInfo{Minimum: 0, Maximum: 255}
	hat     = ev.AbsInfo{Minimum: -1, Maximum: 1}
)

// dpadHat — крестовина, которую устройство передаёт осями HAT0X/Y.
var dpadHat = map[uint16]axisPress{
	ev.BtnDpadUp: {axis: ev.AbsHat0y, value: -1}, ev.BtnDpadDown: {axis: ev.AbsHat0y, value: 1},
	ev.BtnDpadLeft: {axis: ev.AbsHat0x, value: -1}, ev.BtnDpadRight: {axis: ev.AbsHat0x, value: 1},
}

// templates — встроенные шаблоны по имени.
var templates = map[string]vtemplate{
	// Xbox 360 (проводной, драйвер xpad): 045e:028e — его узнают SDL, Steam и игры.
	"xbox360": {
		id: "xbox360",
		build: func(project.VirtualDevice) (ev.Setup, error) {
			return ev.Setup{
				ID: ev.ID{Bustype: ev.BusUSB, Vendor: 0x045e, Product: 0x028e, Version: 0x0110},
				Keys: []uint16{ev.BtnSouth, ev.BtnEast, ev.BtnNorth, ev.BtnWest, ev.BtnTl, ev.BtnTr,
					ev.BtnSelect, ev.BtnStart, ev.BtnMode, ev.BtnThumbl, ev.BtnThumbr},
				Abs: map[uint16]ev.AbsInfo{ev.AbsX: stick, ev.AbsY: stick, ev.AbsRx: stick, ev.AbsRy: stick,
					ev.AbsZ: trigger, ev.AbsRz: trigger, ev.AbsHat0x: hat, ev.AbsHat0y: hat},
				FF: []uint16{ffRumble},
			}, nil
		},
		remap: map[uint16]uint16{ev.BtnWest: ev.BtnNorth, ev.BtnNorth: ev.BtnWest},
		buttonAxes: merge(dpadHat, map[uint16]axisPress{
			ev.BtnTl2: {axis: ev.AbsZ, value: 255}, ev.BtnTr2: {axis: ev.AbsRz, value: 255},
		}),
		oneSided: map[uint16]bool{ev.AbsZ: true, ev.AbsRz: true},
	},
	// DualShock 4 (054c:09cc, драйвер hid-playstation): кнопки по сторонам света, курки — и
	// кнопкой, и осью, крестовина — осями.
	"ds4": {
		id: "ds4",
		build: func(project.VirtualDevice) (ev.Setup, error) {
			return ev.Setup{
				ID: ev.ID{Bustype: ev.BusUSB, Vendor: 0x054c, Product: 0x09cc, Version: 0x8111},
				Keys: []uint16{ev.BtnSouth, ev.BtnEast, ev.BtnNorth, ev.BtnWest, ev.BtnTl, ev.BtnTr,
					ev.BtnTl2, ev.BtnTr2, ev.BtnSelect, ev.BtnStart, ev.BtnMode, ev.BtnThumbl, ev.BtnThumbr},
				Abs: map[uint16]ev.AbsInfo{ev.AbsX: stick8, ev.AbsY: stick8, ev.AbsRx: stick8, ev.AbsRy: stick8,
					ev.AbsZ: trigger, ev.AbsRz: trigger, ev.AbsHat0x: hat, ev.AbsHat0y: hat},
				FF: []uint16{ffRumble},
			}, nil
		},
		buttonAxes: merge(dpadHat, map[uint16]axisPress{
			ev.BtnTl2: {axis: ev.AbsZ, value: 255, key: true}, ev.BtnTr2: {axis: ev.AbsRz, value: 255, key: true},
		}),
		oneSided: map[uint16]bool{ev.AbsZ: true, ev.AbsRz: true},
	},
	// Универсальный джойстик: 12 кнопок (BTN_TRIGGER…BTN_BASE6), 6 осей и крестовина.
	"joystick": {
		id: "joystick",
		build: func(project.VirtualDevice) (ev.Setup, error) {
			var btns []uint16
			for c := ev.BtnTrigger; c <= ev.BtnBase6; c++ {
				btns = append(btns, uint16(c))
			}
			return ev.Setup{
				ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0010, Version: 1},
				Keys: btns,
				Abs: map[uint16]ev.AbsInfo{ev.AbsX: stick, ev.AbsY: stick, ev.AbsZ: stick, ev.AbsRx: stick,
					ev.AbsRy: stick, ev.AbsRz: stick, ev.AbsHat0x: hat, ev.AbsHat0y: hat},
			}, nil
		},
		buttonAxes: dpadHat,
	},
	// Руль с педалями (FR-VD-6): поворот −1…1, педали 0…1 (0 — отпущена), крестовина осями.
	"wheel": {
		id:         "wheel",
		build:      wheelSetup,
		buttonAxes: dpadHat,
		oneSided:   map[uint16]bool{ev.AbsY: true, ev.AbsZ: true, ev.AbsRz: true},
		inverted:   map[uint16]bool{ev.AbsY: true, ev.AbsZ: true, ev.AbsRz: true},
		controls:   wheelControls(),
	},
	// Лётный джойстик (FR-VD-7): ручка, РУД и ползунок (0…1), 4 шляпки, 56 кнопок.
	"flightstick": {
		id:         "flightstick",
		build:      flightSetup,
		buttonAxes: dpadHat,
		oneSided:   map[uint16]bool{ev.AbsThrottle: true, ev.AbsZ: true},
		controls:   flightControls(),
	},
	// Тач-экран: до 10 касаний (multitouch protocol B), координаты 0…32767 на весь экран.
	"touchscreen": {
		id: "touchscreen",
		build: func(project.VirtualDevice) (ev.Setup, error) {
			pos := ev.AbsInfo{Minimum: 0, Maximum: 32767}
			return ev.Setup{
				ID:    ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0011, Version: 1},
				Keys:  []uint16{ev.BtnTouch},
				Props: []uint16{ev.InputPropDirect},
				Abs: map[uint16]ev.AbsInfo{ev.AbsX: pos, ev.AbsY: pos, ev.AbsMtSlot: {Minimum: 0, Maximum: 9},
					ev.AbsMtTrackingId: {Minimum: 0, Maximum: 65535}, ev.AbsMtPositionX: pos, ev.AbsMtPositionY: pos},
			}, nil
		},
	},
	// Дополнительные клавиатура и мышь (например, отдельная «клавиатура игрока 2»).
	"keyboard": {id: "keyboard", build: func(project.VirtualDevice) (ev.Setup, error) { return keyboardSetup(), nil }},
	"mouse":    {id: "mouse", build: func(project.VirtualDevice) (ev.Setup, error) { return mouseSetup(), nil }},
	// Свой набор кнопок и осей (buttons, axes в проекте).
	"custom": {id: "custom", build: customSetup},
}

// templateOrder — порядок шаблонов в списках.
var templateOrder = []string{"xbox360", "ds4", "wheel", "flightstick", "joystick", "touchscreen", "keyboard", "mouse", "custom"}

// ffRumble — FF_RUMBLE: вибрация двумя моторами (её ждут игры от геймпадов).
const ffRumble = 0x50

// merge объединяет таблицы «кнопка → ось».
func merge(a, b map[uint16]axisPress) map[uint16]axisPress {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

// customSetup описывает устройство шаблона custom: кнопки — именами mKey или ядра, оси — с диапазонами.
func customSetup(v project.VirtualDevice) (ev.Setup, error) {
	s := ev.Setup{ID: ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0012, Version: 1}, Abs: map[uint16]ev.AbsInfo{}}
	for _, b := range v.Buttons {
		codes := buttonCodes(b)
		if len(codes) == 0 {
			return s, fmt.Errorf("%w: button %q", contracts.ErrUnknownControl, b)
		}
		s.Keys = append(s.Keys, codes[0])
	}
	for name, r := range v.Axes {
		code, ok := axisCode(name)
		if !ok {
			return s, fmt.Errorf("%w: axis %q", contracts.ErrUnknownControl, name)
		}
		if r.Max <= r.Min {
			return s, fmt.Errorf("axis %q: max must be greater than min", name)
		}
		s.Abs[code] = ev.AbsInfo{Minimum: r.Min, Maximum: r.Max}
	}
	if len(s.Keys)+len(s.Abs) == 0 {
		return s, fmt.Errorf("custom device needs buttons or axes")
	}
	slices.Sort(s.Keys)
	return s, nil
}

// buttonCodes — возможные коды кнопки по имени, по порядку: кнопка геймпада ("X" — западная
// кнопка), клавиша или кнопка мыши ("X" — клавиша X), имя ядра ("BTN_TRIGGER"). Имена вроде A, B,
// X, Y есть и у геймпада, и у клавиатуры: устройство берёт первый код, который у него есть.
func buttonCodes(name string) []uint16 {
	var out []uint16
	if k, ok := keys.LookupGamepad(name); ok {
		out = append(out, k.Code)
	}
	if k, ok := keys.Lookup(name); ok {
		out = append(out, k.Code)
	}
	if c, ok := ev.ParseCode(ev.EvKey, strings.ToUpper(name)); ok {
		out = append(out, c)
	}
	return out
}

// axisCode находит ось по имени mKey ("LX") или ядра ("ABS_THROTTLE").
func axisCode(name string) (uint16, bool) {
	if k, ok := keys.LookupAxis(name); ok {
		return k.Code, true
	}
	return ev.ParseCode(ev.EvAbs, strings.ToUpper(name))
}

// vdevice — виртуальное устройство проекта: device плюс правила шаблона (кнопки-оси, замена кодов).
// Нажатия и Held — в кодах макросов (до замены), чтобы движок отпускал то, что нажал.
type vdevice struct {
	*device
	t       vtemplate
	setup   ev.Setup
	spec    project.VirtualDevice
	project string
	node    string

	// mu защищает held — нажатые кнопки в кодах макросов, touching — палец касается экрана
	// и nextTrack — номер следующего касания (ABS_MT_TRACKING_ID).
	mu        sync.Mutex
	held      map[uint16]bool
	touching  bool
	nextTrack int32
	// axes — последние отправленные значения осей (для проверки вживую, State); пусто — покой.
	axes map[uint16]int32
}

// setAxisValue запоминает отправленное значение оси (для State).
func (v *vdevice) setAxisValue(code uint16, value int32) {
	v.mu.Lock()
	if v.axes == nil {
		v.axes = map[uint16]int32{}
	}
	v.axes[code] = value
	v.mu.Unlock()
}

// isTouch сообщает, что устройство — сенсорный экран (multitouch, protocol B).
func (v *vdevice) isTouch() bool {
	_, ok := v.setup.Abs[ev.AbsMtTrackingId]
	return ok
}

// touchPos переводит долю экрана f (0…1) в значение оси code.
func (v *vdevice) touchPos(code uint16, f float64) int32 {
	info := v.setup.Abs[code]
	f = max(0, min(1, f))
	return info.Minimum + int32(math.Round(f*float64(info.Maximum-info.Minimum)))
}

// touchEvents — положение пальца в слоте 0: позиция multitouch и обычные оси (для программ,
// которые понимают только одно касание).
func (v *vdevice) touchEvents(x, y float64) []ev.Event {
	px, py := v.touchPos(ev.AbsMtPositionX, x), v.touchPos(ev.AbsMtPositionY, y)
	return []ev.Event{
		{Type: ev.EvAbs, Code: ev.AbsMtPositionX, Value: px}, {Type: ev.EvAbs, Code: ev.AbsMtPositionY, Value: py},
		{Type: ev.EvAbs, Code: ev.AbsX, Value: px}, {Type: ev.EvAbs, Code: ev.AbsY, Value: py},
	}
}

// TouchDown касается экрана в точке (x, y) — долях экрана (contracts.TouchSetter). Палец уже
// касается — переносится в эту точку.
func (v *vdevice) TouchDown(ctx context.Context, x, y float64) error {
	if !v.isTouch() {
		return fmt.Errorf("%s: %w: not a touchscreen", v.name, contracts.ErrUnknownControl)
	}
	v.mu.Lock()
	if v.touching {
		v.mu.Unlock()
		return v.TouchMove(ctx, x, y)
	}
	id := v.nextTrack
	v.nextTrack = (v.nextTrack + 1) % 65536
	v.mu.Unlock()

	// Новое касание в слоте 0: номер касания, место, «палец на экране».
	events := append([]ev.Event{
		{Type: ev.EvAbs, Code: ev.AbsMtSlot, Value: 0},
		{Type: ev.EvAbs, Code: ev.AbsMtTrackingId, Value: id},
	}, v.touchEvents(x, y)...)
	events = append(events, ev.Event{Type: ev.EvKey, Code: ev.BtnTouch, Value: ev.ValueDown})
	if err := v.Emit(ctx, events...); err != nil {
		return err
	}
	v.mu.Lock()
	v.touching = true
	v.mu.Unlock()
	return nil
}

// TouchMove двигает касающийся палец в точку (x, y) (contracts.TouchSetter).
func (v *vdevice) TouchMove(ctx context.Context, x, y float64) error {
	v.mu.Lock()
	touching := v.touching
	v.mu.Unlock()
	if !touching {
		return v.TouchDown(ctx, x, y)
	}
	return v.Emit(ctx, append([]ev.Event{{Type: ev.EvAbs, Code: ev.AbsMtSlot, Value: 0}}, v.touchEvents(x, y)...)...)
}

// TouchUp отрывает палец от экрана (contracts.TouchSetter).
func (v *vdevice) TouchUp(ctx context.Context) error {
	v.mu.Lock()
	touching := v.touching
	v.touching = false
	v.mu.Unlock()
	if !touching {
		return nil
	}
	return v.Emit(ctx, liftEvents()...)
}

// liftEvents — события «палец оторван» для слота 0: номер касания −1 (по протоколу ядра
// multitouch B это конец касания) и BTN_TOUCH = 0.
func liftEvents() []ev.Event {
	return []ev.Event{
		{Type: ev.EvAbs, Code: ev.AbsMtSlot, Value: 0},
		{Type: ev.EvAbs, Code: ev.AbsMtTrackingId, Value: -1},
		{Type: ev.EvKey, Code: ev.BtnTouch, Value: ev.ValueUp},
	}
}

// Press зажимает кнопку code (код макросов).
func (v *vdevice) Press(ctx context.Context, code uint16) error { return v.button(ctx, code, true) }

// Release отпускает кнопку code.
func (v *vdevice) Release(ctx context.Context, code uint16) error { return v.button(ctx, code, false) }

// Tap нажимает кнопку, держит hold и отпускает (отпускает и при отмене ctx).
func (v *vdevice) Tap(ctx context.Context, code uint16, hold time.Duration) error {
	if err := v.Press(ctx, code); err != nil {
		return err
	}
	if err := v.clk.Sleep(ctx, hold); err != nil {
		_ = v.Release(context.WithoutCancel(ctx), code)
		return err
	}
	return v.Release(ctx, code)
}

// button отправляет нажатие или отпускание: осью (курки, крестовина) или кнопкой с заменой кода.
func (v *vdevice) button(ctx context.Context, code uint16, down bool) error {
	value := int32(ev.ValueUp)
	if down {
		value = ev.ValueDown
	}

	// Кнопка, которую устройство передаёт осью: при отпускании ось возвращается в покой.
	var events []ev.Event
	if p, ok := v.t.buttonAxes[code]; ok {
		pos := v.setup.Abs[p.axis].Value
		if down {
			pos = p.value
		}
		events = append(events, ev.Event{Type: ev.EvAbs, Code: p.axis, Value: pos})
		if p.key {
			events = append(events, ev.Event{Type: ev.EvKey, Code: code, Value: value})
		}
	} else {
		dev := code
		if c, ok := v.t.remap[code]; ok {
			dev = c
		}
		if !slices.Contains(v.setup.Keys, dev) {
			return fmt.Errorf("%s: %w: %s", v.name, contracts.ErrUnknownControl, ev.CodeName(ev.EvKey, code))
		}
		events = append(events, ev.Event{Type: ev.EvKey, Code: dev, Value: value})
	}

	// Отправка и учёт нажатого (и положения оси у кнопок-осей).
	if err := v.Emit(ctx, events...); err != nil {
		return err
	}
	for _, e := range events {
		if e.Type == ev.EvAbs {
			v.setAxisValue(e.Code, e.Value)
		}
	}
	v.mu.Lock()
	if down {
		v.held[code] = true
	} else {
		delete(v.held, code)
	}
	v.mu.Unlock()
	return nil
}

// SetAxis ставит ось code в положение value: −1…1 (стики, крестовина) или 0…1 (курки).
func (v *vdevice) SetAxis(ctx context.Context, code uint16, value float64) error {
	info, ok := v.setup.Abs[code]
	if !ok {
		return fmt.Errorf("%s: %w: %s", v.name, contracts.ErrUnknownControl, ev.CodeName(ev.EvAbs, code))
	}
	if v.t.inverted[code] {
		value = 1 - max(0, min(1, value))
	}
	raw := axisValue(info, value, v.t.oneSided[code])
	if err := v.Emit(ctx, ev.Event{Type: ev.EvAbs, Code: code, Value: raw}); err != nil {
		return err
	}
	v.setAxisValue(code, raw)
	return nil
}

// axisValue переводит положение из макроса в значение оси: −1…1 — на весь диапазон (0 — середина),
// у осей «от нуля» 0…1 — от минимума до максимума. Значения за пределами обрезаются.
func axisValue(info ev.AbsInfo, value float64, oneSided bool) int32 {
	lo, hi := -1.0, 1.0
	if oneSided {
		lo = 0
	}
	value = max(lo, min(hi, value))
	f := (value - lo) / (hi - lo)
	return info.Minimum + int32(math.Round(f*float64(info.Maximum-info.Minimum)))
}

// Held возвращает нажатые кнопки в кодах макросов.
func (v *vdevice) Held() []uint16 {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := slices.Collect(maps.Keys(v.held))
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ReleaseAll отпускает все кнопки и возвращает оси в покой (SEC-2).
func (v *vdevice) ReleaseAll() error {
	// Кнопки — как у обычного устройства.
	err := v.device.ReleaseAll()

	// Оси — в положение покоя (центр стиков, отпущенные курки), одним пакетом. У сенсорного
	// экрана оси не сбрасываются: номер касания 0 означал бы новое касание — только отрываем палец.
	events := make([]ev.Event, 0, len(v.setup.Abs)+1)
	if v.isTouch() {
		v.mu.Lock()
		if v.touching {
			events = append(events, liftEvents()...)
			v.touching = false
		}
		v.mu.Unlock()
	} else {
		for _, code := range slices.Sorted(maps.Keys(v.setup.Abs)) {
			events = append(events, ev.Event{Type: ev.EvAbs, Code: code, Value: v.setup.Abs[code].Value})
		}
	}
	if len(events) > 0 {
		events = append(events, ev.Sync())
		v.device.mu.Lock()
		if werr := v.w.Write(events...); werr != nil && err == nil {
			err = fmt.Errorf("%s: reset axes: %w", v.name, werr)
		}
		v.device.mu.Unlock()
	}
	v.mu.Lock()
	clear(v.held)
	clear(v.axes)
	v.mu.Unlock()
	return err
}

// state возвращает нажатые кнопки и положения осей именами для макросов (contracts.VirtualState):
// имена шаблона ({wheel.Gas}), иначе общие; оси без имени не показываются.
func (v *vdevice) state() contracts.VirtualState {
	v.mu.Lock()
	held := slices.Sorted(maps.Keys(v.held))
	axes := maps.Clone(v.axes)
	v.mu.Unlock()

	// Имена шаблона по коду: кнопки и оси отдельно.
	btnNames, axisNames := map[uint16]string{}, map[uint16]string{}
	for _, c := range v.t.controls {
		if c.axis {
			axisNames[c.code] = c.name
		} else if _, dup := btnNames[c.code]; !dup {
			btnNames[c.code] = c.name
		}
	}

	// Кнопки.
	out := contracts.VirtualState{Buttons: []string{}, Axes: map[string]float64{}}
	for _, code := range held {
		name, ok := btnNames[code]
		if !ok {
			name, ok = keys.NameOf(code)
		}
		if ok {
			out.Buttons = append(out.Buttons, name)
		}
	}

	// Оси: значение — как в макросах (−1…1 или 0…1, у педалей 0 — отпущена).
	for code, info := range v.setup.Abs {
		name, ok := axisNames[code]
		if !ok {
			name, ok = keys.AxisNameOf(code)
		}
		span := float64(info.Maximum - info.Minimum)
		if !ok || span <= 0 {
			continue
		}
		raw, moved := axes[code]
		if !moved {
			raw = info.Value
		}
		f := float64(raw-info.Minimum) / span
		switch {
		case v.t.inverted[code]:
			f = 1 - f
		case !v.t.oneSided[code]:
			f = f*2 - 1
		}
		out.Axes[name] = math.Round(f*1000) / 1000
	}
	return out
}

// Проверки на этапе компиляции, что vdevice реализует контракты.
var (
	_ contracts.VirtualDevice = (*vdevice)(nil)
	_ contracts.AxisSetter    = (*vdevice)(nil)
	_ contracts.TouchSetter   = (*vdevice)(nil)
)
