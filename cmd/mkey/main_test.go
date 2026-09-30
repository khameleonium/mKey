package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"mkey/internal/i18n"
)

// TestLangFromArgs проверяет предварительный поиск флага --lang.
func TestLangFromArgs(t *testing.T) {
	t.Parallel()

	// Таблица: аргументы → ожидаемый язык.
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"version", "--lang", "ru"}, "ru"},
		{[]string{"--lang=en", "version"}, "en"},
		{[]string{"version"}, ""},
		{[]string{"--lang"}, ""},
	}
	for _, c := range cases {
		if got := langFromArgs(c.args); got != c.want {
			t.Errorf("langFromArgs(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// runCmd выполняет корневую команду с аргументами на заданном языке и возвращает вывод.
func runCmd(t *testing.T, lang string, args ...string) string {
	t.Helper()
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	cmd := newRootCmd(i18n.New(cat, lang))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return out.String()
}

// TestVersion проверяет человекочитаемый и JSON-вывод `mkey version`.
func TestVersion(t *testing.T) {
	t.Parallel()

	// Русский вывод содержит версию и переведённый текст.
	if out := runCmd(t, "ru", "version"); !strings.Contains(out, "mKey dev") || !strings.Contains(out, "коммит") {
		t.Errorf("ru output = %q", out)
	}

	// JSON-вывод разбирается и содержит поле version.
	var v map[string]string
	if err := json.Unmarshal([]byte(runCmd(t, "en", "version", "--json")), &v); err != nil || v["version"] != "dev" {
		t.Errorf("json output = %v, err = %v", v, err)
	}
}
