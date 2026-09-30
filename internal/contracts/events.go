package contracts

import (
	"context"
	"errors"
	"log/slog"

	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// Темы шины, которые публикует модуль store.
const (
	// TopicProjectsChanged — проекты загружены или изменились; Payload: []string (ID изменённых проектов).
	TopicProjectsChanged = "store.projects_changed"
	// TopicProjectError — файл проекта не удалось загрузить (работает прежняя версия); Payload: ProjectError.
	TopicProjectError = "store.project_error"
	// TopicEngineError — проект загружен, но движок его отверг; Payload: ProjectError.
	TopicEngineError = "engine.project_error"
	// TopicEmergency — экстренная остановка (SEC-1): всё остановлено, перехват снят, mKey приостановлен
	// (триггеры не срабатывают) до TopicResumed; Payload: nil.
	TopicEmergency = "input.emergency"
	// TopicResumed — работа возобновлена после экстренной остановки (`mkey resume`); Payload: nil.
	TopicResumed = "input.resumed"
)

// ErrEventInactive — событие не найдено или его проект выключен.
var ErrEventInactive = errors.New("event is not active")

// ProjectError — ошибка загрузки или проверки проекта.
type ProjectError struct {
	// ID — идентификатор проекта.
	ID string `json:"id"`
	// Error — текст ошибки.
	Error string `json:"error"`
}

// ProjectState — загруженный проект и сведения о файле.
type ProjectState struct {
	// Project — последняя успешно разобранная версия проекта.
	Project project.Project `json:"project"`
	// Path — путь к файлу проекта.
	Path string `json:"path"`
	// Error — ошибка последней попытки загрузки (пусто, если файл в порядке).
	Error string `json:"error,omitempty"`
}

// Projects — хранилище проектов (модуль store).
type Projects interface {
	// Dir возвращает каталог файлов проектов.
	Dir() string
	// List возвращает все проекты, отсортированные по ID.
	List() []ProjectState
	// Get возвращает проект по ID.
	Get(id string) (ProjectState, bool)
	// SetEnabled включает или выключает проект (изменение записывается в файл, комментарии сохраняются).
	SetEnabled(id string, enabled bool) error
	// SetEventEnabled включает или выключает событие проекта.
	SetEventEnabled(projectID, eventID string, enabled bool) error
	// Import сохраняет новый проект из содержимого файла; новый проект по умолчанию выключен (SEC-7).
	Import(name string, data []byte) (string, error)
}

// EventRef — ссылка на событие проекта.
type EventRef struct {
	// Project — ID проекта.
	Project string `json:"project"`
	// Event — ID события.
	Event string `json:"event"`
	// Name — название события для сообщений.
	Name string `json:"name,omitempty"`
}

// Fire — одно срабатывание триггера.
type Fire struct {
	// Toggle — новое состояние триггера-переключателя (nil — триггер не переключатель).
	// Состояние «выключено» не запускает действия, а лишь останавливает циклы `while: toggled`.
	Toggle *bool
	// Held сообщает, удерживается ли ещё клавиша триггера (для циклов `while: held`); nil — неприменимо.
	Held func() bool
	// Modifiers — модификаторы горячей клавиши, которые приложения видят зажатыми (для release_modifiers).
	Modifiers ModifierControl
	// Vars — значения, которые триггер передаёт действиям (например, набранный текст hotstring).
	Vars map[string]any
}

// ModifierControl временно отпускает модификаторы, зажатые пользователем, и возвращает их (FR-HK-3).
type ModifierControl interface {
	// Release отпускает модификаторы для приложений (через passthrough-устройство).
	Release()
	// Restore снова зажимает модификаторы, которые пользователь всё ещё держит.
	Restore()
}

// TriggerType — вид триггера (точка расширения PointTrigger).
type TriggerType interface {
	Extension
	// Arm проверяет параметры и начинает следить за триггером; fire вызывается при каждом срабатывании
	// (из любой горутины). Возвращает функцию снятия триггера.
	Arm(ctx context.Context, ev EventRef, t project.Trigger, fire func(Fire)) (disarm func(), err error)
}

// ConditionType — вид условия (точка расширения PointCondition).
type ConditionType interface {
	Extension
	// Validate проверяет параметры условия.
	Validate(c project.Condition) error
	// Check вычисляет условие в контексте выполнения события.
	Check(ctx context.Context, rc RunContext, c project.Condition) (bool, error)
}

// ActionType — вид действия (точка расширения PointAction).
type ActionType interface {
	Extension
	// Validate проверяет параметры действия (в том числе вложенных действий).
	Validate(a project.Action) error
	// Run выполняет действие.
	Run(ctx context.Context, rc RunContext, a project.Action) error
}

// RunContext — всё, что нужно действиям и условиям во время одного выполнения события.
type RunContext interface {
	// Event возвращает событие, которое выполняется.
	Event() EventRef
	// Send выполняет макрос DSL; клавиши, зажатые одним действием, остаются зажатыми для следующих
	// и отпускаются по окончании выполнения события.
	Send(ctx context.Context, src string) error
	// RunActions выполняет вложенные действия (repeat, if).
	RunActions(ctx context.Context, actions []project.Action) error
	// Check вычисляет список условий (все должны выполняться).
	Check(ctx context.Context, conds []project.Condition) (bool, error)
	// Toggled сообщает текущее состояние триггера-переключателя события.
	Toggled() bool
	// Held сообщает, удерживается ли ещё клавиша триггера.
	Held() bool
	// Fire возвращает срабатывание, запустившее выполнение.
	Fire() Fire
	// Vars возвращает переменные проекта.
	Vars() VarStore
	// Logger возвращает логгер события.
	Logger() *slog.Logger
}

// VarStore — переменные одного проекта (FR-EV-6).
type VarStore interface {
	// Get возвращает значение переменной.
	Get(name string) (any, bool)
	// Set задаёт значение (тип приводится к объявленному типу переменной).
	Set(name string, value any) error
	// Add прибавляет delta к числовой переменной.
	Add(name string, delta float64) error
	// All возвращает копию всех переменных.
	All() map[string]any
}

// EventStatus — состояние события для API и CLI.
type EventStatus struct {
	EventRef
	// Enabled — событие и его проект включены.
	Enabled bool `json:"enabled"`
	// Running — сколько выполнений идёт сейчас.
	Running int `json:"running"`
	// Toggled — состояние переключателя (для toggle-триггеров).
	Toggled bool `json:"toggled"`
	// Triggers — виды триггеров события.
	Triggers []string `json:"triggers"`
}

// Events — сервис событий (модуль engine).
type Events interface {
	// Statuses возвращает состояние всех событий включённых и выключенных проектов.
	Statuses() []EventStatus
	// RunEvent запускает событие вручную (как триггер manual) и ждёт завершения.
	RunEvent(ctx context.Context, projectID, eventID string) error
	// Vars возвращает переменные проекта (nil, если проекта нет).
	Vars(projectID string) VarStore
}

// KeyState — текущее состояние физических клавиш и кнопок (модуль hotkeys).
type KeyState interface {
	// IsDown сообщает, зажата ли клавиша сейчас на любом устройстве (модификатор без стороны — любой из двух).
	IsDown(k keys.Key) bool
	// WaitKey ждёт нажатия клавиши k (для `mkey wait key`).
	WaitKey(ctx context.Context, k keys.Key) error
	// Resume возобновляет работу после экстренной остановки: перехват и срабатывание триггеров.
	Resume()
	// Suspended сообщает, приостановлен ли mKey после экстренной остановки.
	Suspended() bool
}

// Notifier показывает уведомления рабочего стола (модуль desktop).
type Notifier interface {
	// Notify показывает уведомление с заголовком title и текстом body.
	Notify(ctx context.Context, title, body string) error
}
