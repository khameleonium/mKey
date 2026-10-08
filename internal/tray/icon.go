package tray

import (
	"image"
	"image/color"
	"math"

	"github.com/khameleonium/mKey/internal/lib/sni"
)

// iconSizes — размеры картинок значка: панель трея выбирает ближайший к своему размеру.
var iconSizes = []int{16, 22, 24, 32, 48, 64}

// palette — цвета значка: key — клавиша (фон), ink — скобки и буква «m».
type palette struct {
	key, ink color.NRGBA
}

// Цвета значка: оранжевый — mKey работает (как значок программы, packaging/mkey.svg), красный —
// приостановлен после экстренной остановки.
var (
	activePalette = palette{key: color.NRGBA{0xf0, 0x74, 0x3a, 0xff}, ink: color.NRGBA{0x1b, 0x1e, 0x25, 0xff}}
	pausedPalette = palette{key: color.NRGBA{0xe5, 0x48, 0x4d, 0xff}, ink: color.NRGBA{0x1b, 0x1e, 0x25, 0xff}}
)

// drawIcons рисует значок во всех размерах.
func drawIcons(p palette) []sni.Pixmap {
	out := make([]sni.Pixmap, 0, len(iconSizes))
	for _, s := range iconSizes {
		out = append(out, sni.PixmapFromImage(drawIcon(s, p)))
	}
	return out
}

// drawIcon рисует значок размером size×size, как в packaging/mkey.svg: скруглённая клавиша, на ней
// фигурные скобки и буква «m». Фигуры описаны в координатах 64×64; каждый пиксель сглаживается
// выборкой 4×4 точек.
func drawIcon(size int, p palette) *image.NRGBA {
	const samples = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 64 / float64(size)
	for py := range size {
		for px := range size {
			// Считаем, сколько точек пикселя попало в каждую фигуру (линии лежат поверх клавиши).
			var nKey, nInk int
			for sy := range samples {
				for sx := range samples {
					x := (float64(px) + (float64(sx)+0.5)/samples) * scale
					y := (float64(py) + (float64(sy)+0.5)/samples) * scale
					switch {
					case onStrokes(x, y):
						nInk++
					case inRoundRect(x, y, 4, 4, 56, 56, 14):
						nKey++
					}
				}
			}

			// Смешиваем цвета пропорционально покрытию.
			total := float64(samples * samples)
			img.SetNRGBA(px, py, mix(total, []int{nKey, nInk}, []color.NRGBA{p.key, p.ink}))
		}
	}
	return img
}

// mix смешивает цвета cs с весами ns (число точек из total) в один полупрозрачный пиксель.
func mix(total float64, ns []int, cs []color.NRGBA) color.NRGBA {
	var r, g, b, a float64
	for i, n := range ns {
		w := float64(n) / total
		r += float64(cs[i].R) * w
		g += float64(cs[i].G) * w
		b += float64(cs[i].B) * w
		a += w
	}
	if a == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(b / a), A: uint8(math.Round(a * 255))}
}

// inRoundRect сообщает, лежит ли точка внутри прямоугольника со скруглёнными углами радиуса rad.
func inRoundRect(x, y, rx, ry, w, h, rad float64) bool {
	if x < rx || y < ry || x > rx+w || y > ry+h {
		return false
	}
	// Расстояние до ближайшего «внутреннего» прямоугольника, от которого отсчитывается скругление.
	cx := math.Max(rx+rad, math.Min(x, rx+w-rad))
	cy := math.Max(ry+rad, math.Min(y, ry+h-rad))
	return math.Hypot(x-cx, y-cy) <= rad
}

// pt — точка в координатах 64×64.
type pt struct{ x, y float64 }

// stroke — линия значка: ломаная (кривые уже разбиты на отрезки) и половина её толщины.
// Расстояние до отрезков даёт и скруглённые концы, и скруглённые стыки (как round в SVG).
type stroke struct {
	points []pt
	half   float64
}

// strokes — линии значка: две фигурные скобки (толщина 3.5) и буква «m» (толщина 4), как пути
// packaging/mkey.svg.
var strokes = buildStrokes()

// buildStrokes переводит пути значка в ломаные.
func buildStrokes() []stroke {
	// Левая скобка — по пути SVG (относительные кривые «c» и отрезки «v»), правая — её зеркало.
	left := path(pt{18, 18}, []seg{
		cubic(-3, 0, -4, 1.5, -4, 4), vline(5.5), cubic(0, 2.5, -1.2, 4.5, -3.5, 4.5),
		cubic(2.3, 0, 3.5, 2, 3.5, 4.5), vline(5.5), cubic(0, 2.5, 1, 4, 4, 4),
	})
	right := make([]pt, len(left))
	for i, q := range left {
		right[i] = pt{64 - q.x, q.y}
	}

	// Буква «m»: две ножки по краям, средняя — общая, сверху — две полуокружности радиуса 4.5.
	m := []pt{{23, 40}, {23, 30}}
	m = append(m, arc(27.5, 30, 4.5)...)
	m = append(m, pt{32, 40}, pt{32, 30})
	m = append(m, arc(36.5, 30, 4.5)...)
	m = append(m, pt{41, 40})
	return []stroke{{left, 1.75}, {right, 1.75}, {m, 2}}
}

// seg — кусок пути: добавляет точки, начиная от текущей.
type seg func(from pt) []pt

// path собирает ломаную из кусков, начиная с точки start.
func path(start pt, segs []seg) []pt {
	out := []pt{start}
	for _, s := range segs {
		out = append(out, s(out[len(out)-1])...)
	}
	return out
}

// vline — отрезок вниз на dy (команда SVG «v»).
func vline(dy float64) seg {
	return func(from pt) []pt { return []pt{{from.x, from.y + dy}} }
}

// cubic — кубическая кривая Безье с относительными точками (команда SVG «c»), 12 отрезков —
// гладко даже в значке 64×64.
func cubic(x1, y1, x2, y2, x, y float64) seg {
	return func(from pt) []pt {
		var out []pt
		for i := 1; i <= 12; i++ {
			t := float64(i) / 12
			u := 1 - t
			b := func(p0, p1, p2, p3 float64) float64 {
				return u*u*u*p0 + 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t*p3
			}
			out = append(out, pt{b(from.x, from.x+x1, from.x+x2, from.x+x), b(from.y, from.y+y1, from.y+y2, from.y+y)})
		}
		return out
	}
}

// arc — верхняя полуокружность с центром (cx, cy) и радиусом r, слева направо (как «a … 0 0 1» в
// значке), 12 отрезков.
func arc(cx, cy, r float64) []pt {
	var out []pt
	for i := 1; i <= 12; i++ {
		a := math.Pi - math.Pi*float64(i)/12
		out = append(out, pt{cx + r*math.Cos(a), cy - r*math.Sin(a)})
	}
	return out
}

// onStrokes сообщает, лежит ли точка на какой-нибудь линии значка.
func onStrokes(x, y float64) bool {
	for _, s := range strokes {
		for i := 1; i < len(s.points); i++ {
			if distToSegment(x, y, s.points[i-1], s.points[i]) <= s.half {
				return true
			}
		}
	}
	return false
}

// distToSegment — расстояние от точки (x, y) до отрезка ab.
func distToSegment(x, y float64, a, b pt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((x-a.x)*dx+(y-a.y)*dy)/l))
	}
	return math.Hypot(x-(a.x+t*dx), y-(a.y+t*dy))
}
