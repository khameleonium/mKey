package registry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"mkey/internal/bus"
	"mkey/internal/contracts"
)

// stubTranslator — переводчик-заглушка для тестов: всегда возвращает ключ.
type stubTranslator struct{}

func (stubTranslator) T(key string, _ ...contracts.Arg) string { return key }
func (stubTranslator) Lang() string                            { return "en" }

// fakeModule — модуль для тестов, записывающий вызовы в общий журнал.
type fakeModule struct {
	id       string
	log      *[]string
	initErr  error
	startErr error
	panicIn  string
}

func (f *fakeModule) ID() string { return f.id }

func (f *fakeModule) Init(context.Context, contracts.Host) error {
	*f.log = append(*f.log, f.id+".init")
	if f.panicIn == "init" {
		panic("boom")
	}
	return f.initErr
}

func (f *fakeModule) Start(context.Context) error {
	*f.log = append(*f.log, f.id+".start")
	return f.startErr
}

func (f *fakeModule) Stop(context.Context) error {
	*f.log = append(*f.log, f.id+".stop")
	return nil
}

// newTestManager создаёт менеджер с тихим логгером и заданными модулями.
func newTestManager(t *testing.T, enabled func(string) bool, entries ...Entry) *Manager {
	t.Helper()
	m, err := NewManager(Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: stubTranslator{},
		Bus:        bus.New(0),
		Enabled:    enabled,
	}, entries)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// TestManagerEmpty проверяет, что ядро стартует и останавливается без модулей вообще (T0.7).
func TestManagerEmpty(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, nil)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestManagerLifecycleOrder проверяет порядок вызовов: Init по порядку, Start по порядку, Stop в обратном.
func TestManagerLifecycleOrder(t *testing.T) {
	t.Parallel()

	// Два работающих модуля.
	var log []string
	m := newTestManager(t, nil,
		Entry{Module: &fakeModule{id: "a", log: &log}, Core: true},
		Entry{Module: &fakeModule{id: "b", log: &log}},
	)

	// Полный цикл запуска и остановки.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Сверяем журнал вызовов.
	want := []string{"a.init", "b.init", "a.start", "b.start", "b.stop", "a.stop"}
	if !slices.Equal(log, want) {
		t.Fatalf("calls = %v, want %v", log, want)
	}
}

// TestManagerDisabledOptional проверяет, что отключённый необязательный модуль не запускается,
// а обязательный запускается независимо от конфига.
func TestManagerDisabledOptional(t *testing.T) {
	t.Parallel()

	// Конфиг отключает оба модуля, но "core" обязательный.
	var log []string
	m := newTestManager(t, func(string) bool { return false },
		Entry{Module: &fakeModule{id: "core", log: &log}, Core: true},
		Entry{Module: &fakeModule{id: "opt", log: &log}},
	)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Проверяем состояния.
	st := m.Statuses()
	if st[0].State != StateRunning || st[1].State != StateDisabled {
		t.Fatalf("statuses = %+v", st)
	}
}

// TestManagerOptionalFailure проверяет, что сбой (ошибка или паника) необязательного модуля
// не мешает остальным (NFR-7, NFR-11).
func TestManagerOptionalFailure(t *testing.T) {
	t.Parallel()

	// Один модуль падает с ошибкой в Start, другой паникует в Init, третий здоров.
	var log []string
	m := newTestManager(t, nil,
		Entry{Module: &fakeModule{id: "bad", log: &log, startErr: errors.New("no device")}},
		Entry{Module: &fakeModule{id: "panicky", log: &log, panicIn: "init"}},
		Entry{Module: &fakeModule{id: "good", log: &log}},
	)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start must succeed despite optional failures: %v", err)
	}

	// Сбойные помечены failed, здоровый работает.
	st := m.Statuses()
	if st[0].State != StateFailed || st[1].State != StateFailed || st[2].State != StateRunning {
		t.Fatalf("statuses = %+v", st)
	}
	if !slices.Contains(log, "bad.stop") {
		t.Fatalf("failed module must be stopped, calls = %v", log)
	}
}

// TestManagerCoreFailure проверяет, что сбой обязательного модуля прерывает запуск
// и останавливает уже инициализированные модули.
func TestManagerCoreFailure(t *testing.T) {
	t.Parallel()

	// Первый модуль здоров, второй (обязательный) падает в Init.
	var log []string
	m := newTestManager(t, nil,
		Entry{Module: &fakeModule{id: "first", log: &log}},
		Entry{Module: &fakeModule{id: "core", log: &log, initErr: errors.New("broken")}, Core: true},
	)
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("Start must fail when a core module fails")
	}

	// Первый модуль был инициализирован и должен быть остановлен.
	if !slices.Contains(log, "first.stop") {
		t.Fatalf("initialized modules must be stopped, calls = %v", log)
	}
}

// TestNewManagerValidation проверяет отказ при повторяющихся и пустых ID.
func TestNewManagerValidation(t *testing.T) {
	t.Parallel()
	var log []string
	opts := Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: stubTranslator{},
		Bus:        bus.New(0),
	}

	// Повторяющийся ID.
	_, err := NewManager(opts, []Entry{
		{Module: &fakeModule{id: "x", log: &log}},
		{Module: &fakeModule{id: "x", log: &log}},
	})
	if err == nil {
		t.Fatal("duplicate ids must be rejected")
	}

	// Пустой ID.
	if _, err := NewManager(opts, []Entry{{Module: &fakeModule{id: " ", log: &log}}}); err == nil {
		t.Fatal("empty id must be rejected")
	}
}

// greeter — тестовый контракт для проверки реестра сервисов.
type greeter interface{ Greet() string }

// english — реализация greeter.
type english struct{}

func (english) Greet() string { return "hello" }

// TestServices проверяет регистрацию и поиск сервиса по типу контракта.
func TestServices(t *testing.T) {
	t.Parallel()
	s := NewServices()

	// До регистрации сервиса нет.
	if _, err := contracts.LookupService[greeter](s); !errors.Is(err, contracts.ErrServiceNotFound) {
		t.Fatalf("want ErrServiceNotFound, got %v", err)
	}

	// Регистрируем и находим.
	if err := contracts.ProvideService[greeter](s, english{}); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	g, err := contracts.LookupService[greeter](s)
	if err != nil || g.Greet() != "hello" {
		t.Fatalf("Lookup = %v, %v", g, err)
	}

	// Повторная регистрация запрещена.
	if err := contracts.ProvideService[greeter](s, english{}); !errors.Is(err, contracts.ErrServiceExists) {
		t.Fatalf("want ErrServiceExists, got %v", err)
	}
}

// testExt — расширение для тестов реестра точек расширения.
type testExt struct{ id string }

func (e testExt) Meta() contracts.ExtensionMeta { return contracts.ExtensionMeta{ID: e.id} }

// TestExtensions проверяет регистрацию, сортировку и ошибки реестра точек расширения.
func TestExtensions(t *testing.T) {
	t.Parallel()
	e := NewExtensions()

	// Регистрируем два действия в обратном порядке.
	for _, id := range []string{"send", "pause"} {
		if err := e.Register(contracts.PointAction, testExt{id}); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
	}

	// Список отсортирован по ID.
	list := e.List(contracts.PointAction)
	if len(list) != 2 || list[0].Meta().ID != "pause" || list[1].Meta().ID != "send" {
		t.Fatalf("List = %v", list)
	}

	// Ошибки: повтор ID, неизвестная точка, пустой ID.
	if err := e.Register(contracts.PointAction, testExt{"send"}); !errors.Is(err, contracts.ErrExtensionExists) {
		t.Fatalf("want ErrExtensionExists, got %v", err)
	}
	if err := e.Register("nope", testExt{"x"}); !errors.Is(err, contracts.ErrUnknownExtensionPoint) {
		t.Fatalf("want ErrUnknownExtensionPoint, got %v", err)
	}
	if err := e.Register(contracts.PointAction, testExt{""}); err == nil {
		t.Fatal("empty id must be rejected")
	}
}
