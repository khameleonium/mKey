//go:build nogui

package app

import (
	"io/fs"

	"mkey/internal/registry"
)

// webFS в консольной сборке пуст: окна нет, API слушает только Unix-сокет для команд терминала.
func webFS() fs.FS { return nil }

// guiModules в консольной сборке пуст: значка в трее нет.
func guiModules() []registry.Entry { return nil }
