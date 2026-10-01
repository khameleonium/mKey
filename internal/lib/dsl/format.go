package dsl

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"mkey/internal/lib/devmap"
)

// Format возвращает текст макроса в каноническом виде (FR-DSL-4):
// канонические имена клавиш, без пробелов между действиями, длительности в миллисекундах
// или секундах, текст — в кавычках с экранированием только служебных символов.
// Parse(Format(nodes)) даёт то же дерево (без учёта позиций).
func Format(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		formatNode(&b, n)
	}
	return b.String()
}

// formatNode дописывает в b текст одного узла.
func formatNode(b *strings.Builder, n Node) {
	switch n.Kind {
	case KindTap:
		b.WriteString("{" + formatKeys(n.Keys))
		if n.Repeat > 1 {
			b.WriteString("*" + strconv.Itoa(n.Repeat))
		}
		if n.HoldMS > 0 {
			b.WriteString(" " + formatDuration(n.HoldMS))
		}
		b.WriteString("}")
	case KindDown:
		b.WriteString("^{" + formatKeys(n.Keys) + "}")
	case KindUp:
		b.WriteString("~{" + formatKeys(n.Keys) + "}")
	case KindUpAll:
		b.WriteString("~{*}")
	case KindAxis:
		b.WriteString("{" + formatKeys(n.Keys) + "=" + formatNumber(n.Value) + "}")
	case KindPause:
		b.WriteString("[" + formatDuration(n.MinMS))
		if n.MaxMS != n.MinMS {
			b.WriteString(".." + formatDuration(n.MaxMS))
		}
		b.WriteString("]")
	case KindText:
		b.WriteString("{" + quoteText(n.Text) + "}")
	case KindCommand:
		b.WriteString("{" + n.Command)
		for _, a := range n.Args {
			b.WriteString(" " + formatArg(a))
		}
		b.WriteString("}")
	case KindGroup:
		b.WriteString("(" + Format(n.Children) + ")")
		if n.Repeat > 1 {
			b.WriteString("*" + strconv.Itoa(n.Repeat))
		}
	}
}

// formatKeys склеивает ссылки на клавиши через "+".
func formatKeys(refs []KeyRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = formatKeyRef(r)
	}
	return strings.Join(parts, "+")
}

// formatKeyRef записывает одну ссылку: "A", "pad2.South" или "#30".
func formatKeyRef(r KeyRef) string {
	if r.Code != nil {
		return "#" + strconv.Itoa(int(*r.Code))
	}
	if r.Device != "" {
		// У первого авто-ID-устройства кнопка пишется слитно: {UnKey001} (FR-DEV-2).
		return devmap.Ref(r.Device, r.Name)
	}
	return r.Name
}

// formatDuration записывает длительность: целые десятые доли секунды от 1 с — в секундах ("1.5s"),
// остальное — в миллисекундах ("250").
func formatDuration(ms int64) string {
	if ms >= 1000 && ms%100 == 0 {
		return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) + "s"
	}
	return strconv.FormatInt(ms, 10)
}

// formatNumber записывает число без лишних нулей.
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// formatArg записывает аргумент команды; число со знаком сохраняет явный знак ("+10", "-5", "-0").
func formatArg(a Arg) string {
	if a.Word != "" {
		return a.Word
	}
	if !a.Signed {
		return formatNumber(a.Number)
	}
	if math.Signbit(a.Number) {
		return "-" + formatNumber(math.Abs(a.Number))
	}
	return "+" + formatNumber(a.Number)
}

// quoteText записывает текст в кавычках: экранируются кавычка, обратная косая черта
// и управляющие символы; буквы любых алфавитов остаются как есть.
func quoteText(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
