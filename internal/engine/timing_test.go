package engine

import (
	"errors"
	"testing"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
)

// TestTiming проверяет интервалы нажатий: чтение, смена, отказ вне пределов (прежние остаются).
func TestTiming(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	if got := m.Timing(); got.KeyHoldMS != 20 || got.KeyDelayMS != 10 || got.LayoutSwitchMS != 60 {
		t.Fatalf("defaults = %+v", got)
	}

	// Верные значения применяются.
	want := contracts.Timing{KeyHoldMS: 0, KeyDelayMS: 1000, LayoutSwitchMS: 5000}
	if err := m.SetTiming(want); err != nil || m.Timing() != want {
		t.Fatalf("set: %v, got %+v", err, m.Timing())
	}

	// Вне пределов — ErrBadTiming, значения прежние.
	for _, bad := range []contracts.Timing{{KeyHoldMS: -1}, {KeyDelayMS: 1001}, {LayoutSwitchMS: 5001}} {
		if err := m.SetTiming(bad); !errors.Is(err, contracts.ErrBadTiming) {
			t.Errorf("SetTiming(%+v) = %v", bad, err)
		}
	}
	if m.Timing() != want {
		t.Fatalf("changed by a bad call: %+v", m.Timing())
	}
}
