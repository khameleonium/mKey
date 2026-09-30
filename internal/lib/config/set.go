package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// SetModuleValue записывает в файл настроек path значение modules.<module>.<key> (строка),
// сохраняя остальное содержимое и комментарии. Файла нет — он создаётся. Запись атомарная:
// во временный файл и переименованием.
func SetModuleValue(path, module, key, value string) error {
	// Текущий файл как дерево YAML (с комментариями); нет файла — пустой документ.
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("config: %w", err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("config: the file is not a map")
	}

	// modules → <module> → <key> = value.
	modules := mapChild(root, "modules")
	sec := mapChild(modules, module)
	setScalar(sec, key, value)

	// Проверяем, что результат — правильные настройки, и записываем.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if _, err := Parse(buf.Bytes()); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mapChild возвращает вложенную карту по ключу, создавая её при необходимости.
func mapChild(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				*v = yaml.Node{Kind: yaml.MappingNode}
			}
			return v
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v
}

// setScalar задаёт строковое значение ключа карты (в кавычках — в сочетаниях есть «^» и «{»).
func setScalar(m *yaml.Node, key, value string) {
	v := &yaml.Node{Kind: yaml.ScalarNode, Value: value, Style: yaml.DoubleQuotedStyle}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v.HeadComment, v.LineComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}
