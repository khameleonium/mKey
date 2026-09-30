package recorder

import (
	"context"
	"encoding/json"
	"fmt"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
)

// playAction — действие play: воспроизвести запись из проекта (например, по горячей клавише).
//
//   - play: "игра"                                     # имя записи
//   - play: { name: "игра", speed: 2, repeat: 3 }
type playAction struct{ m *Module }

// playParams — параметры действия play.
type playParams struct {
	Name      string  `json:"name"`
	Speed     float64 `json:"speed"`
	Repeat    int     `json:"repeat"`
	SkipMoves bool    `json:"skip_moves"`
}

// Meta возвращает метаданные действия (форма блока в конструкторе строится по схеме).
func (playAction) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "play", NameKey: "action.play", DescriptionKey: "action.play.description",
		Category: "record", Icon: "play", Provider: ModuleID,
		ParamsSchema: json.RawMessage(`{"type":"object","required":["name"],"properties":{` +
			`"name":{"type":"string","x-widget":"recording"},` +
			`"speed":{"type":"number","minimum":0.1,"default":1},` +
			`"repeat":{"type":"integer","minimum":1,"default":1},` +
			`"skip_moves":{"type":"boolean","x-advanced":true}}}`),
	}
}

// params разбирает значение действия: строка — имя записи, карта — параметры.
func (playAction) params(v any) (playParams, error) {
	var p playParams
	if s, ok := v.(string); ok {
		p.Name = s
	} else if err := project.Decode(v, &p); err != nil {
		return p, err
	}
	switch {
	case p.Name == "":
		return p, project.Required("action", "play", "name")
	case p.Speed != 0 && (p.Speed < minSpeed || p.Speed > maxSpeed):
		return p, fmt.Errorf("speed must be between %g and %g", minSpeed, maxSpeed)
	case p.Repeat < 0:
		return p, fmt.Errorf("repeat must not be negative")
	}
	return p, nil
}

// Validate проверяет параметры (наличие файла записи проверяется при выполнении: её могут записать позже).
func (a playAction) Validate(act project.Action) error {
	_, err := a.params(act.Value)
	return err
}

// Run воспроизводит запись и ждёт окончания; остановка события прерывает воспроизведение.
func (a playAction) Run(ctx context.Context, _ contracts.RunContext, act project.Action) error {
	p, err := a.params(act.Value)
	if err != nil {
		return err
	}
	return a.m.Play(ctx, p.Name, contracts.PlayOptions{Speed: p.Speed, Repeat: p.Repeat, SkipMoves: p.SkipMoves})
}
