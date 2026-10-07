package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Встроенные виды триггеров, условий и действий регистрируются в точках расширения так же,
// как их регистрировали бы другие модули или плагины (SPEC §4.3). Чтобы не писать отдельный
// тип на каждый вид, используются обёртки с функциями.

// builtinTrigger — вид триггера из функции взведения.
type builtinTrigger struct {
	meta     contracts.ExtensionMeta
	validate func(t project.Trigger) error
	arm      func(ctx context.Context, ref contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error)
}

// Meta возвращает метаданные.
func (b builtinTrigger) Meta() contracts.ExtensionMeta { return b.meta }

// Validate проверяет параметры.
func (b builtinTrigger) Validate(t project.Trigger) error { return b.validate(t) }

// Arm взводит триггер.
func (b builtinTrigger) Arm(ctx context.Context, ref contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	return b.arm(ctx, ref, t, fire)
}

// builtinCondition — вид условия из функций проверки параметров и вычисления.
type builtinCondition struct {
	meta     contracts.ExtensionMeta
	validate func(c project.Condition) error
	check    func(ctx context.Context, rc contracts.RunContext, c project.Condition) (bool, error)
}

// Meta возвращает метаданные.
func (b builtinCondition) Meta() contracts.ExtensionMeta { return b.meta }

// Validate проверяет параметры.
func (b builtinCondition) Validate(c project.Condition) error { return b.validate(c) }

// Check вычисляет условие.
func (b builtinCondition) Check(ctx context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
	return b.check(ctx, rc, c)
}

// builtinAction — вид действия из функций проверки параметров и выполнения.
type builtinAction struct {
	meta     contracts.ExtensionMeta
	validate func(a project.Action) error
	run      func(ctx context.Context, rc contracts.RunContext, a project.Action) error
	// toDSL переводит значение действия в макрос; nil — действие нельзя записать макросом.
	toDSL func(any) (string, error)
	// dry описывает действие для сухого прогона; nil — по макросу toDSL.
	dry func(ctx context.Context, d *dryRun, a project.Action) error
}

// Meta возвращает метаданные.
func (b builtinAction) Meta() contracts.ExtensionMeta { return b.meta }

// Validate проверяет параметры.
func (b builtinAction) Validate(a project.Action) error { return b.validate(a) }

// Run выполняет действие.
func (b builtinAction) Run(ctx context.Context, rc contracts.RunContext, a project.Action) error {
	return b.run(ctx, rc, a)
}

// DryRun описывает действие для сухого прогона (contracts.ActionDryRunner): своим описанием dry
// или шагами макроса toDSL. Таймлайн строит только движок, поэтому dc — всегда *dryRun.
func (b builtinAction) DryRun(ctx context.Context, dc contracts.DryRunContext, a project.Action) error {
	d, ok := dc.(*dryRun)
	switch {
	case !ok:
		return fmt.Errorf("dry run %s: foreign timeline %T", b.meta.ID, dc)
	case b.dry != nil:
		return b.dry(ctx, d, a)
	case b.toDSL != nil:
		src, err := b.toDSL(a.Value)
		if err != nil {
			return err
		}
		return d.Send(src)
	}
	return d.add(contracts.DryStep{Kind: contracts.DryAction, Action: b.meta.ID})
}

// meta собирает метаданные встроенного вида: i18n-ключи по шаблону "<точка>.<id>", схема параметров.
func meta(point, id, category, schema string) contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: id, NameKey: point + "." + id, DescriptionKey: point + "." + id + ".description",
		Category: category, Provider: ModuleID, ParamsSchema: json.RawMessage(schema),
	}
}

// registerBuiltins регистрирует встроенные виды триггеров, условий и действий движка.
func (m *Module) registerBuiltins(ext contracts.ExtensionRegistry) error {
	for _, t := range m.builtinTriggers() {
		if err := ext.Register(contracts.PointTrigger, t); err != nil {
			return err
		}
	}
	for _, c := range m.builtinConditions() {
		if err := ext.Register(contracts.PointCondition, c); err != nil {
			return err
		}
	}
	for _, a := range m.builtinActions() {
		if err := ext.Register(contracts.PointAction, a); err != nil {
			return err
		}
	}
	return nil
}
