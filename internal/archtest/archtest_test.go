package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// modulePath — путь Go-модуля mKey (из go.mod).
const modulePath = "mkey"

// infra — инфраструктурные пакеты ядра, доступные всем модулям.
var infra = map[string]bool{"contracts": true, "registry": true, "bus": true, "i18n": true}

// assembly — пакеты-сборщики, которым разрешено импортировать всё.
var assembly = map[string]bool{"app": true, "archtest": true}

// group возвращает «верхний» раздел пакета внутри internal/: для internal/desktop/x11 — "desktop",
// для библиотек internal/lib/keys — "lib". Для пакетов вне internal/ возвращает пустую строку.
func group(importPath string) string {
	rest, ok := strings.CutPrefix(importPath, modulePath+"/internal/")
	if !ok {
		return ""
	}
	top, _, _ := strings.Cut(rest, "/")
	return top
}

// allowed сообщает, может ли пакет importer импортировать пакет imported.
// Возвращает текстовое описание правила, если импорт запрещён.
func allowed(importer, imported string) (bool, string) {
	// Импорты вне internal/ (stdlib, сторонние, pkg/) правилами не ограничены.
	to := group(imported)
	if to == "" {
		return true, ""
	}
	from := group(importer)

	// Применяем правила по группе импортирующего пакета.
	switch {
	case assembly[from]:
		return true, ""
	case from == "lib":
		return to == "lib", "internal/lib may import only internal/lib"
	case from == "contracts":
		return to == "lib", "internal/contracts may import only internal/lib"
	case infra[from]:
		return to == "contracts" || to == "lib", "core infrastructure may import only contracts and lib"
	default:
		// Модуль: инфраструктура, библиотеки и собственные подпакеты.
		return infra[to] || to == "lib" || to == from,
			"modules must not import other modules; use internal/contracts or the bus"
	}
}

// TestModuleBoundaries разбирает все не-тестовые Go-файлы internal/ и проверяет правила импорта.
func TestModuleBoundaries(t *testing.T) {
	t.Parallel()

	// Обходим дерево internal/ (тест запускается из internal/archtest).
	root := ".."
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Пропускаем каталоги, не-Go и тестовые файлы (тестам разрешено больше — фейки, сборка).
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Определяем import path пакета по каталогу файла.
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		importer := modulePath + "/internal/" + filepath.ToSlash(rel)

		// Разбираем только импорты файла и проверяем каждый.
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			imported, _ := strconv.Unquote(imp.Path.Value)
			if ok, rule := allowed(importer, imported); !ok {
				t.Errorf("%s: import %q is forbidden: %s", path, imported, rule)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
}

// TestAllowedRules проверяет сами правила на характерных примерах.
func TestAllowedRules(t *testing.T) {
	t.Parallel()

	// Таблица: кто импортирует, что импортирует, разрешено ли.
	cases := []struct {
		from, to string
		want     bool
	}{
		{"mkey/internal/recorder", "mkey/internal/contracts", true},
		{"mkey/internal/recorder", "mkey/internal/lib/keys", true},
		{"mkey/internal/recorder", "mkey/internal/input", false},
		{"mkey/internal/desktop/x11", "mkey/internal/desktop", true},
		{"mkey/internal/desktop/x11", "mkey/internal/output", false},
		{"mkey/internal/lib/dsl", "mkey/internal/lib/keys", true},
		{"mkey/internal/lib/dsl", "mkey/internal/contracts", false},
		{"mkey/internal/contracts", "mkey/internal/registry", false},
		{"mkey/internal/registry", "mkey/internal/contracts", true},
		{"mkey/internal/registry", "mkey/internal/bus", false},
		{"mkey/internal/app", "mkey/internal/recorder", true},
		{"mkey/internal/recorder", "github.com/spf13/cobra", true},
	}
	for _, c := range cases {
		if got, _ := allowed(c.from, c.to); got != c.want {
			t.Errorf("allowed(%s → %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
