package app

import _ "embed"

// ConfigTemplate — шаблон config.yaml: все настройки mKey по разделам, с пояснениями
// и значениями по умолчанию. Демон при запуске дополняет им файл настроек
// (config.Complete): чего нет — дописывается, значения пользователя не меняются.
// Добавили настройку в модуль — добавьте её сюда с пояснением (это проверяет TestConfigTemplate).
//
//go:embed config.yaml
var ConfigTemplate []byte
