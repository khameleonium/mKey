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

// TestTouchInKernel проверяет касания настоящего виртуального сенсорного экрана: касание, движение,
// отрыв (номер касания −1) и что «отпустить всё» без касания не создаёт касания. Безопасно для
// живой сессии: экран сразу захватывается тестом (EVIOCGRAB) — события получает только тест,
// рабочий стол касаний не видит.
func TestTouchInKernel(t *testing.T) {
	m := newModule(func(s ev.Setup) (eventWriter, error) { return ev.CreateUInput(ev.DefaultUInputPath, s) }, clock.Real{})
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.cfg.SettleMS = 0

	// Экран, открытый и захваченный тестом до первого касания.
	d, err := m.createVirtual(wanted{spec: project.VirtualDevice{Name: "ittouch", Template: "touchscreen"}, project: "it"})
	if err != nil {
		t.Skipf("uinput unavailable: %v", err)
	}
	defer func() { _ = d.close() }()
	time.Sleep(200 * time.Millisecond)
	dev, err := ev.Open(d.node)
	if err != nil {
		t.Skipf("cannot open %s: %v", d.node, err)
	}
	defer func() { _ = dev.Close() }()
	if err := dev.Grab(); err != nil {
		t.Skipf("cannot grab %s: %v", d.node, err)
	}

	// Чтение событий в фоне.
	events := make(chan ev.Event, 64)
	go func() {
		buf := make([]ev.Event, 16)
		for {
			got, err := dev.ReadEvents(buf)
			if err != nil {
				return
			}
			for _, e := range got {
				events <- e
			}
		}
	}()
	collect := func() []ev.Event {
		var out []ev.Event
		for {
			select {
			case e := <-events:
				out = append(out, e)
			case <-time.After(100 * time.Millisecond):
				return out
			}
		}
	}
	find := func(list []ev.Event, typ, code uint16) (int32, bool) {
		for _, e := range list {
			if e.Type == typ && e.Code == code {
				return e.Value, true
			}
		}
		return 0, false
	}
	ctx := context.Background()

	// «Отпустить всё» без касания — ни одного события касания (раньше здесь возникало фантомное).
	if err := d.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(collect(), ev.EvAbs, ev.AbsMtTrackingId); ok {
		t.Fatal("ReleaseAll without a touch sent a tracking id")
	}

	// Касание в середине: номер касания ≥ 0, BTN_TOUCH 1, X — середина диапазона.
	if err := d.TouchDown(ctx, 0.5, 0.25); err != nil {
		t.Fatal(err)
	}
	got := collect()
	if id, ok := find(got, ev.EvAbs, ev.AbsMtTrackingId); !ok || id < 0 {
		t.Fatalf("touch down: %v", got)
	}
	if v, _ := find(got, ev.EvKey, ev.BtnTouch); v != 1 {
		t.Fatalf("BTN_TOUCH = %d", v)
	}
	if x, _ := find(got, ev.EvAbs, ev.AbsMtPositionX); x < 16300 || x > 16500 {
		t.Fatalf("x = %d", x)
	}

	// Движение и отрыв: номер касания −1, BTN_TOUCH 0; повторный отрыв — без событий.
	if err := d.TouchMove(ctx, 0.6, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := d.TouchUp(ctx); err != nil {
		t.Fatal(err)
	}
	got = collect()
	if id, ok := find(got, ev.EvAbs, ev.AbsMtTrackingId); !ok || id != -1 {
		t.Fatalf("touch up: %v", got)
	}
	if err := d.TouchUp(ctx); err != nil {
		t.Fatal(err)
	}
	if extra := collect(); len(extra) != 0 {
		t.Fatalf("second TouchUp sent %v", extra)
	}
}
