package mkrec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// TestWriteRead проверяет запись в текст и чтение без потерь.
func TestWriteRead(t *testing.T) {
	t.Parallel()
	ms := time.Millisecond
	h := Header{
		Created:  time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Devices:  []Device{{ID: 0, Kinds: []string{"keyboard"}, Name: `AT "kbd" #1`}, {ID: 1, Kinds: []string{"mouse"}, Name: "Mouse"}},
		Centered: true,
	}
	frames := []Frame{
		{T: 0, Device: 0, Events: []ev.Event{{Type: ev.EvKey, Code: ev.KeyA, Value: 1}}},
		{T: 120 * ms, Device: 0, Events: []ev.Event{{Type: ev.EvKey, Code: ev.KeyA, Value: 0}}},
		{T: 350 * ms, Device: 1, Events: []ev.Event{{Type: ev.EvRel, Code: ev.RelX, Value: 12}, {Type: ev.EvRel, Code: ev.RelY, Value: -3}}},
		{T: 500 * ms, Device: 1, Events: []ev.Event{{Type: ev.EvKey, Code: ev.BtnLeft, Value: 1}}},
		{T: 1200 * ms, Device: 1, Events: []ev.Event{{Type: ev.EvRel, Code: ev.RelWheel, Value: -1}, {Type: ev.EvRel, Code: ev.RelWheelHiRes, Value: -120}}},
		{T: 1300 * ms, Device: 2, Events: []ev.Event{{Type: ev.EvAbs, Code: ev.AbsX, Value: 1234}, {Type: ev.EvKey, Code: 0x2ff, Value: 1}}},
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if err := w.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(5 * time.Second); err != nil {
		t.Fatal(err)
	}

	// Текст читается человеком.
	text := buf.String()
	for _, want := range []string{"0.000 0 ^{A}\n", "0.350 1 move +12 -3\n", "0.500 1 ^{Mouse0}\n", "1.200 1 wheel -1 wheel-hr -120\n", "ev 3 0 1234 ^{#767}", "5.000 end\n", "pointer center\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}

	// Чтение даёт то же самое.
	rec, err := Read(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Duration != 5*time.Second || len(rec.Frames) != len(frames) || rec.Header.Devices[0].Name != `AT "kbd" #1` ||
		!rec.Header.Centered || !rec.Header.Created.Equal(h.Created) {
		t.Fatalf("rec = %+v", rec.Header)
	}
	for i, f := range frames {
		g := rec.Frames[i]
		if g.T != f.T || g.Device != f.Device || len(g.Events) != len(f.Events) {
			t.Fatalf("frame %d = %+v, want %+v", i, g, f)
		}
		for j := range f.Events {
			if g.Events[j] != f.Events[j] {
				t.Fatalf("frame %d event %d = %v, want %v", i, j, g.Events[j], f.Events[j])
			}
		}
	}
}

// TestEditedFile проверяет чтение файла, поправленного вручную: удалённые строки, комментарии,
// перепутанный порядок, имена клавиш в любом регистре; и ошибки с номером строки.
func TestEditedFile(t *testing.T) {
	t.Parallel()
	rec, err := Read(strings.NewReader("# мой файл\nmkrec 1\n\n1.5 0 ~{ctrl}\n0.2 0 ^{Ctrl} ^{c}\n# 0.3 0 ~{C}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Frames) != 2 || rec.Frames[0].T != 200*time.Millisecond || len(rec.Frames[0].Events) != 2 || rec.Duration != 1500*time.Millisecond {
		t.Fatalf("rec = %+v", rec)
	}
	// Ошибки: вид, строка (с 1) и неверное слово — по ним пишется понятное сообщение.
	for _, c := range []struct {
		src, code, arg string
		line           int
	}{
		{"hello\n", ProblemHeader, "hello", 1},
		{"mkrec 9\n", ProblemVersion, "9", 1},
		{"# комментарий\n\nmkrec 1\n0.1 0 ^{Mous0}\n", ProblemKey, "Mous0", 4},
		{"mkrec 1\n0.1 0 move 5\n", ProblemNumbers, "move", 2},
		{"mkrec 1\n0.1 x ^{A}\n", ProblemDeviceNumber, "x", 2},
		{"mkrec 1\n0.1 0 jump 1\n", ProblemAction, "jump", 2},
		{"mkrec 1\n0.1 0\n", ProblemShort, "", 2},
		{"mkrec 1\nstart 1\n", ProblemLine, "start", 2},
		{"mkrec 1\npointer 1 2\n", ProblemPointer, "", 2},
		{"mkrec 1\ndevice 0 mouse\n", ProblemDevice, "", 2},
		{"mkrec 1\ncreated вчера\n", ProblemCreated, "", 2},
		{"mkrec 1\n0.1 0 ^{#x}\n", ProblemKey, "#x", 2},
		{"{\"format\":\"mkrec\",\"version\":1}\n[1,0,2,0,1]\n", ProblemOldFormat, "", 0},
		{"", ProblemEmpty, "", 0},
	} {
		_, err := Read(strings.NewReader(c.src))
		var p *Problem
		if !errors.Is(err, ErrFormat) || !errors.As(err, &p) || p.Code != c.code || p.Arg != c.arg || p.Line != c.line {
			t.Errorf("%q: err = %#v", c.src, err)
		}
	}
}

// TestKeyNamesRoundTrip проверяет, что имя каждой клавиши, которое пишет запись, читается обратно в тот же код.
func TestKeyNamesRoundTrip(t *testing.T) {
	t.Parallel()
	for code := uint16(1); code < 0x300; code++ {
		name := keyName(code)
		got, err := keyCode(name + "}")
		if err != nil || got != code {
			if _, ok := keys.NameOf(code); ok {
				t.Errorf("code %#x: name %q → %#x, %v", code, name, got, err)
			}
		}
	}
}

// TestCoalesceMoves проверяет склейку перемещений мыши.
func TestCoalesceMoves(t *testing.T) {
	t.Parallel()
	ms := time.Millisecond
	mv := func(tm time.Duration, dx int32) Frame {
		return Frame{T: tm, Device: 1, Events: []ev.Event{{Type: ev.EvRel, Code: ev.RelX, Value: dx}}}
	}
	key := Frame{T: 1 * ms, Device: 0, Events: []ev.Event{{Type: ev.EvKey, Code: ev.KeyA, Value: 1}}}
	in := []Frame{mv(0, 2), key, mv(2*ms, 3), mv(9*ms, 5)}
	out := CoalesceMoves(in, 4*ms)
	if len(out) != 3 || out[0].Events[0].Value != 5 || out[2].Events[0].Value != 5 || in[0].Events[0].Value != 2 {
		t.Fatalf("out = %+v", out)
	}
	if Keep(ev.Event{Type: ev.EvKey, Value: ev.ValueRepeat}) || Keep(ev.Event{Type: ev.EvMsc}) || !Keep(ev.Event{Type: ev.EvKey, Value: 1}) {
		t.Fatal("Keep")
	}
}
