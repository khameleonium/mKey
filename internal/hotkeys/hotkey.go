package hotkeys

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// Режимы срабатывания горячей клавиши (параметр on).
const (
	onPress   = "press"
	onRelease = "release"
	onHold    = "hold"
	onDouble  = "double"
	onToggle  = "toggle"
)

// hotkeyParams — параметры триггера hotkey.
type hotkeyParams struct {
	// Keys — сочетание записью зажатием, например "^{Ctrl}^{Alt}{H}" или "{F8}".
	Keys string `json:"keys"`
	// On — когда срабатывать: press (по умолчанию), release, hold, double, toggle.
	On string `json:"on"`
	// Consume — не передавать нажатие основной клавиши в систему (нужен захват устройства).
	Consume bool `json:"consume"`
	// Device — только для устройства, в имени которого есть эта строка, или "vid:pid".
	Device string `json:"device"`
	// HoldMS — для on: hold — сколько держать (по умолчанию 500 мс).
	HoldMS int `json:"hold_ms"`
	// DoubleMS — для on: double — наибольший промежуток между нажатиями (по умолчанию 300 мс).
	DoubleMS int `json:"double_ms"`
}

// hotkey — зарегистрированная горячая клавиша.
type hotkey struct {
	ev       contracts.EventRef
	chord    []keys.Key // последний элемент — основная клавиша
	on       string
	consume  bool
	device   string
	hold     time.Duration
	double   time.Duration
	fire     func(contracts.Fire)
	toggled  bool
	last     time.Time // время прошлого нажатия (on: double)
	pressSeq uint64    // номер нажатия (on: hold): таймер срабатывает, только если нажатие то же
	pending  bool      // основная клавиша нажата при выполненном сочетании (on: release)
}

// hotkeyType — вид триггера hotkey.
type hotkeyType struct{ m *Module }

// Meta возвращает метаданные вида триггера.
func (hotkeyType) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "hotkey", NameKey: "trigger.hotkey", DescriptionKey: "trigger.hotkey.description",
		Category: "input", Icon: "keyboard", Provider: ModuleID,
		ParamsSchema: []byte(`{"type":"object","required":["keys"],"properties":{` +
			`"keys":{"type":"string","x-widget":"keys"},` +
			`"on":{"enum":["press","release","hold","double","toggle"],"default":"press"},` +
			`"consume":{"type":"boolean"},"device":{"type":"string","x-widget":"device","x-advanced":true},` +
			`"hold_ms":{"type":"integer","minimum":1,"x-widget":"ms","x-advanced":true},` +
			`"double_ms":{"type":"integer","minimum":1,"x-widget":"ms","x-advanced":true}}}`),
	}
}

// parseHotkey разбирает и проверяет параметры триггера hotkey.
func parseHotkey(tr project.Trigger) (hotkeyParams, []keys.Key, error) {
	var p hotkeyParams
	if err := project.Decode(tr.Params, &p); err != nil {
		return p, nil, fmt.Errorf("hotkey: %w", err)
	}
	if strings.Trim(p.Keys, "{} ") == "" {
		return p, nil, project.Required("trigger", "hotkey", "keys")
	}
	chord, err := parseChord(p.Keys)
	if err != nil {
		return p, nil, fmt.Errorf("hotkey: %w", err)
	}
	if p.On == "" {
		p.On = onPress
	}
	switch p.On {
	case onPress, onRelease, onHold, onDouble, onToggle:
	default:
		return p, nil, fmt.Errorf("hotkey: unknown on %q", p.On)
	}
	return p, chord, nil
}

// Validate проверяет параметры, ничего не регистрируя.
func (hotkeyType) Validate(tr project.Trigger) error {
	_, _, err := parseHotkey(tr)
	return err
}

// Arm проверяет параметры и регистрирует горячую клавишу.
func (t hotkeyType) Arm(_ context.Context, ref contracts.EventRef, tr project.Trigger, fire func(contracts.Fire)) (func(), error) {
	p, chord, err := parseHotkey(tr)
	if err != nil {
		return nil, err
	}
	h := &hotkey{
		ev: ref, chord: chord, on: p.On, consume: p.Consume, device: p.Device, fire: fire,
		hold: ms(p.HoldMS, 500), double: ms(p.DoubleMS, 300),
	}

	// Регистрация и пересчёт захвата.
	m := t.m
	m.mu.Lock()
	m.hotkeys[h] = struct{}{}
	m.updateGrab()
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.hotkeys, h)
		m.updateGrab()
		m.mu.Unlock()
	}, nil
}

// onDown проверяет горячие клавиши при нажатии code на устройстве device (под блокировкой).
// Возвращает true, если нажатие нужно «съесть». Срабатывания добавляются в fires.
func (m *Module) onDown(device string, code uint16, fires *[]func()) bool {
	drop := false
	for h := range m.hotkeys {
		// Основная клавиша, устройство и остальные клавиши сочетания.
		main := h.chord[len(h.chord)-1]
		if !main.Matches(code) || (h.device != "" && !m.deviceOK(device, h.device)) || !m.chordHeld(h) {
			continue
		}
		if h.consume {
			drop = true
		}

		// Действие по режиму срабатывания.
		switch h.on {
		case onPress:
			*fires = append(*fires, m.fireFunc(h, device, nil))
		case onToggle:
			h.toggled = !h.toggled
			state := h.toggled
			*fires = append(*fires, m.fireFunc(h, device, &state))
		case onDouble:
			now := time.Now()
			if !h.last.IsZero() && now.Sub(h.last) <= h.double {
				h.last = time.Time{}
				*fires = append(*fires, m.fireFunc(h, device, nil))
			} else {
				h.last = now
			}
		case onHold:
			h.pressSeq++
			seq := h.pressSeq
			time.AfterFunc(h.hold, func() { m.holdElapsed(h, device, seq) })
		case onRelease:
			h.pending = true
		}
	}
	return drop
}

// onUp обрабатывает отпускание code: срабатывание on: release и отмена ожидания on: hold (под блокировкой).
func (m *Module) onUp(device string, code uint16, fires *[]func()) {
	for h := range m.hotkeys {
		main := h.chord[len(h.chord)-1]
		if !main.Matches(code) {
			continue
		}
		switch h.on {
		case onRelease:
			if h.pending {
				h.pending = false
				*fires = append(*fires, m.fireFunc(h, device, nil))
			}
		case onHold:
			h.pressSeq++ // отпустили раньше срока — таймер уже не сработает
		}
	}
}

// holdElapsed вызывается по таймеру удержания: срабатывает, если это нажатие ещё продолжается.
func (m *Module) holdElapsed(h *hotkey, device string, seq uint64) {
	m.mu.Lock()
	_, armed := m.hotkeys[h]
	ok := armed && h.pressSeq == seq && m.anyDown(h.chord[len(h.chord)-1])
	var f func()
	if ok {
		f = m.fireFunc(h, device, nil)
	}
	m.mu.Unlock()
	if f != nil {
		f()
	}
}

// chordHeld проверяет, что все клавиши сочетания, кроме основной, зажаты,
// а лишних модификаторов нет (под блокировкой).
func (m *Module) chordHeld(h *hotkey) bool {
	// Все клавиши сочетания, кроме основной, зажаты.
	others := h.chord[:len(h.chord)-1]
	for _, k := range others {
		if !m.anyDown(k) {
			return false
		}
	}

	// Каждый зажатый модификатор входит в сочетание.
	for _, codes := range m.down {
		for c := range codes {
			if !isModifier(c) {
				continue
			}
			covered := false
			for _, k := range h.chord {
				covered = covered || k.Matches(c)
			}
			if !covered {
				return false
			}
		}
	}
	return true
}

// deviceOK сообщает, подходит ли устройство под фильтр горячей клавиши.
func (m *Module) deviceOK(device, filter string) bool {
	for _, d := range m.input.Devices() {
		if d.Info.Path == device {
			return deviceMatches(d.Info, filter)
		}
	}
	return false
}

// fireFunc готовит срабатывание: сведения об удержании и управление модификаторами сочетания.
func (m *Module) fireFunc(h *hotkey, device string, toggle *bool) func() {
	main := h.chord[len(h.chord)-1]
	f := contracts.Fire{
		Toggle: toggle,
		Held: func() bool {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.anyDown(main)
		},
		Modifiers: m.newModControl(h.chord[:len(h.chord)-1]),
		Vars:      map[string]any{"device": device},
	}
	return func() { h.fire(f) }
}

// modControl временно отпускает для приложений модификаторы сочетания, которые держит пользователь (FR-HK-3).
type modControl struct {
	m    *Module
	mods []keys.Key
	// released — что было отпущено: устройство → коды (для Restore).
	released map[string][]uint16
}

// newModControl создаёт управление модификаторами из клавиш сочетания (кроме основной).
func (m *Module) newModControl(chord []keys.Key) *modControl {
	var mods []keys.Key
	for _, k := range chord {
		if isModifier(k.Code) {
			mods = append(mods, k)
		}
	}
	return &modControl{m: m, mods: mods, released: map[string][]uint16{}}
}

// Release отпускает зажатые пользователем модификаторы через passthrough-копии их устройств.
// На незахваченных устройствах это невозможно — такие модификаторы остаются зажатыми.
func (c *modControl) Release() {
	// Какие модификаторы сочетания сейчас зажаты и на каких устройствах.
	c.m.mu.Lock()
	for device, codes := range c.m.down {
		for code := range codes {
			for _, k := range c.mods {
				if k.Matches(code) {
					c.released[device] = append(c.released[device], code)
				}
			}
		}
	}
	c.m.mu.Unlock()

	// Отпускаем их для приложений.
	for device, codes := range c.released {
		events := make([]ev.Event, 0, len(codes))
		for _, code := range codes {
			events = append(events, ev.Event{Type: ev.EvKey, Code: code, Value: ev.ValueUp})
		}
		if err := c.m.input.Inject(device, events...); err != nil && !errors.Is(err, contracts.ErrNotGrabbed) {
			c.m.log.Warn("release modifiers", "device", device, "err", err)
		}
	}
}

// Restore снова зажимает для приложений модификаторы, которые пользователь всё ещё держит.
func (c *modControl) Restore() {
	for device, codes := range c.released {
		var events []ev.Event
		c.m.mu.Lock()
		for _, code := range codes {
			if c.m.down[device][code] {
				events = append(events, ev.Event{Type: ev.EvKey, Code: code, Value: ev.ValueDown})
			}
		}
		c.m.mu.Unlock()
		if len(events) > 0 {
			if err := c.m.input.Inject(device, events...); err != nil && !errors.Is(err, contracts.ErrNotGrabbed) {
				c.m.log.Warn("restore modifiers", "device", device, "err", err)
			}
		}
	}
	clear(c.released)
}

// parseChord разбирает сочетание записью зажатием: "^{Ctrl}^{Alt}{H}" (зажатые клавиши и последняя
// нажатая) или "{F8}" (одна клавиша).
func parseChord(s string) ([]keys.Key, error) {
	refs, err := dsl.ParseHotkey(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	return refsToKeys(refs)
}

// parseSequence разбирает последовательность "{G}{G}": несколько одиночных клавиш.
func parseSequence(s string) ([]keys.Key, error) {
	nodes, err := dsl.Parse(s)
	if err != nil {
		return nil, err
	}
	var out []keys.Key
	for _, n := range nodes {
		if n.Kind != dsl.KindTap || len(n.Keys) != 1 || n.Repeat > 0 || n.HoldMS > 0 {
			return nil, fmt.Errorf("a sequence is a list of single keys, e.g. {G}{G}")
		}
		k, err := refsToKeys(n.Keys)
		if err != nil {
			return nil, err
		}
		out = append(out, k...)
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("a sequence needs at least two keys")
	}
	return out, nil
}

// refsToKeys переводит ссылки DSL в клавиши (префиксы устройств появятся в фазе 7).
func refsToKeys(refs []dsl.KeyRef) ([]keys.Key, error) {
	out := make([]keys.Key, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.Device != "":
			return nil, fmt.Errorf("device prefixes (%s.) are not supported in hotkeys yet", r.Device)
		case r.Code != nil:
			out = append(out, keys.Key{Name: fmt.Sprintf("#%d", *r.Code), Type: ev.EvKey, Code: *r.Code})
		default:
			k, ok := keys.Lookup(r.Name)
			if !ok {
				return nil, fmt.Errorf("unknown key %q", r.Name)
			}
			out = append(out, k)
		}
	}
	return out, nil
}

// ms переводит миллисекунды из параметров в time.Duration со значением по умолчанию.
func ms(v, def int) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(v) * time.Millisecond
}
