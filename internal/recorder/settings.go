package recorder

import (
	"fmt"
	"maps"
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

// knownKinds — классы устройств, которые можно записывать (все классы evdev).
var knownKinds = func() []string {
	out := make([]string, len(ev.AllKinds))
	for i, k := range ev.AllKinds {
		out[i] = string(k)
	}
	return out
}()

// maxMergeMS — верхний предел окон склейки движений (больше секунды — путь курсора теряется).
const maxMergeMS = 1000

// checkSettings проверяет настройки записи. Ошибка — contracts.ErrBadRecordSettings с пояснением.
func checkSettings(s contracts.RecordSettings) error {
	// Классы устройств: только известные (пустой список допустим — тогда записываются лишь
	// устройства, отмеченные по отдельности; ничего не отмечено — запись будет пустой, о чём
	// предупреждают при её начале).
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
		Kinds: slices.Clone(c.Kinds), Devices: maps.Clone(c.Devices), Moves: c.Moves, MergeMovesMS: c.MergeMovesMS,
		CenterPointer: c.CenterPointer, CoalesceMS: c.CoalesceMS,
	}
}

// RecordSettings возвращает настройки записи по умолчанию (contracts.Recorder).
func (m *Module) RecordSettings() contracts.RecordSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settingsOf(m.cfg)
}

// SetRecordSettings проверяет и меняет настройки записи и сохраняет их в config.yaml
// (modules.recorder), если демон дал contracts.ConfigWriter; идущая запись дописывается
// с прежними (contracts.Recorder). Меняют из окна и из меню значка — результат один.
func (m *Module) SetRecordSettings(s contracts.RecordSettings) error {
	if err := checkSettings(s); err != nil {
		return err
	}

	// Применяем.
	m.mu.Lock()
	m.cfg.Kinds, m.cfg.Moves, m.cfg.MergeMovesMS = slices.Clone(s.Kinds), s.Moves, s.MergeMovesMS
	m.cfg.Devices = maps.Clone(s.Devices)
	m.cfg.CenterPointer, m.cfg.CoalesceMS = s.CenterPointer, s.CoalesceMS
	m.mu.Unlock()

	// Сохраняем в файл настроек.
	if m.writer == nil {
		return nil
	}
	return m.writer.SetModuleValues(ModuleID, []contracts.ConfigValue{
		{Key: "kinds", Value: nonNil(s.Kinds)}, {Key: "devices", Value: nonNilMap(s.Devices)}, {Key: "moves", Value: s.Moves}, {Key: "merge_moves_ms", Value: s.MergeMovesMS},
		{Key: "center_pointer", Value: s.CenterPointer}, {Key: "coalesce_ms", Value: s.CoalesceMS},
	})
}

// nonNil — пустой список вместо nil: в config.yaml пишется «[]», а не «null».
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// nonNilMap — пустая карта вместо nil: в config.yaml пишется «{}», а не «null».
func nonNilMap(m map[string]bool) map[string]bool {
	if m == nil {
		return map[string]bool{}
	}
	return m
}
