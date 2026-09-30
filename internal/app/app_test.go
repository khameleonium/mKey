package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"mkey/internal/contracts"
	"mkey/internal/registry"
)

// noHardware возвращает конфиг модулей, при котором ввод и вывод не трогают настоящие устройства:
// тесты не должны создавать виртуальные устройства в живой сессии разработчика (AGENTS.md §5).
func noHardware(t *testing.T) func(id string) contracts.ConfigSection {
	dir := t.TempDir()
	return func(id string) contracts.ConfigSection {
		switch id {
		case "input":
			return registry.RawConfig(`{"dir": "` + dir + `"}`)
		case "output":
			return registry.RawConfig(`{"uinput_path": "` + dir + `/uinput"}`)
		}
		return nil
	}
}

// TestAppStartsWithoutOptionalModules проверяет, что программа стартует и останавливается,
// когда все необязательные модули отключены (T0.7, NFR-11).
func TestAppStartsWithoutOptionalModules(t *testing.T) {
	t.Parallel()

	// Собираем приложение со встроенным списком модулей и отключёнными необязательными.
	a, err := New(Options{
		Lang:    "ru",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Enabled: func(string) bool { return false },
		Config:  noHardware(t),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Полный цикл запуска и остановки.
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Все обязательные модули работали, необязательные — отключены.
	for _, st := range a.Manager.Statuses() {
		if !st.Core && st.State != registry.StateDisabled {
			t.Errorf("optional module %s: state %s, want disabled", st.ID, st.State)
		}
	}
}

// TestEachOptionalModuleCanBeDisabled проверяет, что отключение любого одного
// необязательного модуля не ломает запуск остальных (матрица из SPEC §12).
func TestEachOptionalModuleCanBeDisabled(t *testing.T) {
	t.Parallel()

	// Перебираем необязательные модули встроенного списка.
	for _, e := range Modules() {
		if e.Core {
			continue
		}
		id := e.Module.ID()
		t.Run(id, func(t *testing.T) {
			// Собираем приложение, где отключён только модуль id.
			a, err := New(Options{
				Lang:    "en",
				Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
				Enabled: func(other string) bool { return other != id },
				Config:  noHardware(t),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			// Программа должна подняться и корректно остановиться.
			if err := a.Start(context.Background()); err != nil {
				t.Fatalf("Start without %s: %v", id, err)
			}
			if err := a.Stop(context.Background()); err != nil {
				t.Fatalf("Stop without %s: %v", id, err)
			}
		})
	}
}
