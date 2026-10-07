package hotkeys

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// historySize — сколько последних нажатий хранить для распознавания последовательностей.
const historySize = 32

// sequenceParams — параметры триггера sequence.
type sequenceParams struct {
	// Keys — последовательность одиночных клавиш, например "{G}{G}".
	Keys string `json:"keys"`
	// WithinMS — за сколько миллисекунд нужно успеть нажать всю последовательность (по умолчанию 1000).
	WithinMS int `json:"within_ms"`
}

// sequence — зарегистрированная последовательность.
type sequence struct {
	keys   []contracts.DeviceKey
	within time.Duration
	fire   func(contracts.Fire)
}

// sequenceType — вид триггера sequence.
type sequenceType struct{ m *Module }

// Meta возвращает метаданные вида триггера.
func (sequenceType) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "sequence", NameKey: "trigger.sequence", DescriptionKey: "trigger.sequence.description",
		Category: "input", Icon: "keyboard", Provider: ModuleID,
		ParamsSchema: []byte(`{"type":"object","required":["keys"],"properties":{"keys":{"type":"string","x-widget":"keys"},` +
			`"within_ms":{"type":"integer","minimum":1,"x-widget":"ms","x-advanced":true}}}`),
	}
}

// parseSequenceParams разбирает и проверяет параметры триггера sequence.
func (m *Module) parseSequenceParams(tr project.Trigger) (sequenceParams, []contracts.DeviceKey, error) {
	var p sequenceParams
	if err := project.Decode(tr.Params, &p); err != nil {
		return p, nil, fmt.Errorf("sequence: %w", err)
	}
	if strings.Trim(p.Keys, "{} ") == "" {
		return p, nil, project.Required("trigger", "sequence", "keys")
	}
	ks, err := m.parseSequence(p.Keys)
	if err != nil {
		return p, nil, fmt.Errorf("sequence: %w", err)
	}
	return p, ks, nil
}

// Validate проверяет параметры, ничего не регистрируя.
func (t sequenceType) Validate(tr project.Trigger) error {
	_, _, err := t.m.parseSequenceParams(tr)
	return err
}

// Arm проверяет параметры и регистрирует последовательность.
func (t sequenceType) Arm(_ context.Context, _ contracts.EventRef, tr project.Trigger, fire func(contracts.Fire)) (func(), error) {
	p, ks, err := t.m.parseSequenceParams(tr)
	if err != nil {
		return nil, err
	}
	s := &sequence{keys: ks, within: ms(p.WithinMS, 1000), fire: fire}

	// Регистрация.
	m := t.m
	m.mu.Lock()
	m.sequences[s] = struct{}{}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.sequences, s)
		m.mu.Unlock()
	}, nil
}

// feedSequence добавляет нажатие code на устройстве device в историю и проверяет
// последовательности (под блокировкой).
func (m *Module) feedSequence(device string, code uint16, fires *[]func()) {
	// Модификаторы в последовательности не участвуют.
	if isModifier(code) {
		return
	}
	now := time.Now()
	m.history = append(m.history, press{device: device, code: code, at: now})
	if len(m.history) > historySize {
		m.history = m.history[len(m.history)-historySize:]
	}

	// Хвост истории совпадает с последовательностью и уложился во время.
	for s := range m.sequences {
		n := len(s.keys)
		if len(m.history) < n {
			continue
		}
		tail := m.history[len(m.history)-n:]
		ok := now.Sub(tail[0].at) <= s.within
		for i, k := range s.keys {
			ok = ok && m.matches(k, tail[i].device, tail[i].code)
		}
		if ok {
			f := s.fire
			*fires = append(*fires, func() { f(contracts.Fire{}) })
			m.history = nil // одна последовательность — одно срабатывание
			return
		}
	}
}
