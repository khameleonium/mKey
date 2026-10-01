package config

import (
	"bytes"
	"errors"
	"os"

	"go.yaml.in/yaml/v3"
)

// Шаблон config.yaml — полный список настроек с пояснениями и значениями по умолчанию (его
// составляет internal/app: только он знает все модули). При запуске демона файл настроек
// дополняется по шаблону: чего в нём нет — дописывается с пояснением, что есть — не меняется.
// Так в файле всегда видны все настройки, в том числе появившиеся в новой версии mKey.

// Complete дополняет файл настроек path настройками из шаблона tmpl, которых в нём нет.
// Значения и комментарии пользователя сохраняются; ключи, которых нет в шаблоне, остаются
// на месте (после шаблонных). Файла нет — записывается шаблон. true — файл изменён.
func Complete(path string, tmpl []byte) (bool, error) {
	// Файл пользователя (его может не быть) и шаблон.
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if _, err := Parse(tmpl); err != nil {
			return false, err
		}
		return true, writeAtomic(path, tmpl)
	}
	user, err := parseNode(data)
	if err != nil {
		return false, err
	}
	base, err := parseNode(tmpl)
	if err != nil {
		return false, err
	}

	// Слияние: дерево шаблона, в котором значения пользователя заменяют значения по умолчанию.
	if !merge(base.Content[0], user.Content[0]) {
		return false, nil
	}
	if user.HeadComment != "" {
		base.HeadComment = user.HeadComment
	}
	out, err := encodeNode(base)
	if err != nil {
		return false, err
	}
	if _, err := Parse(out); err != nil {
		return false, err
	}
	return true, writeAtomic(path, out)
}

// merge переносит значения пользователя из карты user в карту шаблона base: совпадающие ключи
// получают значение пользователя (карты — рекурсивно), ключи только пользователя дописываются
// в конец. Возвращает true, если в user не хватало хотя бы одного ключа шаблона.
func merge(base, user *yaml.Node) bool {
	missing := false
	used := map[string]bool{}

	// Ключи шаблона: значение пользователя, если оно есть.
	for i := 0; i+1 < len(base.Content); i += 2 {
		key := base.Content[i].Value
		uk, uv := lookup(user, key)
		if uv == nil {
			missing = true
			continue
		}
		used[key] = true
		if base.Content[i+1].Kind == yaml.MappingNode && uv.Kind == yaml.MappingNode {
			missing = merge(base.Content[i+1], uv) || missing
			continue
		}

		// Значение пользователя; пояснение шаблона остаётся, если у пользователя своего нет.
		if uk.HeadComment == "" {
			uk.HeadComment = base.Content[i].HeadComment
		}
		if uv.LineComment == "" {
			uv.LineComment = base.Content[i+1].LineComment
		}
		base.Content[i], base.Content[i+1] = uk, uv
	}

	// Ключи, которых нет в шаблоне (например, enabled: false у модуля), — в конец.
	for i := 0; i+1 < len(user.Content); i += 2 {
		if !used[user.Content[i].Value] {
			base.Content = append(base.Content, user.Content[i], user.Content[i+1])
		}
	}
	return missing
}

// lookup находит в карте ключ и значение по имени (nil — нет).
func lookup(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}
