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
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/khameleonium/mKey/internal/lib/devmap"
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
	// Devices — имена кнопок устройств, которые встречаются в событиях ({Геймпад.Старт},
	// ADR-0027): mKey дописывает их при сохранении проекта в файл, а при загрузке проекта такое
	// же устройство получает эти имена.
	Devices []devmap.Names `yaml:"devices,omitempty" json:"devices,omitempty"`
	// VirtualDevices — виртуальные устройства проекта (FR-VD-1, -2, решение владельца): существуют,
	// пока проект включён; в макросах — {pad2.South}, в системе — «mKey pad2».
	VirtualDevices []VirtualDevice `yaml:"virtual_devices,omitempty" json:"virtual_devices,omitempty"`
	// Bindings — привязки «физический ввод → виртуальный выход» (FR-VD-3): работают прямо в потоке
	// ввода, пока проект включён.
	Bindings []Binding `yaml:"bindings,omitempty" json:"bindings,omitempty"`
}

// Binding — привязка: нажатие From передаётся на To.
//   - кнопка → кнопка: from: "{W}", to: "{pad2.DPadUp}" (или клавиша mKey: to: "{Space}");
//   - кнопка → ось: from: "{A}", to: "{pad2.LX}", value: -1 — пока From нажата, ось в этом
//     положении; отпустили — ось в покое (или в положении другой нажатой кнопки этой оси).
type Binding struct {
	// From — физическая кнопка, как в горячих клавишах: "{W}", "{Геймпад.Старт}", "{UnKey001}".
	From string `yaml:"from" json:"from"`
	// To — кнопка или ось виртуального устройства проекта ("{pad2.South}", "{pad2.LX}") или
	// клавиша mKey ("{Space}").
	To string `yaml:"to" json:"to"`
	// Value — положение оси при нажатии From (для To-оси): −1…1, у курков 0…1.
	Value float64 `yaml:"value,omitempty" json:"value,omitempty"`
	// Hide — спрятать нажатие From от системы (устройство захватывается), чтобы программы видели
	// только виртуальное устройство.
	Hide bool `yaml:"hide,omitempty" json:"hide,omitempty"`

	// Настройки для осей (docs/projects.md, «Привязки осей»). From может быть осью: стик
	// ("{Геймпад.LX}", "{UnKey.Axis01}", "{LX}" — любого геймпада) или мышью ("{MouseX}",
	// "{MouseWheel}").

	// Invert — перевернуть ось-источник (влево ↔ вправо, у курка — отпущен ↔ нажат).
	Invert bool `yaml:"invert,omitempty" json:"invert,omitempty"`
	// Deadzone — мёртвая зона стика 0…0.9: отклонения меньше неё считаются центром (стик не
	// «дрожит»), остальное растягивается на весь ход.
	Deadzone float64 `yaml:"deadzone,omitempty" json:"deadzone,omitempty"`
	// Sensitivity — чувствительность 0…10 (0 — 1): ось → ось — множитель отклонения; мышь →
	// стик — насколько быстрое движение мыши даёт полный наклон.
	Sensitivity float64 `yaml:"sensitivity,omitempty" json:"sensitivity,omitempty"`
	// Threshold — ось → кнопка: при каком наклоне нажимать кнопку, −1…1 (знак — направление:
	// −0.5 — наклон влево/вверх наполовину); у мыши знак задаёт направление движения.
	Threshold float64 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	// RampMS — кнопка → ось: за сколько миллисекунд ось плавно доходит до положения Value
	// (0 — сразу), 0…5000.
	RampMS int `yaml:"ramp_ms,omitempty" json:"ramp_ms,omitempty"`
	// Steer — мышь → ось «как руль» (ADR-0040): движение мыши поворачивает ось, и она остаётся
	// в этом положении, когда мышь остановилась (без Steer — возвращается в центр).
	Steer bool `yaml:"steer,omitempty" json:"steer,omitempty"`
	// RecenterMS — с Steer: за сколько миллисекунд ось сама возвращается из упора в центр,
	// когда мышь не движется (0 — держит положение), 0…10000.
	RecenterMS int `yaml:"recenter_ms,omitempty" json:"recenter_ms,omitempty"`
	// Curve — ось → ось: кривая отклика 0.2…5 (0 — 1, прямая): больше 1 — точнее у центра и
	// быстрее к краю, меньше 1 — наоборот.
	Curve float64 `yaml:"curve,omitempty" json:"curve,omitempty"`
}

// VirtualDevice — виртуальное устройство проекта.
type VirtualDevice struct {
	// Name — имя в макросах ({pad2.South}): буквы, цифры и «_», начинается с буквы.
	Name string `yaml:"name" json:"name"`
	// Template — шаблон: xbox360, ds4, wheel, flightstick, joystick, touchscreen, keyboard, mouse или custom.
	Template string `yaml:"template" json:"template"`
	// Buttons и Axes — набор кнопок и осей для шаблона custom: кнопки — именами mKey ("South")
	// или ядра ("BTN_TRIGGER"), оси — именами ("LX", "ABS_THROTTLE") с диапазонами.
	Buttons []string             `yaml:"buttons,omitempty" json:"buttons,omitempty"`
	Axes    map[string]AxisRange `yaml:"axes,omitempty" json:"axes,omitempty"`
}

// AxisRange — диапазон оси виртуального устройства.
type AxisRange struct {
	Min int32 `yaml:"min" json:"min"`
	Max int32 `yaml:"max" json:"max"`
}

// vdevNameRe — имя виртуального устройства: буква, затем буквы, цифры и «_» (как имена в макросах).
var vdevNameRe = regexp.MustCompile(`^\p{L}[\p{L}\p{N}_]*$`)

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
func (t Trigger) MarshalYAML() (any, error) { return withType(t.Type, t.Params) }

// UnmarshalYAML разбирает условие вида {type: variable, …}.
func (c *Condition) UnmarshalYAML(n *yaml.Node) error {
	typ, params, err := typedMap(n)
	c.Type, c.Params = typ, params
	return err
}

// MarshalYAML записывает условие обратно в форму {type: …, параметры…}.
func (c Condition) MarshalYAML() (any, error) { return withType(c.Type, c.Params) }

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

// withType собирает карту {type: …, параметры…} для записи в YAML: type — первым,
// параметры — следом по алфавиту (обычная карта Go записалась бы целиком по алфавиту).
func withType(typ string, params map[string]any) (*yaml.Node, error) {
	// Ключ type и его значение.
	out := &yaml.Node{Kind: yaml.MappingNode}
	out.Content = append(out.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: typ})

	// Параметры в постоянном порядке.
	for _, k := range slices.Sorted(maps.Keys(params)) {
		var v yaml.Node
		if err := v.Encode(params[k]); err != nil {
			return nil, fmt.Errorf("param %s: %w", k, err)
		}
		key := &yaml.Node{Kind: yaml.ScalarNode}
		if err := key.Encode(k); err != nil {
			return nil, err
		}
		out.Content = append(out.Content, key, &v)
	}
	return out, nil
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

		// Триггер обязателен. Действий может не быть: например, замена слова (hotstring
		// с replace) выполняется самим триггером, и больше ничего делать не нужно.
		if len(e.AllTriggers()) == 0 {
			errs = append(errs, &Problem{Event: e.ID, Part: PartTrigger, Index: -1, Err: ErrNoTrigger})
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

	// Имена кнопок устройств: модель и имена по правилам.
	for i, n := range p.Devices {
		if err := n.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("devices #%d: %w", i+1, err))
		}
	}

	// Виртуальные устройства: имя по правилам и без повторов, шаблон указан (сам шаблон и набор
	// кнопок проверяет модуль виртуальных устройств).
	vnames := map[string]bool{}
	for i, v := range p.VirtualDevices {
		switch {
		case !vdevNameRe.MatchString(v.Name):
			errs = append(errs, fmt.Errorf("virtual_devices #%d: invalid name %q (letters, digits and _, starting with a letter)", i+1, v.Name))
		case vnames[strings.ToLower(v.Name)]:
			errs = append(errs, fmt.Errorf("virtual_devices #%d: duplicate name %q", i+1, v.Name))
		case v.Template == "":
			errs = append(errs, fmt.Errorf("virtual_devices #%d: missing template", i+1))
		}
		vnames[strings.ToLower(v.Name)] = true
	}

	// Привязки: обе стороны указаны.
	for i, b := range p.Bindings {
		if strings.TrimSpace(b.From) == "" || strings.TrimSpace(b.To) == "" {
			errs = append(errs, fmt.Errorf("bindings #%d: both from and to are required", i+1))
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
