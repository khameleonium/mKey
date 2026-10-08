package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/project"
	"github.com/khameleonium/mKey/internal/registry"
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

// SetEnabled включает или выключает проект (в тестах — без изменения файла).
func (p *memProjects) SetEnabled(id string, _ bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.files[id]; !ok {
		return errors.New("not found")
	}
	return nil
}

func (p *memProjects) Templates() []contracts.Template {
	return []contracts.Template{
		{ID: "autoclicker", Content: "version: 1\nname: t\nevents: []\n"},
		{ID: "scripted", Content: "version: 1\nname: s\nevents: [ { id: e, trigger: { type: manual }, actions: [ { lua: \"os.exit()\" } ] } ]\n"},
	}
}

// Import сохраняет импортированный проект под ID из имени файла.
func (p *memProjects) Import(name string, data []byte) (string, error) {
	id := strings.TrimSuffix(name, ".mkey.yaml")
	return id, p.SaveRaw(id, data)
}

// TestScriptsWarning проверяет, что импорт и создание из шаблона возвращают скрипты (SEC-7).
func TestScriptsWarning(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.svc.projects = &memProjects{files: map[string]string{}}
	h := m.routes(true)
	body := `{"name":"x.mkey.yaml","content":"version: 1\nevents: [ { id: e, trigger: { type: manual }, actions: [ { shell: \"rm -rf ~\" } ] } ]\n"}`
	if code, out := call(t, h, "POST", "/api/v1/projects/import", body, nil); code != 200 || len(out["scripts"].([]any)) != 1 {
		t.Fatalf("import: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/projects", `{"id":"y","template":"scripted"}`, nil); code != 200 || len(out["scripts"].([]any)) != 1 {
		t.Fatalf("template: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/projects", `{"id":"z","template":"autoclicker"}`, nil); code != 200 || len(out["scripts"].([]any)) != 0 {
		t.Fatalf("plain template: %d %v", code, out)
	}
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

// TestDescribeEvent проверяет записи монитора нажатий: клавиши, автоповтор, оси, колесо, мышь,
// касания и выбор групп (?show).
func TestDescribeEvent(t *testing.T) {
	t.Parallel()
	now := time.Now()
	axes := map[string]time.Time{}
	all := watchShow{watchWheel: true, watchAxes: true, watchMoves: true, watchTouch: true}
	kbd, pad := watchDevice{name: "Kbd"}, watchDevice{name: "Touchpad", touch: true}
	ie := func(typ, code uint16, v int32, at time.Duration) contracts.InputEvent {
		return contracts.InputEvent{Device: "/d", Event: ev.Event{Time: now.Add(at), Type: typ, Code: code, Value: v}}
	}

	// Клавиша: нажатие и отпускание с именем для макросов — даже если не выбрано ничего; автоповтор
	// и SYN не показываются.
	if e, ok := describeEvent(ie(ev.EvKey, ev.KeyA, 1, 0), kbd, watchShow{}, axes); !ok || e.Name != "A" || e.Action != "down" || e.Kernel != "KEY_A" || e.DeviceName != "Kbd" || e.Group != "keys" {
		t.Fatalf("key down = %+v", e)
	}
	if e, ok := describeEvent(ie(ev.EvKey, 0x2ff, 0, 0), kbd, all, axes); !ok || e.Name != "#767" || e.Action != "up" {
		t.Fatalf("unknown key = %+v", e)
	}
	for _, x := range []contracts.InputEvent{ie(ev.EvKey, ev.KeyA, 2, 0), ie(ev.EvSyn, 0, 0, 0)} {
		if _, ok := describeEvent(x, kbd, all, axes); ok {
			t.Fatalf("shown: %+v", x)
		}
	}

	// Ось геймпада: не чаще раза в 100 мс; без группы axes — не показывается.
	if e, ok := describeEvent(ie(ev.EvAbs, ev.AbsX, 100, 0), kbd, all, axes); !ok || e.Kind != "axis" || e.Group != watchAxes {
		t.Fatalf("first axis value = %+v", e)
	}
	if _, ok := describeEvent(ie(ev.EvAbs, ev.AbsX, 120, 50*time.Millisecond), kbd, all, axes); ok {
		t.Fatal("axis not throttled")
	}
	if e, ok := describeEvent(ie(ev.EvAbs, ev.AbsX, 130, 150*time.Millisecond), kbd, all, axes); !ok || e.Value != 130 {
		t.Fatalf("axis after interval = %+v", e)
	}
	if _, ok := describeEvent(ie(ev.EvAbs, ev.AbsY, 1, 0), kbd, watchShow{watchTouch: true}, axes); ok {
		t.Fatal("axis shown without axes")
	}

	// Колесо и сдвиг мыши — каждое в своей группе.
	if e, ok := describeEvent(ie(ev.EvRel, ev.RelWheel, -1, 0), kbd, all, axes); !ok || e.Kind != "wheel" || e.Value != -1 || e.Group != watchWheel {
		t.Fatalf("wheel = %+v", e)
	}
	if e, ok := describeEvent(ie(ev.EvRel, ev.RelY, 4, 0), kbd, all, axes); !ok || e.Kind != "move" || e.DY != 4 || e.Group != watchMoves {
		t.Fatalf("move = %+v", e)
	}
	if _, ok := describeEvent(ie(ev.EvRel, ev.RelX, 3, 0), kbd, watchShow{watchWheel: true}, axes); ok {
		t.Fatal("move shown without moves")
	}

	// Тачпад: положение пальца и «касание» — группа touch, служебные оси скрыты; без touch
	// не видно ни того, ни другого, а настоящие кнопки тачпада видны.
	if e, ok := describeEvent(ie(ev.EvAbs, ev.AbsMtPositionX, 512, 0), pad, all, axes); !ok || e.Kind != "touch" || e.Name != "X" || e.Group != watchTouch {
		t.Fatalf("touch X = %+v", e)
	}
	if _, ok := describeEvent(ie(ev.EvAbs, 0x39, 7, 0), pad, all, axes); ok { // ABS_MT_TRACKING_ID
		t.Fatal("tracking id shown")
	}
	if e, ok := describeEvent(ie(ev.EvKey, ev.BtnTouch, 1, 0), pad, all, axes); !ok || e.Group != watchTouch {
		t.Fatalf("BTN_TOUCH = %+v", e)
	}
	noTouch := watchShow{watchAxes: true, watchMoves: true}
	for _, x := range []contracts.InputEvent{ie(ev.EvKey, ev.BtnToolFinger, 1, 0), ie(ev.EvAbs, ev.AbsY, 300, time.Second)} {
		if _, ok := describeEvent(x, pad, noTouch, axes); ok {
			t.Fatalf("touch shown without touch: %+v", x)
		}
	}
	if _, ok := describeEvent(ie(ev.EvKey, ev.BtnLeft, 1, 0), pad, noTouch, axes); !ok {
		t.Fatal("touchpad button hidden")
	}
}

// TestParseWatchShow проверяет выбор групп монитора: ?show и прежний ?moves=1.
func TestParseWatchShow(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]string{
		"":                        "axes,touch,wheel",
		"?moves=1":                "axes,moves,touch,wheel",
		"?show=":                  "",
		"?show=moves,touch,bogus": "bogus,moves,touch",
	} {
		show := parseWatchShow(httptest.NewRequest(http.MethodGet, "/api/v1/input/watch"+q, nil))
		var got []string
		for g, on := range show {
			if on && g != "" {
				got = append(got, g)
			}
		}
		slices.Sort(got)
		if strings.Join(got, ",") != want {
			t.Errorf("%q = %v, want %s", q, got, want)
		}
	}
}

// TestWatchDevices проверяет фильтр монитора по устройству: через инспектор (часть названия даёт
// все подходящие), без него — по имени файла и названию; неизвестное устройство — 404 до потока.
func TestWatchDevices(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.svc.input = &captureInput{}

	// Без инспектора: имя файла и часть названия без учёта регистра.
	for _, ref := range []string{"event3", "/dev/input/event3", "test KEY"} {
		if got := m.watchDevices(ref); !got["/dev/input/event3"] || len(got) != 1 {
			t.Errorf("watchDevices(%q) = %v", ref, got)
		}
	}
	if got := m.watchDevices("мышь"); len(got) != 0 {
		t.Errorf("unknown device found: %v", got)
	}

	// С инспектором: все устройства, подходящие по названию.
	dev := func(path, name string) contracts.DeviceDetails {
		return contracts.DeviceDetails{InputDevice: contracts.InputDevice{Info: ev.Info{Path: path, Name: name}}}
	}
	m.svc.inspect = fakeInspector{devs: []contracts.DeviceDetails{
		dev("/dev/input/event5", "Logitech Keyboard"), dev("/dev/input/event6", "Logitech Mouse"), dev("/dev/input/event7", "Pad"),
	}}
	if got := m.watchDevices("logitech"); len(got) != 2 || !got["/dev/input/event5"] || !got["/dev/input/event6"] {
		t.Errorf("inspector filter = %v", got)
	}

	// Неизвестное устройство — ошибка сразу, без потока.
	h := m.routes(true)
	if code, out := call(t, h, "GET", "/api/v1/input/watch?device=nothing", "", nil); code != 404 ||
		out["error"].(map[string]any)["code"] != "api.device_not_found" {
		t.Fatalf("unknown: %d %v", code, out)
	}
}
