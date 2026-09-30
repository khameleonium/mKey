// Package web встраивает собранный веб-интерфейс mKey в бинарник (D8).
//
// Сборка фронтенда (`make web`) кладёт файлы в web/dist. Если фронтенд не собран
// (например, `go build` без Node), в dist лежит только .gitkeep — тогда отдаётся
// заглушка из web/stub с подсказкой, как собрать интерфейс. Так `go build ./...`
// всегда работает без Node (AGENTS.md §3).
package web

import (
	"embed"
	"io/fs"
)

// dist — собранный фронтенд (может содержать только .gitkeep).
//
//go:embed all:dist
var dist embed.FS

// stub — заглушка на случай несобранного фронтенда.
//
//go:embed stub/index.html
var stub embed.FS

// FS возвращает файловую систему веб-интерфейса с index.html в корне:
// собранный фронтенд, если он есть, иначе заглушку.
func FS() fs.FS {
	// Предпочитаем собранный фронтенд.
	if Built() {
		sub, err := fs.Sub(dist, "dist")
		if err == nil {
			return sub
		}
	}

	// Фронтенд не собран — отдаём заглушку. Ошибка невозможна: путь зашит при компиляции.
	sub, _ := fs.Sub(stub, "stub")
	return sub
}

// Built сообщает, встроен ли в бинарник собранный фронтенд (а не заглушка).
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}
