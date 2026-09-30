package contracts

import (
	"errors"
	"reflect"
)

// ErrServiceNotFound возвращается, когда запрошенный сервис никто не предоставил
// (например, модуль-поставщик отключён в конфиге).
var ErrServiceNotFound = errors.New("service not found")

// ErrServiceExists возвращается при повторной регистрации сервиса того же типа.
var ErrServiceExists = errors.New("service already registered")

// ErrUnsupported возвращается, когда возможность недоступна в текущем окружении
// (например, чтение пикселя в композиторе без нужного протокола).
// Вызывающий код обязан показать пользователю понятное объяснение, а не молчать.
var ErrUnsupported = errors.New("not supported in this environment")

// ServiceRegistry — реестр сервисов, через который модули получают реализации контрактов.
//
// Ключ сервиса — тип интерфейса контракта. Напрямую этим интерфейсом обычно
// не пользуются: удобнее обобщённые функции ProvideService и LookupService.
type ServiceRegistry interface {
	// Provide регистрирует реализацию svc под ключом key (тип контракта).
	Provide(key reflect.Type, svc any) error
	// Lookup возвращает реализацию, зарегистрированную под ключом key.
	Lookup(key reflect.Type) (any, bool)
}

// ProvideService регистрирует svc как реализацию контракта T.
func ProvideService[T any](r ServiceRegistry, svc T) error {
	return r.Provide(reflect.TypeFor[T](), svc)
}

// LookupService возвращает реализацию контракта T или ErrServiceNotFound.
func LookupService[T any](r ServiceRegistry) (T, error) {
	// Ищем сервис по типу контракта.
	var zero T
	v, ok := r.Lookup(reflect.TypeFor[T]())
	if !ok {
		return zero, ErrServiceNotFound
	}

	// Приводим найденное значение к типу контракта; несовпадение — ошибка регистрации.
	svc, ok := v.(T)
	if !ok {
		return zero, ErrServiceNotFound
	}
	return svc, nil
}
