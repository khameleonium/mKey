// Package buildinfo хранит сведения о сборке mKey: версию, коммит и дату.
//
// Значения подставляются при сборке через -ldflags (см. Makefile), например:
//
//	-X github.com/khameleonium/mKey/internal/lib/buildinfo.Version=1.0.0
//
// При сборке без ldflags (go build, go run) используются значения по умолчанию.
package buildinfo

// Creator — создатель mKey латиницей (решение владельца: упоминается везде) — для машиночитаемого
// вывода (`mkey version --json`). В текстах для людей имя на языке интерфейса — i18n-ключ app.creator
// («Создатель: Илья Ульянов | khameleonium» / «Creator: Ilya Ulyanov | khameleonium»).
const Creator = "Ilya Ulyanov | khameleonium"

// Сведения о сборке. Переменные, а не константы, потому что их задаёт компоновщик.
var (
	// Version — версия mKey по semver или "dev" для локальной сборки.
	Version = "dev"
	// Commit — короткий хеш git-коммита или "unknown".
	Commit = "unknown"
	// Date — дата сборки в формате RFC 3339 или "unknown".
	Date = "unknown"
)
