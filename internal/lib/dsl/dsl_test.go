package dsl

import (
	"errors"
	"reflect"
	"testing"

	ev "mkey/internal/lib/evdev"
)

// TestParseFormat проверяет разбор правильных записей и их канонический вид.
func TestParseFormat(t *testing.T) {
	t.Parallel()

	// Таблица: исходный текст → канонический текст.
	cases := map[string]string{
		`{A}`:                     `{A}`,
		`{a}`:                     `{A}`,
		`^{mouse0}[500]~{MOUSE0}`: `^{Mouse0}[500]~{Mouse0}`,
		`^{SHIFT}{"Привет, Вера!"}~{SHIFT}{ENTER}`: `^{Shift}{"Привет, Вера!"}~{Shift}{Enter}`,
		`{ "ABCabc\nAAA" }`:                     `{"ABCabc\nAAA"}`,
		`{Space 500}`:                           `{Space 500}`,
		`{Space 1.5s}`:                          `{Space 1.5s}`,
		`{Space 250ms}`:                         `{Space 250}`,
		`{A*3}`:                                 `{A*3}`,
		`{A * 1}`:                               `{A}`,
		`^{ctrl} ^{shift} {t} ~{shift} ~{ctrl}`: `^{Ctrl}^{Shift}{T}~{Shift}~{Ctrl}`,
		`~{*}`:                                  `~{*}`,
		`[250] [1.5s] [2s] [100..300]`:          `[250][1.5s][2s][100..300]`,
		"( {A} [50] {B} ) * 10":                 `({A}[50]{B})*10`,
		"({A})":                                 `({A})`,
		"{A} // комментарий\n{B}":               `{A}{B}`,
		`{Move +10 -5}`:                         `{Move +10 -5}`,
		`{move 100 200}`:                        `{Move 100 200}`,
		`{Wheel up 3}`:                          `{Wheel up 3}`,
		`{Click}`:                               `{Click}`,
		`{#30}{#0x110}`:                         `{#30}{#272}`,
		`{pad2.South}{Педаль.Левая}{UnKey.001}`: `{pad2.South}{Педаль.Левая}{UnKey001}`,
		// Авто-ID (FR-DEV-2): слитная запись — у первого устройства, у остальных — через точку.
		`{unkey001}{UnKey2001}{UnKey2.001}{UnKey12034}`: `{UnKey001}{UnKey2.001}{UnKey2.001}{UnKey12.034}`,
		`{pad2.LX=0.5}{pad2.LY = -1}`:                   `{pad2.LX=0.5}{pad2.LY=-1}`,
		`{"a\"b\\c\/d\teA😀"}`:                           `{"a\"b\\c/d\teA😀"}`,
		"{\"line1\nline2\"}":                            `{"line1\nline2"}`,
		``:                                              ``,
		"  \n // only comment":                          ``,
	}
	for in, want := range cases {
		nodes, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got := Format(nodes); got != want {
			t.Errorf("Format(Parse(%q)) = %q, want %q", in, got, want)
		}
	}
}

// TestParseErrors проверяет коды ошибок и их позиции (FR-DSL-3).
func TestParseErrors(t *testing.T) {
	t.Parallel()

	// Таблица: текст → код ошибки, строка, столбец.
	cases := []struct {
		in        string
		code      string
		line, col int
	}{
		{`{Mous0}`, ErrUnknownKeyHint, 1, 2},
		{`{A}{Xyzzyq}`, ErrUnknownKey, 1, 5},
		{`Hello`, ErrTextOutside, 1, 1},
		{`"Hello"`, ErrTextOutside, 1, 1},
		{`{A`, ErrUnclosedBrace, 1, 1},
		{`{A]`, ErrUnexpectedChar, 1, 3},
		{`[250`, ErrUnclosedPause, 1, 1},
		{`({A}`, ErrUnclosedGroup, 1, 1},
		{`{A})`, ErrUnexpectedClose, 1, 4},
		{`{"abc`, ErrUnclosedText, 1, 2},
		{`{"a\fb"}`, ErrBadEscape, 1, 4},
		{`{"a\qb"}`, ErrBadEscape, 1, 4},
		{`{"\ud83d"}`, ErrBadEscape, 1, 3},
		{`{}`, ErrEmptyBraces, 1, 1},
		{`^{"text"}`, ErrPrefixNotAllowed, 1, 1},
		{`^{A*3}`, ErrPrefixNotAllowed, 1, 1},
		{`~{Space 500}`, ErrPrefixNotAllowed, 1, 1},
		{`^{Move +1 +1}`, ErrPrefixNotAllowed, 1, 1},
		{`{*}`, ErrStarOnlyRelease, 1, 1},
		{`^{*}`, ErrStarOnlyRelease, 1, 1},
		{`{A*3 500}`, ErrRepeatAndHold, 1, 6},
		{`{A*0}`, ErrBadRepeat, 1, 4},
		{`{A*10001}`, ErrBadRepeat, 1, 4},
		{`[2h]`, ErrBadDuration, 1, 2},
		{`[3601s]`, ErrTooLong, 1, 2},
		{`[300..100]`, ErrPauseRange, 1, 1},
		{`{A*3=1}`, ErrAxisChord, 1, 1},
		{`{A} {Ctrl+C}`, ErrChord, 1, 5},
		{`{{}`, ErrBraceKey, 1, 1},
		{`^{}}`, ErrBraceKey, 1, 1},
		{`{#zz}`, ErrBadNumber, 1, 2},
		{"{A}\n  {Mous0}", ErrUnknownKeyHint, 2, 4},
		{"{A}\n{\"Привет\"}\n{B", ErrUnclosedBrace, 3, 1},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		var de *Error
		if !errors.As(err, &de) {
			t.Errorf("Parse(%q) = %v, want *Error %s", c.in, err, c.code)
			continue
		}
		if de.Code != c.code || de.Pos.Line != c.line || de.Pos.Col != c.col {
			t.Errorf("Parse(%q) = %s at %d:%d, want %s at %d:%d", c.in, de.Code, de.Pos.Line, de.Pos.Col, c.code, c.line, c.col)
		}
	}
}

// TestSuggestionInError проверяет подсказку ближайшего имени.
func TestSuggestionInError(t *testing.T) {
	t.Parallel()
	_, err := Parse(`{Mous0}`)
	var de *Error
	if !errors.As(err, &de) || de.Args["name"] != "Mous0" || de.Args["suggestion"] != "Mouse0" {
		t.Fatalf("err = %v", err)
	}
}

// stripPos обнуляет позиции в дереве, чтобы сравнивать структуру.
func stripPos(nodes []Node) []Node {
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		n.Pos = Pos{}
		n.Children = stripPos(n.Children)
		out[i] = n
	}
	return out
}

// TestRoundTrip проверяет, что Parse(Format(Parse(x))) совпадает с Parse(x) (FR-DSL-4).
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	src := "^{Shift}{\"Привет, Вера!\"}~{Shift}{Enter}\n({A}[50..80]^{Ctrl}{C 200}~{Ctrl})*3 {Move +1 -1}{pad2.LX=-0.25}~{*}"
	first, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(Format(first))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !reflect.DeepEqual(stripPos(first), stripPos(second)) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", first, second)
	}
}

// FuzzParse проверяет, что разбор не паникует на любом вводе, а удачно разобранный
// текст после форматирования разбирается в то же дерево.
func FuzzParse(f *testing.F) {
	// Начальный корпус — характерные записи.
	for _, s := range []string{`{A}`, `^{Shift}{"x"}~{Shift}`, `[1..2]`, `({A})*2`, `{Move +1 -1}`, `{"A"}`, `{#30}`, `{a.b=1}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		nodes, err := Parse(src)
		if err != nil {
			var de *Error
			if !errors.As(err, &de) {
				t.Fatalf("error is not *Error: %v", err)
			}
			return
		}
		again, err := Parse(Format(nodes))
		if err != nil {
			t.Fatalf("formatted text does not parse: %q → %q: %v", src, Format(nodes), err)
		}
		if !reflect.DeepEqual(stripPos(nodes), stripPos(again)) {
			t.Fatalf("round trip mismatch for %q", src)
		}
	})
}

// compile — вспомогательная функция: разбор и компиляция с устройствами по умолчанию.
func compile(t *testing.T, src string) ([]Step, error) {
	t.Helper()
	nodes, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return Compile(nodes, DefaultResolver{})
}

// TestCompile проверяет план выполнения: устройства, коды, повторы, команды.
func TestCompile(t *testing.T) {
	t.Parallel()
	steps, err := compile(t, `^{Mouse0}[500]~{Mouse0}{C*2}({A}[10..20])*3{Move +10 -5}{Wheel Down 3}{Click Right}{"hi"}~{*}{#30}`)
	if err != nil {
		t.Fatal(err)
	}

	// Ожидаемые виды шагов по порядку.
	kinds := []StepKind{StepPress, StepWait, StepRelease, StepTap, StepLoop, StepMove, StepWheel, StepTap, StepText, StepReleaseAll, StepTap}
	if len(steps) != len(kinds) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, k := range kinds {
		if steps[i].Kind != k {
			t.Errorf("step %d kind = %s, want %s", i, steps[i].Kind, k)
		}
	}

	// Кнопка мыши — на мышь, клавиша — на клавиатуру с повтором 2.
	if tg := steps[0].Targets[0]; tg.Device != DeviceMouse || tg.Code != ev.BtnLeft {
		t.Errorf("Mouse0 target = %+v", tg)
	}
	if s := steps[3]; s.Count != 2 || s.Targets[0].Code != ev.KeyC || s.Targets[0].Device != DeviceKeyboard {
		t.Errorf("C*2 = %+v", s)
	}

	// Группа с повтором 3, пауза 10..20 внутри; перемещение и прокрутка.
	if s := steps[4]; s.Count != 3 || len(s.Body) != 2 || s.Body[1].MinMS != 10 || s.Body[1].MaxMS != 20 {
		t.Errorf("loop = %+v", s)
	}
	if s := steps[5]; s.DX != 10 || s.DY != -5 {
		t.Errorf("move = %+v", s)
	}
	if s := steps[6]; s.DY != -1 || s.Count != 3 {
		t.Errorf("wheel = %+v", s)
	}
	if tg := steps[7].Targets[0]; tg.Code != ev.BtnRight || tg.Device != DeviceMouse {
		t.Errorf("click right = %+v", tg)
	}
	if tg := steps[10].Targets[0]; tg.Code != ev.KeyA {
		t.Errorf("#30 = %+v", tg)
	}
}

// TestCompileErrors проверяет ошибки компиляции: возможности следующих фаз и неверные аргументы.
func TestCompileErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`{Move 100 200}`:   ErrNotSupported,
		`{Click 10 20}`:    ErrNotSupported,
		`{Touch 1 2}`:      ErrNotSupported,
		`{pad2.South}`:     ErrUnknownDevice,
		`{pad2.LX=0.5}`:    ErrUnknownDevice,
		`{Move +1}`:        ErrBadCommandArgs,
		`{Move +1.5 +1}`:   ErrBadCommandArgs,
		`{Wheel Sideways}`: ErrBadCommandArgs,
		`{Wheel Up -1}`:    ErrBadCommandArgs,
		`{Click Nose}`:     ErrBadCommandArgs,
	}
	for in, code := range cases {
		_, err := compile(t, in)
		var de *Error
		if !errors.As(err, &de) || de.Code != code {
			t.Errorf("Compile(%q) = %v, want %s", in, err, code)
		}
	}
}

// TestChordHint проверяет подсказку для {Ctrl+Alt+Del}: как записать сочетание зажатием.
func TestChordHint(t *testing.T) {
	t.Parallel()
	_, err := Parse(`{ctrl+alt+del}`)
	var de *Error
	if !errors.As(err, &de) || de.Args["macro"] != "^{Ctrl}^{Alt}{Delete}~{Alt}~{Ctrl}" || de.Args["hotkey"] != "^{Ctrl}^{Alt}{Delete}" {
		t.Fatalf("err = %v", err)
	}
}

// TestCheckHolds проверяет правила зажатия: дважды не зажать, зажатую не нажать, отпустить только зажатую.
func TestCheckHolds(t *testing.T) {
	t.Parallel()
	ok := []string{
		`^{Ctrl}{A}~{Ctrl}`,
		`^{Shift}{"Привет"}~{Shift}`,
		`^{Ctrl}{A}`,         // не отпущена — mKey отпустит сам
		`^{Ctrl}{C}~{LCtrl}`, // Ctrl и LCtrl — одна клавиша
		`(^{A}[10]~{A})*5`,
		`^{A}^{B}~{*}^{A}`,
	}
	for _, src := range ok {
		nodes, err := Parse(src)
		if err == nil {
			err = CheckHolds(nodes)
		}
		if err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := []struct {
		src  string
		code string
		col  int
	}{
		{`^{Ctrl}^{Ctrl}`, ErrAlreadyHeld, 8},
		{`^{Ctrl}^{LCtrl}`, ErrAlreadyHeld, 8},
		{`^{A}{A}`, ErrAlreadyHeld, 5},
		{`~{Ctrl}`, ErrNotHeld, 1},
		{`^{A}~{A}~{A}`, ErrNotHeld, 9},
		{`(^{A})*2`, ErrAlreadyHeld, 2},
	}
	for _, c := range bad {
		nodes, err := Parse(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		var de *Error
		if err := CheckHolds(nodes); !errors.As(err, &de) || de.Code != c.code || de.Pos.Col != c.col {
			t.Errorf("%s: err = %v, want %s at col %d", c.src, err, c.code, c.col)
		}
	}
}

// TestParseHotkey проверяет запись горячих клавиш зажатием.
func TestParseHotkey(t *testing.T) {
	t.Parallel()
	refs, err := ParseHotkey(`^{Ctrl}^{Alt}{H}`)
	if err != nil || len(refs) != 3 || refs[0].Name != "Ctrl" || refs[2].Name != "H" {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	if refs, err := ParseHotkey(`{F8}`); err != nil || len(refs) != 1 {
		t.Fatalf("F8: %+v %v", refs, err)
	}
	for _, src := range []string{``, `{A}{B}`, `^{Ctrl}`, `^{Ctrl}{H}~{Ctrl}`, `{A*2}`, `^{Ctrl}^{Ctrl}{H}`, `{Ctrl+H}`, `{"x"}`} {
		if _, err := ParseHotkey(src); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
}

// TestDeviceResolver проверяет отправку кнопок устройств с авто-ID (FR-DEV-2): KEY_* — на клавиатуру,
// кнопки мыши — на мышь, кнопки джойстика — ошибка; ошибка поиска — с позицией в макросе.
func TestDeviceResolver(t *testing.T) {
	t.Parallel()
	lookup := func(device, button string) (uint16, string, error) {
		switch device + "." + button {
		case "UnKey.016":
			return ev.KeyCalc, "UnKey016", nil
		case "UnKey.A":
			return ev.KeyA, "UnKey.A", nil
		case "UnKey2.001":
			return ev.BtnSide, "UnKey2.001", nil
		case "UnKey3.001":
			return ev.BtnTrigger, "UnKey3.001", nil
		}
		return 0, "", NewError(Pos{}, ErrUnknownButton, "device", device, "button", button)
	}
	compile := func(src string, r Resolver) ([]Step, error) {
		nodes, err := Parse(src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		return Compile(nodes, r)
	}

	// Клавиатура, мышь; обычные клавиши — как раньше.
	steps, err := compile(`{UnKey016}{UnKey.A}{UnKey2.001}{B}`, DeviceResolver{Lookup: lookup})
	if err != nil {
		t.Fatal(err)
	}
	var got []Target
	for _, s := range steps {
		got = append(got, s.Targets...)
	}
	want := []Target{
		{Device: DeviceKeyboard, Code: ev.KeyCalc, Name: "UnKey016"}, {Device: DeviceKeyboard, Code: ev.KeyA, Name: "UnKey.A"},
		{Device: DeviceMouse, Code: ev.BtnSide, Name: "UnKey2.001"}, {Device: DeviceKeyboard, Code: ev.KeyB, Name: "B"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %+v", got)
	}

	// Кнопка джойстика — нельзя нажать; неизвестная кнопка — с позицией; без Lookup — неизвестное устройство.
	for _, c := range []struct {
		src  string
		r    Resolver
		code string
		col  int
	}{
		{`{UnKey3.001}`, DeviceResolver{Lookup: lookup}, ErrCannotSend, 1},
		{`[10]{UnKey.999}`, DeviceResolver{Lookup: lookup}, ErrUnknownButton, 5},
		{`{UnKey016}`, DeviceResolver{}, ErrUnknownDevice, 1},
	} {
		_, err := compile(c.src, c.r)
		var de *Error
		if !errors.As(err, &de) || de.Code != c.code || de.Pos.Col != c.col {
			t.Errorf("%s: err = %#v", c.src, err)
		}
	}
}
