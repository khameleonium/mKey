package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SetModuleValue записывает в файл настроек path значение modules.<module>.<key>, сохраняя
// остальное содержимое и комментарии. value — строка, число, true/false или список строк.
// Файла нет — он создаётся. Запись атомарная: во временный файл и переименованием.
func SetModuleValue(path, module, key string, value any) error {
	return SetModuleValues(path, module, []KeyValue{{key, value}})
}

// KeyValue — ключ настройки и его новое значение.
type KeyValue struct {
	Key   string
	Value any
}

// SetModuleValues записывает несколько значений секции modules.<module> за одну запись файла
// (как SetModuleValue).
func SetModuleValues(path, module string, values []KeyValue) error {
	return Update(path, func(root *yaml.Node) error {
		// modules → <module> → <key> = value.
		sec := mapChild(mapChild(root, "modules"), module)
		for _, kv := range values {
			if err := setValue(sec, kv.Key, kv.Value); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetValue записывает в файл настроек path значение верхнего уровня (например, language).
func SetValue(path, key string, value any) error {
	return Update(path, func(root *yaml.Node) error { return setValue(root, key, value) })
}

// Update читает файл настроек как дерево YAML (с комментариями), даёт его изменить функции edit,
// проверяет результат строгим разбором (Parse) и атомарно записывает. Файла нет — начинаем
// с пустого документа.
func Update(path string, edit func(root *yaml.Node) error) error {
	// Текущий файл как дерево YAML (с комментариями); нет файла — пустой документ.
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	doc, err := parseNode(data)
	if err != nil {
		return err
	}

	// Изменение.
	if err := edit(doc.Content[0]); err != nil {
		return err
	}

	// Проверяем, что результат — правильные настройки, и записываем.
	out, err := encodeNode(doc)
	if err != nil {
		return err
	}
	if _, err := Parse(out); err != nil {
		return err
	}
	return writeAtomic(path, out)
}

// parseNode разбирает YAML в дерево; пустой файл — документ с пустой картой.
func parseNode(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config: %w", err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config: the file is not a map")
	}
	return &doc, nil
}

// encodeNode записывает дерево в YAML с отступом в 2 пробела.
func encodeNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return spaceSections(buf.Bytes()), nil
}

// spaceSections возвращает пустые строки между разделами, которые теряет запись YAML: перед
// ключом верхнего уровня и перед пояснением к ключу верхнего или второго уровня (разделом
// modules.<модуль>), если строкой выше — значение. Так файл остаётся удобным для чтения.
func spaceSections(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+32)
	for i, line := range lines {
		// Отступ строки и что она такое: пояснение или ключ.
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		comment := strings.HasPrefix(trimmed, "#")
		section := trimmed != "" && (indent == 0 || (comment && indent == 2))

		// Пустая строка, если выше — значение (не пояснение, не начало раздела и не пустая строка).
		if section && i > 0 {
			prev := strings.TrimSpace(lines[i-1])
			if prev != "" && !strings.HasPrefix(prev, "#") && !strings.HasSuffix(prev, ":") {
				out = append(out, "")
			}
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// writeAtomic записывает файл через временный и переименование (файл не бывает записан наполовину).
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
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

// setValue задаёт значение ключа карты, сохраняя комментарии у прежнего значения. Строки — в
// кавычках (в сочетаниях есть «^» и «{»), списки — в строку ([keyboard, mouse]).
func setValue(m *yaml.Node, key string, value any) error {
	// Значение как узел YAML.
	v := &yaml.Node{}
	if s, ok := value.(string); ok {
		v = &yaml.Node{Kind: yaml.ScalarNode, Value: s, Style: yaml.DoubleQuotedStyle}
	} else if err := v.Encode(value); err != nil {
		return fmt.Errorf("config: %s: %w", key, err)
	}
	if v.Kind == yaml.SequenceNode {
		v.Style = yaml.FlowStyle
	}

	// Заменяем прежнее значение (с его комментариями) или добавляем ключ в конец.
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v.HeadComment, v.LineComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment
			m.Content[i+1] = v
			return nil
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return nil
}
