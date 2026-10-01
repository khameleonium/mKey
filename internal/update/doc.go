// Package update — модуль mKey «update»: проверка и установка новых версий (ADR-0030, FR-INST-6).
//
// Проверка (GitHub API, последний выпуск) — только по просьбе человека (кнопка «Проверить»,
// `mkey update --check`) или раз в сутки, если это включено (modules.update.check: true, по
// умолчанию выключено). О новой версии модуль сообщает уведомлением один раз и событием шины
// contracts.TopicUpdateAvailable.
//
// Установка: архив своей сборки (mkey или mkey-cli, своя архитектура) и checksums.txt выпуска,
// сверка sha256, замена программы переименованием (прежняя — рядом как mkey.old) и перезапуск
// демона (contracts.Lifecycle.Restart). Программу, установленную пакетом (вне домашней папки),
// и сборку разработчика модуль не обновляет и объясняет почему (UpdateInfo.Reason).
//
// Модуль предоставляет contracts.Updater; использует contracts.Notifier и contracts.Lifecycle
// (оба необязательны).
package update
