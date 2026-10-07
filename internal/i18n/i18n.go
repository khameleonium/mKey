package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// FallbackLang — язык, на который переводчик откатывается при отсутствии перевода.
const FallbackLang = "en"

// locales — встроенные файлы переводов.
//
//go:embed locales/*.json
var locales embed.FS

// Catalog — все загруженные переводы: язык → ключ → текст.
type Catalog map[string]map[string]string

// LoadCatalog читает все встроенные файлы переводов.
func LoadCatalog() (Catalog, error) {
	// Перечисляем файлы локалей.
	entries, err := locales.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("read locales: %w", err)
	}

	// Разбираем каждый файл; имя файла без расширения — код языка.
	cat := make(Catalog, len(entries))
	for _, e := range entries {
		data, err := locales.ReadFile(path.Join("locales", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read locale %s: %w", e.Name(), err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			return nil, fmt.Errorf("parse locale %s: %w", e.Name(), err)
		}
		cat[strings.TrimSuffix(e.Name(), ".json")] = msgs
	}
	return cat, nil
}

// Langs возвращает отсортированный список языков каталога.
func (c Catalog) Langs() []string {
	langs := make([]string, 0, len(c))
	for l := range c {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	return langs
}

// Translator — реализация contracts.Translator для одного языка.
type Translator struct {
	// lang — текущий язык.
	lang string
	// cat — каталог всех переводов.
	cat Catalog
}

// New создаёт переводчик для языка lang. Неизвестный язык заменяется на FallbackLang.
func New(cat Catalog, lang string) *Translator {
	if _, ok := cat[lang]; !ok {
		lang = FallbackLang
	}
	return &Translator{lang: lang, cat: cat}
}

// Lang возвращает текущий язык.
func (t *Translator) Lang() string { return t.lang }

// WithLang возвращает переводчик на язык lang с тем же каталогом.
func (t *Translator) WithLang(lang string) contracts.Translator { return New(t.cat, lang) }

// T возвращает перевод ключа с подставленными параметрами.
func (t *Translator) T(key string, args ...contracts.Arg) string {
	// Ищем перевод: текущий язык → язык по умолчанию → сам ключ.
	msg, ok := t.cat[t.lang][key]
	if !ok {
		msg, ok = t.cat[FallbackLang][key]
	}
	if !ok {
		return key
	}

	// Подставляем плейсхолдеры {name}.
	for _, a := range args {
		msg = strings.ReplaceAll(msg, "{"+a.Name+"}", fmt.Sprint(a.Value))
	}
	return msg
}

// A — короткий конструктор параметра перевода: i18n.A("version", v).
func A(name string, value any) contracts.Arg {
	return contracts.Arg{Name: name, Value: value}
}

// DetectLang определяет язык по переменным окружения LC_ALL, LC_MESSAGES, LANG
// (в этом порядке, как в POSIX). getenv передаётся явно, чтобы функцию было легко тестировать.
// Возвращает "ru" для русских локалей и FallbackLang для остальных.
func DetectLang(getenv func(string) string) string {
	// Берём первую непустую переменную локали.
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := getenv(name)
		if v == "" {
			continue
		}

		// Сравниваем только код языка ("ru_RU.UTF-8" → "ru").
		if strings.HasPrefix(strings.ToLower(v), "ru") {
			return "ru"
		}
		return FallbackLang
	}
	return FallbackLang
}

// Проверка на этапе компиляции, что Translator реализует контракт.
var _ contracts.Translator = (*Translator)(nil)
