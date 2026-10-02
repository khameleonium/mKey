package engine

import (
	"fmt"

	"mkey/internal/contracts"
)

// Интервалы нажатий по умолчанию (contracts.TimingSettings): меняются из окна («Настройки» →
// «Нажатия mKey») без перезапуска. Раннер читает их через timing() под tmu, поэтому смена во время
// выполнения макроса безопасна: следующие нажатия идут уже с новыми интервалами.

// Timing возвращает текущие интервалы нажатий.
func (m *Module) Timing() contracts.Timing {
	m.tmu.RLock()
	defer m.tmu.RUnlock()
	return contracts.Timing{KeyHoldMS: m.cfg.KeyHoldMS, KeyDelayMS: m.cfg.KeyDelayMS, LayoutSwitchMS: m.cfg.LayoutSwitchMS}
}

// SetTiming проверяет и меняет интервалы нажатий; ErrBadTiming — значение вне пределов.
func (m *Module) SetTiming(t contracts.Timing) error {
	// Каждое значение — в своих пределах.
	for _, v := range []struct {
		name     string
		val, max int
	}{
		{"key_hold_ms", t.KeyHoldMS, contracts.MaxKeyHoldMS},
		{"key_delay_ms", t.KeyDelayMS, contracts.MaxKeyDelayMS},
		{"layout_switch_ms", t.LayoutSwitchMS, contracts.MaxLayoutSwitchMS},
	} {
		if v.val < 0 || v.val > v.max {
			return fmt.Errorf("%w: %s: must be from 0 to %d", contracts.ErrBadTiming, v.name, v.max)
		}
	}

	// Применяем.
	m.tmu.Lock()
	defer m.tmu.Unlock()
	m.cfg.KeyHoldMS, m.cfg.KeyDelayMS, m.cfg.LayoutSwitchMS = t.KeyHoldMS, t.KeyDelayMS, t.LayoutSwitchMS
	return nil
}

var _ contracts.TimingSettings = (*Module)(nil)
