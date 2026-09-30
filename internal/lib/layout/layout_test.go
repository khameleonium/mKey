package layout

import (
	"testing"

	ev "mkey/internal/lib/evdev"
)

// TestDefinitionsConsistent проверяет, что в каждом ряду столько символов, сколько клавиш.
func TestDefinitionsConsistent(t *testing.T) {
	t.Parallel()
	for name, rows := range definitions {
		for i, r := range rows {
			if len([]rune(r.lower)) != len(r.codes) || len([]rune(r.shifted)) != len(r.codes) {
				t.Errorf("%s row %d: %d keys, %d lower, %d shifted", name, i, len(r.codes), len([]rune(r.lower)), len([]rune(r.shifted)))
			}
		}
	}
}

// TestFind проверяет характерные символы английской и русской раскладок.
func TestFind(t *testing.T) {
	t.Parallel()
	us, _ := Get("us")
	ru, _ := Get("RU(winkeys)")

	// Таблица: раскладка, символ, ожидаемые клавиша и Shift.
	cases := []struct {
		l     *Layout
		r     rune
		code  uint16
		shift bool
	}{
		{us, 'a', ev.KeyA, false},
		{us, 'A', ev.KeyA, true},
		{us, ',', ev.KeyComma, false},
		{us, '<', ev.KeyComma, true},
		{us, '!', ev.Key1, true},
		{us, ' ', ev.KeySpace, false},
		{ru, 'п', ev.KeyG, false},
		{ru, 'П', ev.KeyG, true},
		{ru, 'ё', ev.KeyGrave, false},
		{ru, ',', ev.KeySlash, true},
		{ru, '.', ev.KeySlash, false},
		{ru, '!', ev.Key1, true},
		{ru, '№', ev.Key3, true},
		{ru, 'б', ev.KeyComma, false},
	}
	for _, c := range cases {
		s, ok := c.l.Find(c.r)
		if !ok || s.Code != c.code || s.Shift != c.shift {
			t.Errorf("%s: Find(%q) = %+v, %v; want %#x shift=%v", c.l.Name, c.r, s, ok, c.code, c.shift)
		}
	}

	// Кириллицы нет в английской раскладке, латиницы — в русской.
	if _, ok := us.Find('п'); ok {
		t.Error("us must not contain Cyrillic")
	}
	if _, ok := ru.Find('a'); ok {
		t.Error("ru must not contain Latin letters")
	}
	if _, ok := Get("klingon"); ok {
		t.Error("unknown layout must not be found")
	}
}
