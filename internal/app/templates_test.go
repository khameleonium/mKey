package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
)

// TestBuiltinTemplates проверяет встроенные шаблоны проектов собранной программой (без настоящих
// устройств): каждый разбирается и проходит полную проверку движком (виды триггеров, действий,
// виртуальные устройства, привязки), у каждого есть название и описание на ru и en.
func TestBuiltinTemplates(t *testing.T) {
	t.Parallel()

	// Программа без значка в трее, плагинов и обновлений — им здесь нечего проверять.
	a, err := New(Options{
		Lang:    "en",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Enabled: func(id string) bool { return id != "tray" && id != "plugins" && id != "update" },
		Config:  noHardware(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	projects, err := contracts.LookupService[contracts.Projects](a.Manager.Services())
	if err != nil {
		t.Fatal(err)
	}
	events, err := contracts.LookupService[contracts.Events](a.Manager.Services())
	if err != nil {
		t.Fatal(err)
	}

	// Каждый шаблон: разбор, полная проверка, тексты на обоих языках.
	tpls := projects.Templates()
	if len(tpls) < 7 {
		t.Fatalf("templates = %d", len(tpls))
	}
	for _, tpl := range tpls {
		p, err := project.Parse([]byte(tpl.Content), tpl.ID)
		if err != nil {
			t.Errorf("%s: parse: %v", tpl.ID, err)
			continue
		}
		if err := events.ValidateProject(p); err != nil {
			t.Errorf("%s: validate: %v", tpl.ID, err)
		}
		for _, lang := range []string{"ru", "en"} {
			for _, key := range []string{"template." + tpl.ID + ".name", "template." + tpl.ID + ".description"} {
				if a.Translator.WithLang(lang).T(key) == key {
					t.Errorf("%s: no %s text %s", tpl.ID, lang, key)
				}
			}
		}
	}
}
