// Команда newmodule создаёт каркас нового модуля mKey (T0.8, docs/modules.md).
//
// Использование: make new-module NAME=<id>  (или go run ./tools/newmodule -name <id>)
//
// Что создаётся:
//   - internal/<id>/doc.go, module.go, module_test.go — модуль с пустым жизненным циклом;
//   - web/src/features/<id>/index.ts — папка фичи во фронтенде;
//   - i18n-ключ <id>.module.name (название в «Диагностике») в web/src/lib/i18n/*.json;
//   - строка подключения модуля и импорт в internal/app/modules.go (по маркерам mkey:modules и mkey:imports).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// nameRe — допустимое имя модуля: оно же имя Go-пакета и каталога.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// Маркеры в internal/app/modules.go, над которыми генератор вставляет строки.
const (
	importsMarker = "// mkey:imports"
	modulesMarker = "// mkey:modules"
)

// main разбирает флаги и создаёт модуль; при ошибке завершает процесс с кодом 1.
func main() {
	// Разбираем аргументы командной строки.
	name := flag.String("name", "", "module id (lowercase letters and digits)")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	// Создаём модуль и сообщаем результат.
	if err := generate(*root, *name); err != nil {
		fmt.Fprintln(os.Stderr, "newmodule:", err)
		os.Exit(1)
	}
	fmt.Printf("module %q created: internal/%s, web/src/features/%s; registered in internal/app/modules.go\n", *name, *name, *name)
}

// generate создаёт все файлы модуля name в репозитории root.
func generate(root, name string) error {
	// Проверяем имя и то, что модуль ещё не существует.
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid module name %q: use lowercase letters and digits, starting with a letter", name)
	}
	modDir := filepath.Join(root, "internal", name)
	if _, err := os.Stat(modDir); err == nil {
		return fmt.Errorf("internal/%s already exists", name)
	}

	// Создаём Go-файлы модуля по шаблонам.
	data := struct{ Name string }{name}
	for file, tpl := range goTemplates {
		if err := writeTemplate(filepath.Join(modDir, file), tpl, data); err != nil {
			return err
		}
	}

	// Создаём папку фичи во фронтенде.
	if err := writeTemplate(filepath.Join(root, "web", "src", "features", name, "index.ts"), featureTemplate, data); err != nil {
		return err
	}

	// Добавляем i18n-ключ с названием модуля в переводы окна (его показывает «Диагностика» →
	// «Части программы»); название стоит сразу поправить на понятное человеку.
	key := name + ".module.name"
	texts := map[string]string{"ru": "Модуль " + name, "en": "Module " + name}
	dir := filepath.Join(root, "web", "src", "lib", "i18n")
	for lang, text := range texts {
		if err := addKey(filepath.Join(dir, lang+".json"), key, text); err != nil {
			return err
		}
	}

	// Подключаем модуль в internal/app/modules.go.
	return register(filepath.Join(root, "internal", "app", "modules.go"), name)
}

// writeTemplate выполняет шаблон tpl с данными data и записывает результат в path, создавая каталоги.
func writeTemplate(path, tpl string, data any) error {
	// Выполняем шаблон в буфер.
	var buf bytes.Buffer
	if err := template.Must(template.New(filepath.Base(path)).Parse(tpl)).Execute(&buf, data); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}

	// Go-файлы форматируем так же, как gofmt, чтобы они сразу проходили линтер.
	out := buf.Bytes()
	if strings.HasSuffix(path, ".go") {
		formatted, err := format.Source(out)
		if err != nil {
			return fmt.Errorf("format %s: %w", path, err)
		}
		out = formatted
	}

	// Записываем файл.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// addKey добавляет ключ key с текстом text в JSON-каталог переводов path.
// Файл перезаписывается с ключами, отсортированными по алфавиту (так диффы стабильны).
func addKey(path, key, text string) error {
	// Читаем существующий каталог.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	msgs := map[string]string{}
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if _, exists := msgs[key]; exists {
		return fmt.Errorf("%s: key %q already exists", path, key)
	}
	msgs[key] = text

	// Записываем обратно без HTML-экранирования, с отступом в 2 пробела (как у prettier).
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(msgs); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// register вставляет импорт и запись модуля в modules.go над маркерами.
func register(path, name string) error {
	// Читаем файл сборки программы.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src := string(raw)

	// Вставляем импорт и строку в список модулей.
	src, err = insertAbove(src, importsMarker, fmt.Sprintf("\t%q\n", "github.com/khameleonium/mKey/internal/"+name))
	if err != nil {
		return err
	}
	src, err = insertAbove(src, modulesMarker, fmt.Sprintf("\t\t{Module: %s.New(), Core: false},\n", name))
	if err != nil {
		return err
	}

	// Форматируем результат, как gofmt, и записываем.
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format modules.go: %w", err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

// insertAbove вставляет строку line перед строкой, содержащей маркер marker.
func insertAbove(src, marker, line string) (string, error) {
	// Находим начало строки с маркером.
	i := strings.Index(src, marker)
	if i < 0 {
		return "", errors.New("marker " + marker + " not found in modules.go")
	}
	lineStart := strings.LastIndex(src[:i], "\n") + 1

	// Вставляем новую строку перед ней.
	return src[:lineStart] + line + src[lineStart:], nil
}

// goTemplates — шаблоны Go-файлов модуля: имя файла → текст шаблона.
var goTemplates = map[string]string{
	"doc.go": `// Package {{.Name}} — модуль mKey «{{.Name}}».
//
// TODO: опишите назначение модуля, его основные типы, какие контракты он предоставляет
// и использует, какие точки расширения регистрирует и как его расширять (AGENTS.md §6.1).
package {{.Name}}
`,
	"module.go": `package {{.Name}}

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/khameleonium/mKey/internal/contracts"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "{{.Name}}"

// Config — настройки модуля из секции modules.{{.Name}} в config.yaml.
type Config struct{}

// Module — реализация contracts.Module для модуля «{{.Name}}».
type Module struct {
	// log — логгер модуля (заполняется в Init).
	log *slog.Logger
	// cfg — настройки модуля (заполняются в Init).
	cfg Config
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и регистрирует сервисы и точки расширения модуля.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Запоминаем логгер и читаем секцию конфига.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// TODO: зарегистрируйте сервисы (contracts.ProvideService) и расширения (host.Extensions().Register).
	return nil
}

// Start запускает фоновую работу модуля.
func (m *Module) Start(context.Context) error {
	return nil
}

// Stop останавливает модуль и освобождает его ресурсы.
func (m *Module) Stop(context.Context) error {
	return nil
}

// Проверка на этапе компиляции, что Module реализует контракт.
var _ contracts.Module = (*Module)(nil)
`,
	"module_test.go": `package {{.Name}}

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/registry"
)

// TestLifecycle проверяет, что модуль проходит полный цикл Init → Start → Stop.
func TestLifecycle(t *testing.T) {
	t.Parallel()

	// Собираем менеджер с одним этим модулем.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	m, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        bus.New(0),
	}, []registry.Entry{ {Module: New()} })
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Запускаем и останавливаем; модуль должен быть в состоянии running, затем stopped.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := m.Statuses()[0]; st.State != registry.StateRunning {
		t.Fatalf("state = %s, err = %v", st.State, st.Err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
`,
}

// featureTemplate — заготовка папки фичи во фронтенде.
const featureTemplate = `// Фронтенд-часть модуля «{{.Name}}»: страницы, компоненты и блоки конструктора модуля.
// TODO: опишите, что показывает эта фича (AGENTS.md §6.1).

/** Идентификатор модуля (совпадает с ID Go-модуля internal/{{.Name}}). */
export const MODULE_ID = "{{.Name}}";
`
