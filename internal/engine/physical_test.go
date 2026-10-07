package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/clock"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
)

// physInspector — инспектор с одним подключённым устройством «Sega» (авто-ID UnKey2): кнопка Start —
// BTN_START, кнопка 001 — BTN_TRIGGER_HAPPY1; устройство UnKey3 известно, но не подключено.
type physInspector struct{ contracts.Inspector }

// Devices возвращает подключённое устройство.
func (physInspector) Devices() []contracts.DeviceDetails {
	return []contracts.DeviceDetails{{
		InputDevice: contracts.InputDevice{Info: ev.Info{Path: "/dev/input/event9", Name: "Sega pad"}},
		AutoID:      "UnKey2", DeviceName: "Sega",
	}}
}

// ResolveKey знает кнопки Start и 001 устройств Sega/UnKey2 и UnKey3.
func (physInspector) ResolveKey(device, button string) (contracts.DeviceKey, error) {
	code := map[string]uint16{"start": ev.BtnStart, "001": ev.BtnTriggerHappy1}[strings.ToLower(button)]
	if code == 0 {
		return contracts.DeviceKey{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", device, "button", button)
	}
	return contracts.DeviceKey{Key: keys.Key{Name: device + "." + button, Code: code}, Device: device}, nil
}

// physOutput — модуль ввода: копия устройства для нажатий по пути.
type physOutput struct{ dev *fakeDevice }

// DeviceOutput отдаёт копию только устройства event9.
func (p physOutput) DeviceOutput(path string) (contracts.VirtualDevice, error) {
	if path != "/dev/input/event9" {
		return nil, contracts.ErrDeviceGone
	}
	return p.dev, nil
}

// TestPhysicalButtons: кнопки, которых нет у клавиатуры и мыши mKey, нажимаются на копии самого
// устройства (по имени и по авто-ID); зажатое отпускается в конце макроса; неподключённое
// устройство — ErrDeviceGone; без модуля ввода — прежняя ошибка «не умею нажимать».
func TestPhysicalButtons(t *testing.T) {
	t.Parallel()
	m, devs := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	pad := newFakeDevice("Sega pad")
	m.inspect, m.devOut = physInspector{}, physOutput{dev: pad}

	// Нажатие по имени, зажатие по авто-ID без отпускания — отпустится в конце.
	if err := m.Run(context.Background(), `{Sega.Start}^{unkey2.001}`); err != nil {
		t.Fatal(err)
	}
	want := []ev.Event{
		{Type: ev.EvKey, Code: ev.BtnStart, Value: 1}, {Type: ev.EvKey, Code: ev.BtnStart, Value: 0},
		{Type: ev.EvKey, Code: ev.BtnTriggerHappy1, Value: 1}, {Type: ev.EvKey, Code: ev.BtnTriggerHappy1, Value: 0},
	}
	if got := pad.log(); len(got) != len(want) || !equalEvents(got, want) {
		t.Fatalf("pad events = %v", got)
	}
	if len(devs.kb.log()) != 0 {
		t.Fatalf("keyboard used: %v", devs.kb.log())
	}

	// Устройство не подключено.
	if err := m.Run(context.Background(), `{UnKey3.001}`); !errors.Is(err, contracts.ErrDeviceGone) {
		t.Fatalf("unconnected = %v", err)
	}

	// Без модуля ввода — понятная ошибка разбора.
	m.devOut = nil
	var de *dsl.Error
	if err := m.Run(context.Background(), `{Sega.Start}`); !errors.As(err, &de) || de.Code != dsl.ErrCannotSend {
		t.Fatalf("without input = %v", err)
	}
}

// equalEvents сравнивает события без учёта времени.
func equalEvents(a, b []ev.Event) bool {
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Code != b[i].Code || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}
