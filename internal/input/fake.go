package input

import (
	"errors"
	"os"

	"github.com/khameleonium/mKey/internal/lib/clock"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// Режим без настоящих устройств (`mkey daemon --fake-backends`, T12.11): модуль следит за пустой
// временной папкой вместо /dev/input, поэтому не читает и не захватывает ни одного настоящего
// устройства; копии устройств существуют только в памяти. Нужен для проверок окна и e2e-тестов.

// errFakeDevice — в режиме без устройств ничего не открывается.
var errFakeDevice = errors.New("fake backends: no real input devices")

// memClone — копия устройства в памяти: события никуда не уходят.
type memClone struct{}

// Write ничего не отправляет.
func (memClone) Write(...ev.Event) error { return nil }

// Close ничего не делает.
func (memClone) Close() error { return nil }

// NewFake создаёт модуль без настоящих устройств: каталог устройств — пустая временная папка
// (создаётся в Init, удаляется в Stop), открыть устройство нельзя, копии — в памяти.
func NewFake() *Module {
	m := newModule("", func(string) (deviceReader, error) { return nil, errFakeDevice }, clock.Real{})
	m.createClone = func(ev.Setup) (cloneWriter, error) { return memClone{}, nil }
	m.sysInfo = func(string) (string, string) { return "", "" }
	m.fake = true
	return m
}

// fakeDir создаёт пустую временную папку вместо /dev/input (режим без устройств).
func (m *Module) fakeDir() error {
	dir, err := os.MkdirTemp("", "mkey-fake-input-")
	if err != nil {
		return err
	}
	m.cfg.Dir = dir
	m.log.Warn("fake input: no real input devices are read (--fake-backends)", "dir", dir)
	return nil
}
