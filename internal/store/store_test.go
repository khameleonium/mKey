package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/registry"
)

// startStore запускает модуль с каталогом dir и возвращает его и шину.
func startStore(t *testing.T, dir string) (*Module, *bus.Bus) {
	t.Helper()
	cat, _ := i18n.LoadCatalog()
	b := bus.New(0)
	mod := New()
	mgr, err := registry.NewManager(registry.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Translator: i18n.New(cat, "en"), Bus: b,
		Config: func(string) contracts.ConfigSection { return registry.RawConfig(`{"dir":"` + dir + `"}`) },
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })
	return mod, b
}

// eventually ждёт выполнения условия до 3 секунд.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestExampleOnFirstRun проверяет создание выключенного проекта-примера при первом запуске.
func TestExampleOnFirstRun(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "projects")
	m, _ := startStore(t, dir)
	st, ok := m.Get(exampleID)
	if !ok || st.Error != "" || st.Project.IsEnabled() || len(st.Project.Events) != 3 {
		t.Fatalf("example = %+v, %v", st, ok)
	}
}

// TestHotReload проверяет перечитывание изменённого файла и сохранение прежней версии при ошибке.
func TestHotReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "games.mkey.yaml")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("name: A\nevents:\n  - {id: a, trigger: {type: manual}, actions: [{send: '{A}'}]}\n")
	m, b := startStore(t, dir)
	errs, cancel := b.Subscribe(contracts.TopicProjectError)
	defer cancel()

	// Изменение применяется на лету.
	write("name: B\nevents:\n  - {id: b, trigger: {type: manual}, actions: [{send: '{B}'}]}\n")
	eventually(t, "reload", func() bool { st, _ := m.Get("games"); return st.Project.Name == "B" })

	// Ошибка в файле: прежняя версия работает, ошибка видна и опубликована.
	write("name: C\nevents: [ {id: c} ]\n")
	select {
	case <-errs:
	case <-time.After(3 * time.Second):
		t.Fatal("project error not published")
	}
	st, _ := m.Get("games")
	if st.Project.Name != "B" || st.Error == "" {
		t.Fatalf("state = %+v", st)
	}

	// Удаление файла убирает проект.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remove", func() bool { _, ok := m.Get("games"); return !ok })
}

// TestSetEnabledKeepsComments проверяет включение проекта и события с сохранением комментариев.
func TestSetEnabledKeepsComments(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "projects")
	m, _ := startStore(t, dir)

	// Включаем пример и одно его событие выключаем.
	if err := m.SetEnabled(exampleID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEventEnabled(exampleID, "btw", false); err != nil {
		t.Fatal(err)
	}
	st, _ := m.Get(exampleID)
	if !st.Project.IsEnabled() || st.Project.Events[2].IsEnabled() {
		t.Fatalf("state = %+v", st.Project)
	}

	// Комментарии файла на месте.
	data, _ := os.ReadFile(filepath.Join(dir, "example.mkey.yaml"))
	if !strings.Contains(string(data), "# F8 — включить/выключить автокликер") {
		t.Fatalf("comments lost:\n%s", data)
	}

	// Неизвестное событие — ошибка.
	if err := m.SetEventEnabled(exampleID, "nope", true); err == nil {
		t.Fatal("unknown event must fail")
	}
}

// TestImport проверяет импорт: проект выключен, имя очищено, повтор получает новый ID.
func TestImport(t *testing.T) {
	t.Parallel()
	m, _ := startStore(t, t.TempDir())
	src := []byte("name: Shared\nenabled: true\nevents:\n  - {id: x, trigger: {type: manual}, actions: [{shell: 'echo hi'}]}\n")
	id, err := m.Import("Мой проект!.mkey.yaml", src)
	if err != nil || id != "Мой-проект" {
		t.Fatalf("Import = %q, %v", id, err)
	}
	st, _ := m.Get(id)
	if st.Project.IsEnabled() {
		t.Fatal("imported project must be disabled")
	}
	id2, err := m.Import("Мой проект!.mkey.yaml", src)
	if err != nil || id2 != "Мой-проект-2" {
		t.Fatalf("second Import = %q, %v", id2, err)
	}
	if _, err := m.Import("bad.yaml", []byte("events: [ {id: 1} ]")); err == nil {
		t.Fatal("invalid project must not be imported")
	}
}
