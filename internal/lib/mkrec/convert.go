package mkrec

import (
	"math"
	"time"

	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/project"
)

// Превращение записи в блоки конструктора (FR-REC-6, T6.4): действия проекта, которые можно
// править и запускать как обычное событие.
//
// Правила:
//   - нажатие и отпускание клавиши — «Нажать клавишу» (tap), если клавиша была нажата не дольше
//     HoldMin, иначе «Удерживать клавишу» (hold);
//   - если пока клавиша нажата другая клавиша успела нажаться и отпуститься (Shift при наборе
//     заглавной) или двигалась мышь, крутилось колёсико (перетаскивание, движение в игре),
//     — «Зажать клавишу» (key_down) … «Отпустить клавишу» (key_up). Перекрытие при быстром наборе
//     (нажали «а», нажали «б», отпустили «а») остаётся двумя обычными нажатиями;
//   - щелчок левой, правой, средней и боковыми кнопками мыши — «Щелчок мыши» (mouse_click);
//   - колёсико — «Прокрутить колесо» (wheel); перемещения — «Сдвинуть мышь» (mouse_move),
//     по желанию упрощённые (меньше блоков, путь почти тот же);
//   - время между действиями — «Пауза» (pause); паузы короче MinPause не пишутся, их время
//     переносится на следующую паузу (общий темп сохраняется);
//   - запись с калибровкой (pointer center) начинается блоком «Курсор в центр экрана» (pointer_center).
//
// Геймпады, тач и оси пока не превращаются (их нельзя воспроизвести).

// ConvertOptions — параметры превращения.
type ConvertOptions struct {
	// Simplify — упрощать движения мыши (алгоритм Рамера — Дугласа — Пекера).
	Simplify bool
	// Tolerance — допустимое отклонение упрощённого пути, в точках (0 — 2 точки).
	Tolerance float64
	// MinPause — паузы короче не пишутся (0 — 20 мс).
	MinPause time.Duration
	// HoldMin — нажатие дольше становится «Удерживать клавишу» (0 — 300 мс).
	HoldMin time.Duration
}

// withDefaults подставляет значения по умолчанию.
func (o ConvertOptions) withDefaults() ConvertOptions {
	if o.Tolerance <= 0 {
		o.Tolerance = 2
	}
	if o.MinPause <= 0 {
		o.MinPause = 20 * time.Millisecond
	}
	if o.HoldMin <= 0 {
		o.HoldMin = 300 * time.Millisecond
	}
	return o
}

// ActionCenter — вид действия «Курсор в центр экрана» (регистрирует модуль recorder).
const ActionCenter = "pointer_center"

// clickNames — кнопки мыши, щелчок которыми записывается блоком «Щелчок мыши».
var clickNames = map[uint16]string{
	ev.BtnLeft: "Left", ev.BtnRight: "Right", ev.BtnMiddle: "Middle", ev.BtnSide: "Back", ev.BtnExtra: "Forward",
}

// step — действие с моментом начала и длительностью (для расчёта пауз между действиями).
type step struct {
	t   time.Duration
	dur time.Duration
	act project.Action
}

// press — нажатая и ещё не отпущенная клавиша.
type press struct {
	at   time.Duration
	code uint16
	// down — пока клавиша нажата, было другое действие, и для неё уже записано «Зажать клавишу».
	down bool
}

// ToActions превращает запись в действия проекта.
func ToActions(rec *Recording, opts ConvertOptions) []project.Action {
	opts = opts.withDefaults()
	var steps []step
	var held []*press
	var path []pathPoint // путь мыши с последнего действия (ещё не записанный)
	var pos pathPoint    // положение указателя относительно начала записи

	// flushMoves записывает накопленные перемещения блоками «Сдвинуть мышь».
	flushMoves := func() {
		steps = append(steps, moveSteps(path, opts)...)
		path = nil
	}
	// markBusy отмечает: пока нажаты клавиши hs, произошло другое действие.
	markBusy := func(hs []*press) {
		for _, h := range hs {
			if !h.down {
				// Первое постороннее действие: клавишу записываем как «Зажать» в момент нажатия.
				h.down = true
				steps = insertStep(steps, step{t: h.at, act: project.Action{Type: "key_down", Value: keyName(h.code)}})
			}
		}
	}

	for _, f := range rec.Frames {
		// Путь мыши: сдвиги действия складываются в одну точку пути (в момент действия).
		start, moved := pos, false
		for _, e := range f.Events {
			if e.Type == ev.EvRel && e.Code == ev.RelX {
				pos.x, moved = pos.x+float64(e.Value), true
			}
			if e.Type == ev.EvRel && e.Code == ev.RelY {
				pos.y, moved = pos.y+float64(e.Value), true
			}
		}
		if moved {
			if len(path) == 0 {
				path = append(path, pathPoint{t: f.T, x: start.x, y: start.y})
			}
			path = append(path, pathPoint{t: f.T, x: pos.x, y: pos.y})
			markBusy(held)
		}

		for _, e := range f.Events {
			switch {
			// Колёсико (обычное; высокое разрешение дублирует его и пропускается).
			case e.Type == ev.EvRel && (e.Code == ev.RelWheel || e.Code == ev.RelHwheel):
				flushMoves()
				markBusy(held)
				steps = append(steps, step{t: f.T, act: project.Action{Type: "wheel", Value: wheelValue(e)}})

			// Нажатие клавиши или кнопки (клавиатура и мышь; геймпады пока не превращаются).
			case e.Type == ev.EvKey && e.Value == ev.ValueDown && playable(e.Code):
				flushMoves()
				held = append(held, &press{at: f.T, code: e.Code})

			// Отпускание: одно нажатие, удержание или «Отпустить» после «Зажать».
			case e.Type == ev.EvKey && e.Value == ev.ValueUp && playable(e.Code):
				i := indexHeld(held, e.Code)
				if i < 0 {
					continue
				}
				flushMoves()
				h := held[i]
				held = append(held[:i], held[i+1:]...)

				// Клавиши, нажатые раньше и всё ещё нажатые, «держат» эту (Shift при наборе заглавной).
				var outer []*press
				for _, o := range held {
					if o.at <= h.at {
						outer = append(outer, o)
					}
				}
				markBusy(outer)

				if h.down {
					steps = append(steps, step{t: f.T, act: project.Action{Type: "key_up", Value: keyName(h.code)}})
					continue
				}
				steps = insertStep(steps, pressStep(h, f.T-h.at, opts))
			}
		}
	}

	// Конец записи: перемещения и не отпущенные клавиши («Зажать» без «Отпустить» отпустит mKey сам).
	flushMoves()
	for _, h := range held {
		if !h.down {
			steps = insertStep(steps, step{t: h.at, act: project.Action{Type: "key_down", Value: keyName(h.code)}})
		}
	}

	// Паузы между действиями; калибровка — в начале.
	out := withPauses(steps, opts.MinPause)
	if rec.Header.Centered {
		// Пустые параметры — карта, чтобы в файле было «pointer_center: {}», а не «null».
		out = append([]project.Action{{Type: ActionCenter, Value: map[string]any{}}}, out...)
	}
	return out
}

// pressStep — одно нажатие клавиши длительностью d: щелчок мыши, нажатие или удержание.
func pressStep(h *press, d time.Duration, opts ConvertOptions) step {
	name := keyName(h.code)
	switch {
	case d >= opts.HoldMin:
		return step{t: h.at, dur: d, act: project.Action{Type: "hold", Value: map[string]any{"key": name, "ms": int(d.Milliseconds())}}}
	case clickNames[h.code] != "":
		return step{t: h.at, act: project.Action{Type: "mouse_click", Value: clickNames[h.code]}}
	}
	return step{t: h.at, act: project.Action{Type: "tap", Value: name}}
}

// insertStep вставляет действие по времени (нажатие узнаётся в момент отпускания, а начинается раньше).
func insertStep(steps []step, s step) []step {
	i := len(steps)
	for i > 0 && steps[i-1].t > s.t {
		i--
	}
	steps = append(steps, step{})
	copy(steps[i+1:], steps[i:])
	steps[i] = s
	return steps
}

// withPauses расставляет паузы между действиями. Паузы короче minPause не пишутся, их время
// переносится вперёд, чтобы общий темп не сбивался.
func withPauses(steps []step, minPause time.Duration) []project.Action {
	var out []project.Action
	var cursor, carry time.Duration
	for i, s := range steps {
		gap := s.t - cursor + carry
		carry = 0
		switch {
		case i == 0:
			// Перед первым действием паузы нет: событие начинается сразу.
		case gap >= minPause:
			out = append(out, project.Action{Type: "pause", Value: int(gap.Milliseconds())})
		case gap > 0:
			carry = gap
		}
		out = append(out, s.act)
		cursor = max(cursor, s.t+s.dur)
	}
	return out
}

// pathPoint — точка пути указателя: время и положение относительно начала записи.
type pathPoint struct {
	t    time.Duration
	x, y float64
}

// moveSteps превращает путь указателя в блоки «Сдвинуть мышь» (каждый — от прошлой точки).
// При упрощении остаются только точки, без которых путь отклонился бы больше чем на Tolerance.
func moveSteps(path []pathPoint, opts ConvertOptions) []step {
	if len(path) < 2 {
		return nil
	}
	pts := path
	if opts.Simplify {
		pts = simplify(path, opts.Tolerance)
	}
	var out []step
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		ddx, ddy := int(math.Round(b.x))-int(math.Round(a.x)), int(math.Round(b.y))-int(math.Round(a.y))
		if ddx == 0 && ddy == 0 {
			continue
		}
		out = append(out, step{t: b.t, act: project.Action{Type: "mouse_move", Value: map[string]any{"dx": ddx, "dy": ddy}}})
	}
	return out
}

// simplify — алгоритм Рамера — Дугласа — Пекера: оставляет крайние точки и те, что отстоят
// от прямой между соседними оставленными больше чем на tol.
func simplify(pts []pathPoint, tol float64) []pathPoint {
	if len(pts) < 3 {
		return pts
	}
	a, b := pts[0], pts[len(pts)-1]
	far, idx := 0.0, 0
	for i := 1; i < len(pts)-1; i++ {
		if d := distToSegment(pts[i], a, b); d > far {
			far, idx = d, i
		}
	}
	if far <= tol {
		return []pathPoint{a, b}
	}
	left := simplify(pts[:idx+1], tol)
	right := simplify(pts[idx:], tol)
	return append(left[:len(left)-1], right...)
}

// distToSegment — расстояние от точки p до отрезка ab.
func distToSegment(p, a, b pathPoint) float64 {
	vx, vy := b.x-a.x, b.y-a.y
	wx, wy := p.x-a.x, p.y-a.y
	l2 := vx*vx + vy*vy
	if l2 == 0 {
		return math.Hypot(wx, wy)
	}
	t := max(0, min(1, (wx*vx+wy*vy)/l2))
	return math.Hypot(wx-t*vx, wy-t*vy)
}

// wheelValue — параметры блока «Прокрутить колесо»: направление и число щелчков.
func wheelValue(e ev.Event) map[string]any {
	dir := "Up"
	switch {
	case e.Code == ev.RelWheel && e.Value < 0:
		dir = "Down"
	case e.Code == ev.RelHwheel && e.Value > 0:
		dir = "Right"
	case e.Code == ev.RelHwheel:
		dir = "Left"
	}
	n := e.Value
	if n < 0 {
		n = -n
	}
	return map[string]any{"direction": dir, "count": int(n)}
}

// indexHeld ищет нажатую клавишу по коду.
func indexHeld(held []*press, code uint16) int {
	for i, h := range held {
		if h.code == code {
			return i
		}
	}
	return -1
}

// playable сообщает, что клавишу можно повторить: клавиатура и кнопки мыши (не геймпад).
func playable(code uint16) bool {
	return code < 0x100 || (code >= 0x110 && code <= 0x117) || code >= 0x160
}
