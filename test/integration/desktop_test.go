//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"mkey/internal/desktop/kde"
)

// TestKDELayouts читает раскладки через D-Bus KWin (только чтение, раскладка не переключается).
func TestKDELayouts(t *testing.T) {
	// Тест имеет смысл только в сессии KDE.
	if !strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "KDE") {
		t.Skip("not a KDE session")
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skipf("no session D-Bus: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Раскладки прочитаны, текущая входит в список.
	info, err := kde.New(conn).Layouts(context.Background())
	if err != nil {
		t.Fatalf("Layouts: %v", err)
	}
	t.Logf("layouts: %+v", info)
	found := false
	for _, l := range info.Available {
		found = found || l == info.Current
	}
	if !found || !info.CanSwitch {
		t.Fatalf("unexpected info: %+v", info)
	}
}
