package registry

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"mkey/internal/contracts"
)

// Extensions — потокобезопасная реализация contracts.ExtensionRegistry.
type Extensions struct {
	// mu защищает карту точек расширения.
	mu sync.RWMutex
	// points — расширения по точкам, внутри точки — по ID.
	points map[contracts.ExtensionPoint]map[string]contracts.Extension
	// order — ID расширений каждой точки в порядке регистрации.
	order map[contracts.ExtensionPoint][]string
}

// NewExtensions создаёт реестр, в котором заранее заведены все известные точки расширения.
func NewExtensions() *Extensions {
	// Заводим пустую карту для каждой известной точки: регистрация в неизвестную точку — ошибка.
	e := &Extensions{
		points: make(map[contracts.ExtensionPoint]map[string]contracts.Extension),
		order:  make(map[contracts.ExtensionPoint][]string),
	}
	for _, p := range contracts.AllExtensionPoints() {
		e.points[p] = make(map[string]contracts.Extension)
	}
	return e
}

// Register добавляет расширение ext в точку point.
// Возвращает ошибку для неизвестной точки, пустого ID или повторного ID.
func (e *Extensions) Register(point contracts.ExtensionPoint, ext contracts.Extension) error {
	// Проверяем метаданные расширения до захвата блокировки.
	if ext == nil {
		return fmt.Errorf("register %s: nil extension", point)
	}
	meta := ext.Meta()
	if strings.TrimSpace(meta.ID) == "" {
		return fmt.Errorf("register %s: empty extension id", point)
	}

	// Добавляем расширение в точку, если точка существует и ID свободен.
	e.mu.Lock()
	defer e.mu.Unlock()
	items, ok := e.points[point]
	if !ok {
		return fmt.Errorf("register %q: %w", point, contracts.ErrUnknownExtensionPoint)
	}
	if _, exists := items[meta.ID]; exists {
		return fmt.Errorf("register %s/%s: %w", point, meta.ID, contracts.ErrExtensionExists)
	}
	items[meta.ID] = ext
	e.order[point] = append(e.order[point], meta.ID)
	return nil
}

// Get возвращает расширение по ID в точке point.
func (e *Extensions) Get(point contracts.ExtensionPoint, id string) (contracts.Extension, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ext, ok := e.points[point][id]
	return ext, ok
}

// List возвращает все расширения точки point в порядке регистрации (порядок стабилен:
// модули запускаются в заданном порядке и регистрируют виды по списку).
func (e *Extensions) List(point contracts.ExtensionPoint) []contracts.Extension {
	e.mu.RLock()
	defer e.mu.RUnlock()
	items := make([]contracts.Extension, 0, len(e.order[point]))
	for _, id := range e.order[point] {
		items = append(items, e.points[point][id])
	}
	return items
}

// Проверка на этапе компиляции, что Extensions реализует контракт.
var _ contracts.ExtensionRegistry = (*Extensions)(nil)

// Unregister убирает расширение id из точки point; false — такого не было.
func (e *Extensions) Unregister(point contracts.ExtensionPoint, id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.points[point][id]; !ok {
		return false
	}
	delete(e.points[point], id)
	e.order[point] = slices.DeleteFunc(e.order[point], func(x string) bool { return x == id })
	return true
}
