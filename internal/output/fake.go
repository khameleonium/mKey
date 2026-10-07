package output

import (
	"sync/atomic"

	"github.com/khameleonium/mKey/internal/lib/clock"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// Режим без настоящих устройств (`mkey daemon --fake-backends`, T12.11): виртуальные клавиатура,
// мышь, геймпады и копии устройств существуют только в памяти — события никуда не уходят. Нужен
// для проверок окна и автотестов (e2e) на машине без /dev/uinput и без риска нажать что-то
// в живой сессии (AGENTS.md §5).

// memWriter — устройство в памяти: считает пакеты событий и ничего не отправляет.
type memWriter struct {
	packets atomic.Int64
}

// Write учитывает пакет событий.
func (w *memWriter) Write(...ev.Event) error {
	w.packets.Add(1)
	return nil
}

// Close ничего не делает.
func (w *memWriter) Close() error { return nil }

// NewFake создаёт модуль, чьи устройства существуют только в памяти (без /dev/uinput).
func NewFake() *Module {
	m := newModule(func(ev.Setup) (eventWriter, error) { return &memWriter{}, nil }, clock.Real{})
	m.cfg.SettleMS = 0
	m.fake = true
	return m
}
