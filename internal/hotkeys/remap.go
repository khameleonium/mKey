package hotkeys

import (
	ev "mkey/internal/lib/evdev"
)

// remapRule — переназначение клавиши (FR-HK-4).
type remapRule struct {
	from, to uint16
	device   string
	project  string
}

// reloadRemaps перечитывает переназначения из включённых проектов и пересчитывает захват.
func (m *Module) reloadRemaps() {
	// Без хранилища проектов переназначений нет.
	if m.projects == nil {
		return
	}

	// Собираем правила включённых проектов; ошибочные пропускаем с предупреждением.
	var rules []remapRule
	for _, st := range m.projects.List() {
		if !st.Project.IsEnabled() {
			continue
		}
		for _, r := range st.Project.Remaps {
			from, err1 := parseChord(r.From)
			to, err2 := parseChord(r.To)
			if err1 != nil || err2 != nil || len(from) != 1 || len(to) != 1 {
				m.log.Warn("invalid remap skipped", "project", st.Project.ID, "from", r.From, "to", r.To)
				continue
			}
			rules = append(rules, remapRule{from: from[0].Code, to: to[0].Code, device: r.Device, project: st.Project.ID})
		}
	}

	// Применяем.
	m.mu.Lock()
	m.remaps = rules
	m.updateGrab()
	m.mu.Unlock()
	if len(rules) > 0 {
		m.log.Info("remaps loaded", "count", len(rules))
	}
}

// applyRemap заменяет код клавиши по правилам переназначения (под блокировкой).
// Замена действует на систему только для захваченных устройств — поэтому для них и включается захват.
func (m *Module) applyRemap(device string, e *ev.Event) {
	for _, r := range m.remaps {
		if e.Code == r.from && (r.device == "" || m.deviceOK(device, r.device)) {
			e.Code = r.to
			return
		}
	}
}
