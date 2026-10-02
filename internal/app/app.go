package app

import (
	"context"
	"fmt"
	"log/slog"

	"mkey/internal/bus"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/registry"
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
