//go:build integration

package output

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/project"
)

// TestTemplatesInKernel создаёт виртуальные устройства по шаблонам через настоящий /dev/uinput и
// проверяет, каким их видит система: имя, VID:PID, кнопки, оси, вибрация. Для геймпадов ещё
// нажимается кнопка и читается из устройства. Безопасно для живой сессии: рабочий стол (libinput)
// не обрабатывает геймпады и джойстики; тач-экран только создаётся, касаний нет; клавиатура и мышь
// не создаются.
func TestTemplatesInKernel(t *testing.T) {
	m := newModule(func(s ev.Setup) (eventWriter, error) { return ev.CreateUInput(ev.DefaultUInputPath, s) }, clock.Real{})
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.cfg.SettleMS = 0

	for _, c := range []struct {
		template string
		vid, pid uint16
		ff       bool
	}{{"xbox360", 0x045e, 0x028e, true}, {"ds4", 0x054c, 0x09cc, true}, {"joystick", vendorMKey, 0x0010, false}, {"touchscreen", vendorMKey, 0x0011, false}} {
		t.Run(c.template, func(t *testing.T) {
			// Создание устройства ядром.
			d, err := m.createVirtual(wanted{spec: project.VirtualDevice{Name: "it" + c.template, Template: c.template}, project: "it"})
			if err != nil {
				t.Skipf("uinput unavailable: %v", err)
			}
			defer func() { _ = d.close() }()
			if d.node == "" {
				t.Fatal("no event node")
			}

			// Каким его видит система.
			time.Sleep(200 * time.Millisecond) // udev выдаёт права на новый eventN не сразу
			dev, err := ev.Open(d.node)
			if err != nil {
				t.Skipf("cannot open %s: %v", d.node, err)
			}
			defer func() { _ = dev.Close() }()
			info := dev.Info()
			if info.Name != "mKey it"+c.template || info.ID.Vendor != c.vid || info.ID.Product != c.pid {
				t.Errorf("info = %s %s", info.Name, info.ID)
			}
			if len(info.Caps.Codes[ev.EvKey]) != len(d.setup.Keys) || len(info.Caps.Abs) != len(d.setup.Abs) {
				t.Errorf("caps: keys %d/%d abs %d/%d", len(info.Caps.Codes[ev.EvKey]), len(d.setup.Keys), len(info.Caps.Abs), len(d.setup.Abs))
			}
			if c.ff != info.Caps.Has(ev.EvFf, ffRumble) {
				t.Errorf("FF_RUMBLE announced = %v", !c.ff)
			}
			if c.template == "touchscreen" {
				if !slices.Contains(info.Caps.Props, ev.InputPropDirect) {
					t.Error("touchscreen without INPUT_PROP_DIRECT")
				}
				return
			}

			// Нажатие доходит до устройства (у Xbox — с заменой кода, как у xpad).
			btn, want := uint16(ev.BtnSouth), uint16(ev.BtnSouth)
			if c.template == "joystick" {
				btn, want = ev.BtnTrigger, ev.BtnTrigger
			}
			if err := d.Press(context.Background(), btn); err != nil {
				t.Fatal(err)
			}
			got := make(chan ev.Event, 1)
			go func() {
				buf := make([]ev.Event, 16)
				for {
					events, err := dev.ReadEvents(buf)
					if err != nil {
						return
					}
					for _, e := range events {
						if e.Type == ev.EvKey {
							got <- e
							return
						}
					}
				}
			}()
			select {
			case e := <-got:
				if e.Code != want || e.Value != 1 {
					t.Errorf("read %v", e)
				}
			case <-time.After(2 * time.Second):
				t.Error("press not delivered")
			}
			_ = d.ReleaseAll()
		})
	}
}
