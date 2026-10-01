package app

import (
	"mkey/internal/api"
	"mkey/internal/desktop"
	"mkey/internal/engine"
	"mkey/internal/hotkeys"
	"mkey/internal/input"
	"mkey/internal/inspector"
	"mkey/internal/output"
	"mkey/internal/platform"
	"mkey/internal/pluginhost"
	"mkey/internal/recorder"
	"mkey/internal/registry"
	"mkey/internal/script/lua"
	"mkey/internal/script/shell"
	"mkey/internal/session"
	"mkey/internal/setup"
	"mkey/internal/store"
	"mkey/internal/update"
	// mkey:imports — генератор `make new-module` добавляет импорты модулей над этой строкой.
)

// Modules возвращает полный список встроенных модулей mKey в порядке запуска.
//
// Как подключить новый модуль: добавьте одну строку в список ниже
// (или создайте модуль командой `make new-module NAME=<id>` — она добавит строку сама).
// Core: true — обязательный модуль (его сбой останавливает запуск),
// иначе — необязательный (его можно отключить в config.yaml, его сбой не влияет на остальных).
// Порядок важен: модули, от сервисов которых зависят другие, идут раньше.
// Модули окна программы (значок в трее) добавляются в конце только в полной сборке (gui.go / nogui.go).
func Modules() []registry.Entry {
	list := []registry.Entry{
		// Сведения о сессии и системе нужны почти всем — первыми.
		{Module: session.New(), Core: true},
		{Module: platform.New(), Core: true},
		// Проекты из файлов (нужны hotkeys для переназначений и engine для событий).
		{Module: store.New(), Core: false},
		// Десктоп-адаптеры (раскладки, уведомления); без них текст набирается в раскладке из настроек.
		{Module: desktop.New(), Core: false},
		// Ввод и вывод: без них mKey бесполезен, но без прав они работают в режиме «недоступно».
		{Module: input.New(), Core: true},
		{Module: output.New(), Core: true},
		// Инспектор устройств: подробности и авто-ID (UnKey001) — до hotkeys, engine и lua,
		// которые по нему узнают кнопки конкретных устройств (нужен input).
		{Module: inspector.New(), Core: false},
		// Горячие клавиши и перехват (нужен input).
		{Module: hotkeys.New(), Core: false},
		// Выполнение макросов и событий (нужны output; store, hotkeys и desktop — по возможности).
		{Module: engine.New(), Core: true},
		// Скрипты: регистрируют действия lua и shell.
		{Module: lua.New(), Core: false},
		{Module: shell.New(), Core: false},
		// Запись и воспроизведение ввода (нужны input и output; регистрирует действие play).
		{Module: recorder.New(), Core: false},
		// Диагностика и настройка.
		{Module: setup.New(), Core: false},
		// Плагины: добавляют виды действий, условий и триггеров (после модулей со встроенными видами).
		{Module: pluginhost.New(), Core: false},
		{Module: update.New(), Core: false},
		// mkey:modules — генератор `make new-module` добавляет модули над этой строкой
		// (до API: API получает сервисы модулей при запуске).
		// HTTP API для CLI и веб-интерфейса — последним: пользуется сервисами всех модулей.
		{Module: api.New(webFS()), Core: true},
	}
	return append(list, guiModules()...)
}
