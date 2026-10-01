package contracts

// Place — место на диске, где mKey хранит файлы пользователя: папка проектов, записей, скриптов,
// файл настроек, журнал (точка расширения PointPlace). Каждый модуль сам регистрирует свои места,
// а окно программы и команда «mkey paths» показывают их, чтобы человеку не приходилось искать.
// В Meta: ID — постоянный идентификатор места ("projects", "recordings"…), NameKey — название,
// DescriptionKey — что там лежит и как этим пользоваться.
type Place interface {
	Extension
	// Path возвращает полный путь к папке или файлу.
	Path() string
	// IsDir сообщает, что место — папка (иначе — файл).
	IsDir() bool
	// Order — порядок в списке «Где что лежит» (меньше — выше).
	Order() int
}

// StaticPlace — готовая реализация Place с неизменным путём (её хватает встроенным модулям).
type StaticPlace struct {
	// M — метаданные места (ID, NameKey, DescriptionKey, Provider).
	M ExtensionMeta
	// P — полный путь; Dir — это папка; N — порядок в списке.
	P   string
	Dir bool
	N   int
}

// Meta возвращает метаданные места.
func (p StaticPlace) Meta() ExtensionMeta { return p.M }

// Path возвращает полный путь.
func (p StaticPlace) Path() string { return p.P }

// IsDir сообщает, что место — папка.
func (p StaticPlace) IsDir() bool { return p.Dir }

// Order возвращает порядок в списке.
func (p StaticPlace) Order() int { return p.N }

// Идентификаторы встроенных мест: по ним окно программы показывает папку рядом с разделом.
const (
	// PlaceConfig — файл настроек config.yaml.
	PlaceConfig = "config"
	// PlaceProjects — папка проектов.
	PlaceProjects = "projects"
	// PlaceRecordings — папка записей.
	PlaceRecordings = "recordings"
	// PlaceLuaScripts и PlaceShellScripts — папки скриптов Lua и bash.
	PlaceLuaScripts   = "lua_scripts"
	PlaceShellScripts = "shell_scripts"
	// PlaceLog — журнал работы.
	PlaceLog = "log"
	// PlacePlugins — папка плагинов пользователя.
	PlacePlugins = "plugins"
)
