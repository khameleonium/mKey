//go:build !nogui

package app

import (
	"io/fs"

	"github.com/khameleonium/mKey/internal/registry"
	"github.com/khameleonium/mKey/internal/tray"
	"github.com/khameleonium/mKey/web"
)

// webFS возвращает встроенные файлы окна программы (веб-интерфейса).
func webFS() fs.FS { return web.FS() }

// guiModules возвращает модули, которые есть только в полной сборке: значок в трее.
func guiModules() []registry.Entry {
	return []registry.Entry{
		{Module: tray.New(), Core: false},
	}
}
