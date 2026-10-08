// Package builder — модуль сборки самостоятельного файла макроса (FR-BUILD-1, ADR-0042).
//
// Сервис contracts.MacroBuilder берёт проект из хранилища (contracts.Projects), проверяет его
// (contracts.Events), находит записи (place recordings) и скрипты (места lua_scripts и
// shell_scripts), на которые ссылаются действия play, lua и shell, и дописывает всё к программе
// mkey в формате lib/bundle. Действия плагинов в файл не кладутся (ошибка ErrBuildPlugin):
// плагин — отдельная программа, её на чужом компьютере может не быть.
//
// Модуль регистрирует место builds — папку собранных файлов (~/.local/share/mkey/builds или
// modules.builder.dir). Запускает собранный файл команда mkey (cmd/mkey/standalone.go).
//
// Расширение: новый вид файлов, на которые ссылается проект, — ещё одна группа в collect и
// раздел архива в lib/bundle.
package builder
