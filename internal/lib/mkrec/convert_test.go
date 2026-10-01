package mkrec

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// convert читает запись из текста и превращает её в действия (в JSON — для сравнения).
func convert(t *testing.T, body string, opts ConvertOptions) string {
	t.Helper()
	rec, err := Read(strings.NewReader("mkrec 1\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ToActions(rec, opts))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestToActions проверяет правила превращения записи в блоки.
func TestToActions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, want string
	}{
		{"нажатие и пауза",
			"0.000 0 ^{A}\n0.080 0 ~{A}\n0.500 0 ^{B}\n0.560 0 ~{B}\n",
			`[{"type":"tap","value":"A"},{"type":"pause","value":500},{"type":"tap","value":"B"}]`},
		{"удержание дольше 300 мс",
			"0.000 0 ^{Space}\n0.700 0 ~{Space}\n1.000 0 ^{A}\n1.050 0 ~{A}\n",
			`[{"type":"hold","value":{"key":"Space","ms":700}},{"type":"pause","value":300},{"type":"tap","value":"A"}]`},
		{"Shift при наборе — зажать и отпустить",
			"0.000 0 ^{LShift}\n0.100 0 ^{A}\n0.150 0 ~{A}\n0.200 0 ~{LShift}\n",
			`[{"type":"key_down","value":"LShift"},{"type":"pause","value":100},{"type":"tap","value":"A"},{"type":"pause","value":100},{"type":"key_up","value":"LShift"}]`},
		{"быстрый набор с перекрытием — два нажатия",
			"0.000 0 ^{A}\n0.040 0 ^{B}\n0.060 0 ~{A}\n0.100 0 ~{B}\n",
			`[{"type":"tap","value":"A"},{"type":"pause","value":40},{"type":"tap","value":"B"}]`},
		{"щелчок мыши и колёсико",
			"0.000 1 ^{Mouse0}\n0.050 1 ~{Mouse0}\n0.300 1 wheel -2 wheel-hr -240\n0.400 1 ^{Mouse1}\n0.450 1 ~{Mouse1}\n",
			`[{"type":"mouse_click","value":"Left"},{"type":"pause","value":300},{"type":"wheel","value":{"count":2,"direction":"Down"}},{"type":"pause","value":100},{"type":"mouse_click","value":"Right"}]`},
		{"перетаскивание — зажать кнопку, сдвиги, отпустить",
			"0.000 1 ^{Mouse0}\n0.100 1 move +10 +0\n0.200 1 move +10 +0\n0.300 1 ~{Mouse0}\n",
			`[{"type":"key_down","value":"Mouse0"},{"type":"pause","value":100},{"type":"mouse_move","value":{"dx":10,"dy":0}},{"type":"pause","value":100},{"type":"mouse_move","value":{"dx":10,"dy":0}},{"type":"pause","value":100},{"type":"key_up","value":"Mouse0"}]`},
		{"короткие паузы переносятся, геймпад пропускается, калибровка в начале",
			"pointer center\n0.000 0 ^{A}\n0.010 0 ~{A}\n0.015 0 ^{B}\n0.020 0 ~{B}\n0.030 2 ^{South}\n0.040 2 ~{South}\n0.035 0 ^{C}\n0.040 0 ~{C}\n",
			`[{"type":"pointer_center","value":{}},{"type":"tap","value":"A"},{"type":"tap","value":"B"},{"type":"pause","value":35},{"type":"tap","value":"C"}]`},
		{"клавиша не отпущена до конца — зажать",
			"0.000 0 ^{W}\n0.100 0 ^{A}\n0.150 0 ~{A}\n",
			`[{"type":"key_down","value":"W"},{"type":"pause","value":100},{"type":"tap","value":"A"}]`},
	}
	for _, c := range cases {
		if got := convert(t, c.body, ConvertOptions{}); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// TestSimplifyMoves проверяет упрощение пути мыши: прямой путь — один блок, угол сохраняется,
// итоговое смещение не меняется; без упрощения — блок на каждый сдвиг.
func TestSimplifyMoves(t *testing.T) {
	t.Parallel()

	// Путь: 10 шагов вправо, затем 10 шагов вниз (угол), по 10 мс.
	var b strings.Builder
	for i := range 20 {
		dx, dy := 5, 0
		if i >= 10 {
			dx, dy = 0, 5
		}
		b.WriteString(fmtLine(time.Duration(i+1)*10*time.Millisecond, dx, dy))
	}
	body := b.String()

	simple := convert(t, body, ConvertOptions{Simplify: true})
	want := `[{"type":"mouse_move","value":{"dx":50,"dy":0}},{"type":"pause","value":100},{"type":"mouse_move","value":{"dx":0,"dy":50}}]`
	if simple != want {
		t.Errorf("simplified:\n got %s\nwant %s", simple, want)
	}

	full := convert(t, body, ConvertOptions{})
	if n := strings.Count(full, `"mouse_move"`); n != 20 {
		t.Errorf("not simplified: %d moves", n)
	}
}

// fmtLine — строка записи со сдвигом мыши.
func fmtLine(at time.Duration, dx, dy int) string {
	return seconds(at) + " 1 move " + signed(dx) + " " + signed(dy) + "\n"
}

// signed записывает число со знаком.
func signed(v int) string {
	if v >= 0 {
		return "+" + itoa(v)
	}
	return itoa(v)
}

// itoa — число строкой без лишних зависимостей в тесте.
func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
