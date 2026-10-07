package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/input"
	"github.com/khameleonium/mKey/internal/output"
	"github.com/khameleonium/mKey/internal/registry"
	"github.com/khameleonium/mKey/internal/update"
)

// Options — параметры сборки приложения.
type Options struct {
	// Lang — язык пользовательских сообщений ("ru", "en").
	Lang string
	// Logger — логгер приложения.
	Logger *slog.Logger
	// Enabled сообщает, включён ли необязательный модуль. nil — включены все.
	Enabled func(id string) bool
	// Modules — список модулей. nil — встроенный список из Modules().
	Modules []registry.Entry
	// Config возвращает секцию конфига модуля (из config.yaml). nil — всем модулям пустые секции
	// (тесты и команды без демона).
	Config func(id string) contracts.ConfigSection
	// FakeBackends — режим без настоящих устройств (`mkey daemon --fake-backends`, T12.11): ввод
	// и вывод только в памяти, без значка в трее и проверки обновлений (для проверок окна и e2e).
	FakeBackends bool
}

// App — собранное приложение: ядро и модули.
type App struct {
	// Translator — переводчик сообщений на выбранный язык.
	Translator *i18n.Translator
	// Bus — шина событий.
	Bus *bus.Bus
	// Manager — менеджер жизненного цикла модулей (и владелец реестров).
	Manager *registry.Manager
}

// New собирает приложение: загружает переводы, создаёт шину и менеджер модулей.
func New(opts Options) (*App, error) {
	// Загружаем переводы и выбираем язык.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	tr := i18n.New(cat, opts.Lang)

	// Определяем состав модулей: явно переданный или встроенный.
	entries := opts.Modules
	if entries == nil {
		entries = Modules()
	}
	if opts.FakeBackends {
		entries = fakeBackends(entries)
	}

	// Создаём шину и менеджер модулей.
	b := bus.New(0)
	mgr, err := registry.NewManager(registry.Options{
		Logger:     opts.Logger,
		Translator: tr,
		Bus:        b,
		Enabled:    opts.Enabled,
		Config:     opts.Config,
	}, entries)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	return &App{Translator: tr, Bus: b, Manager: mgr}, nil
}

// Start запускает все модули.
func (a *App) Start(ctx context.Context) error {
	return a.Manager.Start(ctx)
}

// Stop останавливает все модули в обратном порядке.
func (a *App) Stop(ctx context.Context) error {
	return a.Manager.Stop(ctx)
}

// fakeBackends заменяет в списке модули ввода и вывода на их варианты без настоящих устройств
// и убирает значок в трее и проверку обновлений (им нечего делать в проверочном запуске).
func fakeBackends(entries []registry.Entry) []registry.Entry {
	out := make([]registry.Entry, 0, len(entries))
	for _, e := range entries {
		switch e.Module.ID() {
		case input.ModuleID:
			e.Module = input.NewFake()
		case output.ModuleID:
			e.Module = output.NewFake()
		case "tray", update.ModuleID:
			continue
		}
		out = append(out, e)
	}
	return out
}
