package recorder

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// TestDefaultName проверяет имя записи по умолчанию: mKeyRec_ДДММГГГГ_ЧЧММСС.
func TestDefaultName(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 5, 7, 0, time.Local)
	if got := defaultName(at); got != "mKeyRec_01102026_090507" {
		t.Fatalf("defaultName = %q", got)
	}
	if !validName(defaultName(at)) {
		t.Fatal("default name is not a valid recording name")
	}
}

// TestRecordSettings проверяет проверку и применение настроек записи: без движений мыши
// записываются только нажатия.
func TestRecordSettings(t *testing.T) {
	t.Parallel()
	m, clk, _ := newTestModule(t)

	// Значения по умолчанию.
	def := m.RecordSettings()
	if !def.Moves || !def.CenterPointer || def.MergeMovesMS != 0 || def.CoalesceMS != 0 ||
		strings.Join(def.Kinds, ",") != "keyboard,mouse" {
		t.Fatalf("defaults = %+v", def)
	}

	// Неверные настройки отклоняются и не меняют текущие.
	for _, bad := range []contracts.RecordSettings{
		{Kinds: nil},
		{Kinds: []string{"кофеварка"}},
		{Kinds: []string{"mouse"}, MergeMovesMS: -1},
		{Kinds: []string{"mouse"}, CoalesceMS: 5000},
	} {
		if err := m.SetRecordSettings(bad); !errors.Is(err, contracts.ErrBadRecordSettings) {
			t.Errorf("SetRecordSettings(%+v) = %v", bad, err)
		}
	}
	if got := m.RecordSettings(); strings.Join(got.Kinds, ",") != "keyboard,mouse" || got.MergeMovesMS != 0 {
		t.Fatalf("settings changed by a bad call: %+v", got)
	}

	// Без движений мыши: перемещение не записывается, нажатие кнопки мыши — записывается.
	s := def
	s.Moves = false
	if err := m.SetRecordSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartRecording(contracts.RecordOptions{Name: "клики"}); err != nil {
		t.Fatal(err)
	}
	f := feeder{m: m, clk: clk, base: clk.Now()}
	ms := time.Millisecond
	f.at(10*ms, "/mouse", ev.EvRel, ev.RelX, 5)
	f.at(10*ms, "/mouse", ev.EvSyn, ev.SynReport, 0)
	f.key(20*ms, "/mouse", ev.BtnLeft, 1)
	f.key(30*ms, "/mouse", ev.BtnLeft, 0)
	info, err := m.StopRecording("")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(info.Path)
	if text := string(data); strings.Contains(text, " 0 move") || !strings.Contains(text, "^{Mouse0}") {
		t.Fatalf("recording without moves:\n%s", text)
	}
}
