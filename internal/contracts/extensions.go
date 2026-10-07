package contracts

import (
	"encoding/json"
	"errors"
)

// ErrExtensionExists возвращается при повторной регистрации расширения с тем же ID в той же точке.
var ErrExtensionExists = errors.New("extension already registered")

// ErrUnknownExtensionPoint возвращается при регистрации в несуществующую точку расширения.
var ErrUnknownExtensionPoint = errors.New("unknown extension point")

// ExtensionPoint — идентификатор точки расширения (SPEC §4.3, таблица «Точки расширения»).
type ExtensionPoint string

// Точки расширения mKey. Конкретные интерфейсы для каждой точки появляются
// в соответствующих фазах разработки; здесь фиксируются только идентификаторы.
const (
	// PointTrigger — типы триггеров событий (hotkey, window, pixel…).
	PointTrigger ExtensionPoint = "trigger"
	// PointCondition — типы условий (window, key_state, variable…).
	PointCondition ExtensionPoint = "condition"
	// PointAction — типы действий (send, repeat, lua…).
	PointAction ExtensionPoint = "action"
	// PointDSLCommand — команды языка макросов ({Move ...}, {Wheel ...}).
	PointDSLCommand ExtensionPoint = "dsl_command"
	// PointDesktopAdapter — адаптеры рабочего стола (x11, gnome, kde…).
	PointDesktopAdapter ExtensionPoint = "desktop_adapter"
	// PointDeviceTemplate — шаблоны виртуальных устройств (xbox360, touchscreen…).
	PointDeviceTemplate ExtensionPoint = "device_template"
	// PointDeviceProfile — профили известных физических устройств (зарезервировано: имена кнопок
	// сейчас переносят проекты, раздел devices, ADR-0027).
	PointDeviceProfile ExtensionPoint = "device_profile"
	// PointInitSystem — бэкенды init-систем и автозапуска (systemd, openrc, runit…).
	PointInitSystem ExtensionPoint = "init_system"
	// PointElevator — бэкенды повышения прав (pkexec, sudo в терминале…).
	PointElevator ExtensionPoint = "elevator"
	// PointDeviceAccess — способы выдачи доступа к устройствам (uaccess, группа, mdev).
	PointDeviceAccess ExtensionPoint = "device_access"
	// PointPackageManager — пакетные менеджеры дистрибутивов (apt, dnf, xbps…).
	PointPackageManager ExtensionPoint = "package_manager"
	// PointLuaExtension — расширения Lua API (mkey.<ns>.*).
	PointLuaExtension ExtensionPoint = "lua_extension"
	// PointRouteProvider — дополнительные маршруты HTTP API модулей.
	PointRouteProvider ExtensionPoint = "route_provider"
	// PointPlace — папки и файлы пользователя («Где что лежит»: проекты, записи, настройки…).
	PointPlace ExtensionPoint = "place"
	// PointProjectTemplate — дополнительные шаблоны проектов (плагины-данные, ADR-0029).
	PointProjectTemplate ExtensionPoint = "project_template"
	// PointScriptLanguage — языки файлов-скриптов (ScriptLanguage) для раздела «Скрипты» окна
	// (FR-UI-1.7): модули lua и shell.
	PointScriptLanguage ExtensionPoint = "script_language"
)

// AllExtensionPoints возвращает список всех известных точек расширения в стабильном порядке.
func AllExtensionPoints() []ExtensionPoint {
	return []ExtensionPoint{
		PointTrigger, PointCondition, PointAction, PointDSLCommand,
		PointDesktopAdapter, PointDeviceTemplate, PointDeviceProfile,
		PointInitSystem, PointElevator, PointDeviceAccess, PointPackageManager,
		PointLuaExtension, PointRouteProvider, PointPlace, PointProjectTemplate, PointScriptLanguage,
	}
}

// ExtensionMeta — метаданные зарегистрированного типа.
// По ним GUI автоматически строит блок конструктора и форму параметров.
type ExtensionMeta struct {
	// ID — уникальный в пределах точки идентификатор, например "send" или "x11".
	ID string `json:"id"`
	// NameKey — i18n-ключ отображаемого имени.
	NameKey string `json:"name_key"`
	// DescriptionKey — i18n-ключ краткого описания.
	DescriptionKey string `json:"description_key,omitempty"`
	// Category — категория в палитре конструктора (keyboard, mouse, logic…).
	Category string `json:"category,omitempty"`
	// Icon — имя иконки из набора GUI.
	Icon string `json:"icon,omitempty"`
	// ParamsSchema — JSON Schema параметров этого типа (может быть пустой).
	ParamsSchema json.RawMessage `json:"params_schema,omitempty"`
	// RequiredCaps — capabilities окружения, без которых тип недоступен.
	RequiredCaps []string `json:"required_caps,omitempty"`
	// Provider — ID модуля или плагина, который зарегистрировал тип.
	Provider string `json:"provider"`
	// Names и Descriptions — название и описание на языках ("ru" → текст) для видов, у которых
	// нет ключей перевода mKey (виды плагинов, ADR-0029); используются, если NameKey пуст.
	Names        map[string]string `json:"names,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
}

// TopicExtensionsChanged — набор зарегистрированных видов изменился (плагин включён, выключен
// или перезапущен); движок перепроверяет проекты, окно перечитывает палитру. Payload: nil.
const TopicExtensionsChanged = "registry.extensions_changed"

// Extension — зарегистрированная реализация в точке расширения.
type Extension interface {
	// Meta возвращает метаданные расширения.
	Meta() ExtensionMeta
}

// ExtensionRegistry — реестры всех точек расширения.
type ExtensionRegistry interface {
	// Register добавляет расширение ext в точку point.
	Register(point ExtensionPoint, ext Extension) error
	// Get возвращает расширение по ID в точке point.
	Get(point ExtensionPoint, id string) (Extension, bool)
	// List возвращает все расширения точки point в порядке регистрации (так их задумал автор
	// модуля: например, палитра конструктора показывает «Нажать», затем «Зажать», «Отпустить»).
	List(point ExtensionPoint) []Extension
	// Unregister убирает расширение id из точки point (плагин выключен или упал, ADR-0029);
	// false — такого не было.
	Unregister(point ExtensionPoint, id string) bool
}
