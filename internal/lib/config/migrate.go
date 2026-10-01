package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Миграция config.yaml на новую версию схемы (ADR-0030 п. 5): перед изменением сохраняется
// копия config.yaml.v<N>.bak, затем файл переводится шагами vN → vN+1. Комментарии и порядок
// ключей сохраняются (работаем с деревом YAML).

// Step переводит дерево настроек (корневую карту) с версии N на N+1.
type Step func(root *yaml.Node) error

// migrations — шаги по исходной версии: migrations[1] переводит v1 → v2. Пока схема одна (v1),
// шагов нет; новый шаг добавляется вместе с увеличением CurrentVersion.
var migrations = map[int]Step{}

// Migrate переводит файл настроек path на текущую версию схемы. Возвращает путь резервной копии
// ("" — переводить было нечего: файла нет или версия текущая). Файл новее программы — ошибка.
func Migrate(path string) (string, error) {
	return migrate(path, CurrentVersion, migrations)
}

// migrate — Migrate с явными текущей версией и шагами (для тестов).
func migrate(path string, current int, steps map[int]Step) (string, error) {
	// Файл и его версия (нет ключа version — 1).
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	doc, err := parseNode(data)
	if err != nil {
		return "", err
	}
	root := doc.Content[0]
	ver := 1
	if _, v := lookup(root, "version"); v != nil {
		if ver, err = strconv.Atoi(v.Value); err != nil {
			return "", fmt.Errorf("config: version %q is not a number", v.Value)
		}
	}
	switch {
	case ver == current:
		return "", nil
	case ver > current:
		return "", fmt.Errorf("config: version %d is newer than supported %d", ver, current)
	}

	// Резервная копия — до любых изменений.
	backup := path + ".v" + strconv.Itoa(ver) + ".bak"
	if err := writeAtomic(backup, data); err != nil {
		return "", fmt.Errorf("config: backup: %w", err)
	}

	// Шаги до текущей версии.
	for v := ver; v < current; v++ {
		step, ok := steps[v]
		if !ok {
			return backup, fmt.Errorf("config: no migration from version %d", v)
		}
		if err := step(root); err != nil {
			return backup, fmt.Errorf("config: migrate v%d: %w", v, err)
		}
	}
	if err := setValue(root, "version", current); err != nil {
		return backup, err
	}

	// Запись: результат должен разбираться строго.
	out, err := encodeNode(doc)
	if err != nil {
		return backup, err
	}
	if _, err := parseUpTo(out, current); err != nil {
		return backup, err
	}
	return backup, writeAtomic(path, out)
}
