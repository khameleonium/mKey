// Команда mkey — единый исполняемый файл mKey: CLI-клиент, демон и мастер установки.
//
// Здесь только точка входа и описание команд CLI. Бизнес-логика живёт в модулях
// (internal/*), а CLI обращается к демону через HTTP API по Unix-сокету (SPEC §5.10, D9).
package main

import (
	"errors"
	"fmt"
	"os"

	"mkey/internal/i18n"
)

// main разбирает аргументы, выполняет команду и завершает процесс с кодом ошибки при сбое.
func main() {
	// Загружаем переводы и определяем язык до разбора команд: от языка зависят тексты справки.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkey:", err)
		os.Exit(1)
	}
	lang := langFromArgs(os.Args[1:])
	if lang == "" {
		lang = i18n.DetectLang(os.Getenv)
	}
	tr := i18n.New(cat, lang)

	// Выполняем команду; ошибку печатаем на языке пользователя
	// (готовые сообщения вроде ошибки в макросе — без префикса «Ошибка:»).
	if err := newRootCmd(tr).Execute(); err != nil {
		var ue userError
		if errors.As(err, &ue) {
			fmt.Fprintln(os.Stderr, ue)
		} else {
			fmt.Fprintln(os.Stderr, tr.T("cli.error.prefix", i18n.A("message", err)))
		}
		os.Exit(1)
	}
}

// langFromArgs ищет флаг --lang в аргументах до полноценного разбора (форматы "--lang ru" и "--lang=ru").
// Возвращает пустую строку, если флаг не указан.
func langFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "--lang" && i+1 < len(args):
			return args[i+1]
		case len(a) > len("--lang=") && a[:len("--lang=")] == "--lang=":
			return a[len("--lang="):]
		}
	}
	return ""
}
