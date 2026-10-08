package contracts

// ConfigValue — значение одной настройки секции modules.<модуль> в config.yaml.
type ConfigValue struct {
	Key   string
	Value any
}

// ConfigWriter — запись настроек в config.yaml (предоставляет демон, cmd/mkey). Модуль, чьи
// настройки меняют из окна или меню значка, сохраняет их сам, чтобы любое место изменения давало
// один и тот же результат в файле. Комментарии и порядок ключей файла сохраняются.
type ConfigWriter interface {
	// SetModuleValues записывает значения секции modules.<module> одной записью файла.
	SetModuleValues(module string, values []ConfigValue) error
}
