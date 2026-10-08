package builder

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/bundle"
	"github.com/khameleonium/mKey/internal/lib/project"
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
	}, []registry.Entry{{Module: New()}})
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

// memProjects — хранилище проектов в памяти: файл проекта по ID.
type memProjects struct {
	contracts.Projects
	files map[string]string
}

func (p memProjects) Get(id string) (contracts.ProjectState, bool) {
	raw, ok := p.files[id]
	if !ok {
		return contracts.ProjectState{}, false
	}
	pr, err := project.Parse([]byte(raw), id)
	st := contracts.ProjectState{Project: pr}
	if err != nil {
		st.Error = err.Error()
	}
	return st, true
}
func (p memProjects) Raw(id string) ([]byte, error) { return []byte(p.files[id]), nil }

// plugins — один плагин «web» с действием http.
type plugins struct{ contracts.Plugins }

func (plugins) List() []contracts.PluginInfo { return []contracts.PluginInfo{{ID: "web"}} }

// kind — вид действия в реестре (для проверки действий плагинов).
type kind struct{ id, provider string }

func (k kind) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: k.id, Provider: k.provider}
}

// setup — модуль с проектами, местами записей и скриптов во временной папке и «программой mkey».
func setup(t *testing.T, projects map[string]string) (*Module, string) {
	t.Helper()
	dir := t.TempDir()
	m := New()
	m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m.cfg.Dir = filepath.Join(dir, "builds")
	m.services = registry.NewServices()
	m.ext = registry.NewExtensions()
	m.now = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	exe := filepath.Join(dir, "mkey")
	if err := os.WriteFile(exe, []byte("\x7fELF fake mkey"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.exe = func() (string, error) { return exe, nil }
	_ = contracts.ProvideService[contracts.Projects](m.services, memProjects{files: projects})
	_ = contracts.ProvideService[contracts.Plugins](m.services, plugins{})
	_ = m.ext.Register(contracts.PointAction, kind{"send", "engine"})
	_ = m.ext.Register(contracts.PointAction, kind{"http", "web"})
	for id, sub := range map[string]string{contracts.PlaceRecordings: "rec", contracts.PlaceLuaScripts: "lua", contracts.PlaceShellScripts: "sh"} {
		_ = os.MkdirAll(filepath.Join(dir, sub), 0o700)
		_ = m.ext.Register(contracts.PointPlace, contracts.StaticPlace{M: contracts.ExtensionMeta{ID: id}, P: filepath.Join(dir, sub), Dir: true})
	}
	_ = os.WriteFile(filepath.Join(dir, "rec", "бег.mkrec"), []byte("mkrec 1\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "lua", "fish.lua"), []byte("print(1)"), 0o600)
	return m, dir
}

// game — проект с записью (внутри «Повторять»), скриптом Lua из файла и кодом bash в строке.
const game = `# комментарий сохраняется
version: 1
name: Моя игра
events:
  - id: go
    trigger: {type: manual}
    actions:
      - repeat:
          times: 2
          do:
            - play: {name: бег}
      - lua: {file: fish.lua}
      - shell: "echo hi"
`

// TestBuild собирает файл «работать, как проект» в папку по умолчанию и «выполнить и выйти» по
// пути: внутри — проект как есть, запись и скрипт, сведения о сборке.
func TestBuild(t *testing.T) {
	t.Parallel()
	m, dir := setup(t, map[string]string{"game": game})
	ctx := context.Background()

	res, err := m.Build(ctx, contracts.BuildRequest{Project: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != filepath.Join(dir, "builds", "Моя_игра") || strings.Join(res.Files, ",") != "recordings/бег.mkrec,scripts/lua/fish.lua" {
		t.Fatalf("result = %+v", res)
	}
	if st, err := os.Stat(res.Path); err != nil || st.Mode().Perm() != 0o755 || st.Size() != res.Size {
		t.Fatalf("file = %v %v", st, err)
	}
	c, err := bundle.ReadFile(res.Path)
	if err != nil || string(c.Project) != game || c.Manifest.Mode != bundle.ModeEvents || c.Manifest.Name != "Моя игра" || c.Manifest.Created != "2026-10-08T12:00:00Z" {
		t.Fatalf("contents = %+v, %v", c.Manifest, err)
	}

	// «Выполнить и выйти» по пути.
	out := filepath.Join(dir, "out", "run-once")
	if res, err = m.Build(ctx, contracts.BuildRequest{Project: "game", Mode: contracts.BuildModeOnce, Event: "go", Output: out}); err != nil || res.Path != out {
		t.Fatalf("once = %+v, %v", res, err)
	}
	if c, _ := bundle.ReadFile(out); c.Manifest.Event != "go" {
		t.Fatalf("once manifest = %+v", c.Manifest)
	}
}

// TestBuildErrors: нет проекта, нет события, нет записи, действие плагина — понятные ошибки.
func TestBuildErrors(t *testing.T) {
	t.Parallel()
	m, _ := setup(t, map[string]string{
		"empty":   "version: 1\nname: e\nevents: []\n",
		"game":    game,
		"missing": "version: 1\nname: x\nevents: [{id: e, trigger: {type: manual}, actions: [{play: нет}, {shell: {file: run.sh}}]}]\n",
		"plugin":  "version: 1\nname: x\nevents: [{id: e, trigger: {type: manual}, actions: [{http: {url: 'http://x'}}]}]\n",
	})
	ctx := context.Background()

	// Без записей и скриптов — пустой список, а не nil (окно читает его длину).
	if res, err := m.Build(ctx, contracts.BuildRequest{Project: "empty"}); err != nil || res.Files == nil {
		t.Fatalf("empty = %+v, %v", res, err)
	}
	for _, c := range []struct {
		req  contracts.BuildRequest
		want error
		text string
	}{
		{contracts.BuildRequest{Project: "nope"}, contracts.ErrBuildProject, "nope"},
		{contracts.BuildRequest{Project: "game", Mode: contracts.BuildModeOnce, Event: "zzz"}, contracts.ErrBuildEvent, "zzz"},
		{contracts.BuildRequest{Project: "missing"}, contracts.ErrBuildMissing, "нет.mkrec, run.sh"},
		{contracts.BuildRequest{Project: "plugin"}, contracts.ErrBuildPlugin, "http (web)"},
	} {
		_, err := m.Build(ctx, c.req)
		if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.text) {
			t.Errorf("%+v: %v", c.req, err)
		}
	}
}

// TestFileName проверяет имя файла по названию проекта.
func TestFileName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"Моя игра": "Моя_игра", "../x/y": "xy", "  ": "macro", ".hidden": "hidden"} {
		if got := fileName(in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}
