// Package fileblock — блок mKey внутри чужого текстового файла (конфиги композиторов,
// /etc/modules, /etc/mdev.conf).
//
// Строки mKey обрамляются маркерами, поэтому их можно заменить при повторной установке
// и убрать при удалении, не трогая остальной файл:
//
//	# >>> mKey >>> managed by mKey, do not edit
//	exec /home/user/.local/bin/mkey daemon
//	# <<< mKey <<<
//
// Функции работают со строками и ничего не пишут на диск — запись делает вызывающий.
package fileblock

import "strings"

// Маркеры блока. Строка комментария начинается с "#" — так понимают почти все форматы
// конфигов; для других форматов есть функции с собственным префиксом комментария.
const (
	beginText = ">>> mKey >>> managed by mKey, do not edit"
	endText   = "<<< mKey <<<"
)

// Set возвращает содержимое content, в котором блок mKey (с комментариями "#") заменён строками lines.
// prepend — поставить блок в начало (важно, где побеждает первое совпадение, как в mdev.conf).
func Set(content string, lines []string, prepend bool) string {
	return SetWith(content, "#", lines, prepend)
}

// SetWith — как Set, но с заданным префиксом комментария (например, "//" для KDL).
func SetWith(content, comment string, lines []string, prepend bool) string {
	// Убираем прежний блок, если он был.
	rest := StripWith(content, comment)

	// Собираем новый блок и ставим его в начало или в конец.
	block := comment + " " + beginText + "\n" + strings.Join(lines, "\n") + "\n" + comment + " " + endText + "\n"
	switch {
	case prepend:
		return block + rest
	case rest == "" || strings.HasSuffix(rest, "\n"):
		return rest + block
	default:
		return rest + "\n" + block
	}
}

// Strip возвращает содержимое без блока mKey (с комментариями "#").
func Strip(content string) string { return StripWith(content, "#") }

// StripWith — как Strip, но с заданным префиксом комментария.
func StripWith(content, comment string) string {
	begin, end := comment+" "+beginText, comment+" "+endText
	start := strings.Index(content, begin)
	if start < 0 {
		return content
	}
	stop := strings.Index(content[start:], end)
	if stop < 0 {
		return content
	}
	stop += start + len(end)
	if stop < len(content) && content[stop] == '\n' {
		stop++
	}
	return content[:start] + content[stop:]
}

// Has сообщает, есть ли в содержимом блок mKey (с любым префиксом комментария).
func Has(content string) bool { return strings.Contains(content, beginText) }
