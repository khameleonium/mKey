package desktop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"mkey/internal/contracts"
)

// brokenAdapter — адаптер окружения, который всегда отказывает.
type brokenAdapter struct{}

func (brokenAdapter) Layouts(context.Context) (contracts.LayoutInfo, error) {
	return contracts.LayoutInfo{}, errors.New("no kwin")
}
func (brokenAdapter) Switch(context.Context, string) error { return errors.New("no kwin") }

// TestFallbackToConfig проверяет раскладки из настроек и откат на них при сбое адаптера.
func TestFallbackToConfig(t *testing.T) {
	t.Parallel()
	m := &Module{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m.fallback = configLayouts{cfg: Config{Layouts: []string{"us", "ru"}, Layout: "ru"}}

	// Без адаптера: раскладки из настроек, переключения нет.
	info, err := m.Layouts(context.Background())
	if err != nil || info.Current != "ru" || info.Source != "config" || info.CanSwitch {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}
	if err := m.Switch(context.Background(), "us"); !errors.Is(err, contracts.ErrUnsupported) {
		t.Fatalf("Switch = %v", err)
	}

	// Сломанный адаптер: откат на настройки.
	m.adapter = brokenAdapter{}
	if info, err := m.Layouts(context.Background()); err != nil || info.Source != "config" {
		t.Fatalf("fallback = %+v, %v", info, err)
	}

	// Пустые настройки: одна раскладка us.
	if info, _ := (configLayouts{}).Layouts(context.Background()); info.Current != "us" || len(info.Available) != 1 {
		t.Fatalf("defaults = %+v", info)
	}
}
