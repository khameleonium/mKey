package engine

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/dsl"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// Сухой прогон события (FR-UI-6, contracts.DryRunner): действия описываются строками таймлайна
// со временем начала, ничего не нажимается и не выполняется. Время считается по тем же правилам,
// что и при выполнении (runner.go): нажатие {A} — удержание key_hold_ms и пауза key_delay_ms,
// символ текста — так же, пауза со случайной длительностью — по наименьшей.

// maxDrySteps — наибольшее число строк таймлайна: дальше таймлайн обрезается (Truncated).
const maxDrySteps = 1000

// errDryFull — таймлайн заполнен; прогон заканчивается, результат помечается Truncated.
var errDryFull = errors.New("dry run: too many steps")

// dryRun — один сухой прогон: таймлайн, текущее время и вложенность (contracts.DryRunContext).
type dryRun struct {
	m       *Module
	project string
	out     contracts.DryRun
	// at — время начала следующего шага, мс; depth — текущая вложенность.
	at    int64
	depth int
}

// DryRun описывает событие ev проекта projectID, ничего не выполняя (contracts.DryRunner).
func (m *Module) DryRun(ctx context.Context, projectID string, ev project.Event) (contracts.DryRun, error) {
	d := &dryRun{m: m, project: projectID, out: contracts.DryRun{Steps: []contracts.DryStep{}}}

	// Условия события: без них оно не выполнится вовсе.
	if len(ev.Conditions) > 0 {
		_ = d.add(contracts.DryStep{Kind: contracts.DryNote, Key: "dry.event_conditions", Conditions: dryConditions(ev.Conditions)})
	}

	// Действия. Переполнение таймлайна — не ошибка: показываем начало и пометку «обрезано».
	err := d.RunActions(ctx, ev.Actions)
	if errors.Is(err, errDryFull) {
		d.out.Truncated, err = true, nil
	}
	if err != nil {
		return contracts.DryRun{}, err
	}
	d.out.TotalMS = d.at
	return d.out, nil
}

// Project возвращает проект прогоняемого события.
func (d *dryRun) Project() string { return d.project }

// add добавляет строку таймлайна на текущем времени и уровне.
func (d *dryRun) add(s contracts.DryStep) error {
	if len(d.out.Steps) >= maxDrySteps {
		return errDryFull
	}
	s.AtMS, s.Depth = d.at, d.depth
	d.out.Steps = append(d.out.Steps, s)
	return nil
}

// Note добавляет строку о действии, которое прогон описывает, но не выполняет.
func (d *dryRun) Note(key string, args map[string]string) {
	_ = d.add(contracts.DryStep{Kind: contracts.DryNote, Key: key, Args: args})
}

// Spend сдвигает время таймлайна: описанное действие длится ms миллисекунд.
func (d *dryRun) Spend(ms int64) { d.at += max(ms, 0) }

// RunActions описывает действия по порядку: у вида с ActionDryRunner — его описанием, у
// остальных — строкой «выполнится при запуске».
func (d *dryRun) RunActions(ctx context.Context, actions []project.Action) error {
	for _, a := range actions {
		// Отмена (окно закрыли) прерывает прогон.
		if err := ctx.Err(); err != nil {
			return err
		}
		at, err := d.m.actionType(a.Type)
		if err != nil {
			return err
		}
		if dr, ok := at.(contracts.ActionDryRunner); ok {
			err = dr.DryRun(ctx, d, a)
		} else {
			err = d.add(contracts.DryStep{Kind: contracts.DryAction, Action: a.Type})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Group добавляет заголовок группы и описывает её тело на уровень глубже. Тело показывается один
// раз; при times > 1 время прохода умножается на число повторов, при times == 0 (повтор «пока…»)
// длительность события помечается как заранее неизвестная.
func (d *dryRun) Group(_ context.Context, head contracts.DryStep, times int, body func() error) error {
	head.Kind = contracts.DryGroup
	if err := d.add(head); err != nil {
		return err
	}
	start := d.at
	d.depth++
	err := body()
	d.depth--
	switch {
	case times > 1:
		d.at = start + (d.at-start)*int64(times)
	case times == 0:
		d.out.Open = true
	}
	return err
}

// branches описывает «Если»: ветку then под заголовком с условиями и ветку els под «Иначе».
// Обе ветки начинаются в одно время; дальше время идёт от более долгой.
func (d *dryRun) branches(ctx context.Context, conds []project.Condition, then, els []project.Action) error {
	// Ветка «то».
	start := d.at
	err := d.Group(ctx, contracts.DryStep{Key: "dry.if", Conditions: dryConditions(conds)}, 1, func() error {
		return d.RunActions(ctx, then)
	})
	if err != nil || len(els) == 0 {
		return err
	}

	// Ветка «иначе» — с того же времени.
	thenEnd := d.at
	d.at = start
	err = d.Group(ctx, contracts.DryStep{Key: "dry.else"}, 1, func() error {
		return d.RunActions(ctx, els)
	})
	d.at = max(d.at, thenEnd)
	return err
}

// Send разбирает макрос и добавляет его шаги.
func (d *dryRun) Send(src string) error {
	steps, err := d.m.compileSource(src)
	if err != nil {
		return err
	}
	return d.steps(steps)
}

// steps добавляет шаги макроса и сдвигает время так же, как их выполнял бы раннер.
func (d *dryRun) steps(steps []dsl.Step) error {
	tm := d.m.Timing()
	hold, delay := int64(tm.KeyHoldMS), int64(tm.KeyDelayMS)
	for _, s := range steps {
		// Повтор внутри макроса — группа с телом один раз.
		if s.Kind == dsl.StepLoop {
			err := d.Group(context.Background(), contracts.DryStep{Key: "dry.loop", Count: s.Count}, s.Count, func() error {
				return d.steps(s.Body)
			})
			if err != nil {
				return err
			}
			continue
		}

		// Строка шага с его параметрами.
		row := contracts.DryStep{Kind: string(s.Kind), Count: s.Count, Text: s.Text, DX: s.DX, DY: s.DY, Value: s.Value, Device: s.Device}
		for _, t := range s.Targets {
			row.Keys = append(row.Keys, t.Name)
			_, physical := dsl.IsPhysical(t.Device) // имя кнопки уже с устройством: {Sega.Start}
			if row.Device == "" && !physical && t.Device != dsl.DeviceKeyboard && t.Device != dsl.DeviceMouse {
				row.Device = t.Device
			}
		}
		for _, p := range s.Points {
			row.Points = append(row.Points, coordText(p.X)+", "+coordText(p.Y))
		}

		// Длительность шага — как у раннера. Удержание в строке — только заданное в макросе
		// ({A 500}); обычное (key_hold_ms) не показывается, но учитывается во времени.
		var spent int64
		switch s.Kind {
		case dsl.StepTap:
			row.MS = s.HoldMS
			spent = int64(max(s.Count, 1)) * (cmp.Or(s.HoldMS, hold) + delay)
		case dsl.StepWait:
			row.MS, row.MaxMS = s.MinMS, s.MaxMS
			if row.MaxMS <= row.MS {
				row.MaxMS = 0
			}
			spent = s.MinMS
		case dsl.StepText:
			spent = int64(utf8.RuneCountInString(s.Text)) * (hold + delay)
		case dsl.StepWheel:
			spent = int64(max(s.Count, 1)) * delay
		case dsl.StepTouch:
			row.MS, spent = s.HoldMS, cmp.Or(s.HoldMS, hold)
		case dsl.StepSwipe:
			row.MS, spent = s.HoldMS, s.HoldMS
		}
		if err := d.add(row); err != nil {
			return err
		}
		d.at += spent
	}
	return nil
}

// coordText — координата для таймлайна: "50%" или "960".
func coordText(c dsl.Coord) string {
	s := strconv.FormatFloat(c.Value, 'f', -1, 64)
	if c.Percent {
		s += "%"
	}
	return s
}

// dryConditions — копия списка условий для строки таймлайна (строка не должна делить память
// с проектом, который прогоняется).
func dryConditions(conds []project.Condition) []project.Condition {
	return slices.Clone(conds)
}

// targetArgs — параметры строки о другом событии или проекте: project, event (пусто — весь проект).
func targetArgs(d contracts.DryRunContext, t eventTarget) map[string]string {
	if t.Project == "" {
		t.Project = d.Project()
	}
	return map[string]string{"project": t.Project, "event": t.Event}
}

var _ contracts.DryRunner = (*Module)(nil)
