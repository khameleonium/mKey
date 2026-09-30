package i18n

import (
	"regexp"
	"slices"
	"testing"

	"mkey/internal/lib/dsl"
)

// placeholderRe находит плейсхолдеры вида {name} в тексте перевода.
var placeholderRe = regexp.MustCompile(`\{[a-zA-Z_][a-zA-Z0-9_]*\}`)

// TestCatalogCompleteness проверяет, что все языки содержат одинаковые ключи,
// непустые тексты и одинаковые плейсхолдеры (NFR-9, T0.6).
func TestCatalogCompleteness(t *testing.T) {
	t.Parallel()

	// Загружаем каталог и проверяем набор языков.
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if got := cat.Langs(); !slices.Equal(got, []string{"en", "ru"}) {
		t.Fatalf("langs = %v, want [en ru]", got)
	}

	// Сверяем каждый язык с английским (эталоном) в обе стороны.
	ref := cat[FallbackLang]
	for lang, msgs := range cat {
		for key, text := range ref {
			other, ok := msgs[key]
			if !ok {
				t.Errorf("%s: missing key %q", lang, key)
				continue
			}
			if other == "" {
				t.Errorf("%s: empty text for %q", lang, key)
			}
			if !samePlaceholders(text, other) {
				t.Errorf("%s: placeholders differ for %q: %q vs %q", lang, key, text, other)
			}
		}
		for key := range msgs {
			if _, ok := ref[key]; !ok {
				t.Errorf("%s: extra key %q not present in %s", lang, key, FallbackLang)
			}
		}
	}
}

// samePlaceholders сообщает, совпадают ли наборы плейсхолдеров в двух текстах.
func samePlaceholders(a, b string) bool {
	pa := placeholderRe.FindAllString(a, -1)
	pb := placeholderRe.FindAllString(b, -1)
	slices.Sort(pa)
	slices.Sort(pb)
	return slices.Equal(pa, pb)
}

// TestTranslate проверяет подстановку параметров и откат на английский и на ключ.
func TestTranslate(t *testing.T) {
	t.Parallel()

	// Тестовый каталог: в русском нет одного ключа.
	cat := Catalog{
		"en": {"hello": "Hello, {name}!", "only_en": "English only"},
		"ru": {"hello": "Привет, {name}!"},
	}
	tr := New(cat, "ru")

	// Перевод с плейсхолдером, откат на английский, откат на ключ.
	if got := tr.T("hello", A("name", "Вера")); got != "Привет, Вера!" {
		t.Errorf("T(hello) = %q", got)
	}
	if got := tr.T("only_en"); got != "English only" {
		t.Errorf("T(only_en) = %q", got)
	}
	if got := tr.T("missing.key"); got != "missing.key" {
		t.Errorf("T(missing) = %q", got)
	}

	// Неизвестный язык заменяется на английский.
	if got := New(cat, "de").Lang(); got != "en" {
		t.Errorf("Lang() = %q, want en", got)
	}
}

// TestDetectLang проверяет определение языка по переменным окружения.
func TestDetectLang(t *testing.T) {
	t.Parallel()

	// Таблица: переменные окружения → ожидаемый язык.
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "ru_RU.UTF-8"}, "ru"},
		{map[string]string{"LANG": "en_US.UTF-8"}, "en"},
		{map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "ru_RU.UTF-8"}, "en"},
		{map[string]string{"LC_MESSAGES": "ru_UA.UTF-8"}, "ru"},
		{map[string]string{}, "en"},
	}

	// Прогоняем случаи с подменённым getenv.
	for _, c := range cases {
		getenv := func(k string) string { return c.env[k] }
		if got := DetectLang(getenv); got != c.want {
			t.Errorf("DetectLang(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

// TestDSLErrorsTranslated проверяет, что у каждого кода ошибки языка макросов есть перевод.
func TestDSLErrorsTranslated(t *testing.T) {
	t.Parallel()
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range dsl.AllErrorCodes {
		if _, ok := cat[FallbackLang][code]; !ok {
			t.Errorf("no translation for %s", code)
		}
	}
}
