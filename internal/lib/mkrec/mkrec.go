// Package mkrec — формат записи ввода mKey (.mkrec, FR-REC-3): чтение и запись.
//
// Файл — обычный текст, его удобно читать и править в любом редакторе: одна строка — одно
// действие (пакет событий одного устройства). Лишние действия можно просто удалить.
//
//	# Запись mKey. Строки, начинающиеся с #, — комментарии.
//	mkrec 1
//	created 2026-09-30T12:00:00Z
//	device 0 keyboard "AT Translated Set 2 keyboard"
//	device 1 mouse "Logitech USB Optical Mouse"
//	pointer center                      ← перед записью указатель поставлен в центр экрана
//	0.000 0 ^{A}                        ← время в секундах, номер устройства, действия
//	0.120 0 ~{A}
//	0.350 1 move +12 -3                 ← сдвиг мыши вправо на 12 и вверх на 3 точки
//	0.500 1 ^{Mouse0}                   ← нажата левая кнопка мыши
//	0.620 1 ~{Mouse0}
//	1.200 1 wheel -1                    ← колесо на щелчок вниз
//	# ---- пауза 3,2 с ----             ← пометка: долго ничего не происходило
//	4.400 0 ^{B}   # заметка            ← комментарий после действия
//	shift -2                            ← всё ниже — на 2 с раньше (убрать паузу)
//	7.000 end                           ← конец записи (с учётом сдвига — 5.000)
//
// Действия: ^{Клавиша} — нажать, ~{Клавиша} — отпустить (имена — как в макросах, docs/dsl.md;
// {#30} — клавиша по коду), move +dx +dy, wheel ±n, hwheel ±n, wheel-hr ±n, hwheel-hr ±n
// (колесо высокого разрешения), ev тип код значение — любое другое событие evdev.
// Автоповторы клавиш (значение 2) и служебные события EV_MSC не записываются.
//
// Для удобной правки (ADR-0024): # после пробела начинает комментарий до конца строки;
// строка «shift ±секунды» сдвигает время всех строк ниже (сдвиги складываются); при записи
// перед паузой от PauseMark программа сама вставляет пометку-комментарий.
package mkrec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
)

// Version — версия формата.
const Version = 1

// FileExt — расширение файлов записей.
const FileExt = ".mkrec"

// PauseMark — пауза, перед которой при записи вставляется пометка «# ---- пауза N с ----»:
// по ним легко найти нужное место в длинном файле.
const PauseMark = time.Second

// Device — записанное устройство.
type Device struct {
	// ID — номер устройства в записи (на него ссылаются строки действий).
	ID int
	// Kinds — классы устройства (keyboard, mouse, gamepad…).
	Kinds []string
	// Name — имя устройства.
	Name string
	// Caps — возможности устройства (кнопки, оси с диапазонами, модель) для его копии при
	// воспроизведении; есть у геймпадов, джойстиков, сенсорных экранов и прочих — не у клавиатур и мышей.
	Caps *Caps
}

// Caps — возможности записанного устройства: строка «caps» заголовка.
//
//	caps 2 id=0003:045e:028e:0110 props=INPUT_PROP_DIRECT keys=BTN_SOUTH,BTN_EAST rels=REL_X abs=ABS_X:-32768:32767:16:128:0,ABS_Y:…
//
// У оси: минимум, максимум, fuzz, flat и положение покоя (к нему ось возвращается в конце повтора).
type Caps struct {
	ID    ev.ID
	Props []uint16
	Keys  []uint16
	Rels  []uint16
	Abs   map[uint16]ev.AbsInfo
}

// Header — сведения в начале файла.
type Header struct {
	Version int
	Created time.Time
	Devices []Device
	// Centered — перед записью указатель мыши поставлен в центр рабочего стола (калибровка,
	// FR-REC-5): перед воспроизведением его тоже нужно поставить туда.
	Centered bool
}

// Frame — одно действие: пакет событий одного устройства, отправляемый разом.
type Frame struct {
	// T — время от начала записи.
	T time.Duration
	// Device — номер устройства в записи.
	Device int
	// Events — события пакета без SYN_REPORT.
	Events []ev.Event
}

// Recording — запись целиком.
type Recording struct {
	Header   Header
	Frames   []Frame
	Duration time.Duration
}

// Keep сообщает, записывается ли событие: без автоповторов, служебных EV_MSC и SYN.
func Keep(e ev.Event) bool {
	switch {
	case e.Type == ev.EvMsc, e.Type == ev.EvSyn:
		return false
	case e.Type == ev.EvKey && e.Value == ev.ValueRepeat:
		return false
	}
	return true
}

// IsMove сообщает, что действие — только перемещение мыши (REL_X/REL_Y).
func (f Frame) IsMove() bool {
	if len(f.Events) == 0 {
		return false
	}
	for _, e := range f.Events {
		if e.Type != ev.EvRel || (e.Code != ev.RelX && e.Code != ev.RelY) {
			return false
		}
	}
	return true
}

// HasPress сообщает, что в действии нажата кнопка или клавиша.
func (f Frame) HasPress() bool {
	for _, e := range f.Events {
		if e.Type == ev.EvKey && e.Value == ev.ValueDown {
			return true
		}
	}
	return false
}

// ---- Запись ----

// fileComment — пояснение в начале каждого файла записи.
const fileComment = `# Запись mKey (mkey rec). Одна строка — одно действие: время в секундах от начала,
# номер устройства, действия. Строки можно удалять и править.
# ^{A} — нажать, ~{A} — отпустить, move +x +y — сдвинуть мышь, wheel ±n — колесо.
# Знак # начинает комментарий: в начале строки или после действия (через пробел).
# shift -2.5 — всё, что ниже, начнётся на 2,5 с раньше (так убирают паузу); shift 1 — на 1 с позже.
# Строка caps — кнопки и оси геймпада или экрана для его копии при повторе; её лучше не менять.
# Подробно: docs/recording.md
`

// Writer пишет запись в поток.
type Writer struct {
	w   *bufio.Writer
	err error
	// last — время последнего записанного действия (для пометок о паузах); wrote — действия уже были.
	last  time.Duration
	wrote bool
}

// NewWriter возвращает писателя записи в w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: bufio.NewWriterSize(w, 64*1024)}
}

// printf пишет строку и запоминает первую ошибку.
func (w *Writer) printf(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, args...)
	}
}

// WriteHeader пишет пояснение и сведения о записи.
func (w *Writer) WriteHeader(h Header) error {
	w.printf("%s", fileComment)
	w.printf("mkrec %d\n", Version)
	if !h.Created.IsZero() {
		w.printf("created %s\n", h.Created.UTC().Format(time.RFC3339))
	}
	for _, d := range h.Devices {
		kinds := strings.Join(d.Kinds, ",")
		if kinds == "" {
			kinds = "other"
		}
		w.printf("device %d %s %s\n", d.ID, kinds, strconv.Quote(d.Name))
		if d.Caps != nil {
			w.printf("caps %d %s\n", d.ID, formatCaps(*d.Caps))
		}
	}
	if h.Centered {
		w.printf("pointer center\n")
	}
	return w.err
}

// WriteFrame пишет одно действие строкой. Перед долгой паузой (от PauseMark) с прошлого
// действия вставляет пометку-комментарий «# ---- пауза 3,2 с ----».
func (w *Writer) WriteFrame(f Frame) error {
	if w.wrote && f.T-w.last >= PauseMark {
		w.printf("# ---- пауза %s с ----\n", pauseText(f.T-w.last))
	}
	w.last, w.wrote = f.T, true
	w.printf("%s %d %s\n", seconds(f.T), f.Device, FormatEvents(f.Events))
	return w.err
}

// pauseText записывает длительность паузы с одним знаком после запятой: «3,2».
func pauseText(d time.Duration) string {
	return strings.Replace(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".", ",", 1)
}

// Append дописывает строки действий из r (черновика идущей записи).
func (w *Writer) Append(r io.Reader) error {
	if w.err == nil {
		_, w.err = io.Copy(w.w, r)
	}
	return w.err
}

// Close пишет длительность записи и сбрасывает буфер.
func (w *Writer) Close(duration time.Duration) error {
	w.printf("%s end\n", seconds(duration))
	return w.Flush()
}

// Flush сбрасывает буфер в поток.
func (w *Writer) Flush() error {
	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

// seconds записывает время секундами с точностью до миллисекунды: «1.250».
func seconds(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Milliseconds())/1000, 'f', 3, 64)
}

// FormatEvents записывает события действия словами: «^{A} move +3 -1».
func FormatEvents(events []ev.Event) string {
	var parts []string
	var dx, dy int32
	hasMove := false
	for _, e := range events {
		switch {
		case e.Type == ev.EvKey && e.Value == ev.ValueDown:
			parts = append(parts, "^{"+keyName(e.Code)+"}")
		case e.Type == ev.EvKey && e.Value == ev.ValueUp:
			parts = append(parts, "~{"+keyName(e.Code)+"}")
		case e.Type == ev.EvRel && e.Code == ev.RelX:
			dx, hasMove = dx+e.Value, true
		case e.Type == ev.EvRel && e.Code == ev.RelY:
			dy, hasMove = dy+e.Value, true
		case e.Type == ev.EvRel && relWords[e.Code] != "":
			parts = append(parts, fmt.Sprintf("%s %+d", relWords[e.Code], e.Value))
		default:
			parts = append(parts, fmt.Sprintf("ev %d %d %d", e.Type, e.Code, e.Value))
		}
	}
	if hasMove {
		parts = append([]string{fmt.Sprintf("move %+d %+d", dx, dy)}, parts...)
	}
	return strings.Join(parts, " ")
}

// relWords — слова для осей колеса.
var relWords = map[uint16]string{
	ev.RelWheel: "wheel", ev.RelHwheel: "hwheel", ev.RelWheelHiRes: "wheel-hr", ev.RelHwheelHiRes: "hwheel-hr",
}

// keyName возвращает имя клавиши mKey или «#код», если имени нет.
func keyName(code uint16) string {
	if n, ok := keys.NameOf(code); ok {
		return n
	}
	return "#" + strconv.Itoa(int(code))
}

// ---- Чтение ----

// ErrFormat — файл не является записью mKey или в нём ошибка (с номером строки).
var ErrFormat = errors.New("not a valid mKey recording")

// Виды ошибок в файле записи (Problem.Code): по ним программа пишет человеку понятное
// сообщение на его языке (i18n-ключи "rec.problem.<вид>").
const (
	// ProblemEmpty — в файле нет ни одной значимой строки.
	ProblemEmpty = "empty"
	// ProblemOldFormat — запись первой версии mKey (до текстового формата), её нужно записать заново.
	ProblemOldFormat = "old_format"
	// ProblemHeader — первая значимая строка не «mkrec 1»; Arg — что там написано.
	ProblemHeader = "header"
	// ProblemVersion — запись новее этой версии mKey; Arg — версия.
	ProblemVersion = "version"
	// ProblemLine — непонятное начало строки (не время, не device/created/pointer); Arg — первое слово.
	ProblemLine = "line"
	// ProblemCreated — дата в строке created не разобрана.
	ProblemCreated = "created"
	// ProblemDevice — строка device не по образцу.
	ProblemDevice = "device"
	// ProblemCaps — строка caps не по образцу или для устройства, которого нет выше; Arg — неверная часть.
	ProblemCaps = "caps"
	// ProblemPointer — после pointer должно быть center.
	ProblemPointer = "pointer"
	// ProblemShort — после времени нет номера устройства или действий.
	ProblemShort = "short"
	// ProblemDeviceNumber — номер устройства не число; Arg — что написано.
	ProblemDeviceNumber = "device_number"
	// ProblemKey — неизвестная клавиша; Arg — имя.
	ProblemKey = "key"
	// ProblemAction — неизвестное действие; Arg — слово.
	ProblemAction = "action"
	// ProblemNumbers — после слова не хватает чисел или число неверное; Arg — слово.
	ProblemNumbers = "numbers"
	// ProblemShift — после shift должно быть число секунд (например, -2.5); Arg — что написано.
	ProblemShift = "shift"
	// ProblemNegative — после сдвигов время строки стало меньше нуля; Arg — итоговое время.
	ProblemNegative = "negative"
)

// Problem — ошибка в файле записи: где она и что не так. Возвращается из Read; errors.Is(err, ErrFormat) — да.
type Problem struct {
	// Line — номер строки с 1 (0 — файл целиком); Text — сама строка, как в файле.
	Line int
	Text string
	// Code — вид ошибки (Problem*); Arg — неверное слово, клавиша или номер.
	Code string
	Arg  string
}

// Error описывает ошибку по-английски (для журнала); человеку сообщение пишет API по Code.
func (p *Problem) Error() string {
	msg := ErrFormat.Error() + ": " + p.Code
	if p.Arg != "" {
		msg += " " + strconv.Quote(p.Arg)
	}
	if p.Line > 0 {
		msg = fmt.Sprintf("%s: line %d", msg, p.Line)
	}
	return msg
}

// Unwrap позволяет проверять ошибку через errors.Is(err, ErrFormat).
func (p *Problem) Unwrap() error { return ErrFormat }

// problem создаёт ошибку вида code (строку и её номер дописывает Read).
func problem(code, arg string) error { return &Problem{Code: code, Arg: arg} }

// formatCaps записывает возможности устройства частями «имя=значение» (коды — именами ядра).
func formatCaps(c Caps) string {
	names := func(typ uint16, codes []uint16) string {
		out := make([]string, len(codes))
		for i, code := range codes {
			out[i] = ev.CodeName(typ, code)
		}
		return strings.Join(out, ",")
	}
	parts := []string{fmt.Sprintf("id=%04x:%04x:%04x:%04x", c.ID.Bustype, c.ID.Vendor, c.ID.Product, c.ID.Version)}
	if len(c.Props) > 0 {
		props := make([]string, len(c.Props))
		for i, p := range c.Props {
			props[i] = ev.PropName(p)
		}
		parts = append(parts, "props="+strings.Join(props, ","))
	}
	if len(c.Keys) > 0 {
		parts = append(parts, "keys="+names(ev.EvKey, c.Keys))
	}
	if len(c.Rels) > 0 {
		parts = append(parts, "rels="+names(ev.EvRel, c.Rels))
	}
	if len(c.Abs) > 0 {
		codes := slices.Sorted(maps.Keys(c.Abs))
		axes := make([]string, len(codes))
		for i, code := range codes {
			a := c.Abs[code]
			axes[i] = fmt.Sprintf("%s:%d:%d:%d:%d:%d", ev.CodeName(ev.EvAbs, code), a.Minimum, a.Maximum, a.Fuzz, a.Flat, a.Value)
		}
		parts = append(parts, "abs="+strings.Join(axes, ","))
	}
	return strings.Join(parts, " ")
}

// parseCaps разбирает части строки caps. Ошибка — Problem вида ProblemCaps с неверной частью.
func parseCaps(parts []string) (*Caps, error) {
	c := &Caps{Abs: map[uint16]ev.AbsInfo{}}
	for _, part := range parts {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return nil, problem(ProblemCaps, part)
		}
		items := strings.Split(val, ",")
		switch key {
		case "id":
			var ids [4]uint64
			f := strings.Split(val, ":")
			if len(f) != 4 {
				return nil, problem(ProblemCaps, part)
			}
			for i := range f {
				v, err := strconv.ParseUint(f[i], 16, 16)
				if err != nil {
					return nil, problem(ProblemCaps, part)
				}
				ids[i] = v
			}
			c.ID = ev.ID{Bustype: uint16(ids[0]), Vendor: uint16(ids[1]), Product: uint16(ids[2]), Version: uint16(ids[3])}
		case "props":
			for _, it := range items {
				p, ok := ev.ParseProp(it)
				if !ok {
					return nil, problem(ProblemCaps, it)
				}
				c.Props = append(c.Props, p)
			}
		case "keys", "rels":
			typ := uint16(ev.EvKey)
			if key == "rels" {
				typ = ev.EvRel
			}
			for _, it := range items {
				code, ok := ev.ParseCode(typ, it)
				if !ok {
					return nil, problem(ProblemCaps, it)
				}
				if typ == ev.EvKey {
					c.Keys = append(c.Keys, code)
				} else {
					c.Rels = append(c.Rels, code)
				}
			}
		case "abs":
			for _, it := range items {
				// ИМЯ:минимум:максимум:fuzz:flat[:покой] — покой (значение при подключении) необязателен.
				f := strings.Split(it, ":")
				if len(f) != 5 && len(f) != 6 {
					return nil, problem(ProblemCaps, it)
				}
				code, ok := ev.ParseCode(ev.EvAbs, f[0])
				var n [5]int64
				for i := range len(f) - 1 {
					v, err := strconv.ParseInt(f[i+1], 10, 32)
					if err != nil {
						ok = false
					}
					n[i] = v
				}
				if !ok || n[1] <= n[0] {
					return nil, problem(ProblemCaps, it)
				}
				c.Abs[code] = ev.AbsInfo{Minimum: int32(n[0]), Maximum: int32(n[1]), Fuzz: int32(n[2]), Flat: int32(n[3]), Value: int32(n[4])}
			}
		default:
			return nil, problem(ProblemCaps, part)
		}
	}
	return c, nil
}

// oldFormatPrefix — начало первой строки записи первой версии (JSON).
const oldFormatPrefix = `{"format":"mkrec"`

// Read читает запись. Действия упорядочиваются по времени (время можно править вручную).
func Read(r io.Reader) (*Recording, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	rec := &Recording{}
	started := false
	var shift time.Duration // сумма строк «shift» выше текущей
	for n := 1; sc.Scan(); n++ {
		// Пустые строки и комментарии (во всю строку и после действия).
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		// bad дописывает к ошибке номер строки и саму строку.
		bad := func(err error) error {
			var p *Problem
			if !errors.As(err, &p) {
				p = &Problem{Code: ProblemLine, Arg: err.Error()}
			}
			p.Line, p.Text = n, strings.TrimSpace(sc.Text())
			return p
		}

		// Первая значимая строка — «mkrec <версия>» (или запись первой версии в JSON).
		fields := strings.Fields(line)
		if !started {
			if strings.HasPrefix(line, oldFormatPrefix) {
				return nil, &Problem{Code: ProblemOldFormat}
			}
			if len(fields) != 2 || fields[0] != "mkrec" {
				return nil, bad(problem(ProblemHeader, fields[0]))
			}
			v, err := strconv.Atoi(fields[1])
			if err != nil || v < 1 || v > Version {
				return nil, bad(problem(ProblemVersion, fields[1]))
			}
			rec.Header.Version, started = v, true
			continue
		}

		// Сведения заголовка или действие.
		if err := readLine(rec, line, fields, &shift); err != nil {
			return nil, bad(err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !started {
		return nil, &Problem{Code: ProblemEmpty}
	}

	// Порядок по времени; длительность — не меньше времени последнего действия.
	slices.SortStableFunc(rec.Frames, func(a, b Frame) int { return int(a.T - b.T) })
	if n := len(rec.Frames); n > 0 && rec.Frames[n-1].T > rec.Duration {
		rec.Duration = rec.Frames[n-1].T
	}
	return rec, nil
}

// stripComment отрезает комментарий: # в начале строки или после пробела, вне кавычек
// (в имени устройства "…" и в клавише по коду {#30} знак # — не комментарий).
func stripComment(line string) string {
	quoted := false
	for i, r := range line {
		switch {
		case r == '"' && (i == 0 || line[i-1] != '\\'):
			quoted = !quoted
		case r == '#' && !quoted && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// readLine разбирает строку заголовка или действия. shift — сумма строк «shift» выше:
// она прибавляется ко времени действий и конца записи.
func readLine(rec *Recording, line string, f []string, shift *time.Duration) error {
	switch f[0] {
	case "shift":
		// shift ±секунды — сдвиг времени всех строк ниже.
		if len(f) != 2 {
			return problem(ProblemShift, strings.Join(f[1:], " "))
		}
		s, err := strconv.ParseFloat(f[1], 64)
		if err != nil || math.IsNaN(s) || math.IsInf(s, 0) {
			return problem(ProblemShift, f[1])
		}
		*shift += time.Duration(math.Round(s*1000)) * time.Millisecond
		return nil
	case "created":
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(line, "created")))
		if err != nil {
			return problem(ProblemCreated, "")
		}
		rec.Header.Created = t
		return nil
	case "device":
		// device <номер> <классы> "<имя>"
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 4 {
			return problem(ProblemDevice, "")
		}
		id, err := strconv.Atoi(parts[1])
		if err != nil {
			return problem(ProblemDevice, parts[1])
		}
		name, err := strconv.Unquote(strings.TrimSpace(parts[3]))
		if err != nil {
			name = strings.TrimSpace(parts[3])
		}
		rec.Header.Devices = append(rec.Header.Devices, Device{ID: id, Kinds: strings.Split(parts[2], ","), Name: name})
		return nil
	case "caps":
		// caps <номер> id=… props=… keys=… rels=… abs=… — к устройству с этим номером выше.
		if len(f) < 2 {
			return problem(ProblemCaps, "")
		}
		id, err := strconv.Atoi(f[1])
		if err != nil {
			return problem(ProblemCaps, f[1])
		}
		caps, err := parseCaps(f[2:])
		if err != nil {
			return err
		}
		for i := range rec.Header.Devices {
			if rec.Header.Devices[i].ID == id {
				rec.Header.Devices[i].Caps = caps
				return nil
			}
		}
		return problem(ProblemCaps, f[1])
	case "pointer":
		if len(f) != 2 || f[1] != "center" {
			return problem(ProblemPointer, "")
		}
		rec.Header.Centered = true
		return nil
	}

	// Действие: <время> <устройство> <действия…> или <время> end.
	t, err := strconv.ParseFloat(f[0], 64)
	if err != nil || t < 0 {
		return problem(ProblemLine, f[0])
	}
	at := time.Duration(t*1000+0.5)*time.Millisecond + *shift
	if at < 0 {
		return problem(ProblemNegative, seconds(at))
	}
	if len(f) == 2 && f[1] == "end" {
		rec.Duration = at
		return nil
	}
	if len(f) < 3 {
		return problem(ProblemShort, "")
	}
	dev, err := strconv.Atoi(f[1])
	if err != nil {
		return problem(ProblemDeviceNumber, f[1])
	}
	frame := Frame{T: at, Device: dev}
	if frame.Events, err = ParseEvents(f[2:]); err != nil {
		return err
	}
	rec.Frames = append(rec.Frames, frame)
	return nil
}

// ParseEvents разбирает действия строки («^{A} move +3 -1») в события.
func ParseEvents(tokens []string) ([]ev.Event, error) {
	var out []ev.Event
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		// need возвращает n целых чисел после слова.
		need := func(n int) ([]int64, error) {
			if i+n >= len(tokens) {
				return nil, problem(ProblemNumbers, tok)
			}
			vals := make([]int64, n)
			for j := range n {
				v, err := strconv.ParseInt(tokens[i+1+j], 10, 32)
				if err != nil {
					return nil, problem(ProblemNumbers, tok)
				}
				vals[j] = v
			}
			i += n
			return vals, nil
		}
		switch {
		case strings.HasPrefix(tok, "^{") || strings.HasPrefix(tok, "~{"):
			code, err := keyCode(tok[2:])
			if err != nil {
				return nil, err
			}
			v := int32(ev.ValueDown)
			if tok[0] == '~' {
				v = ev.ValueUp
			}
			out = append(out, ev.Event{Type: ev.EvKey, Code: code, Value: v})
		case tok == "move":
			v, err := need(2)
			if err != nil {
				return nil, err
			}
			if v[0] != 0 {
				out = append(out, ev.Event{Type: ev.EvRel, Code: ev.RelX, Value: int32(v[0])})
			}
			if v[1] != 0 {
				out = append(out, ev.Event{Type: ev.EvRel, Code: ev.RelY, Value: int32(v[1])})
			}
		case tok == "ev":
			v, err := need(3)
			if err != nil {
				return nil, err
			}
			// Тип и код события — в пределах ядра (EV_MAX, KEY_MAX); иначе это опечатка.
			if v[0] < 0 || v[0] > int64(ev.EvMax) || v[1] < 0 || v[1] > int64(ev.KeyMax) {
				return nil, problem(ProblemNumbers, tok)
			}
			out = append(out, ev.Event{Type: uint16(v[0]), Code: uint16(v[1]), Value: int32(v[2])})
		default:
			code, ok := wordRel(tok)
			if !ok {
				return nil, problem(ProblemAction, tok)
			}
			v, err := need(1)
			if err != nil {
				return nil, err
			}
			out = append(out, ev.Event{Type: ev.EvRel, Code: code, Value: int32(v[0])})
		}
	}
	return out, nil
}

// wordRel находит ось колеса по слову.
func wordRel(word string) (uint16, bool) {
	for code, w := range relWords {
		if w == word {
			return code, true
		}
	}
	return 0, false
}

// keyCode разбирает «A}» или «#30}» (без открывающей части) в код клавиши.
func keyCode(s string) (uint16, error) {
	name, ok := strings.CutSuffix(s, "}")
	if !ok || name == "" {
		return 0, problem(ProblemKey, s)
	}
	if n, ok := strings.CutPrefix(name, "#"); ok {
		v, err := strconv.ParseUint(n, 10, 16)
		if err != nil {
			return 0, problem(ProblemKey, name)
		}
		return uint16(v), nil
	}
	if k, ok := keys.Lookup(name); ok && k.Type == ev.EvKey {
		return k.Code, nil
	}
	if k, ok := keys.LookupGamepad(name); ok {
		return k.Code, nil
	}
	return 0, problem(ProblemKey, name)
}

// CoalesceMoves склеивает идущие подряд перемещения одного устройства, попавшие в окно window:
// смещения суммируются, итоговая траектория та же. Файл получается короче, а мышь с частотой
// 1000 Гц при повторе не упирается в ограничитель скорости виртуальных устройств (SEC-4).
func CoalesceMoves(frames []Frame, window time.Duration) []Frame {
	out := make([]Frame, 0, len(frames))
	lastOf := map[int]int{} // устройство → индекс его последнего действия в out
	for _, f := range frames {
		// Перемещение склеивается с последним действием той же мыши, если это тоже перемещение
		// и оно было недавно (действия других устройств между ними не мешают: сдвиг во времени < window).
		if i, ok := lastOf[f.Device]; ok && f.IsMove() {
			last := &out[i]
			if last.IsMove() && f.T-last.T < window {
				last.Events = AddMoves(last.Events, f.Events)
				continue
			}
		}
		lastOf[f.Device] = len(out)
		f.Events = append([]ev.Event(nil), f.Events...)
		out = append(out, f)
	}
	return out
}

// AddMoves прибавляет смещения b к смещениям a (по осям X и Y) и возвращает результат.
func AddMoves(a, b []ev.Event) []ev.Event {
	for _, eb := range b {
		found := false
		for i := range a {
			if a[i].Type == eb.Type && a[i].Code == eb.Code {
				a[i].Value += eb.Value
				found = true
			}
		}
		if !found {
			a = append(a, eb)
		}
	}
	return a
}
