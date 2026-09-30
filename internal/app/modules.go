package app

import (
	"mkey/internal/api"
	"mkey/internal/desktop"
	"mkey/internal/engine"
	"mkey/internal/input"
	"mkey/internal/output"
	"mkey/internal/platform"
	"mkey/internal/registry"
	"mkey/internal/session"
	"mkey/internal/setup"
	"mkey/web"
	// mkey:imports — генератор `make new-module` добавляет импорты модулей над этой строкой.
)

// Modules возвращает полный список встроенных модулей mKey в порядке запуска.
//
// Как подключить новый модуль: добавьте одну строку в список ниже
// (или создайте модуль командой `make new-module NAME=<id>` — она добавит строку сама).
// Core: true — обязательный модуль (его сбой останавливает запуск),
// иначе — необязательный (его можно отключить в config.yaml, его сбой не влияет на остальных).
// Порядок важен: модули, от сервисов которых зависят другие, идут раньше.
func Modules() []registry.Entry {
	return []registry.Entry{
		// Сведения о сессии и системе нужны почти всем — первыми.
		{Module: session.New(), Core: true},
		{Module: platform.New(), Core: true},
		// Десктоп-адаптеры (раскладки); без них текст набирается в раскладке из настроек.
		{Module: desktop.New(), Core: false},
		// Ввод и вывод: без них mKey бесполезен, но без прав они работают в режиме «недоступно».
		{Module: input.New(), Core: true},
		{Module: output.New(), Core: true},
		// Выполнение макросов (нужны output и, по возможности, desktop).
		{Module: engine.New(), Core: true},
		// Диагностика и настройка.
		{Module: setup.New(), Core: false},
		// HTTP API для CLI и веб-интерфейса — последним: пользуется сервисами всех модулей.
		{Module: api.New(web.FS()), Core: true},
		// mkey:modules — генератор `make new-module` добавляет модули над этой строкой.
	}
}
