package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
)

// Касания виртуального сенсорного экрана из макросов ({Touch 50% 80%}, {Swipe …}, T12.1).
// Сенсорный экран — виртуальное устройство проекта с шаблоном touchscreen; без имени в макросе
// берётся единственный такой экран включённых проектов. Координаты — проценты экрана или пиксели
// (размер экрана — contracts.ScreenInfo).

// swipeStep — шаг движения пальца при свайпе: 10 мс — плавно для программ (100 обновлений
// в секунду) и не перегружает устройство.
const swipeStep = 10 * time.Millisecond

// touch выполняет касание или свайп.
func (m *Module) touch(ctx context.Context, r *run, s dsl.Step) error {
	// Сенсорный экран и точки в долях экрана.
	name, ts, err := m.touchscreen(s.Device)
	if err != nil {
		return err
	}
	pts := make([][2]float64, 0, len(s.Points))
	for _, p := range s.Points {
		x, y, err := m.screenFraction(ctx, p)
		if err != nil {
			return err
		}
		pts = append(pts, [2]float64{x, y})
	}

	// Палец на экране; при любом выходе (и отмене) — оторвать.
	if err := ts.TouchDown(ctx, pts[0][0], pts[0][1]); err != nil {
		return err
	}
	r.mu.Lock()
	r.touched[name] = true
	r.mu.Unlock()
	lift := func() error {
		r.mu.Lock()
		delete(r.touched, name)
		r.mu.Unlock()
		return ts.TouchUp(context.WithoutCancel(ctx))
	}

	// Касание: удержание (заданное или как у нажатия клавиши), затем отрыв.
	if s.Kind == dsl.StepTouch {
		hold := time.Duration(m.cfg.KeyHoldMS) * time.Millisecond
		if s.HoldMS > 0 {
			hold = time.Duration(s.HoldMS) * time.Millisecond
		}
		if err := m.clk.Sleep(ctx, hold); err != nil {
			_ = lift()
			return err
		}
		return lift()
	}

	// Свайп: палец плавно ведётся от начала до конца за HoldMS, затем отрыв.
	total := time.Duration(s.HoldMS) * time.Millisecond
	steps := max(1, int(total/swipeStep))
	for i := 1; i <= steps; i++ {
		if err := m.clk.Sleep(ctx, total/time.Duration(steps)); err != nil {
			_ = lift()
			return err
		}
		f := float64(i) / float64(steps)
		x := pts[0][0] + (pts[1][0]-pts[0][0])*f
		y := pts[0][1] + (pts[1][1]-pts[0][1])*f
		if err := ts.TouchMove(ctx, x, y); err != nil {
			_ = lift()
			return err
		}
	}
	return lift()
}

// touchscreen находит сенсорный экран: по имени или единственный среди включённых проектов.
func (m *Module) touchscreen(name string) (string, contracts.TouchSetter, error) {
	if m.vdevs == nil {
		return "", nil, dsl.NewError(dsl.Pos{}, dsl.ErrNoTouchscreen)
	}

	// Без имени — единственный сенсорный экран.
	if name == "" {
		var found []string
		for _, d := range m.vdevs.List() {
			if d.Template == contracts.TemplateTouchscreen && d.Error == "" {
				found = append(found, d.Name)
			}
		}
		switch len(found) {
		case 0:
			return "", nil, dsl.NewError(dsl.Pos{}, dsl.ErrNoTouchscreen)
		case 1:
			name = found[0]
		default:
			return "", nil, dsl.NewError(dsl.Pos{}, dsl.ErrManyTouchscreens, "list", strings.Join(found, ", "))
		}
	}

	// Устройство и его касания.
	dev, err := m.vdevs.Device(strings.ToLower(name))
	if err != nil {
		return "", nil, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownDevice, "device", name)
	}
	ts, ok := dev.(contracts.TouchSetter)
	if !ok {
		return "", nil, dsl.NewError(dsl.Pos{}, dsl.ErrNotTouchscreen, "device", name)
	}
	return strings.ToLower(name), ts, nil
}

// screenFraction переводит точку макроса в доли экрана 0…1: проценты — делением на 100, пиксели —
// по размеру экрана (contracts.ScreenInfo).
func (m *Module) screenFraction(ctx context.Context, p dsl.Point) (float64, float64, error) {
	if p.X.Percent && p.Y.Percent {
		return p.X.Value / 100, p.Y.Value / 100, nil
	}
	if m.screen == nil {
		return 0, 0, dsl.NewError(dsl.Pos{}, dsl.ErrScreenUnknown)
	}
	w, h, err := m.screen.ScreenSize(ctx)
	if errors.Is(err, contracts.ErrUnsupported) {
		return 0, 0, dsl.NewError(dsl.Pos{}, dsl.ErrScreenUnknown)
	}
	if err != nil {
		return 0, 0, err
	}
	frac := func(c dsl.Coord, size int) float64 {
		if c.Percent {
			return c.Value / 100
		}
		return min(1, c.Value/float64(max(1, size-1)))
	}
	return frac(p.X, w), frac(p.Y, h), nil
}
