package hotkeys

import (
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// remapRule — переназначение клавиши (FR-HK-4): from — что заменить (возможно, кнопка
// конкретного устройства {UnKey001}), to — код замены.
type remapRule struct {
	from    contracts.DeviceKey
	to      uint16
	device  string
	project string
}

// reloadRemaps перечитывает переназначения и привязки (FR-VD-3) из включённых проектов и
// пересчитывает захват.
func (m *Module) reloadRemaps() {
	// Без хранилища проектов переназначений нет.
	if m.projects == nil {
		return
	}

	// Собираем правила и привязки включённых проектов; ошибочные пропускаем с предупреждением.
	var rules []remapRule
	var binds []*binding
	for _, st := range m.projects.List() {
		if !st.Project.IsEnabled() {
			continue
		}
		binds = append(binds, m.compileBindings(st)...)
		for _, r := range st.Project.Remaps {
			// Заменять можно и кнопку устройства с авто-ID; заменой — только обычную клавишу
			// (нажатие уходит в копию того же устройства).
			from, err1 := m.parseChord(braced(r.From))
			to, err2 := m.parseChord(braced(r.To))
			if err1 != nil || err2 != nil || len(from) != 1 || len(to) != 1 || to[0].Device != "" {
				m.log.Warn("invalid remap skipped", "project", st.Project.ID, "from", r.From, "to", r.To)
				continue
			}
			rules = append(rules, remapRule{from: from[0], to: to[0].Code, device: r.Device, project: st.Project.ID})
		}
	}

	// Применяем; нажатое прежними привязками отпускается (сброс в очереди — до новых нажатий).
	m.mu.Lock()
	m.remaps = rules
	if len(m.bindings) > 0 && !m.bindClosed {
		m.bindCh <- bindAction{reset: true}
	}
	m.bindings = binds
	clear(m.absRanges)
	m.updateGrab()
	m.mu.Unlock()
	if len(rules)+len(binds) > 0 {
		m.log.Info("remaps and bindings loaded", "remaps", len(rules), "bindings", len(binds))
	}
}

// braced заключает имя одной клавиши в скобки, если их нет: в переназначении можно писать
// и "{Esc}", и "Esc" (как в поле одной клавиши блока «Нажать клавишу»).
func braced(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return s
	}
	return "{" + s + "}"
}

// applyRemap заменяет код клавиши по правилам переназначения (под блокировкой).
// Замена действует на систему только для захваченных устройств — поэтому для них и включается захват.
func (m *Module) applyRemap(device string, e *ev.Event) {
	for _, r := range m.remaps {
		if e.Code == r.from.Code && m.matches(r.from, device, e.Code) && (r.device == "" || m.deviceOK(device, r.device)) {
			e.Code = r.to
			return
		}
	}
}
