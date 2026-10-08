package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/registry"
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
		case "api":
			return registry.RawConfig(`{"port": 0, "runtime_dir": "` + dir + `/rt"}`)
		case "store":
			return registry.RawConfig(`{"dir": "` + dir + `/projects"}`)
		case "engine":
			return registry.RawConfig(`{"vars_file": "` + dir + `/vars.json"}`)
		case "tray":
			// Без значка: иначе тест на миг показывал бы настоящий значок на рабочем столе
			// разработчика, а служба значков некоторых окружений от этого падает (SPEC §14, вопрос 5).
			return registry.RawConfig(`{"enabled": false}`)
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

// TestFakeBackends проверяет режим без настоящих устройств (--fake-backends): программа стартует,
// устройств ввода нет, макрос выполняется на виртуальной клавиатуре в памяти, значка в трее
// и проверки обновлений нет.
func TestFakeBackends(t *testing.T) {
	t.Parallel()
	a, err := New(Options{
		Lang:         "en",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:       noHardware(t),
		FakeBackends: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	// Модули: ввод и вывод работают, трея и обновлений нет.
	for _, st := range a.Manager.Statuses() {
		if st.ID == "tray" || st.ID == "update" {
			t.Errorf("module %s must be absent", st.ID)
		}
	}
	in, err := contracts.LookupService[contracts.InputSource](a.Manager.Services())
	if err != nil || len(in.Devices()) != 0 {
		t.Fatalf("input: %v, devices %v", err, in.Devices())
	}

	// Макрос выполняется (устройства — в памяти).
	runner, err := contracts.LookupService[contracts.SequenceRunner](a.Manager.Services())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), `^{Shift}{A}~{Shift}{"hi"}`); err != nil {
		t.Fatalf("run: %v", err)
	}
}
