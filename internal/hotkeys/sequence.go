package hotkeys

import (
	"context"
	"fmt"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
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
	keys   []keys.Key
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
		ParamsSchema: []byte(`{"type":"object","required":["keys"],"properties":{"keys":{"type":"string"},"within_ms":{"type":"integer"}}}`),
	}
}

// Arm проверяет параметры и регистрирует последовательность.
func (t sequenceType) Arm(_ context.Context, _ contracts.EventRef, tr project.Trigger, fire func(contracts.Fire)) (func(), error) {
	var p sequenceParams
	if err := project.Decode(tr.Params, &p); err != nil {
		return nil, fmt.Errorf("sequence: %w", err)
	}
	ks, err := parseSequence(p.Keys)
	if err != nil {
		return nil, fmt.Errorf("sequence: %w", err)
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

// feedSequence добавляет нажатие в историю и проверяет последовательности (под блокировкой).
func (m *Module) feedSequence(code uint16, fires *[]func()) {
	// Модификаторы в последовательности не участвуют.
	if isModifier(code) {
		return
	}
	now := time.Now()
	m.history = append(m.history, press{code: code, at: now})
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
			ok = ok && k.Matches(tail[i].code)
		}
		if ok {
			f := s.fire
			*fires = append(*fires, func() { f(contracts.Fire{}) })
			m.history = nil // одна последовательность — одно срабатывание
			return
		}
	}
}
