package registry

import (
	"fmt"
	"reflect"
	"sync"

	"mkey/internal/contracts"
)

// Services — потокобезопасная реализация contracts.ServiceRegistry.
type Services struct {
	// mu защищает карту сервисов.
	mu sync.RWMutex
	// items — зарегистрированные сервисы по типу контракта.
	items map[reflect.Type]any
}

// NewServices создаёт пустой реестр сервисов.
func NewServices() *Services {
	return &Services{items: make(map[reflect.Type]any)}
}

// Provide регистрирует реализацию svc под ключом key.
// Возвращает ошибку, если ключ пустой, svc не реализует контракт или ключ уже занят.
func (s *Services) Provide(key reflect.Type, svc any) error {
	// Проверяем корректность аргументов: ключ задан, значение реализует интерфейс контракта.
	if key == nil || svc == nil {
		return fmt.Errorf("provide service: nil key or service")
	}
	if key.Kind() == reflect.Interface && !reflect.TypeOf(svc).Implements(key) {
		return fmt.Errorf("provide service %s: %T does not implement it", key, svc)
	}

	// Регистрируем сервис, не допуская повторной регистрации одного контракта.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[key]; exists {
		return fmt.Errorf("provide service %s: %w", key, contracts.ErrServiceExists)
	}
	s.items[key] = svc
	return nil
}

// Lookup возвращает сервис, зарегистрированный под ключом key.
func (s *Services) Lookup(key reflect.Type) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.items[key]
	return v, ok
}

// Проверка на этапе компиляции, что Services реализует контракт.
var _ contracts.ServiceRegistry = (*Services)(nil)
