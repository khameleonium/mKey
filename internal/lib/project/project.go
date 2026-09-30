// Package project — модель проекта mKey и разбор файлов *.mkey.yaml (SPEC §5.2, §8, T3.1).
//
// Проект — список событий. Событие: триггер(ы) → условия → действия. Модель намеренно
// «открытая»: у триггеров, условий и действий есть только вид (Type) и параметры, а
// смысл параметров знает реализация вида в точке расширения (модуль engine, hotkeys,
// плагины). Поэтому новый вид действия не требует изменений в этом пакете.
//
// Формат в YAML:
//
//	trigger: {type: hotkey, keys: "{F8}", on: toggle}   # вид — ключ type, остальное — параметры
//	conditions:
//	  - {type: variable, name: clicks, op: "<", value: 100}
//	actions:
//	  - send: "{Mouse0}[50]"                            # вид — единственный ключ, значение — параметры
//	  - repeat: {while: toggled, do: [{send: "{A}"}]}
//
// Пакет только разбирает и проверяет структуру. Проверку параметров конкретных видов
// выполняет модуль engine по реестрам точек расширения.
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// CurrentVersion — текущая версия схемы файла проекта.
const CurrentVersion = 1

// FileSuffix — окончание имени файла проекта.
const FileSuffix = ".mkey.yaml"

// Политики повторного запуска события (FR-EV-5).
const (
	PolicyIgnore   = "ignore"
	PolicyRestart  = "restart"
	PolicyQueue    = "queue"
	PolicyParallel = "parallel"
)

// idRe — допустимый идентификатор события или переменной.
var idRe = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_\-]*$`)

// Project — содержимое одного файла проекта.
type Project struct {
	// ID — идентификатор проекта: имя файла без ".mkey.yaml" (в самом файле не хранится).
	ID string `yaml:"-" json:"id"`
	// Version — версия схемы файла.
	Version int `yaml:"version" json:"version"`
	// Name — название проекта для пользователя.
	Name string `yaml:"name" json:"name"`
	// Enabled — включён ли проект (по умолчанию — да).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// ActiveWhen — условия, при которых активны все события проекта (например, окно игры).
	ActiveWhen []Condition `yaml:"active_when,omitempty" json:"active_when,omitempty"`
	// Variables — переменные проекта с начальными значениями.
	Variables map[string]Variable `yaml:"variables,omitempty" json:"variables,omitempty"`
	// Events — события проекта по порядку.
	Events []Event `yaml:"events" json:"events"`
	// Remaps — переназначения клавиш 1:1 (FR-HK-4), работают прямо в потоке ввода.
	Remaps []Remap `yaml:"remaps,omitempty" json:"remaps,omitempty"`
}

// Remap — переназначение клавиши: нажатие From система видит как To.
type Remap struct {
	// From — исходная клавиша, например "{CapsLock}" или "CapsLock".
	From string `yaml:"from" json:"from"`
	// To — клавиша, которую увидит система.
	To string `yaml:"to" json:"to"`
	// Device — только для устройства, в имени которого есть эта строка (пусто — для всех).
	Device string `yaml:"device,omitempty" json:"device,omitempty"`
}

// IsEnabled сообщает, включён ли проект.
func (p Project) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// Variable — переменная проекта (FR-EV-6).
type Variable struct {
	// Type — тип: int, float, string, bool.
	Type string `yaml:"type" json:"type"`
	// Value — начальное значение.
	Value any `yaml:"value" json:"value"`
	// Persist — сохранять значение между перезапусками.
	Persist bool `yaml:"persist,omitempty" json:"persist,omitempty"`
}

// Event — событие: триггеры, условия и действия.
type Event struct {
	// ID — идентификатор события внутри проекта.
	ID string `yaml:"id" json:"id"`
	// Name — название для пользователя.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Enabled — включено ли событие (по умолчанию — да).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Trigger — единственный триггер (краткая форма).
	Trigger *Trigger `yaml:"trigger,omitempty" json:"trigger,omitempty"`
	// Triggers — несколько триггеров: событие срабатывает от любого.
	Triggers []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	// Conditions — условия, которые должны выполняться все.
	Conditions []Condition `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	// Actions — действия по порядку.
	Actions []Action `yaml:"actions" json:"actions"`
	// Policy — что делать, если событие сработало во время своего выполнения: ignore (по умолчанию), restart, queue, parallel.
	Policy string `yaml:"policy,omitempty" json:"policy,omitempty"`
	// MaxParallel — предел одновременных запусков для политики parallel (по умолчанию 4).
	MaxParallel int `yaml:"max_parallel,omitempty" json:"max_parallel,omitempty"`
	// ReleaseModifiers — отпускать ли модификаторы горячей клавиши перед действиями: auto, always, never (FR-HK-3).
	ReleaseModifiers string `yaml:"release_modifiers,omitempty" json:"release_modifiers,omitempty"`
}

// IsEnabled сообщает, включено ли событие.
func (e Event) IsEnabled() bool { return e.Enabled == nil || *e.Enabled }

// AllTriggers возвращает все триггеры события (краткая и полная формы вместе).
func (e Event) AllTriggers() []Trigger {
	out := append([]Trigger{}, e.Triggers...)
	if e.Trigger != nil {
		out = append([]Trigger{*e.Trigger}, out...)
	}
	return out
}

// Trigger — триггер: вид и параметры.
type Trigger struct {
	// Type — вид триггера (hotkey, timer, device…).
	Type string `json:"type"`
	// Params — параметры вида.
	Params map[string]any `json:"params,omitempty"`
}

// Condition — условие: вид и параметры (у групп any/all/not параметры содержат вложенные условия).
type Condition struct {
	// Type — вид условия (variable, key_state, any…).
	Type string `json:"type"`
	// Params — параметры вида.
	Params map[string]any `json:"params,omitempty"`
}

// Action — действие: вид и значение (строка, число или карта параметров).
type Action struct {
	// Type — вид действия (send, repeat, lua…).
	Type string `json:"type"`
	// Value — параметры действия как в YAML.
	Value any `json:"value,omitempty"`
}

// UnmarshalYAML разбирает триггер вида {type: hotkey, keys: …}.
func (t *Trigger) UnmarshalYAML(n *yaml.Node) error {
	typ, params, err := typedMap(n)
	t.Type, t.Params = typ, params
	return err
}

// MarshalYAML записывает триггер обратно в форму {type: …, параметры…}.
func (t Trigger) MarshalYAML() (any, error) { return withType(t.Type, t.Params), nil }

// UnmarshalYAML разбирает условие вида {type: variable, …}.
func (c *Condition) UnmarshalYAML(n *yaml.Node) error {
	typ, params, err := typedMap(n)
	c.Type, c.Params = typ, params
	return err
}

// MarshalYAML записывает условие обратно в форму {type: …, параметры…}.
func (c Condition) MarshalYAML() (any, error) { return withType(c.Type, c.Params), nil }

// UnmarshalYAML разбирает действие — карту ровно с одним ключом: {send: "{A}"}.
func (a *Action) UnmarshalYAML(n *yaml.Node) error {
	// Действие — карта с одним ключом.
	if n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return fmt.Errorf("line %d: an action must be a map with exactly one key, e.g. {send: \"{A}\"}", n.Line)
	}

	// Ключ — вид действия, значение — параметры.
	a.Type = n.Content[0].Value
	var v any
	if err := n.Content[1].Decode(&v); err != nil {
		return err
	}
	a.Value = v
	return nil
}

// MarshalYAML записывает действие обратно в форму {вид: значение}.
func (a Action) MarshalYAML() (any, error) { return map[string]any{a.Type: a.Value}, nil }

// typedMap разбирает карту с обязательным ключом type; остальные ключи — параметры.
func typedMap(n *yaml.Node) (string, map[string]any, error) {
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return "", nil, err
	}
	typ, _ := m["type"].(string)
	if typ == "" {
		return "", nil, fmt.Errorf("line %d: missing \"type\"", n.Line)
	}
	delete(m, "type")
	return typ, m, nil
}

// withType собирает карту {type: …, параметры…} для записи в YAML.
func withType(typ string, params map[string]any) map[string]any {
	out := map[string]any{"type": typ}
	for k, v := range params {
		out[k] = v
	}
	return out
}

// Parse разбирает содержимое файла проекта с идентификатором id и проверяет структуру.
func Parse(data []byte, id string) (Project, error) {
	// Строгий разбор: неизвестные ключи верхнего уровня и событий — ошибка (опечатки).
	var p Project
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Project{}, fmt.Errorf("%s: %w", id, err)
	}
	p.ID = id

	// Версия схемы.
	if p.Version == 0 {
		p.Version = CurrentVersion
	}
	if p.Version > CurrentVersion {
		return Project{}, fmt.Errorf("%s: version %d is newer than supported %d", id, p.Version, CurrentVersion)
	}
	return p, Check(p)
}

// Check проверяет структуру проекта: идентификаторы, наличие триггеров и действий, политики.
func Check(p Project) error {
	var errs []error
	seen := map[string]bool{}
	for i, e := range p.Events {
		// Идентификатор события обязателен и уникален.
		where := fmt.Sprintf("event #%d", i+1)
		switch {
		case e.ID == "":
			errs = append(errs, fmt.Errorf("%s: missing id", where))
		case !idRe.MatchString(e.ID):
			errs = append(errs, fmt.Errorf("%s: invalid id %q", where, e.ID))
		case seen[e.ID]:
			errs = append(errs, fmt.Errorf("%s: duplicate id %q", where, e.ID))
		}
		seen[e.ID] = true

		// Триггер и действия.
		if len(e.AllTriggers()) == 0 {
			errs = append(errs, fmt.Errorf("event %q: no trigger", e.ID))
		}
		if len(e.Actions) == 0 {
			errs = append(errs, fmt.Errorf("event %q: no actions", e.ID))
		}

		// Допустимые значения политик.
		switch e.Policy {
		case "", PolicyIgnore, PolicyRestart, PolicyQueue, PolicyParallel:
		default:
			errs = append(errs, fmt.Errorf("event %q: unknown policy %q", e.ID, e.Policy))
		}
		switch e.ReleaseModifiers {
		case "", "auto", "always", "never":
		default:
			errs = append(errs, fmt.Errorf("event %q: unknown release_modifiers %q", e.ID, e.ReleaseModifiers))
		}
	}

	// Переназначения: обе клавиши указаны.
	for i, r := range p.Remaps {
		if r.From == "" || r.To == "" {
			errs = append(errs, fmt.Errorf("remap #%d: both from and to are required", i+1))
		}
	}

	// Переменные: имя и тип.
	for name, v := range p.Variables {
		if !idRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("variable %q: invalid name", name))
		}
		switch v.Type {
		case "int", "float", "string", "bool":
		default:
			errs = append(errs, fmt.Errorf("variable %q: unknown type %q", name, v.Type))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %w", p.ID, errors.Join(errs...))
	}
	return nil
}

// Decode переводит параметры (карту или значение из YAML) в структуру out строго:
// неизвестные поля — ошибка. Используется реализациями видов триггеров, условий и действий.
func Decode(value any, out any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// DecodeActions переводит вложенный список действий (например, do у repeat) в []Action.
func DecodeActions(value any) ([]Action, error) {
	// Список карт с одним ключом.
	list, ok := value.([]any)
	if !ok {
		return nil, errors.New("expected a list of actions")
	}
	out := make([]Action, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok || len(m) != 1 {
			return nil, fmt.Errorf("action #%d: must be a map with exactly one key", i+1)
		}
		for k, v := range m {
			out = append(out, Action{Type: k, Value: v})
		}
	}
	return out, nil
}

// DecodeConditions переводит вложенный список условий (например, у any/all) в []Condition.
func DecodeConditions(value any) ([]Condition, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, errors.New("expected a list of conditions")
	}
	out := make([]Condition, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("condition #%d: must be a map", i+1)
		}
		typ, _ := m["type"].(string)
		if typ == "" {
			return nil, fmt.Errorf("condition #%d: missing type", i+1)
		}
		params := map[string]any{}
		for k, v := range m {
			if k != "type" {
				params[k] = v
			}
		}
		out = append(out, Condition{Type: typ, Params: params})
	}
	return out, nil
}

// IDFromFile возвращает идентификатор проекта по имени файла ("games.mkey.yaml" → "games").
func IDFromFile(name string) (string, bool) {
	id, ok := strings.CutSuffix(name, FileSuffix)
	return id, ok && id != ""
}
