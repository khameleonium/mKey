package input

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// keyLog — нажатия и отпускания копии строкой «код:значение» по порядку.
func keyLog(c *fakeClone) []ev.Event {
	if c == nil {
		return nil
	}
	return c.keys()
}

// TestDeviceOutputCopy проверяет нажатия «от имени» незахваченного устройства: копия создаётся при
// первом нажатии (и ждёт settle_ms), нажатия и отпускания идут в неё, ReleaseAll отпускает зажатое;
// экстренная остановка уничтожает копию, отпустив кнопки, и следующее нажатие создаёт новую;
// отключённое устройство — ErrDeviceGone.
func TestDeviceOutputCopy(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)
	ctx := context.Background()
	out, err := rig.mod.DeviceOutput(rig.path)
	if err != nil {
		t.Fatal(err)
	}

	// Первое нажатие создаёт копию и ждёт, пока её подхватит система.
	if err := out.Press(ctx, ev.BtnTrigger); err != nil {
		t.Fatal(err)
	}
	first := rig.clone()
	want := []ev.Event{{Type: ev.EvKey, Code: ev.BtnTrigger, Value: ev.ValueDown}}
	if got := keyLog(first); !slices.Equal(got, want) {
		t.Fatalf("copy keys = %v", got)
	}
	if sleeps := rig.mod.clk.(interface{ Sleeps() []time.Duration }).Sleeps(); !slices.Contains(sleeps, 300*time.Millisecond) {
		t.Errorf("no settle wait: %v", sleeps)
	}
	if h := out.Held(); !slices.Equal(h, []uint16{ev.BtnTrigger}) {
		t.Fatalf("held = %v", h)
	}

	// ReleaseAll отпускает; Tap — нажатие и отпускание в ту же копию.
	if err := out.ReleaseAll(); err != nil || len(out.Held()) != 0 {
		t.Fatalf("release all: %v, held %v", err, out.Held())
	}
	if err := out.Tap(ctx, ev.BtnThumb, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := keyLog(first); len(got) != 4 || got[3] != (ev.Event{Type: ev.EvKey, Code: ev.BtnThumb, Value: ev.ValueUp}) || rig.clone() != first {
		t.Fatalf("copy keys = %v", got)
	}

	// Экстренная остановка: зажатое отпущено, копия уничтожена; новое нажатие — новая копия.
	if err := out.Press(ctx, ev.BtnTop); err != nil {
		t.Fatal(err)
	}
	rig.mod.EmergencyStop("test")
	if got := keyLog(first); !first.isClosed() || got[len(got)-1] != (ev.Event{Type: ev.EvKey, Code: ev.BtnTop, Value: ev.ValueUp}) {
		t.Fatalf("after emergency: closed=%v keys=%v", first.isClosed(), got)
	}
	if err := out.Press(ctx, ev.BtnTop); err != nil || rig.clone() == first {
		t.Fatalf("new copy: %v", err)
	}

	// Отключённое устройство: копия уничтожена, нажатия — ErrDeviceGone.
	second := rig.clone()
	close(rig.kb.gone)
	eventually(t, "copy closed on removal", second.isClosed)
	if err := out.Press(ctx, ev.BtnTop); !errors.Is(err, contracts.ErrDeviceGone) {
		t.Fatalf("press on removed device = %v", err)
	}
	if _, err := rig.mod.DeviceOutput(rig.path); !errors.Is(err, contracts.ErrDeviceGone) {
		t.Fatalf("DeviceOutput(removed) = %v", err)
	}
}

// TestDeviceOutputGrabbed: у захваченного устройства нажатия идут в его passthrough-копию —
// вторая копия не создаётся.
func TestDeviceOutputGrabbed(t *testing.T) {
	t.Parallel()
	rig := newGrabRig(t)
	rig.grabKeyboards(t)
	pass := rig.clone()
	out, err := rig.mod.DeviceOutput(rig.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Press(context.Background(), ev.BtnTrigger); err != nil {
		t.Fatal(err)
	}
	if rig.clone() != pass || !slices.Contains(keyLog(pass), ev.Event{Type: ev.EvKey, Code: ev.BtnTrigger, Value: ev.ValueDown}) {
		t.Fatalf("press went elsewhere: %v", keyLog(pass))
	}
}
