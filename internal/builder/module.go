package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/buildinfo"
	"github.com/khameleonium/mKey/internal/lib/bundle"
	"github.com/khameleonium/mKey/internal/lib/mkrec"
	"github.com/khameleonium/mKey/internal/lib/paths"
	"github.com/khameleonium/mKey/internal/lib/project"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "builder"

// Config — настройки модуля из секции modules.builder в config.yaml.
type Config struct {
	// Dir — папка собранных файлов ("" — ~/.local/share/mkey/builds).
	Dir string `json:"dir"`
}

// Module — модуль builder: собирает самостоятельный файл макроса (contracts.MacroBuilder).
type Module struct {
	// log — логгер; cfg — настройки; services и ext — сервисы и точки расширения (ищутся при
	// сборке: модули проектов, событий и плагинов могут быть отключены).
	log      *slog.Logger
	cfg      Config
	services contracts.ServiceRegistry
	ext      contracts.ExtensionRegistry
	// exe — путь программы mkey, к которой дописывается проект (в тестах подменяется); now — часы.
	exe func() (string, error)
	now func() time.Time
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{exe: os.Executable, now: time.Now}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, публикует сервис сборки и регистрирует папку собранных файлов.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Логгер, настройки, реестры.
	m.log = host.Logger()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	m.services, m.ext = host.Services(), host.Extensions()

	// Сервис и место «Собранные макросы» (видно в «Где что лежит» и `mkey paths`).
	if err := contracts.ProvideService[contracts.MacroBuilder](m.services, m); err != nil {
		return err
	}
	return m.ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M:   contracts.ExtensionMeta{ID: contracts.PlaceBuilds, NameKey: "place.builds", DescriptionKey: "place.builds.description", Provider: ModuleID},
		P:   m.dir(),
		Dir: true,
		N:   35,
	})
}

// dir — папка собранных файлов.
func (m *Module) dir() string {
	if m.cfg.Dir != "" {
		return m.cfg.Dir
	}
	return filepath.Join(paths.Data(os.Getenv), "builds")
}

// Start запускает модуль (фоновой работы нет).
func (m *Module) Start(context.Context) error { return nil }

// Stop останавливает модуль.
func (m *Module) Stop(context.Context) error { return nil }

// Build собирает самостоятельный файл макроса (contracts.MacroBuilder): проверяет проект и
// событие, находит записи и скрипты, на которые он ссылается, и дописывает всё к программе mkey.
func (m *Module) Build(_ context.Context, req contracts.BuildRequest) (contracts.BuildResult, error) {
	// Проект: есть, без ошибок, проходит полную проверку.
	projects, err := contracts.LookupService[contracts.Projects](m.services)
	if err != nil {
		return contracts.BuildResult{}, fmt.Errorf("%w: %w", contracts.ErrBuildProject, err)
	}
	st, ok := projects.Get(req.Project)
	if !ok {
		return contracts.BuildResult{}, fmt.Errorf("%w: no project %q", contracts.ErrBuildProject, req.Project)
	}
	if st.Error != "" {
		return contracts.BuildResult{}, fmt.Errorf("%w: %s", contracts.ErrBuildProject, st.Error)
	}
	p := st.Project
	if events, err := contracts.LookupService[contracts.Events](m.services); err == nil {
		if err := events.ValidateProject(p); err != nil {
			return contracts.BuildResult{}, fmt.Errorf("%w: %w", contracts.ErrBuildProject, err)
		}
	}
	raw, err := projects.Raw(req.Project)
	if err != nil {
		return contracts.BuildResult{}, fmt.Errorf("%w: %w", contracts.ErrBuildProject, err)
	}

	// Режим: «работать, как проект» или «выполнить событие и выйти» (событие должно быть).
	mode := req.Mode
	if mode == "" {
		mode = contracts.BuildModeEvents
	}
	if mode != contracts.BuildModeEvents && mode != contracts.BuildModeOnce {
		return contracts.BuildResult{}, fmt.Errorf("%w: unknown mode %q", contracts.ErrBuildProject, mode)
	}
	if mode == contracts.BuildModeOnce && !slices.ContainsFunc(p.Events, func(e project.Event) bool { return e.ID == req.Event }) {
		return contracts.BuildResult{}, fmt.Errorf("%w: %q", contracts.ErrBuildEvent, req.Event)
	}

	// Что ещё нужно проекту: записи и скрипты; действия плагинов в файл не положить.
	refs, err := m.references(p)
	if err != nil {
		return contracts.BuildResult{}, err
	}
	files, err := m.collect(refs)
	if err != nil {
		return contracts.BuildResult{}, err
	}

	// Сведения о сборке.
	name := p.Name
	if name == "" {
		name = p.ID
	}
	c := bundle.Contents{
		Manifest: bundle.Manifest{Format: bundle.Format, Name: name, Project: p.ID, Mode: mode,
			Created: m.now().UTC().Format(time.RFC3339), Version: buildinfo.Version, Creator: buildinfo.Creator},
		Project: raw,
		Files:   files,
	}
	if mode == contracts.BuildModeOnce {
		c.Manifest.Event = req.Event
	}

	// Куда писать: путь из запроса или папка собранных файлов, имя — по названию проекта.
	out := req.Output
	if out == "" {
		out = filepath.Join(m.dir(), fileName(name))
	}
	size, err := m.write(out, c)
	if err != nil {
		return contracts.BuildResult{}, err
	}
	res := contracts.BuildResult{Path: out, Size: size, Files: slices.Sorted(maps.Keys(files))}
	m.log.Info("macro built", "project", p.ID, "mode", mode, "path", out, "size", size, "files", len(files))
	return res, nil
}

// refs — на что ссылается проект: имена записей, файлы скриптов Lua и bash.
type refs struct {
	recordings, lua, shell []string
}

// references обходит действия всех событий проекта (вместе с вложенными в «Повторять», «Если» и
// т.п.): play → запись, lua/shell с file → скрипт. Действие, которое добавил плагин, — ошибка.
func (m *Module) references(p project.Project) (refs, error) {
	// Действия плагинов: ID видов действий, которые зарегистрировали плагины.
	pluginActions := map[string]string{}
	if pl, err := contracts.LookupService[contracts.Plugins](m.services); err == nil {
		ids := map[string]bool{}
		for _, info := range pl.List() {
			ids[info.ID] = true
		}
		for _, e := range m.ext.List(contracts.PointAction) {
			if meta := e.Meta(); ids[meta.Provider] {
				pluginActions[meta.ID] = meta.Provider
			}
		}
	}

	// Действия проекта — как данные JSON: обходим всё дерево.
	data, err := json.Marshal(p.Events)
	if err != nil {
		return refs{}, err
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return refs{}, err
	}
	var r refs
	var plugin error

	// action учитывает действие вида kind с параметрами val.
	action := func(kind string, val any) {
		switch {
		case pluginActions[kind] != "":
			plugin = fmt.Errorf("%w: %s (%s)", contracts.ErrBuildPlugin, kind, pluginActions[kind])
		case kind == "play":
			if name := field(val, "name"); name != "" {
				r.recordings = appendNew(r.recordings, name)
			}
		case kind == "lua" && isObject(val):
			if f := field(val, "file"); f != "" {
				r.lua = appendNew(r.lua, f)
			}
		case kind == "shell" && isObject(val):
			if f := field(val, "file"); f != "" {
				r.shell = appendNew(r.shell, f)
			}
		}
	}

	// Действие бывает в двух видах: разобранное ({type, value}) и как в файле ({play: …}) —
	// так хранятся действия внутри «Повторять», «Если» и т.п.
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				walk(it)
			}
		case map[string]any:
			if kind, ok := x["type"].(string); ok {
				if val, has := x["value"]; has {
					action(kind, val)
				}
			}
			for k, val := range x {
				action(k, val)
				walk(val)
			}
		}
	}
	walk(tree)
	return r, plugin
}

// field — строка v (краткая запись «play: имя») или поле key объекта v.
func field(v any, key string) string {
	switch x := v.(type) {
	case string:
		if key == "name" {
			return x
		}
	case map[string]any:
		s, _ := x[key].(string)
		return s
	}
	return ""
}

// isObject сообщает, что v — объект (у lua/shell строка — это код, а не файл).
func isObject(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

// appendNew добавляет s, если его ещё нет.
func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// collect читает записи и скрипты из их папок (места recordings, lua_scripts, shell_scripts).
// Нет файла или папки — ErrBuildMissing с перечнем.
func (m *Module) collect(r refs) (map[string][]byte, error) {
	files := map[string][]byte{}
	var missing []string
	for _, g := range []struct {
		place, dir, ext string
		names           []string
	}{
		{contracts.PlaceRecordings, bundle.RecordingsDir, mkrec.FileExt, r.recordings},
		{contracts.PlaceLuaScripts, bundle.LuaDir, "", r.lua},
		{contracts.PlaceShellScripts, bundle.ShellDir, "", r.shell},
	} {
		dir := m.placePath(g.place)
		for _, n := range g.names {
			// Имя — только файл в своей папке (без «/» и «..»).
			base := n + g.ext
			if dir == "" || base != filepath.Base(base) || strings.HasPrefix(base, ".") {
				missing = append(missing, base)
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, base))
			if err != nil {
				missing = append(missing, base)
				continue
			}
			files[g.dir+"/"+base] = data
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", contracts.ErrBuildMissing, strings.Join(missing, ", "))
	}
	return files, nil
}

// placePath — путь места id ("" — места нет: модуль отключён).
func (m *Module) placePath(id string) string {
	e, ok := m.ext.Get(contracts.PointPlace, id)
	if !ok {
		return ""
	}
	if p, ok := e.(contracts.Place); ok {
		return p.Path()
	}
	return ""
}

// write собирает файл out: программа mkey + содержимое c. Пишется во временный файл рядом и
// переименовывается (недописанного файла не бывает); права — 0755 (запускается двойным щелчком).
func (m *Module) write(out string, c bundle.Contents) (int64, error) {
	// Программа mkey, к которой дописываем.
	exe, err := m.exe()
	if err != nil {
		return 0, err
	}
	src, err := os.Open(exe)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	st, err := src.Stat()
	if err != nil {
		return 0, err
	}

	// Временный файл в папке назначения.
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".mkey-build-*")
	if err != nil {
		return 0, err
	}
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }
	if err := bundle.Write(tmp, src, st.Size(), c); err != nil {
		cleanup()
		return 0, err
	}

	// Права, размер, переименование на место.
	if err := tmp.Chmod(0o755); err != nil {
		cleanup()
		return 0, err
	}
	info, err := tmp.Stat()
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	return info.Size(), nil
}

// fileName — имя файла по названию: без «/», пробелы — «_», не начинается с точки.
func fileName(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r < 32:
			return -1
		case r == ' ':
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	s = strings.TrimLeft(s, ".")
	if s == "" {
		s = "macro"
	}
	return s
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module       = (*Module)(nil)
	_ contracts.MacroBuilder = (*Module)(nil)
)
