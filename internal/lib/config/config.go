// Package config — глобальные настройки mKey из файла config.yaml (SPEC §8, T3.1).
//
// Файл читается один раз при запуске демона: от него зависят язык, состав модулей
// (modules.<id>.enabled) и секции настроек модулей (modules.<id>.*), которые менеджер
// модулей передаёт каждому модулю. Изменения config.yaml вступают в силу после
// перезапуска демона (горячая перезагрузка — у файлов проектов, см. модуль store).
//
// Отсутствующий файл — не ошибка: используются значения по умолчанию.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

// CurrentVersion — текущая версия схемы config.yaml.
const CurrentVersion = 2

// FileName — имя файла настроек в каталоге настроек mKey.
const FileName = "config.yaml"

// Config — содержимое config.yaml.
type Config struct {
	// Version — версия схемы файла.
	Version int `yaml:"version"`
	// Language — язык сообщений ("ru", "en"); пусто — из системы.
	Language string `yaml:"language,omitempty"`
	// Modules — настройки модулей по их ID; ключ enabled включает/выключает необязательный модуль.
	Modules map[string]map[string]any `yaml:"modules,omitempty"`
}

// Load читает настройки из файла path. Отсутствующий файл даёт настройки по умолчанию.
// Неизвестные ключи верхнего уровня — ошибка (защита от опечаток).
func Load(path string) (Config, error) {
	// Читаем файл; его может не быть.
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Version: CurrentVersion}, nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse разбирает содержимое config.yaml.
func Parse(data []byte) (Config, error) { return parseUpTo(data, CurrentVersion) }

// parseUpTo разбирает config.yaml, допуская версии схемы до max.
func parseUpTo(data []byte, max int) (Config, error) {
	// Строгий разбор: неизвестные ключи — ошибка; пустой файл (io.EOF) — настройки по умолчанию.
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	// Версия: пустой файл — текущая; более новая — ошибка (нельзя молча терять настройки).
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Version > max {
		return Config{}, fmt.Errorf("config: version %d is newer than supported %d", c.Version, max)
	}
	return c, nil
}

// Enabled сообщает, включён ли модуль id. По умолчанию включены все модули.
func (c Config) Enabled(id string) bool {
	if v, ok := c.Modules[id]["enabled"].(bool); ok {
		return v
	}
	return true
}

// Section возвращает настройки модуля id в JSON (без ключа enabled) или nil, если их нет.
func (c Config) Section(id string) []byte {
	// Копируем секцию без служебного ключа enabled.
	sec := map[string]any{}
	for k, v := range c.Modules[id] {
		if k != "enabled" {
			sec[k] = v
		}
	}
	if len(sec) == 0 {
		return nil
	}

	// YAML-значения (map[string]any, числа, строки) переводятся в JSON без потерь.
	data, err := json.Marshal(sec)
	if err != nil {
		return nil
	}
	return data
}
