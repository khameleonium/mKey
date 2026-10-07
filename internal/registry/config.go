package registry

import (
	"encoding/json"
	"fmt"

	"github.com/khameleonium/mKey/internal/contracts"
)

// RawConfig — секция конфигурации модуля из config.yaml (modules.<id>), хранящаяся как JSON:
// демон берёт её из lib/config (Config.Section) и передаёт модулю в Init.
type RawConfig json.RawMessage

// Decode заполняет v значениями секции. Пустая секция оставляет v без изменений.
func (c RawConfig) Decode(v any) error {
	// Пустая секция — модуль работает на значениях по умолчанию.
	if len(c) == 0 {
		return nil
	}

	// Разбираем JSON секции в структуру модуля.
	if err := json.Unmarshal(c, v); err != nil {
		return fmt.Errorf("decode module config: %w", err)
	}
	return nil
}

// Проверка на этапе компиляции, что RawConfig реализует контракт.
var _ contracts.ConfigSection = RawConfig(nil)
