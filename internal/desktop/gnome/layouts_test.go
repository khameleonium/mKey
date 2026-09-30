package gnome

import (
	"context"
	"testing"
)

// TestLayouts проверяет разбор вывода gsettings и выбор текущей раскладки.
func TestLayouts(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"sources":     "[('xkb', 'us'), ('xkb', 'ru'), ('ibus', 'anthy')]\n",
		"mru-sources": "[('xkb', 'ru'), ('xkb', 'us')]\n",
	}
	l := &Layouts{get: func(_ context.Context, key string) (string, error) { return values[key], nil }}

	// Раскладки xkb по порядку, текущая — первая из недавних; переключения нет.
	info, err := l.Layouts(context.Background())
	if err != nil || info.Current != "ru" || len(info.Available) != 2 || info.Available[0] != "us" || info.CanSwitch {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}

	// Пустой список недавних — текущей считается первая включённая.
	values["mru-sources"] = "@a(ss) []\n"
	if info, _ := l.Layouts(context.Background()); info.Current != "us" {
		t.Fatalf("Current = %s", info.Current)
	}
}
