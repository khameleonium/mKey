// Package profiles встраивает в бинарник профили известных устройств (FR-DEV-7, ADR-0027):
// готовые имена кнопок и осей для моделей, у которых кнопки без стандартного имени mKey.
//
// Профиль — YAML-файл в profiles/devices/ (формат — lib/devmap.Profile). Модуль inspector
// регистрирует встроенные профили в точке расширения device_profile; профили человека из папки
// профилей (mkey paths) важнее встроенных. Добавить встроенный профиль — положить файл в
// profiles/devices/ (имя файла — по модели, латиницей) и проверить его тестом пакета.
package profiles

import (
	"embed"
	"io/fs"
)

// devices — встроенные профили устройств.
//
//go:embed devices/*.yaml
var devices embed.FS

// Devices возвращает встроенные профили: имя файла → содержимое.
func Devices() map[string][]byte {
	out := map[string][]byte{}
	files, _ := fs.Glob(devices, "devices/*.yaml")
	for _, name := range files {
		if data, err := devices.ReadFile(name); err == nil {
			out[name[len("devices/"):]] = data
		}
	}
	return out
}
