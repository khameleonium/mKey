package contracts

import (
	"context"
	"time"
)

// SequenceRunner выполняет макросы, записанные на языке mKey (модуль engine, T2.3).
type SequenceRunner interface {
	// Run разбирает и выполняет макрос src; блокируется до завершения или отмены ctx.
	// Ошибка разбора или выполнения — *dsl.Error с позицией в тексте макроса.
	// Всё, что макрос зажал и не отпустил, отпускается по его завершению (FR-DSL-5).
	Run(ctx context.Context, src string) error
	// StopAll прерывает все выполняющиеся макросы; их зажатые клавиши отпускаются.
	StopAll()
	// Running возвращает число выполняющихся макросов.
	Running() int
}

// LayoutInfo — раскладки клавиатуры пользователя.
type LayoutInfo struct {
	// Current — текущая раскладка, например "ru".
	Current string `json:"current"`
	// Available — все раскладки, включённые в системе, по порядку.
	Available []string `json:"available"`
	// CanSwitch — окружение позволяет mKey переключать раскладку.
	CanSwitch bool `json:"can_switch"`
	// Source — откуда получены сведения: "kde", "gnome", "config".
	Source string `json:"source"`
}

// LayoutProvider сообщает и переключает раскладку клавиатуры (контракт десктоп-адаптеров, FR-AD-4).
type LayoutProvider interface {
	// Layouts возвращает текущую и доступные раскладки.
	Layouts(ctx context.Context) (LayoutInfo, error)
	// Switch переключает раскладку на name; ErrUnsupported, если окружение этого не умеет.
	Switch(ctx context.Context, name string) error
}

// ModuleStatus — состояние модуля для диагностики и API.
type ModuleStatus struct {
	// ID — идентификатор модуля.
	ID string `json:"id"`
	// Core — обязательный модуль.
	Core bool `json:"core"`
	// State — состояние: pending, disabled, initialized, running, failed, stopped.
	State string `json:"state"`
	// Error — последняя ошибка модуля (пусто, если её нет).
	Error string `json:"error,omitempty"`
}

// Modules — сведения о модулях программы (предоставляет ядро, internal/registry).
type Modules interface {
	// ModuleStatuses возвращает состояния всех модулей в порядке запуска.
	ModuleStatuses() []ModuleStatus
}

// Lifecycle — управление процессом демона (предоставляет сборщик программы, internal/app).
type Lifecycle interface {
	// Shutdown просит демон корректно завершиться.
	Shutdown()
	// StartedAt возвращает время запуска демона.
	StartedAt() time.Time
	// Restart корректно завершает демон (модули останавливаются, клавиши отпускаются) и запускает
	// вместо него программу exe с теми же аргументами (после обновления mKey, ADR-0030).
	Restart(exe string)
}
