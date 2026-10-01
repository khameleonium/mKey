package profiles

import (
	"testing"

	"mkey/internal/lib/devmap"
)

// TestBuiltinProfiles проверяет, что каждый встроенный профиль читается без ошибок, а модели не
// повторяются (иначе непонятно, какой профиль применить).
func TestBuiltinProfiles(t *testing.T) {
	t.Parallel()
	all := Devices()
	if len(all) == 0 {
		t.Fatal("no builtin profiles")
	}
	seen := map[string]string{}
	for name, data := range all {
		p, err := devmap.ParseProfile(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		key := p.Match.Vid + ":" + p.Match.Pid + ":" + p.Match.Name
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s match the same model %s", name, other, key)
		}
		seen[key] = name
	}
}
