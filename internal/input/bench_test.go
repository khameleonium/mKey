package input

import (
	"testing"
	"time"

	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
)

// nopClone — passthrough-копия, которая ничего не делает (измеряем только путь mKey).
type nopClone struct{}

func (nopClone) Write(...ev.Event) error { return nil }
func (nopClone) Close() error            { return nil }

// BenchmarkPassthrough измеряет задержку, которую mKey добавляет к нажатию на захваченной клавиатуре:
// экстренная проверка, синхронный обработчик горячих клавиш и запись в копию (NFR-1: медиана < 1 мс).
func BenchmarkPassthrough(b *testing.B) {
	// Модуль с одним захваченным устройством и обработчиком, как у горячих клавиш.
	m := newModule(b.TempDir(), nil, clock.Real{})
	codes, _ := emergencyCodes(m.cfg.EmergencyStop)
	m.emergency = codes
	m.SetHandler(dropF8{})
	d := &openDevice{down: map[uint16]bool{}, clone: &passthrough{w: nopClone{}, held: map[uint16]bool{}}}
	d.grabbed.Store(true)
	batch := []ev.Event{{Type: ev.EvKey, Code: ev.KeyA, Value: 1}, ev.Sync()}

	// Пачка «нажатие + SYN», как у настоящей клавиатуры.
	b.ReportAllocs()
	for b.Loop() {
		m.process("/dev/input/event0", d, batch)
	}
}

// TestPassthroughLatency проверяет NFR-1 напрямую: средняя обработка пачки заметно меньше 1 мс.
func TestPassthroughLatency(t *testing.T) {
	res := testing.Benchmark(BenchmarkPassthrough)
	per := time.Duration(res.NsPerOp())
	t.Logf("passthrough: %v per key event, %d allocs", per, res.AllocsPerOp())
	if per > 100*time.Microsecond {
		t.Fatalf("passthrough too slow: %v per event (NFR-1 requires < 1 ms)", per)
	}
}
