package recorder

import (
	"fmt"
	"slices"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// Настройки записи по умолчанию (contracts.RecordSettings): с ними запись начинается без
// вопросов — сочетанием, из меню значка, из окна и командой. Хранятся в config.yaml
// (modules.recorder), меняются в окне («Настройки» → «Запись») и действуют со следующей записи.

// defaultName возвращает имя записи по времени начала: mKeyRec_ДДММГГГГ_ЧЧММСС.
func defaultName(t time.Time) string {
	return "mKeyRec_" + t.Format("02012006_150405")
}

// knownKinds — классы устройств, которые можно записывать.
var knownKinds = []string{
	string(ev.KindKeyboard), string(ev.KindMouse), string(ev.KindTouchpad), string(ev.KindTouchscreen),
	string(ev.KindTablet), string(ev.KindGamepad), string(ev.KindJoystick), string(ev.KindOther),
}

// maxMergeMS — верхний предел окон склейки движений (больше секунды — путь курсора теряется).
const maxMergeMS = 1000

// checkSettings проверяет настройки записи. Ошибка — contracts.ErrBadRecordSettings с пояснением.
func checkSettings(s contracts.RecordSettings) error {
	// Классы устройств: хотя бы один, все известные.
	if len(s.Kinds) == 0 {
		return fmt.Errorf("%w: kinds: choose at least one kind of device", contracts.ErrBadRecordSettings)
	}
	for _, k := range s.Kinds {
		if !slices.Contains(knownKinds, k) {
			return fmt.Errorf("%w: kinds: unknown kind %q (known: %v)", contracts.ErrBadRecordSettings, k, knownKinds)
		}
	}

	// Окна склейки: от 0 до секунды.
	for _, v := range []struct {
		name string
		ms   int
	}{{"merge_moves_ms", s.MergeMovesMS}, {"coalesce_ms", s.CoalesceMS}} {
		if v.ms < 0 || v.ms > maxMergeMS {
			return fmt.Errorf("%w: %s: must be from 0 to %d", contracts.ErrBadRecordSettings, v.name, maxMergeMS)
		}
	}
	return nil
}

// settingsOf выделяет настройки записи из настроек модуля.
func (m *Module) settingsOf(c Config) contracts.RecordSettings {
	return contracts.RecordSettings{
		Kinds: slices.Clone(c.Kinds), Moves: c.Moves, MergeMovesMS: c.MergeMovesMS,
		CenterPointer: c.CenterPointer, CoalesceMS: c.CoalesceMS,
	}
}

// RecordSettings возвращает настройки записи по умолчанию (contracts.Recorder).
func (m *Module) RecordSettings() contracts.RecordSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settingsOf(m.cfg)
}

// SetRecordSettings проверяет и меняет настройки записи; идущая запись дописывается
// с прежними (contracts.Recorder).
func (m *Module) SetRecordSettings(s contracts.RecordSettings) error {
	if err := checkSettings(s); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Kinds, m.cfg.Moves, m.cfg.MergeMovesMS = slices.Clone(s.Kinds), s.Moves, s.MergeMovesMS
	m.cfg.CenterPointer, m.cfg.CoalesceMS = s.CenterPointer, s.CoalesceMS
	return nil
}
