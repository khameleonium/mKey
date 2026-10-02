package contracts

import (
	"context"

	"mkey/internal/lib/project"
)

// «Сухой прогон» события (FR-UI-6): что сделает событие и когда, без нажатий и без побочных
// действий. Строит его движок (DryRunner); виды действий описывают себя через ActionDryRunner,
// а действия без него (скрипты, плагины, повтор записи) попадают в таймлайн одной строкой
// «выполнится при запуске».

// Виды строк таймлайна сухого прогона, кроме шагов макроса (press, release, release_all, tap,
// wait, text, move, wheel, axis, touch, swipe — как dsl.StepKind).
const (
	// DryGroup — заголовок вложенной группы: повтор, ветка «Если»/«Иначе», повтор внутри макроса.
	DryGroup = "group"
	// DryNote — действие, которое прогон описывает, но не выполняет (уведомление, переменная…).
	DryNote = "note"
	// DryAction — действие без описания для прогона (скрипт, плагин…): выполнится только при запуске.
	DryAction = "action"
)

// DryStep — строка таймлайна сухого прогона.
type DryStep struct {
	// AtMS — когда начнётся шаг, миллисекунды от начала события (пауза со случайной длительностью
	// считается по наименьшей).
	AtMS int64 `json:"at_ms"`
	// Depth — вложенность: 0 — верхний уровень, внутри группы — на 1 больше.
	Depth int `json:"depth"`
	// Kind — вид строки: шаг макроса (press, tap, wait…) или DryGroup, DryNote, DryAction.
	Kind string `json:"kind"`
	// Keys — клавиши и кнопки шага (press, release, tap, axis).
	Keys []string `json:"keys,omitempty"`
	// Count — число нажатий (tap), щелчков колеса (wheel) или повторов группы.
	Count int `json:"count,omitempty"`
	// MS — длительность: удержание (tap, touch), пауза (wait; MaxMS — верхняя граница случайной
	// паузы), время свайпа (swipe).
	MS    int64 `json:"ms,omitempty"`
	MaxMS int64 `json:"max_ms,omitempty"`
	// Text — набираемый текст (text).
	Text string `json:"text,omitempty"`
	// DX и DY — сдвиг курсора (move) или направление прокрутки (wheel).
	DX int32 `json:"dx,omitempty"`
	DY int32 `json:"dy,omitempty"`
	// Value — положение оси (axis).
	Value float64 `json:"value,omitempty"`
	// Points — точки касания (touch — одна, swipe — две): "50%, 80%" или "960, 540".
	Points []string `json:"points,omitempty"`
	// Device — устройство шага, если это не обычные клавиатура и мышь (виртуальный геймпад, экран).
	Device string `json:"device,omitempty"`
	// Key и Args — текст строки DryGroup и DryNote: i18n-ключ окна и его параметры.
	Key  string            `json:"key,omitempty"`
	Args map[string]string `json:"args,omitempty"`
	// Action — вид действия (DryAction) — по нему окно берёт название из реестра.
	Action string `json:"action,omitempty"`
	// Conditions — условия группы «Если», «Повторять, пока» или самого события (вид и параметры:
	// окно показывает название вида из реестра и краткое содержание параметров).
	Conditions []project.Condition `json:"conditions,omitempty"`
}

// DryRun — результат сухого прогона.
type DryRun struct {
	// Steps — строки таймлайна по порядку.
	Steps []DryStep `json:"steps"`
	// TotalMS — сколько примерно займёт выполнение (повтор «пока…» — один проход).
	TotalMS int64 `json:"total_ms"`
	// Open — длительность заранее неизвестна: есть повтор «пока включено», «пока зажата»,
	// «без конца» или «пока выполняются условия».
	Open bool `json:"open,omitempty"`
	// Truncated — таймлайн слишком длинный и обрезан.
	Truncated bool `json:"truncated,omitempty"`
}

// DryRunner строит сухой прогон действий (модуль engine).
type DryRunner interface {
	// DryRun описывает, что сделает событие ev проекта projectID, ничего не выполняя: условия
	// события (первой строкой) и действия. Проект должен быть проверен (ValidateProject);
	// ошибка — неизвестный вид действия или макрос не разбирается.
	DryRun(ctx context.Context, projectID string, ev project.Event) (DryRun, error)
}

// ActionDryRunner — необязательный интерфейс вида действия (ActionType): описать действие для
// сухого прогона. Вид без него показывается строкой DryAction.
type ActionDryRunner interface {
	// DryRun добавляет в таймлайн dc то, что сделало бы действие a.
	DryRun(ctx context.Context, dc DryRunContext, a project.Action) error
}

// DryRunContext — таймлайн, который строит сухой прогон, для ActionDryRunner.
type DryRunContext interface {
	// Send добавляет шаги макроса DSL src (без нажатий); ошибка — *dsl.Error.
	Send(src string) error
	// RunActions описывает вложенные действия.
	RunActions(ctx context.Context, actions []project.Action) error
	// Group добавляет заголовок группы head и описывает внутри неё body (на уровень глубже).
	// times > 1 — группа повторяется: тело показывается один раз, а время умножается на times;
	// times == 0 — число повторов заранее неизвестно (повтор «пока…»): тело один раз, DryRun.Open.
	Group(ctx context.Context, head DryStep, times int, body func() error) error
	// Note добавляет строку DryNote с текстом key и параметрами args.
	Note(key string, args map[string]string)
	// Spend сдвигает время таймлайна на ms: столько длится описанное действие.
	Spend(ms int64)
	// Project возвращает проект, событие которого прогоняется.
	Project() string
}
