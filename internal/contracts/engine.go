package contracts

import (
	"context"
	"errors"
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
	// Source — откуда получены сведения: ID источника раскладок ("kde", "gnome", "x11"…) или
	// "config" — ни один источник не видит раскладку в этой сессии.
	Source string `json:"source"`
	// Blind — раскладку не видно (Source "config"): mKey печатает текст нажатиями клавиш как есть,
	// без переключения раскладки, — нужную раскладку включает сам человек.
	Blind bool `json:"blind,omitempty"`
}

// LayoutSource — источник раскладок для окружения (точка расширения PointLayoutSource): встроенные —
// KDE, GNOME, X11; модуль или плагин может добавить свой для другого окружения. Модуль desktop
// берёт первый источник, который работает в текущей сессии (Supports).
type LayoutSource interface {
	Extension
	LayoutProvider
	// Supports сообщает, умеет ли источник узнавать раскладку в сессии s.
	Supports(s SessionInfo) bool
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

// Timing — интервалы нажатий по умолчанию (секция modules.engine в config.yaml): с ними
// выполняются все макросы, если в макросе не задано иное ({A 500}).
type Timing struct {
	// KeyHoldMS — сколько держать клавишу при обычном нажатии {A}, мс.
	KeyHoldMS int `json:"key_hold_ms"`
	// KeyDelayMS — пауза после каждого нажатия и каждого символа текста, мс.
	KeyDelayMS int `json:"key_delay_ms"`
	// LayoutSwitchMS — пауза после переключения раскладки при наборе текста, мс.
	LayoutSwitchMS int `json:"layout_switch_ms"`
}

// Пределы интервалов нажатий: больше — макрос выглядел бы зависшим.
const (
	// MaxKeyHoldMS и MaxKeyDelayMS — наибольшие удержание и пауза после нажатия, мс.
	MaxKeyHoldMS  = 1000
	MaxKeyDelayMS = 1000
	// MaxLayoutSwitchMS — наибольшая пауза после переключения раскладки, мс.
	MaxLayoutSwitchMS = 5000
)

// TimingSettings — интервалы нажатий по умолчанию (модуль engine).
type TimingSettings interface {
	// Timing возвращает текущие интервалы.
	Timing() Timing
	// SetTiming проверяет и меняет интервалы; действуют для следующих нажатий сразу.
	// ErrBadTiming — значение вне пределов.
	SetTiming(t Timing) error
}

// ErrBadTiming — интервал нажатий вне допустимых пределов.
var ErrBadTiming = errors.New("invalid key timing")
