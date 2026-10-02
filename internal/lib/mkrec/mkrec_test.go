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
		{"mkrec 1\n0.1 0 ev -1 0 1\n", ProblemNumbers, "ev", 2},
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

// TestEditingAids проверяет удобства ручной правки: комментарии после действий, строки shift
// (складываются, сдвигают и конец записи) и их ошибки.
func TestEditingAids(t *testing.T) {
	t.Parallel()
	ms := time.Millisecond
	src := `mkrec 1
device 0 keyboard "Kbd #1"   # имя с # в кавычках — не комментарий
0.100 0 ^{A}   # нажали A
0.200 0 ~{A}	# отпустили
shift -5
5.300 0 ^{#30}        # клавиша по коду — не комментарий
shift +0.5
5.900 0 ~{#30}
8.000 end   # конец
`
	rec, err := Read(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Header.Devices[0].Name != "Kbd #1" || len(rec.Frames) != 4 {
		t.Fatalf("rec = %+v", rec)
	}

	// Времена: до сдвига — как написано; после shift -5 — на 5 с раньше; после shift +0.5 — на 4,5 с раньше.
	for i, want := range []time.Duration{100 * ms, 200 * ms, 300 * ms, 1400 * ms} {
		if rec.Frames[i].T != want {
			t.Errorf("frame %d: T = %v, want %v", i, rec.Frames[i].T, want)
		}
	}
	if rec.Duration != 3500*ms || rec.Frames[2].Events[0].Code != ev.KeyA {
		t.Errorf("duration = %v, code = %d", rec.Duration, rec.Frames[2].Events[0].Code)
	}

	// Ошибки сдвига: не число, лишние слова, время меньше нуля.
	for _, c := range []struct{ src, code, arg string }{
		{"mkrec 1\nshift назад\n", ProblemShift, "назад"},
		{"mkrec 1\nshift 1 2\n", ProblemShift, "1 2"},
		{"mkrec 1\nshift\n", ProblemShift, ""},
		{"mkrec 1\nshift -1\n0.500 0 ^{A}\n", ProblemNegative, "-0.500"},
		{"mkrec 1\n0.1 0 ^{A}#без пробела\n", ProblemKey, "A}#без"},
	} {
		_, err := Read(strings.NewReader(c.src))
		var p *Problem
		if !errors.As(err, &p) || p.Code != c.code || p.Arg != c.arg {
			t.Errorf("%q: err = %#v", c.src, err)
		}
	}
}

// TestPauseMarks проверяет пометки о паузах при записи: от PauseMark — комментарий перед действием,
// короче — без пометки; файл с пометками читается как прежде.
func TestPauseMarks(t *testing.T) {
	t.Parallel()
	ms := time.Millisecond
	key := []ev.Event{{Type: ev.EvKey, Code: ev.KeyA, Value: 1}}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, at := range []time.Duration{1500 * ms, 2499 * ms, 3499 * ms, 6700 * ms} {
		if err := w.WriteFrame(Frame{T: at, Events: key}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "1.500 0 ^{A}\n2.499 0 ^{A}\n# ---- пауза 1,0 с ----\n3.499 0 ^{A}\n# ---- пауза 3,2 с ----\n6.700 0 ^{A}\n"
	if got := buf.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if rec, err := Read(strings.NewReader("mkrec 1\n" + buf.String())); err != nil || len(rec.Frames) != 4 {
		t.Fatalf("read: %v %v", rec, err)
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

// FuzzRead проверяет, что любой текст файла записи разбирается без паники: либо запись, либо
// понятная ошибка (файлы правят вручную).
func FuzzRead(f *testing.F) {
	for _, s := range []string{
		"mkrec 1\ndevice 0 keyboard \"K\"\n0.000 0 ^{A}\n0.100 0 ~{A}\n0.200 end\n",
		"mkrec 1\npointer center\n0.1 1 move +5 -3 wheel 1\nshift -0.05\n0.2 1 ev 3 0 100\n",
		"# x\nmkrec 1\n0.1 0 ^{#767} # comment\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_, _ = Read(strings.NewReader(s))
	})
}

// TestCaps проверяет строку caps: запись и чтение обратно, ошибки.
func TestCaps(t *testing.T) {
	t.Parallel()
	caps := &Caps{
		ID:    ev.ID{Bustype: 3, Vendor: 0x045e, Product: 0x028e, Version: 0x110},
		Props: []uint16{ev.InputPropDirect},
		Keys:  []uint16{ev.BtnSouth, ev.BtnEast},
		Abs:   map[uint16]ev.AbsInfo{ev.AbsX: {Minimum: -32768, Maximum: 32767, Fuzz: 16, Flat: 128}},
	}
	var buf strings.Builder
	w := NewWriter(&buf)
	if err := w.WriteHeader(Header{Devices: []Device{{ID: 0, Kinds: []string{"gamepad"}, Name: "Pad", Caps: caps}}}); err != nil {
		t.Fatal(err)
	}
	_ = w.WriteFrame(Frame{T: 0, Device: 0, Events: []ev.Event{{Type: ev.EvKey, Code: ev.BtnSouth, Value: 1}}})
	_ = w.Close(time.Second)
	if !strings.Contains(buf.String(), "caps 0 id=0003:045e:028e:0110 props=INPUT_PROP_DIRECT keys=BTN_SOUTH,BTN_EAST abs=ABS_X:-32768:32767:16:128:0") {
		t.Fatalf("file:\n%s", buf.String())
	}
	rec, err := Read(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Header.Devices[0].Caps
	if got == nil || got.ID != caps.ID || len(got.Keys) != 2 || got.Abs[ev.AbsX] != caps.Abs[ev.AbsX] || got.Props[0] != ev.InputPropDirect {
		t.Fatalf("caps = %+v", got)
	}

	// Ошибки: устройства нет выше, неизвестная ось.
	for _, bad := range []string{"mkrec 1\ncaps 5 id=0003:0000:0000:0000\n", "mkrec 1\ndevice 0 gamepad \"P\"\ncaps 0 abs=ABS_NOPE:0:1:0:0\n"} {
		var p *Problem
		if _, err := Read(strings.NewReader(bad)); !errors.As(err, &p) || p.Code != ProblemCaps {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
