package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mkey/internal/contracts"
)

// fakeRecorder — запись и воспроизведение в памяти.
type fakeRecorder struct {
	recording bool
	played    string
	opts      contracts.PlayOptions
	stopped   int
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
	return []contracts.RecordingInfo{{Name: "игра"}}, nil
}
func (f *fakeRecorder) DeleteRecording(name string) error {
	if name != "игра" {
		return contracts.ErrRecordingNotFound
	}
	return nil
}
func (f *fakeRecorder) Play(_ context.Context, name string, o contracts.PlayOptions) error {
	if name != "игра" {
		return contracts.ErrRecordingNotFound
	}
	f.played, f.opts = name, o
	return nil
}
func (f *fakeRecorder) StopPlayback() int            { f.stopped++; return 1 }
func (f *fakeRecorder) RecordHotkey() string         { return "^{Ctrl}^{Alt}{R}" }
func (f *fakeRecorder) SetRecordHotkey(string) error { return nil }
func (f *fakeRecorder) Playing() int                 { return 0 }

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
