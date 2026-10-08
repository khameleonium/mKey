package contracts

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/lib/dsl"
	"github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Привязки «физический ввод → виртуальный выход» (FR-VD-3, ADR-0028): проверка привязки и её
// настроек. Общая для движка (проверка проекта при включении) и модуля hotkeys (исполнение),
// чтобы правила были в одном месте.

// Пределы настроек привязок.
const (
	// MaxDeadzone — мёртвая зона больше 0.9 оставила бы от хода стика почти ничего.
	MaxDeadzone = 0.9
	// MaxSensitivity — чувствительность больше 10 превращает любое движение в упор.
	MaxSensitivity = 10
	// MaxRampMS — плавный наклон дольше 5 с не нужен ни в одной игре.
	MaxRampMS = 5000
	// MaxRecenterMS — возврат руля в центр дольше 10 с от «держит положение» не отличить.
	MaxRecenterMS = 10000
	// MinCurve и MaxCurve — пределы кривой отклика: за ними ось почти не двигается или сразу в упоре.
	MinCurve = 0.2
	MaxCurve = 5
)

// CompiledBinding — проверенная привязка, готовая к исполнению.
type CompiledBinding struct {
	// From — источник: кнопка (Type EV_KEY), ось (EV_ABS) или ось мыши (EV_REL).
	From DeviceKey
	// To — цель; To.Value — положение оси для «кнопка → ось».
	To BindingTarget
	// Hide — прятать источник от системы.
	Hide bool
	// Invert, Deadzone, Sensitivity (1, если не задана), Threshold, Ramp, Steer, Recenter, Curve
	// (1, если не задана) — настройки (project.Binding).
	Invert      bool
	Deadzone    float64
	Sensitivity float64
	Threshold   float64
	Ramp        time.Duration
	Steer       bool
	Recenter    time.Duration
	Curve       float64
}

// CompileBinding проверяет привязку b с уже разобранным источником src (кнопка или ось, см.
// KeyState.ParseBindingSource) и разбирает её цель (ParseBindingTo). Какие настройки допустимы,
// зависит от вида источника и цели:
//
//	кнопка → кнопка   без настроек
//	кнопка → ось      value (−1…1, не 0), ramp_ms
//	ось → ось         invert, deadzone, sensitivity, curve
//	ось → кнопка      threshold (−1…1, не 0), invert, deadzone
//	мышь → ось        invert, sensitivity, steer, recenter_ms (только со steer)
//	мышь → кнопка     threshold (знак — направление), invert
//
// Ошибка — *dsl.Error: ошибки ParseBindingTo, dsl.binding_value, dsl.axis_expected,
// dsl.binding_threshold, dsl.binding_option, dsl.binding_range.
func CompileBinding(b project.Binding, src DeviceKey, own []project.VirtualDevice, vd VirtualDeviceManager) (CompiledBinding, error) {
	// Цель.
	to, err := ParseBindingTo(b.To, own, vd)
	if err != nil {
		return CompiledBinding{}, err
	}
	name := strings.Trim(b.From, "{} ") + " → " + strings.Trim(b.To, "{} ")
	c := CompiledBinding{From: src, To: to, Hide: b.Hide, Invert: b.Invert, Deadzone: b.Deadzone,
		Sensitivity: b.Sensitivity, Threshold: b.Threshold, Ramp: time.Duration(b.RampMS) * time.Millisecond,
		Steer: b.Steer, Recenter: time.Duration(b.RecenterMS) * time.Millisecond, Curve: b.Curve}
	if c.Sensitivity == 0 {
		c.Sensitivity = 1
	}
	if c.Curve == 0 {
		c.Curve = 1
	}

	// Какие настройки заданы и какие допустимы для этого вида привязки.
	axisSrc := src.Type == evdev.EvAbs || src.Type == evdev.EvRel
	set := map[string]bool{
		"value": b.Value != 0, "ramp_ms": b.RampMS != 0, "invert": b.Invert, "deadzone": b.Deadzone != 0,
		"sensitivity": b.Sensitivity != 0, "threshold": b.Threshold != 0, "steer": b.Steer,
		"recenter_ms": b.RecenterMS != 0, "curve": b.Curve != 0,
	}
	var allowed []string
	switch {
	case !axisSrc && to.Axis:
		allowed = []string{"value", "ramp_ms"}
	case !axisSrc:
		allowed = nil
	case src.Type == evdev.EvAbs && to.Axis:
		allowed = []string{"invert", "deadzone", "sensitivity", "curve"}
	case src.Type == evdev.EvAbs:
		allowed = []string{"threshold", "invert", "deadzone"}
	case to.Axis && b.Steer:
		allowed = []string{"invert", "sensitivity", "steer", "recenter_ms"}
	case to.Axis:
		allowed = []string{"invert", "sensitivity", "steer"}
	default:
		allowed = []string{"threshold", "invert"}
	}

	// Лишняя настройка — понятная ошибка; «положение у кнопки» — прежняя, более точная.
	for _, opt := range []string{"value", "ramp_ms", "invert", "deadzone", "sensitivity", "threshold", "steer", "recenter_ms", "curve"} {
		if !set[opt] || slices.Contains(allowed, opt) {
			continue
		}
		if opt == "value" && !to.Axis {
			return CompiledBinding{}, dsl.NewError(dsl.Pos{}, dsl.ErrAxisExpected, "name", strings.Trim(b.To, "{} "))
		}
		return CompiledBinding{}, dsl.NewError(dsl.Pos{}, dsl.ErrBindingOption, "name", name, "option", opt)
	}

	// Обязательные настройки: положение у «кнопка → ось», порог у «ось → кнопка».
	switch {
	case !axisSrc && to.Axis && (b.Value == 0 || b.Value < -1 || b.Value > 1):
		return CompiledBinding{}, dsl.NewError(dsl.Pos{}, dsl.ErrBindingValue, "name", strings.Trim(b.To, "{} "))
	case axisSrc && !to.Axis && (b.Threshold == 0 || b.Threshold < -1 || b.Threshold > 1):
		return CompiledBinding{}, dsl.NewError(dsl.Pos{}, dsl.ErrBindingThreshold, "name", name)
	}
	to.Value = b.Value
	c.To = to

	// Пределы значений (0 у кривой — «не задана», поэтому её проверяем, только если задана).
	for _, r := range []struct {
		opt      string
		v        float64
		min, max float64
		set      bool
	}{
		{"deadzone", b.Deadzone, 0, MaxDeadzone, true},
		{"sensitivity", b.Sensitivity, 0, MaxSensitivity, true},
		{"ramp_ms", float64(b.RampMS), 0, MaxRampMS, true},
		{"recenter_ms", float64(b.RecenterMS), 0, MaxRecenterMS, true},
		{"curve", b.Curve, MinCurve, MaxCurve, b.Curve != 0},
	} {
		if r.set && (r.v < r.min || r.v > r.max) {
			return CompiledBinding{}, dsl.NewError(dsl.Pos{}, dsl.ErrBindingRange, "name", name, "option", r.opt,
				"min", strconv.FormatFloat(r.min, 'g', -1, 64), "max", strconv.FormatFloat(r.max, 'g', -1, 64))
		}
	}
	return c, nil
}
