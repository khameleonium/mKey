package buildinfo

import "testing"

// TestCompareVersions проверяет сравнение номеров версий.
func TestCompareVersions(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.2.3", "v1.2.3", 0, true},
		{"0.9.0", "0.10.0", -1, true},
		{"1.0.0", "1.0.0-rc1", 1, true},
		{"1.0.0-rc1", "1.0.0-rc2", -1, true},
		{"2", "1.9.9", 1, true},
		{"dev", "1.0.0", 0, false},
	} {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("Compare(%q, %q) = %d %v", c.a, c.b, got, ok)
		}
	}
}
