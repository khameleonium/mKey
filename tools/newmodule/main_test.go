package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRepo создаёт во временном каталоге минимальную копию файлов, которые меняет генератор.
func setupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Файлы-источники из настоящего репозитория (тест запускается из tools/newmodule).
	files := []string{
		"internal/app/modules.go",
		"internal/i18n/locales/ru.json",
		"internal/i18n/locales/en.json",
		"web/src/lib/i18n/ru.json",
		"web/src/lib/i18n/en.json",
	}

	// Копируем каждый файл, сохраняя относительный путь.
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		dst := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestGenerate проверяет, что генератор создаёт корректные файлы и подключает модуль.
func TestGenerate(t *testing.T) {
	t.Parallel()
	root := setupRepo(t)

	// Генерируем модуль zzgentest.
	if err := generate(root, "zzgentest"); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Все Go-файлы модуля и обновлённый modules.go синтаксически корректны.
	fset := token.NewFileSet()
	for _, f := range []string{"internal/zzgentest/doc.go", "internal/zzgentest/module.go", "internal/zzgentest/module_test.go", "internal/app/modules.go"} {
		if _, err := parser.ParseFile(fset, filepath.Join(root, f), nil, parser.ParseComments); err != nil {
			t.Errorf("%s does not parse: %v", f, err)
		}
	}

	// Модуль подключён в modules.go.
	mods, _ := os.ReadFile(filepath.Join(root, "internal/app/modules.go"))
	if !strings.Contains(string(mods), `"mkey/internal/zzgentest"`) || !strings.Contains(string(mods), "{Module: zzgentest.New(), Core: false},") {
		t.Errorf("modules.go not updated:\n%s", mods)
	}

	// Ключ i18n добавлен в переводы окна.
	for _, f := range []string{"web/src/lib/i18n/ru.json", "web/src/lib/i18n/en.json"} {
		data, _ := os.ReadFile(filepath.Join(root, f))
		if !strings.Contains(string(data), `"zzgentest.module.name"`) {
			t.Errorf("%s: key not added", f)
		}
	}

	// Повторная генерация того же модуля запрещена.
	if err := generate(root, "zzgentest"); err == nil {
		t.Error("second generate must fail")
	}
}

// TestGenerateRejectsBadNames проверяет отказ для недопустимых имён.
func TestGenerateRejectsBadNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "Demo", "1demo", "my-mod", "my_mod"} {
		if err := generate(t.TempDir(), name); err == nil {
			t.Errorf("name %q must be rejected", name)
		}
	}
}
