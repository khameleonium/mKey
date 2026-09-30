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
}

// NewExtensions создаёт реестр, в котором заранее заведены все известные точки расширения.
func NewExtensions() *Extensions {
	// Заводим пустую карту для каждой известной точки: регистрация в неизвестную точку — ошибка.
	e := &Extensions{points: make(map[contracts.ExtensionPoint]map[string]contracts.Extension)}
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
	return nil
}

// Get возвращает расширение по ID в точке point.
func (e *Extensions) Get(point contracts.ExtensionPoint, id string) (contracts.Extension, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ext, ok := e.points[point][id]
	return ext, ok
}

// List возвращает все расширения точки point, отсортированные по ID.
func (e *Extensions) List(point contracts.ExtensionPoint) []contracts.Extension {
	// Копируем расширения точки под блокировкой.
	e.mu.RLock()
	items := make([]contracts.Extension, 0, len(e.points[point]))
	for _, ext := range e.points[point] {
		items = append(items, ext)
	}
	e.mu.RUnlock()

	// Сортируем по ID, чтобы порядок был стабильным (для GUI и тестов).
	slices.SortFunc(items, func(a, b contracts.Extension) int {
		return strings.Compare(a.Meta().ID, b.Meta().ID)
	})
	return items
}

// Проверка на этапе компиляции, что Extensions реализует контракт.
var _ contracts.ExtensionRegistry = (*Extensions)(nil)
