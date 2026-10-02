package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
	"mkey/internal/lib/mkrec"
)

// fakeRecorder — запись и воспроизведение в памяти.
type fakeRecorder struct {
	recording bool
	played    string
	opts      contracts.PlayOptions
	stopped   int
	converted contracts.ConvertOptions
	// broken — запись «сломанная» с ошибкой в строке 8 (в списке и при воспроизведении).
	broken bool
	// settings — настройки записи.
	settings contracts.RecordSettings
}

func (f *fakeRecorder) StartRecording(o contracts.RecordOptions) (contracts.RecordingInfo, error) {
	if f.recording {
		return contracts.RecordingInfo{}, contracts.ErrAlreadyRecording
	}
	f.recording = true
	return contracts.RecordingInfo{Name: o.Name, StopHotkey: "^{Ctrl}^{Alt}{R}"}, nil
}
func (f *fakeRecorder) StopRecording(string) (contracts.RecordingInfo, error) {
	if !f.recording {
		return contracts.RecordingInfo{}, contracts.ErrNotRecording
	}
	f.recording = false
	return contracts.RecordingInfo{Name: "x", Events: 3}, nil
}
func (f *fakeRecorder) Recording() (contracts.RecordingInfo, bool) {
	return contracts.RecordingInfo{Name: "x"}, f.recording
}
func (f *fakeRecorder) WaitRecording(context.Context) (contracts.RecordingInfo, error) {
	return contracts.RecordingInfo{}, contracts.ErrNotRecording
}
func (f *fakeRecorder) Recordings() ([]contracts.RecordingInfo, error) {
	if f.broken {
		return []contracts.RecordingInfo{{Name: "сломанная", Problem: &contracts.RecordingProblem{Line: 8, Text: "0.100 0 ~{Hh}", Code: mkrec.ProblemKey, Arg: "Hh"}}}, nil
	}
	return []contracts.RecordingInfo{{Name: "игра"}}, nil
}
func (f *fakeRecorder) DeleteRecording(name string) error {
	if name != "игра" {
		return contracts.ErrRecordingNotFound
	}
	return nil
}
func (f *fakeRecorder) Play(_ context.Context, name string, o contracts.PlayOptions) error {
	if f.broken {
		return &mkrec.Problem{Line: 8, Text: "0.100 0 ~{Hh}", Code: mkrec.ProblemKey, Arg: "Hh"}
	}
	if name != "игра" {
		return contracts.ErrRecordingNotFound
	}
	f.played, f.opts = name, o
	return nil
}
func (f *fakeRecorder) StopPlayback() int { f.stopped++; return 1 }
func (f *fakeRecorder) ConvertRecording(name string, o contracts.ConvertOptions) (string, error) {
	switch {
	case name == "пусто":
		return "", contracts.ErrRecordingEmpty
	case name != "игра":
		return "", contracts.ErrRecordingNotFound
	}
	f.converted = o
	return "rec-игра", nil
}
func (f *fakeRecorder) RecordHotkey() string                     { return "^{Ctrl}^{Alt}{R}" }
func (f *fakeRecorder) SetRecordHotkey(string) error             { return nil }
func (f *fakeRecorder) Playing() int                             { return 0 }
func (f *fakeRecorder) RecordSettings() contracts.RecordSettings { return f.settings }
func (f *fakeRecorder) SetRecordSettings(s contracts.RecordSettings) error {
	if len(s.Kinds) == 0 {
		return contracts.ErrBadRecordSettings
	}
	f.settings = s
	return nil
}

// TestRecordSettingsAPI проверяет чтение и сохранение настроек записи в config.yaml.
func TestRecordSettingsAPI(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	rec := &fakeRecorder{settings: contracts.RecordSettings{Kinds: []string{"keyboard"}, Moves: true}}
	m.svc.recorder, m.svc.player = rec, rec
	h := m.routes(true)

	// Чтение.
	if _, out := call(t, h, "GET", "/api/v1/settings/recording", "", nil); out["moves"] != true {
		t.Fatalf("get: %v", out)
	}

	// Неверные — 400 с кодом; верные применяются и сохраняются в config.yaml.
	if code, out := call(t, h, "PUT", "/api/v1/settings/recording", `{"kinds":[]}`, nil); code != 400 ||
		out["error"].(map[string]any)["code"] != "api.record_settings_bad" {
		t.Fatalf("bad: %d %v", code, out)
	}
	body := `{"kinds":["keyboard","mouse"],"moves":false,"merge_moves_ms":0,"center_pointer":false,"coalesce_ms":4}`
	if code, out := call(t, h, "PUT", "/api/v1/settings/recording", body, nil); code != 200 || rec.settings.Moves {
		t.Fatalf("put: %d %v", code, out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	for _, want := range []string{"kinds: [keyboard, mouse]", "moves: false", "merge_moves_ms: 0", "center_pointer: false"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no %q in config:\n%s", want, data)
		}
	}
}

// fakeTiming — интервалы нажатий для тестов API: проверка пределов как у движка.
type fakeTiming struct{ tm contracts.Timing }

// Timing возвращает текущие интервалы.
func (f *fakeTiming) Timing() contracts.Timing { return f.tm }

// SetTiming отклоняет отрицательные значения.
func (f *fakeTiming) SetTiming(tm contracts.Timing) error {
	if tm.KeyHoldMS < 0 || tm.KeyDelayMS < 0 || tm.LayoutSwitchMS < 0 {
		return contracts.ErrBadTiming
	}
	f.tm = tm
	return nil
}

// TestTimingSettings проверяет интервалы нажатий через API: чтение, отказ, сохранение в config.yaml.
func TestTimingSettings(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	tm := &fakeTiming{tm: contracts.Timing{KeyHoldMS: 20, KeyDelayMS: 10, LayoutSwitchMS: 60}}
	m.svc.timing = tm
	h := m.routes(true)

	// Чтение.
	if _, out := call(t, h, "GET", "/api/v1/settings/timing", "", nil); out["key_hold_ms"] != 20.0 {
		t.Fatalf("get: %v", out)
	}

	// Неверные — 400 с кодом; верные применяются и сохраняются в modules.engine.
	if code, out := call(t, h, "PUT", "/api/v1/settings/timing", `{"key_hold_ms":-1}`, nil); code != 400 ||
		out["error"].(map[string]any)["code"] != "api.timing_bad" {
		t.Fatalf("bad: %d %v", code, out)
	}
	body := `{"key_hold_ms":35,"key_delay_ms":5,"layout_switch_ms":100}`
	if code, out := call(t, h, "PUT", "/api/v1/settings/timing", body, nil); code != 200 || tm.tm.KeyHoldMS != 35 {
		t.Fatalf("put: %d %v", code, out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	for _, want := range []string{"engine:", "key_hold_ms: 35", "key_delay_ms: 5", "layout_switch_ms: 100"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no %q in config:\n%s", want, data)
		}
	}
}

// TestRecordingEndpoints проверяет запись, список, удаление и воспроизведение через API.
func TestRecordingEndpoints(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	rec := &fakeRecorder{}
	m.svc.recorder, m.svc.player = rec, rec
	h := m.routes(true)

	// Начало записи, повторное начало — 409, состояние, конец.
	if code, out := call(t, h, "POST", "/api/v1/recordings/start", `{"name":"игра"}`, nil); code != 200 || out["stop_hotkey"] != "^{Ctrl}^{Alt}{R}" {
		t.Fatalf("start: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/v1/recordings/start", `{}`, nil); code != 409 {
		t.Fatalf("second start: %d", code)
	}
	if _, out := call(t, h, "GET", "/api/v1/recordings", "", nil); out["current"] == nil || len(out["recordings"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	if code, out := call(t, h, "POST", "/api/v1/recordings/stop", `{"cut":"^{Ctrl}{C}"}`, nil); code != 200 || out["events"] != 3.0 {
		t.Fatalf("stop: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/v1/recordings/stop", `{}`, nil); code != 409 {
		t.Fatalf("stop again: %d", code)
	}

	// Воспроизведение с параметрами; нет записи — 404; остановка через /stop.
	if code, _ := call(t, h, "POST", "/api/v1/play", `{"name":"игра","speed":2,"repeat":3,"skip_moves":true}`, nil); code != 200 || rec.opts.Speed != 2 || rec.opts.Repeat != 3 || !rec.opts.SkipMoves {
		t.Fatalf("play: %d %+v", code, rec.opts)
	}
	if code, _ := call(t, h, "POST", "/api/v1/play", `{"name":"нет"}`, nil); code != 404 {
		t.Fatalf("play missing: %d", code)
	}
	if code, _ := call(t, h, "POST", "/api/v1/stop", ``, nil); code != 200 || rec.stopped != 1 {
		t.Fatalf("stop playback: %d %d", code, rec.stopped)
	}

	// Превращение в блоки: проект создан; пустая запись и нет записи — понятные ошибки.
	if code, out := call(t, h, "POST", "/api/v1/recordings/%D0%B8%D0%B3%D1%80%D0%B0/convert", `{"simplify":true}`, nil); code != 200 || out["project"] != "rec-игра" || !rec.converted.Simplify {
		t.Fatalf("convert: %d %v", code, out)
	}
	if code, out := call(t, h, "POST", "/api/v1/recordings/%D0%BF%D1%83%D1%81%D1%82%D0%BE/convert", `{}`, nil); code != 400 || out["error"].(map[string]any)["code"] != "api.recording_empty" {
		t.Fatalf("convert empty: %d %v", code, out)
	}
	if code, _ := call(t, h, "POST", "/api/v1/recordings/x/convert", `{}`, nil); code != 404 {
		t.Fatalf("convert missing: %d", code)
	}

	// Удаление.
	if code, _ := call(t, h, "DELETE", "/api/v1/recordings/%D0%B8%D0%B3%D1%80%D0%B0", "", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := call(t, h, "DELETE", "/api/v1/recordings/x", "", nil); code != 404 {
		t.Fatalf("delete missing: %d", code)
	}
}

// hotkeyInput — источник ввода с сочетанием экстренной остановки.
type hotkeyInput struct {
	contracts.InputSource
	combo string
}

func (h *hotkeyInput) EmergencyCombo() string { return h.combo }
func (h *hotkeyInput) SetEmergencyCombo(c string) error {
	h.combo = c
	return nil
}

// TestSystemHotkeys проверяет чтение, проверку, применение и сохранение системных сочетаний.
func TestSystemHotkeys(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	in := &hotkeyInput{combo: "^{Esc}^{Backspace}{Enter}"}
	m.svc.input, m.svc.recorder = in, &fakeRecorder{}
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	h := m.routes(true)

	// Чтение.
	if _, out := call(t, h, "GET", "/api/v1/settings/hotkeys", "", nil); out["emergency"] != "^{Esc}^{Backspace}{Enter}" || out["record"] != "^{Ctrl}^{Alt}{R}" {
		t.Fatalf("get: %v", out)
	}

	// Ошибки: одна обычная клавиша, запись с плюсом, совпадение с записью.
	for body, code := range map[string]string{
		`{"emergency":"{A}"}`:              "api.hotkey_too_simple",
		`{"record":"{R}"}`:                 "api.hotkey_too_simple",
		`{"emergency":"{Ctrl+Alt+Del}"}`:   "dsl.chord_in_braces",
		`{"emergency":"^{ctrl}^{alt}{r}"}`: "api.hotkey_conflict",
	} {
		code2, out := call(t, h, "PUT", "/api/v1/settings/hotkeys", body, map[string]string{"Accept-Language": "ru"})
		if code2 != 400 || out["error"].(map[string]any)["code"] != code {
			t.Errorf("%s: %d %v", body, code2, out)
		}
	}

	// Новое сочетание применяется и сохраняется; F-клавиша одна — для записи можно.
	if code, out := call(t, h, "PUT", "/api/v1/settings/hotkeys", `{"emergency":"^{LCtrl}^{LAlt}{Pause}","record":"{F9}"}`, nil); code != 200 || in.combo != "^{LCtrl}^{LAlt}{Pause}" {
		t.Fatalf("put: %d %v", code, out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	if !strings.Contains(string(data), `emergency_stop: "^{LCtrl}^{LAlt}{Pause}"`) || !strings.Contains(string(data), `hotkey: "{F9}"`) {
		t.Fatalf("config = %s", data)
	}
}

// TestRecordingProblem проверяет понятное сообщение об ошибке в файле записи: в списке
// (problem.message) и при воспроизведении (api.recording_bad с номером строки).
func TestRecordingProblem(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	rec := &fakeRecorder{broken: true}
	m.svc.recorder, m.svc.player = rec, rec
	h := m.routes(true)
	ru := map[string]string{"Accept-Language": "ru"}
	want := `строка 8 («0.100 0 ~{Hh}») — неизвестная клавиша «Hh»`

	// Список: ошибка описана на языке клиента.
	_, out := call(t, h, "GET", "/api/v1/recordings", "", ru)
	list, _ := out["recordings"].([]any)
	if len(list) != 1 {
		t.Fatalf("list: %v", out)
	}
	p, _ := list[0].(map[string]any)["problem"].(map[string]any)
	if msg, _ := p["message"].(string); !strings.HasPrefix(msg, want) || p["line"] != float64(8) || p["code"] != "key" {
		t.Errorf("problem: %v", p)
	}

	// Воспроизведение: 400 api.recording_bad, в сообщении — строка и причина.
	code, out := call(t, h, "POST", "/api/v1/play", `{"name":"сломанная"}`, ru)
	e, _ := out["error"].(map[string]any)
	if msg, _ := e["message"].(string); code != 400 || e["code"] != "api.recording_bad" || !strings.Contains(msg, want) {
		t.Errorf("play: %d %v", code, out)
	}

	// Длинная строка обрезается.
	long := contracts.RecordingProblem{Line: 3, Text: strings.Repeat("x", 100), Code: mkrec.ProblemShort}
	if msg := problemMessage(m.tr.WithLang("ru"), long); !strings.Contains(msg, strings.Repeat("x", maxProblemText)+"…") || strings.Contains(msg, strings.Repeat("x", maxProblemText+1)) {
		t.Errorf("long: %s", msg)
	}
}

// TestInterfaceSettings проверяет язык и тему окна: сохранение в config.yaml и чтение обратно.
func TestInterfaceSettings(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(t)
	m.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	h := m.routes(true)

	// Неверная тема — 400; верные значения сохраняются.
	if code, _ := call(t, h, "PUT", "/api/v1/settings/interface", `{"language":"ru","theme":"pink"}`, nil); code != 400 {
		t.Fatalf("bad theme: %d", code)
	}
	if code, out := call(t, h, "PUT", "/api/v1/settings/interface", `{"language":"en","theme":"dark"}`, nil); code != 200 {
		t.Fatalf("put: %d %v", code, out)
	}
	if _, out := call(t, h, "GET", "/api/v1/settings/interface", "", nil); out["language"] != "en" || out["theme"] != "dark" {
		t.Fatalf("get: %v", out)
	}
	data, _ := os.ReadFile(m.cfg.ConfigFile)
	if !strings.Contains(string(data), `language: "en"`) || !strings.Contains(string(data), `theme: "dark"`) {
		t.Fatalf("config:\n%s", data)
	}
}
