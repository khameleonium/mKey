package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"mkey/internal/lib/project"
)

// varStore — переменные одного проекта (contracts.VarStore, FR-EV-6).
type varStore struct {
	// mu защищает values.
	mu sync.Mutex
	// decl — объявления переменных из проекта (тип, начальное значение, сохранение).
	decl map[string]project.Variable
	// values — текущие значения.
	values map[string]any
	// onPersist вызывается после изменения сохраняемой переменной.
	onPersist func()
}

// newVarStore создаёт переменные проекта: сохранённые значения и значения прежней версии проекта
// (при горячей перезагрузке) важнее начальных, если тип не изменился.
func newVarStore(decl map[string]project.Variable, previous, saved map[string]any, onPersist func()) (*varStore, error) {
	vs := &varStore{decl: decl, values: map[string]any{}, onPersist: onPersist}
	for name, d := range decl {
		// Начальное значение по объявленному типу.
		v, err := convert(d.Type, d.Value)
		if err != nil {
			return nil, fmt.Errorf("variable %q: %w", name, err)
		}

		// Сохранённое значение (только для persist) и значение прежней версии проекта того же типа.
		if old, ok := saved[name]; ok && d.Persist {
			if cv, err := convert(d.Type, old); err == nil {
				v = cv
			}
		}
		if old, ok := previous[name]; ok {
			if cv, err := convert(d.Type, old); err == nil {
				v = cv
			}
		}
		vs.values[name] = v
	}
	return vs, nil
}

// Get возвращает значение переменной.
func (s *varStore) Get(name string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[name]
	return v, ok
}

// Set задаёт значение; для объявленной переменной приводит его к её типу.
func (s *varStore) Set(name string, value any) error {
	// Объявленная переменная — приводим к типу; необъявленная — храним как есть.
	s.mu.Lock()
	d, declared := s.decl[name]
	if declared {
		v, err := convert(d.Type, value)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("variable %q: %w", name, err)
		}
		value = v
	}
	s.values[name] = value
	persist := declared && d.Persist
	s.mu.Unlock()

	// Сохраняемые переменные записываются сразу.
	if persist && s.onPersist != nil {
		s.onPersist()
	}
	return nil
}

// Add прибавляет delta к числовой переменной (необъявленная начинается с 0).
func (s *varStore) Add(name string, delta float64) error {
	s.mu.Lock()
	cur, ok := s.values[name]
	s.mu.Unlock()
	if !ok {
		cur = 0.0
	}
	f, err := toFloat(cur)
	if err != nil {
		return fmt.Errorf("variable %q is not a number", name)
	}
	return s.Set(name, f+delta)
}

// All возвращает копию всех переменных.
func (s *varStore) All() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.values)
}

// persisted возвращает значения переменных, объявленных с persist: true.
func (s *varStore) persisted() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{}
	for name, d := range s.decl {
		if d.Persist {
			out[name] = s.values[name]
		}
	}
	return out
}

// convert приводит значение к типу переменной: int, float, string, bool.
func convert(typ string, v any) (any, error) {
	switch typ {
	case "int":
		f, err := toFloat(v)
		if err != nil {
			return nil, err
		}
		return int64(f), nil
	case "float":
		return toFloat(v)
	case "string":
		if v == nil {
			return "", nil
		}
		return fmt.Sprint(v), nil
	case "bool":
		switch b := v.(type) {
		case bool:
			return b, nil
		case nil:
			return false, nil
		case string:
			return strconv.ParseBool(b)
		}
		return nil, fmt.Errorf("%v is not a boolean", v)
	}
	return nil, fmt.Errorf("unknown type %q", typ)
}

// toFloat переводит число или строку с числом в float64.
func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float64:
		return n, nil
	case string:
		return strconv.ParseFloat(n, 64)
	}
	return 0, fmt.Errorf("%v is not a number", v)
}

// persistFile — файл сохранённых переменных всех проектов: проект → имя → значение.
type persistFile struct {
	mu   sync.Mutex
	path string
}

// load читает сохранённые переменные (отсутствующий файл — пустой набор).
func (p *persistFile) load() map[string]map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]map[string]any{}
	data, err := os.ReadFile(p.path)
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

// save записывает сохраняемые переменные проектов.
func (p *persistFile) save(all map[string]map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}
