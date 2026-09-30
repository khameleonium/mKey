package hotkeys

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/layout"
	"mkey/internal/lib/project"
)

// typedSize — сколько последних набранных символов хранить.
const typedSize = 64

// defaultEndChars — символы, завершающие слово для hotstring.
const defaultEndChars = " \n\t.,!?;:"

// hotstringParams — параметры триггера hotstring.
type hotstringParams struct {
	// Text — слово, при наборе которого срабатывает триггер, например "btw".
	Text string `json:"text"`
	// Replace — чем заменить набранное слово (необязательно); замена выполняется перед действиями события.
	Replace string `json:"replace"`
	// EndChars — символы, завершающие слово (по умолчанию пробел, Enter, Tab и знаки препинания).
	EndChars string `json:"end_chars"`
	// CaseSensitive — учитывать регистр (по умолчанию нет).
	CaseSensitive bool `json:"case_sensitive"`
}

// hotstring — зарегистрированное слово.
type hotstring struct {
	p    hotstringParams
	fire func(contracts.Fire)
}

// hotstringType — вид триггера hotstring.
type hotstringType struct{ m *Module }

// Meta возвращает метаданные вида триггера.
func (hotstringType) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "hotstring", NameKey: "trigger.hotstring", DescriptionKey: "trigger.hotstring.description",
		Category: "input", Icon: "text", Provider: ModuleID,
		ParamsSchema: []byte(`{"type":"object","required":["text"],"properties":{"text":{"type":"string"},` +
			`"replace":{"type":"string"},"end_chars":{"type":"string"},"case_sensitive":{"type":"boolean"}}}`),
	}
}

// Arm проверяет параметры и регистрирует слово.
func (t hotstringType) Arm(_ context.Context, _ contracts.EventRef, tr project.Trigger, fire func(contracts.Fire)) (func(), error) {
	var p hotstringParams
	if err := project.Decode(tr.Params, &p); err != nil {
		return nil, fmt.Errorf("hotstring: %w", err)
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, fmt.Errorf("hotstring: text is required")
	}
	if p.EndChars == "" {
		p.EndChars = defaultEndChars
	}
	h := &hotstring{p: p, fire: fire}

	m := t.m
	m.mu.Lock()
	m.hotstrings[h] = struct{}{}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.hotstrings, h)
		m.mu.Unlock()
	}, nil
}

// resetKeys — клавиши, после которых набранное слово забывается (курсор ушёл в другое место).
var resetKeys = map[uint16]bool{
	ev.KeyUp: true, ev.KeyDown: true, ev.KeyLeft: true, ev.KeyRight: true, ev.KeyHome: true, ev.KeyEnd: true,
	ev.KeyPageup: true, ev.KeyPagedown: true, ev.KeyEsc: true, ev.KeyDelete: true,
	ev.BtnLeft: true, ev.BtnRight: true, ev.BtnMiddle: true,
}

// feedHotstring добавляет набранный символ в буфер и проверяет hotstrings (под блокировкой).
func (m *Module) feedHotstring(code uint16, fires *[]func()) {
	// Без hotstrings буфер не нужен.
	if len(m.hotstrings) == 0 {
		return
	}

	// Служебные клавиши: забыть слово, стереть символ; с Ctrl/Alt/Super — не набор текста.
	switch {
	case resetKeys[code]:
		m.typed = nil
		return
	case code == ev.KeyBackspace:
		if len(m.typed) > 0 {
			m.typed = m.typed[:len(m.typed)-1]
		}
		return
	case m.anyDownCode(ev.KeyLeftctrl, ev.KeyRightctrl, ev.KeyLeftalt, ev.KeyRightalt, ev.KeyLeftmeta, ev.KeyRightmeta):
		m.typed = nil
		return
	}

	// Символ по текущей раскладке (Enter и Tab — тоже завершающие символы).
	var r rune
	switch code {
	case ev.KeyEnter, ev.KeyKpenter:
		r = '\n'
	case ev.KeyTab:
		r = '\t'
	default:
		l, ok := layout.Get(m.layout.Load().(string))
		if !ok {
			l, _ = layout.Get("us")
		}
		shift := m.anyDownCode(ev.KeyLeftshift, ev.KeyRightshift)
		c, ok := l.Char(code, shift)
		if !ok {
			return
		}
		r = c
	}

	// Проверяем hotstrings, если символ завершает слово; иначе копим буфер.
	for h := range m.hotstrings {
		if strings.ContainsRune(h.p.EndChars, r) && m.typedEndsWith(h.p.Text, h.p.CaseSensitive) {
			*fires = append(*fires, m.hotstringFire(h, r))
			m.typed = nil
			return
		}
	}
	m.typed = append(m.typed, r)
	if len(m.typed) > typedSize {
		m.typed = m.typed[len(m.typed)-typedSize:]
	}
}

// typedEndsWith сообщает, что набранный текст заканчивается словом text, перед которым граница слова.
func (m *Module) typedEndsWith(text string, caseSensitive bool) bool {
	word := []rune(text)
	if len(m.typed) < len(word) {
		return false
	}
	tail := string(m.typed[len(m.typed)-len(word):])
	if !caseSensitive {
		tail, text = strings.ToLower(tail), strings.ToLower(text)
	}
	if tail != text {
		return false
	}
	before := len(m.typed) - len(word) - 1
	return before < 0 || (!unicode.IsLetter(m.typed[before]) && !unicode.IsDigit(m.typed[before]))
}

// hotstringFire готовит срабатывание: набранное слово, завершающий символ и, если задана замена,
// макрос замены (стереть слово с завершающим символом и напечатать замену с ним же).
func (m *Module) hotstringFire(h *hotstring, end rune) func() {
	vars := map[string]any{"hotstring": h.p.Text, "end_char": string(end)}
	if h.p.Replace != "" {
		erase := len([]rune(h.p.Text)) + 1
		vars["replace_dsl"] = fmt.Sprintf("{Backspace*%d}", erase) + dsl.Format([]dsl.Node{{Kind: dsl.KindText, Text: h.p.Replace + string(end)}})
	}
	f := h.fire
	return func() { f(contracts.Fire{Vars: vars}) }
}

// anyDownCode сообщает, зажат ли хоть один из кодов на любом устройстве (под блокировкой).
func (m *Module) anyDownCode(codes ...uint16) bool {
	for _, c := range codes {
		if m.anyDown(keys.Key{Code: c}) {
			return true
		}
	}
	return false
}
