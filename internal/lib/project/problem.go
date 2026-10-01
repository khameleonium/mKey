package project

import (
	"errors"
	"fmt"
	"strings"
)

// Ошибки проекта с указанием места — чтобы GUI и CLI могли сказать пользователю простыми словами,
// какое событие и какой блок исправить, и подсветить его (FR-UI-8).

// Части события, в которых бывает ошибка.
const (
	// PartTrigger — триггер («Когда»).
	PartTrigger = "trigger"
	// PartCondition — условие.
	PartCondition = "condition"
	// PartAction — блок действия («Делать»).
	PartAction = "action"
	// PartBinding — привязка проекта (Event пусто: привязки — не в событиях).
	PartBinding = "binding"
	// PartVirtualDevice — виртуальное устройство проекта (Event пусто).
	PartVirtualDevice = "virtual_device"
)

// Problem — ошибка в событии проекта: где она и что случилось.
type Problem struct {
	// Event — ID события.
	Event string
	// Part — часть события (PartTrigger, PartCondition, PartAction); пусто — событие целиком.
	Part string
	// Index — номер триггера, условия или блока в событии (с 0); -1 — часть целиком (например, нет триггера).
	Index int
	// Kind — вид блока ("shell", "hotkey"…); пусто, если вид неизвестен.
	Kind string
	// Err — сама ошибка.
	Err error
}

// Error записывает ошибку по-английски для журнала и CLI: event "a": action #1 (shell): …
// Вложенная ошибка (блок внутри «Повторять») пишется без события: action #1 (tap): ….
func (p *Problem) Error() string {
	var parts []string
	if p.Event != "" {
		parts = append(parts, fmt.Sprintf("event %q", p.Event))
	}
	if p.Part != "" && p.Index >= 0 {
		where := fmt.Sprintf("%s #%d", p.Part, p.Index+1)
		if p.Kind != "" {
			where += " (" + p.Kind + ")"
		}
		parts = append(parts, where)
	}
	return strings.Join(append(parts, p.Err.Error()), ": ")
}

// Unwrap возвращает саму ошибку (для errors.Is/As).
func (p *Problem) Unwrap() error { return p.Err }

// ErrNoTrigger — у события не выбрано, когда срабатывать.
var ErrNoTrigger = errors.New("no trigger")

// FieldError — не заполнено обязательное поле блока.
type FieldError struct {
	// Point — точка расширения вида ("action", "condition", "trigger").
	Point string
	// Kind — вид блока ("shell"); может быть пуст, если его дополняет вызывающий код.
	Kind string
	// Field — имя поля ("name"); пусто — само значение блока (например, клавиша у «Нажать клавишу»).
	Field string
}

// Error записывает ошибку по-английски: "name is required".
func (e *FieldError) Error() string {
	if e.Field == "" {
		return "a value is required"
	}
	return e.Field + " is required"
}

// Required возвращает ошибку «не заполнено поле field» блока вида kind в точке point.
func Required(point, kind, field string) error {
	return &FieldError{Point: point, Kind: kind, Field: field}
}
