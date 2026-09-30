package app

import (
	"testing"

	"mkey/internal/lib/buildinfo"
)

// TestModulesMatchBuild проверяет состав модулей сборки: значок в трее есть только в полной версии,
// а API есть всегда (им пользуются команды терминала).
func TestModulesMatchBuild(t *testing.T) {
	t.Parallel()
	ids := map[string]bool{}
	for _, e := range Modules() {
		ids[e.Module.ID()] = true
	}
	if !ids["api"] || !ids["engine"] || !ids["input"] {
		t.Fatalf("core modules missing: %v", ids)
	}
	if ids["tray"] != buildinfo.GUI {
		t.Fatalf("tray present = %v, GUI build = %v", ids["tray"], buildinfo.GUI)
	}
	if (webFS() != nil) != buildinfo.GUI {
		t.Fatalf("web files present = %v, GUI build = %v", webFS() != nil, buildinfo.GUI)
	}
}
