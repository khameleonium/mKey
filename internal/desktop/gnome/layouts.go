// Package gnome — адаптер GNOME для модуля desktop.
//
// Раскладки читаются командой gsettings из схемы org.gnome.desktop.input-sources:
// ключ sources — включённые раскладки, ключ mru-sources — недавно использованные
// (первая в нём — текущая). Переключать раскладку извне GNOME не позволяет без
// собственного расширения оболочки (его нет: фаза 8 отменена), поэтому CanSwitch = false.
package gnome

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// schema — схема настроек источников ввода GNOME.
const schema = "org.gnome.desktop.input-sources"

// sourceRe находит пары ('xkb', 'us') в выводе gsettings.
var sourceRe = regexp.MustCompile(`\('xkb',\s*'([^']+)'\)`)

// Layouts — contracts.LayoutProvider для GNOME.
type Layouts struct {
	// get читает ключ gsettings (в тестах подменяется).
	get func(ctx context.Context, key string) (string, error)
}

// New создаёт адаптер, вызывающий gsettings.
func New() *Layouts {
	return &Layouts{get: func(ctx context.Context, key string) (string, error) {
		out, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
		return string(out), err
	}}
}

// Layouts возвращает раскладки: список из sources, текущую — первую из mru-sources.
func (l *Layouts) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	// Включённые раскладки.
	raw, err := l.get(ctx, "sources")
	if err != nil {
		return contracts.LayoutInfo{}, err
	}
	available := parse(raw)
	if len(available) == 0 {
		return contracts.LayoutInfo{}, errors.New("gnome: no xkb input sources")
	}

	// Текущая — первая недавно использованная, иначе первая включённая.
	info := contracts.LayoutInfo{Current: available[0], Available: available, Source: "gnome"}
	if mru, err := l.get(ctx, "mru-sources"); err == nil {
		if recent := parse(mru); len(recent) > 0 {
			info.Current = recent[0]
		}
	}
	return info, nil
}

// Switch не поддерживается в GNOME без расширения оболочки.
func (l *Layouts) Switch(context.Context, string) error { return contracts.ErrUnsupported }

// parse извлекает имена раскладок из вывода gsettings, например "[('xkb', 'us'), ('xkb', 'ru')]".
func parse(s string) []string {
	var out []string
	for _, m := range sourceRe.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// Проверка на этапе компиляции, что Layouts реализует контракт.
var _ contracts.LayoutProvider = (*Layouts)(nil)
