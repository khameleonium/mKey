package tray

import (
	"image"
	"image/color"
	"math"

	"mkey/internal/lib/sni"
)

// iconSizes — размеры картинок значка: панель трея выбирает ближайший к своему размеру.
var iconSizes = []int{16, 22, 24, 32, 48, 64}

// palette — цвета клавиши на значке.
type palette struct {
	// base — «боковина» клавиши; top — её верхняя грань.
	base, top color.NRGBA
}

// Цвета значка: синий — mKey работает (как значок программы), красный — приостановлен
// после экстренной остановки.
var (
	activePalette = palette{base: color.NRGBA{0x2b, 0x3a, 0x55, 0xff}, top: color.NRGBA{0x4f, 0x7c, 0xff, 0xff}}
	pausedPalette = palette{base: color.NRGBA{0x5a, 0x23, 0x30, 0xff}, top: color.NRGBA{0xe5, 0x48, 0x4d, 0xff}}
)

// drawIcons рисует значок во всех размерах.
func drawIcons(p palette) []sni.Pixmap {
	out := make([]sni.Pixmap, 0, len(iconSizes))
	for _, s := range iconSizes {
		out = append(out, sni.PixmapFromImage(drawIcon(s, p)))
	}
	return out
}

// drawIcon рисует значок размером size×size: клавишу с буквой «m», как в mkey.svg.
// Фигуры описаны в координатах 64×64; каждый пиксель сглаживается выборкой 4×4 точек.
func drawIcon(size int, p palette) *image.NRGBA {
	const samples = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 64 / float64(size)
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	for py := range size {
		for px := range size {
			// Считаем, сколько точек пикселя попало в каждую фигуру (верхняя фигура перекрывает нижние).
			var nBase, nTop, nLetter int
			for sy := range samples {
				for sx := range samples {
					x := (float64(px) + (float64(sx)+0.5)/samples) * scale
					y := (float64(py) + (float64(sy)+0.5)/samples) * scale
					switch {
					case inLetter(x, y):
						nLetter++
					case inRoundRect(x, y, 8, 6, 48, 44, 8):
						nTop++
					case inRoundRect(x, y, 4, 8, 56, 50, 10):
						nBase++
					}
				}
			}

			// Смешиваем цвета пропорционально покрытию.
			total := float64(samples * samples)
			img.SetNRGBA(px, py, mix(total, []int{nBase, nTop, nLetter}, []color.NRGBA{p.base, p.top, white}))
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

// Геометрия буквы «m» в координатах 64×64: три ножки и две дуги сверху.
const (
	letterTop    = 26.0 // центр дуг по вертикали
	letterBottom = 38.0 // низ ножек
	letterStroke = 2.4  // половина толщины линии
	letterArcR   = 5.5  // радиус дуг
)

// inLetter сообщает, лежит ли точка на букве «m».
func inLetter(x, y float64) bool {
	stems := []float64{21, 32, 43}
	// Ножки: левая начинается выше (как у шрифтовой «m»), остальные — от центра дуг.
	for i, sx := range stems {
		top := letterTop
		if i == 0 {
			top = letterTop - letterArcR
		}
		if math.Abs(x-sx) <= letterStroke && y >= top && y <= letterBottom {
			return true
		}
	}
	// Дуги: верхние половины колец между соседними ножками.
	for i := range 2 {
		cx := (stems[i] + stems[i+1]) / 2
		if y <= letterTop && math.Abs(math.Hypot(x-cx, y-letterTop)-letterArcR) <= letterStroke {
			return true
		}
	}
	return false
}
