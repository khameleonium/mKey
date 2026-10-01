package buildinfo

import (
	"strconv"
	"strings"
)

// CompareVersions сравнивает версии вида "1.2.3" ("v" в начале допускается; суффикс "-rc1" —
// предварительная, младше той же без суффикса): −1 — a меньше b, 0 — равны, 1 — больше.
// ok == false — одна из версий не похожа на номер ("dev").
func CompareVersions(a, b string) (cmp int, ok bool) {
	pa, sa, okA := parseVersion(a)
	pb, sb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case sa == sb:
		return 0, true
	case sa == "":
		return 1, true
	case sb == "":
		return -1, true
	case sa < sb:
		return -1, true
	}
	return 1, true
}

// parseVersion разбирает "v1.2.3-rc1" в числа и суффикс.
func parseVersion(s string) ([3]int, string, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	main, suffix, _ := strings.Cut(s, "-")
	parts := strings.Split(main, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, "", false
		}
		out[i] = n
	}
	return out, suffix, true
}
