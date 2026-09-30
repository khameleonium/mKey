package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/project"
	"mkey/internal/registry"
)

// memProjects — хранилище проектов в памяти для проверки эндпоинтов редактора.
type memProjects struct {
	contracts.Projects
	mu    sync.Mutex
	files map[string]string
}

func (p *memProjects) Get(id string) (contracts.ProjectState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, ok := p.files[id]
	if !ok {
		return contracts.ProjectState{}, false
	}
	pr, err := project.Parse([]byte(raw), id)
	st := contracts.ProjectState{Project: pr, Path: id + ".mkey.yaml"}
	if err != nil {
		st.Error = err.Error()
	}
	return st, true
}

func (p *memProjects) Raw(id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return []byte(p.files[id]), nil
}

func (p *memProjects) SaveRaw(id string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[id] = string(data)
	return nil
}

func (p *memProjects) Save(id string, _ project.Project) error {
	return p.SaveRaw(id, []byte("version: 1\nname: saved\nevents: []\n"))
}

func (p *memProjects) Create(id string, data []byte) (string, error) {
	if id == "" {
		return "", errors.New("empty id")
	}
	return id, p.SaveRaw(id, data)
}

func (p *memProjects) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.files[id]; !ok {
		return errors.New("not found")
	}
	delete(p.files, id)
	return nil
}

func (p *memProjects) Templates() []contracts.Template {
	return []contracts.Template{{ID: "autoclicker", Content: "version: 1\nname: t\nevents: []\n"}}
}

// TestProjectEditorEndpoints проверяет чтение, сохранение, создание, удаление проектов и шаблоны.
func TestProjectEditorEndpoints(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	store := &memProjects{files: map[string]string{"a": "version: 1\nname: A\nevents: []\n"}}
	m.svc.projects = store
	h := m.routes(true)

	// Чтение: структура и текст файла; неизвестный проект — 404.
	if code, out := call(t, h, "GET", "/api/v1/projects/a", "", nil); code != 200 || !strings.Contains(out["raw"].(string), "name: A") {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, _ := call(t, h, "GET", "/api/v1/projects/zzz", "", nil); code != 404 {
		t.Fatalf("get missing: %d", code)
	}

	// Сохранение текста: верный сохраняется, с ошибкой — 400 и файл не меняется.
	if code, out := call(t, h, "PUT", "/api/v1/projects/a", `{"raw":"version: 1\nname: B\nevents: []\n"}`, nil); code != 200 {
		t.Fatalf("put raw: %d %v", code, out)
	}
	if code, _ := call(t, h, "PUT", "/api/v1/projects/a", `{"raw":"version: 99\n"}`, nil); code != 400 || !strings.Contains(store.files["a"], "name: B") {
		t.Fatalf("put bad raw: %d %q", code, store.files["a"])
	}

	// Сохранение структуры и пустое тело.
	if code, _ := call(t, h, "PUT", "/api/v1/projects/a", `{"project":{"version":1,"name":"C","events":[]}}`, nil); code != 200 || !strings.Contains(store.files["a"], "saved") {
		t.Fatalf("put project: %d", code)
	}
	if code, _ := call(t, h, "PUT", "/api/v1/projects/a", `{}`, nil); code != 400 {
		t.Fatalf("put empty: %d", code)
	}

	// Проверка без сохранения.
	if code, _ := call(t, h, "POST", "/api/v1/projects/validate", `{"raw":"version: 1\nname: X\nevents: []\n"}`, nil); code != 200 {
		t.Fatalf("validate: %d", code)
	}

	// Шаблоны с переведёнными названиями; создание из шаблона и из неизвестного шаблона.
	code, out := call(t, h, "GET", "/api/v1/templates", "", nil)
	tpl := out["templates"].([]any)[0].(map[string]any)
	if code != 200 || tpl["name"] != "Autoclicker" {
		t.Fatalf("templates: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/projects", `{"id":"n","template":"autoclicker"}`, nil); code != 200 || out["id"] != "n" {
		t.Fatalf("create: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/v1/projects", `{"id":"n2","template":"nope"}`, nil); code != 404 {
		t.Fatalf("create unknown template: %d", code)
	}

	// Удаление.
	if code, _ := call(t, h, "DELETE", "/api/v1/projects/n", "", nil); code != 200 || store.files["n"] != "" {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := call(t, h, "DELETE", "/api/v1/projects/n", "", nil); code != 400 {
		t.Fatalf("delete missing: %d", code)
	}
}

// TestLogsEndpoint проверяет выдачу последних строк журнала.
func TestLogsEndpoint(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.LogFile = filepath.Join(t.TempDir(), "mkey.log")
	h := m.routes(true)

	// Нет файла — пустой список.
	if code, out := call(t, h, "GET", "/api/v1/logs", "", nil); code != 200 || len(out["lines"].([]any)) != 0 {
		t.Fatalf("no file: %d %v", code, out)
	}

	// Последние 2 строки из 3.
	if err := os.WriteFile(m.cfg.LogFile, []byte("1\n2\n3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := call(t, h, "GET", "/api/v1/logs?lines=2", "", nil)
	lines := out["lines"].([]any)
	if code != 200 || len(lines) != 2 || lines[0] != "2" {
		t.Fatalf("tail: %d %v", code, out)
	}
}

// fakeExt — вид действия для проверки реестра.
type fakeExt struct{ meta contracts.ExtensionMeta }

func (f fakeExt) Meta() contracts.ExtensionMeta { return f.meta }

// TestRegistryLocalized проверяет названия и подписи полей в ответе /registry на языке клиента.
func TestRegistryLocalized(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	ext := registry.NewExtensions()
	_ = ext.Register(contracts.PointAction, fakeExt{contracts.ExtensionMeta{
		ID: "wheel", NameKey: "action.wheel", Category: "mouse", Provider: "test",
		ParamsSchema: []byte(`{"type":"object","properties":{"direction":{"enum":["Up","Down"]},"count":{"type":"integer"}}}`),
	}})
	_ = ext.Register(contracts.PointAction, fakeExt{contracts.ExtensionMeta{
		ID: "pause", NameKey: "action.pause", Provider: "test",
		ParamsSchema: []byte(`{"oneOf":[{"type":"integer"},{"type":"object","properties":{"min_ms":{"type":"integer"}}}]}`),
	}})
	_ = ext.Register(contracts.PointAction, fakeExt{contracts.ExtensionMeta{ID: "x", NameKey: "action.x", Provider: "test"}})
	m.svc.ext = ext
	h := m.routes(true)

	code, out := call(t, h, "GET", "/api/v1/registry", "", map[string]string{"Accept-Language": "ru"})
	acts := out["action"].([]any)
	if code != 200 || len(acts) != 3 {
		t.Fatalf("registry: %d %v", code, out)
	}
	byID := map[string]map[string]any{}
	for _, a := range acts {
		am := a.(map[string]any)
		byID[am["id"].(string)] = am
	}

	// Название, категория, подписи полей и значений.
	w := byID["wheel"]
	props := w["params_schema"].(map[string]any)["properties"].(map[string]any)
	dir := props["direction"].(map[string]any)
	if w["name"] != "Прокрутить колесо" || w["category_name"] != "Мышь" || dir["title"] != "Куда" || dir["x-enum-labels"].([]any)[1] != "вниз" {
		t.Fatalf("wheel: %v", w)
	}

	// Подписи вариантов и полей внутри вариантов.
	variants := byID["pause"]["params_schema"].(map[string]any)["oneOf"].([]any)
	if variants[0].(map[string]any)["title"] != "Точно" ||
		variants[1].(map[string]any)["properties"].(map[string]any)["min_ms"].(map[string]any)["title"] != "От, мс" {
		t.Fatalf("pause: %v", variants)
	}

	// Порядок полей схемы сохранён.
	raw := call2(t, h, "/api/v1/registry")
	if i, j := strings.Index(raw, `"direction"`), strings.Index(raw, `"count"`); i < 0 || j < i {
		t.Fatalf("field order lost: %s", raw)
	}

	// Нет перевода — название равно ID.
	if byID["x"]["name"] != "x" {
		t.Fatalf("x: %v", byID["x"])
	}
}

// TestConvertEndpoints проверяет, что без движка перевод блоков недоступен (503).
func TestConvertEndpoints(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	h := m.routes(true)
	if code, _ := call(t, h, "POST", "/api/v1/dsl/to_actions", `{"text":"{A}"}`, nil); code != 503 {
		t.Fatalf("to_actions: %d", code)
	}
	if code, _ := call(t, h, "POST", "/api/v1/actions/to_dsl", `{"actions":[]}`, nil); code != 503 {
		t.Fatalf("to_dsl: %d", code)
	}
}

// captureInput — источник ввода, который после подписки отдаёт заранее заданные события.
type captureInput struct {
	contracts.InputSource
	events []ev.Event
}

func (c *captureInput) Subscribe(int) (<-chan contracts.InputEvent, func()) {
	ch := make(chan contracts.InputEvent, len(c.events))
	for _, e := range c.events {
		ch <- contracts.InputEvent{Device: "/dev/input/event3", Event: e}
	}
	return ch, func() {}
}

func (c *captureInput) Devices() []contracts.InputDevice {
	return []contracts.InputDevice{{Info: ev.Info{Path: "/dev/input/event3", Name: "Test keyboard"}}}
}

// TestCaptureKey проверяет захват одной клавиши и сочетания.
func TestCaptureKey(t *testing.T) {
	t.Parallel()
	press := []ev.Event{
		{Type: ev.EvKey, Code: ev.KeyH, Value: ev.ValueUp}, // отпускание клавиши, зажатой до захвата
		{Type: ev.EvKey, Code: ev.KeyLeftctrl, Value: ev.ValueDown},
		{Type: ev.EvKey, Code: ev.KeyH, Value: ev.ValueDown},
		{Type: ev.EvKey, Code: ev.KeyH, Value: ev.ValueUp},
	}
	for _, c := range []struct {
		body, want string
	}{
		{`{"timeout_ms":1000}`, "LCtrl"},
		{`{"timeout_ms":1000,"combo":true}`, "^{Ctrl}{H}"},
	} {
		m, _ := newTestModule(t)
		m.svc.input = &captureInput{events: press}
		code, out := call(t, m.routes(true), "POST", "/api/v1/capture/key", c.body, nil)
		if code != 200 || out["name"] != c.want || out["device_name"] != "Test keyboard" {
			t.Errorf("%s: %d %v", c.body, code, out)
		}
	}

	// Нет нажатий — 408.
	m, _ := newTestModule(t)
	m.svc.input = &captureInput{}
	if code, _ := call(t, m.routes(true), "POST", "/api/v1/capture/key", `{"timeout_ms":20}`, nil); code != 408 {
		t.Fatalf("timeout: %d", code)
	}
}

// call2 выполняет GET и возвращает тело ответа как текст.
func call2(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Body.String()
}

// TestDescribeProblem проверяет понятное описание ошибок проекта и место для подсветки.
func TestDescribeProblem(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	tr := m.tr.WithLang("ru")
	p := project.Project{Events: []project.Event{{ID: "a", Name: "Автоклик"}}}

	// Незаполненное поле вложенного блока: место — внешний блок, путь — до вложенного.
	inner := &project.Problem{Part: project.PartAction, Index: 1, Kind: "shell", Err: project.Required("action", "shell", "code")}
	err := &project.Problem{Event: "a", Part: project.PartAction, Index: 0, Kind: "repeat", Err: inner}
	text, d := describeProblem(tr, p, err)
	if text != "Событие «Автоклик» → блок № 1 (Повторять) → блок № 2 (Команда bash): не заполнено поле «Код»" {
		t.Errorf("text = %q", text)
	}
	if d == nil || d.Event != "a" || d.Part != "action" || d.Index != 0 {
		t.Errorf("details = %+v", d)
	}

	// Нет триггера; незаполненное значение; ошибка без места.
	if text, _ := describeProblem(tr, p, &project.Problem{Event: "a", Part: project.PartTrigger, Index: -1, Err: project.ErrNoTrigger}); text != "Событие «Автоклик»: не выбрано, когда срабатывать" {
		t.Errorf("no trigger = %q", text)
	}
	if text, _ := describeProblem(tr, p, &project.Problem{Event: "a", Part: project.PartAction, Index: 2, Kind: "tap", Err: project.Required("action", "tap", "")}); text != "Событие «Автоклик» → блок № 3 (Нажать клавишу): не заполнено значение" {
		t.Errorf("value = %q", text)
	}
	p2 := project.Project{Events: []project.Event{{ID: "x"}, {ID: "event2"}}}
	if text, _ := describeProblem(tr, p2, &project.Problem{Event: "event2", Part: project.PartTrigger, Index: -1, Err: project.ErrNoTrigger}); text != "Событие 2: не выбрано, когда срабатывать" {
		t.Errorf("unnamed = %q", text)
	}
	if text, d := describeProblem(tr, p, errors.New("boom")); text != "boom" || d != nil {
		t.Errorf("plain = %q %+v", text, d)
	}
}
