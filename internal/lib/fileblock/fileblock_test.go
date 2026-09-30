package fileblock

import "testing"

// TestSetStrip проверяет вставку, замену и удаление блока.
func TestSetStrip(t *testing.T) {
	t.Parallel()
	orig := "a\nb\n"

	// Вставка в конец и повторная вставка (замена, без дублирования).
	once := Set(orig, []string{"x"}, false)
	twice := Set(once, []string{"y"}, false)
	if want := "a\nb\n# >>> mKey >>> managed by mKey, do not edit\ny\n# <<< mKey <<<\n"; twice != want {
		t.Fatalf("Set = %q", twice)
	}

	// Удаление возвращает исходный файл; файл без блока не меняется.
	if got := Strip(twice); got != orig || Strip(orig) != orig {
		t.Fatalf("Strip = %q", got)
	}

	// Вставка в начало, файл без перевода строки в конце, другой префикс комментария.
	if got := Set("z", []string{"x"}, true); got != "# >>> mKey >>> managed by mKey, do not edit\nx\n# <<< mKey <<<\nz" {
		t.Fatalf("prepend = %q", got)
	}
	if got := Set("z", []string{"x"}, false); got != "z\n# >>> mKey >>> managed by mKey, do not edit\nx\n# <<< mKey <<<\n" {
		t.Fatalf("no trailing newline = %q", got)
	}
	kdl := SetWith("layout {}\n", "//", []string{`spawn-at-startup "mkey" "daemon"`}, false)
	if !Has(kdl) || StripWith(kdl, "//") != "layout {}\n" {
		t.Fatalf("kdl = %q", kdl)
	}
}
