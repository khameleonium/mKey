//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/input"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/registry"
)

// dropHappy1 — обработчик, «съедающий» кнопку BTN_TRIGGER_HAPPY1 (как горячая клавиша с consume).
type dropHappy1 struct{}

func (dropHappy1) HandleInput(_ string, e *ev.Event, _ bool) bool {
	return e.Type == ev.EvKey && e.Code == ev.BtnTriggerHappy1
}

// TestGrabPassthroughReal проверяет настоящий захват (EVIOCGRAB) и passthrough на виртуальном
// джойстике: съеденная кнопка не доходит до системы, остальные приходят через копию.
// Джойстики композитор не обрабатывает, поэтому в окна ничего не попадает.
func TestGrabPassthroughReal(t *testing.T) {
	requireUinput(t)

	// «Физическое» устройство для теста (без префикса mKey, чтобы модуль input его читал).
	name := uniqueName("Integration Grab Joystick")
	src, err := ev.CreateUInput(ev.DefaultUInputPath, joystickSetup(name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	time.Sleep(300 * time.Millisecond)

	// Модуль input с обработчиком и политикой «захватить только тестовое устройство».
	cat, _ := i18n.LoadCatalog()
	mod := input.New()
	mgr, err := registry.NewManager(registry.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Translator: i18n.New(cat, "en"), Bus: bus.New(0),
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Stop(context.Background()) }()
	if mod.Status().Open == 0 {
		t.Skip("no readable input devices: run `mkey doctor --fix`")
	}
	mod.SetHandler(dropHappy1{})
	mod.SetGrabPolicy(func(d contracts.InputDevice) bool { return d.Info.Name == name })

	// Ждём появления passthrough-копии и открываем её, чтобы видеть, что получает система.
	var clone *ev.Device
	deadline := time.Now().Add(5 * time.Second)
	for clone == nil && time.Now().Before(deadline) {
		procs, _ := ev.ReadProcDevices()
		for _, p := range procs {
			if strings.HasPrefix(p.Name, contracts.VirtualNamePrefix+"passthrough: "+name[:20]) && p.EventPath() != "" {
				clone, _ = ev.Open(p.EventPath())
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if clone == nil {
		t.Fatal("passthrough device did not appear")
	}
	defer func() { _ = clone.Close() }()
	time.Sleep(300 * time.Millisecond) // захват включается после «прогрева» копии

	// Нажимаем съедаемую и обычную кнопки на «физическом» устройстве.
	press := func(code uint16) {
		_ = src.Write(ev.Event{Type: ev.EvKey, Code: code, Value: 1}, ev.Sync(), ev.Event{Type: ev.EvKey, Code: code, Value: 0}, ev.Sync())
	}
	press(ev.BtnTriggerHappy1)
	press(ev.BtnTriggerHappy2)

	// Копия получила только обычную кнопку.
	got := map[uint16]bool{}
	buf := make([]ev.Event, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !got[ev.BtnTriggerHappy2] {
			events, err := clone.ReadEvents(buf)
			if err != nil {
				return
			}
			for _, e := range events {
				if e.Type == ev.EvKey {
					got[e.Code] = true
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough event not received")
	}
	if got[ev.BtnTriggerHappy1] {
		t.Fatal("consumed button leaked to the system")
	}

	// Снятие политики снимает захват и удаляет копию.
	mod.SetGrabPolicy(nil)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		procs, _ := ev.ReadProcDevices()
		found := false
		for _, p := range procs {
			found = found || strings.Contains(p.Name, "passthrough: "+name[:20])
		}
		if !found {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("passthrough device not removed after ungrab")
}
