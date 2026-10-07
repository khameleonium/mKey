package recorder

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// fakeClones — менеджер виртуальных устройств, создающий копии записанных устройств.
type fakeClones struct {
	contracts.VirtualDeviceManager
	mu     sync.Mutex
	made   map[string]*fakeDevice
	setups map[string]ev.Setup
	closed int
}

// Clone создаёт копию и запоминает её возможности.
func (f *fakeClones) Clone(name string, s ev.Setup) (contracts.VirtualDevice, func() error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &fakeDevice{}
	f.made[name], f.setups[name] = d, s
	return d, func() error { f.mu.Lock(); f.closed++; f.mu.Unlock(); return nil }, nil
}

// TestPlayGamepadClone проверяет повтор геймпада: по строке caps создаётся копия с теми же
// кнопками и осями, её события идут на копию (не на клавиатуру), в конце копия уничтожается;
// устройство без caps (старая запись) пропускается.
func TestPlayGamepadClone(t *testing.T) {
	t.Parallel()
	m, _, devs := newTestModule(t)
	clones := &fakeClones{made: map[string]*fakeDevice{}, setups: map[string]ev.Setup{}}
	m.vdm = clones
	writeRecording(t, m.cfg.Dir, "pad", "device 2 gamepad \"Pad\"\n"+
		"caps 2 id=0003:045e:028e:0110 keys=BTN_SOUTH abs=ABS_X:-32768:32767:16:128:0\n"+
		"device 3 joystick \"Old\"\n"+
		"0.000 0 ^{A}\n0.010 0 ~{A}\n"+
		"0.020 2 ^{#304} ev 3 0 20000\n0.030 2 ~{#304}\n"+
		"0.040 3 ^{#288}\n0.050 end\n")
	if err := m.Play(context.Background(), "pad", contracts.PlayOptions{}); err != nil {
		t.Fatal(err)
	}

	// Копия с теми же возможностями; её события — кнопка и ось.
	pad := clones.made["Pad"]
	s := clones.setups["Pad"]
	if pad == nil || s.ID.Vendor != 0x045e || len(s.Keys) != 1 || s.Abs[ev.AbsX].Maximum != 32767 {
		t.Fatalf("clone = %+v %+v", pad, s)
	}
	var got []string
	for _, e := range pad.events {
		got = append(got, e.String())
	}
	if len(pad.events) != 3 || pad.events[1].Type != ev.EvAbs || pad.events[1].Value != 20000 {
		t.Fatalf("pad events: %s", strings.Join(got, "; "))
	}

	// Клавиатура получила только своё; старое устройство без caps не копировалось; копия убрана.
	if len(devs.kb.events) != 2 || clones.made["Old"] != nil || clones.closed != 1 {
		t.Fatalf("kb %d, old %v, closed %d", len(devs.kb.events), clones.made["Old"], clones.closed)
	}
}

// TestFixedPause проверяет «фиксированную паузу»: действия идут через равные промежутки.
func TestFixedPause(t *testing.T) {
	t.Parallel()
	m, clk, _ := newTestModule(t)
	writeRecording(t, m.cfg.Dir, "p", "0.000 0 ^{A}\n0.900 0 ~{A}\n5.000 0 ^{B}\n9.000 end\n")
	start := clk.Now()
	if err := m.Play(context.Background(), "p", contracts.PlayOptions{FixedPauseMS: 100}); err != nil {
		t.Fatal(err)
	}
	if took := clk.Now().Sub(start); took.Milliseconds() != 300 {
		t.Fatalf("played for %s, want 300ms", took)
	}
	if err := m.Play(context.Background(), "p", contracts.PlayOptions{FixedPauseMS: -1}); err == nil {
		t.Fatal("negative pause accepted")
	}
}
