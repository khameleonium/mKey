package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/registry"
)

// TestLifecycle проверяет, что модуль проходит полный цикл Init → Start → Stop.
func TestLifecycle(t *testing.T) {
	t.Parallel()

	// Собираем менеджер с одним этим модулем.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	m, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{{Module: New()}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Запускаем и останавливаем; модуль должен быть в состоянии running, затем stopped.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := m.Statuses()[0]; st.State != registry.StateRunning {
		t.Fatalf("state = %s, err = %v", st.State, st.Err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
