package keys

import (
	"testing"

	ev "mkey/internal/lib/evdev"
)

// TestLookup проверяет поиск по каноническим именам, алиасам и без учёта регистра.
func TestLookup(t *testing.T) {
	t.Parallel()

	// Таблица: запрос, ожидаемое каноническое имя и код.
	cases := []struct {
		query, name string
		code        uint16
	}{
		{"Ctrl", "Ctrl", ev.KeyLeftctrl},
		{"CTRL", "Ctrl", ev.KeyLeftctrl},
		{"control", "Ctrl", ev.KeyLeftctrl},
		{"SHIFT", "Shift", ev.KeyLeftshift},
		{"AltGr", "RAlt", ev.KeyRightalt},
		{"win", "Super", ev.KeyLeftmeta},
		{"enter", "Enter", ev.KeyEnter},
		{"a", "A", ev.KeyA},
		{"q", "Q", ev.KeyQ},
		{"0", "0", ev.Key0},
		{"f24", "F24", ev.KeyF24},
		{"mouse0", "Mouse0", ev.BtnLeft},
		{"Mouse1", "Mouse1", ev.BtnRight},
		{"Mouse3", "Mouse3", ev.BtnSide},
		{"Mouse4", "Mouse4", ev.BtnExtra},
		{" Space ", "Space", ev.KeySpace},
	}
	for _, c := range cases {
		k, ok := Lookup(c.query)
		if !ok || k.Name != c.name || k.Code != c.code || k.Type != ev.EvKey {
			t.Errorf("Lookup(%q) = %+v, %v; want %s/%#x", c.query, k, ok, c.name, c.code)
		}
	}

	// Геймпадные имена не доступны в основном пространстве, а буквы — это клавиатура.
	if _, ok := Lookup("South"); ok {
		t.Error("gamepad names must not resolve in the keyboard namespace")
	}
	if _, ok := Lookup("Mous0"); ok {
		t.Error("typo must not resolve")
	}
}

// TestLookupGamepadAndAxis проверяет пространства имён геймпада и осей.
func TestLookupGamepadAndAxis(t *testing.T) {
	t.Parallel()

	// Алиасы Xbox: A — South, X — West (слева), Y — North (сверху).
	for q, code := range map[string]uint16{"A": ev.BtnSouth, "b": ev.BtnEast, "X": ev.BtnWest, "y": ev.BtnNorth, "start": ev.BtnStart, "LT": ev.BtnTl2} {
		if k, ok := LookupGamepad(q); !ok || k.Code != code {
			t.Errorf("LookupGamepad(%q) = %+v, %v; want %#x", q, k, ok, code)
		}
	}

	// Оси: тип EV_ABS, курки — ABS_Z/ABS_RZ.
	if k, ok := LookupAxis("lt"); !ok || k.Type != ev.EvAbs || k.Code != ev.AbsZ {
		t.Errorf("LookupAxis(lt) = %+v, %v", k, ok)
	}
}

// TestMatches проверяет, что модификатор без стороны распознаёт обе клавиши.
func TestMatches(t *testing.T) {
	t.Parallel()
	ctrl, _ := Lookup("Ctrl")
	lctrl, _ := Lookup("LCtrl")
	if !ctrl.Matches(ev.KeyLeftctrl) || !ctrl.Matches(ev.KeyRightctrl) {
		t.Error("Ctrl must match both sides")
	}
	if !lctrl.Matches(ev.KeyLeftctrl) || lctrl.Matches(ev.KeyRightctrl) {
		t.Error("LCtrl must match only the left side")
	}
}

// TestNameOf проверяет обратный поиск: конкретная клавиша важнее обобщённой.
func TestNameOf(t *testing.T) {
	t.Parallel()
	for code, want := range map[uint16]string{ev.KeyLeftctrl: "LCtrl", ev.BtnLeft: "Mouse0", ev.BtnSouth: "South", ev.KeyEnter: "Enter"} {
		if got, ok := NameOf(code); !ok || got != want {
			t.Errorf("NameOf(%#x) = %q, %v; want %q", code, got, ok, want)
		}
	}

	// Кнопки без имени (например, BTN_TRIGGER_HAPPY3) получат авто-ID позже.
	if _, ok := NameOf(0x2c2); ok {
		t.Error("BTN_TRIGGER_HAPPY3 must have no mKey name")
	}
}

// TestSuggest проверяет подсказки при опечатках (FR-DSL-3).
func TestSuggest(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"mous0":     "Mouse0",
		"Mous0":     "Mouse0",
		"entr":      "Enter",
		"Backspase": "Backspace",
		"xyzzyq":    "",
		"":          "",
	}
	for in, want := range cases {
		if got := Suggest(in); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTablesHaveKnownCodes проверяет, что у всех клавиш есть имя кода в ядре (защита от опечаток в таблице).
func TestTablesHaveKnownCodes(t *testing.T) {
	t.Parallel()
	for _, e := range append(append([]entry{}, keyboardTable...), gamepadTable...) {
		if name := ev.CodeName(ev.EvKey, e.code); name[0] == '0' {
			t.Errorf("%s: code %#x has no kernel name", e.name, e.code)
		}
	}
}
