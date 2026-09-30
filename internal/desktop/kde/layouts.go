// Package kde — адаптер KDE Plasma для модуля desktop.
//
// Раскладки клавиатуры читаются и переключаются через D-Bus-интерфейс KWin
// org.kde.KeyboardLayouts (сервис org.kde.keyboard, объект /Layouts): методы
// getLayoutsList() → a(sss), getLayout() → u, setLayout(u) → b.
// Интерфейс проверен на Plasma 6 (Linux Mint 22.3, KDE Wayland).
package kde

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"

	"mkey/internal/contracts"
)

// Адрес интерфейса раскладок KWin.
const (
	service   = "org.kde.keyboard"
	path      = "/Layouts"
	iface     = "org.kde.KeyboardLayouts"
	sourceKDE = "kde"
)

// LayoutName — описание раскладки, как его отдаёт KWin: короткое имя, вариант, полное имя.
type LayoutName struct {
	Short   string
	Variant string
	Long    string
}

// client — вызовы D-Bus, нужные адаптеру (в тестах подменяются).
type client interface {
	// list возвращает список раскладок.
	list(ctx context.Context) ([]LayoutName, error)
	// current возвращает индекс текущей раскладки.
	current(ctx context.Context) (uint32, error)
	// set переключает раскладку по индексу.
	set(ctx context.Context, index uint32) (bool, error)
}

// Layouts — contracts.LayoutProvider для KDE Plasma.
type Layouts struct {
	c client
}

// New создаёт адаптер поверх подключения к сессионной шине D-Bus.
func New(conn *dbus.Conn) *Layouts {
	return &Layouts{c: dbusClient{obj: conn.Object(service, path)}}
}

// Layouts возвращает текущую и доступные раскладки.
func (l *Layouts) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	// Список раскладок и индекс текущей.
	names, err := l.c.list(ctx)
	if err != nil {
		return contracts.LayoutInfo{}, fmt.Errorf("kde: get layouts: %w", err)
	}
	idx, err := l.c.current(ctx)
	if err != nil {
		return contracts.LayoutInfo{}, fmt.Errorf("kde: get current layout: %w", err)
	}
	if int(idx) >= len(names) {
		return contracts.LayoutInfo{}, fmt.Errorf("kde: current layout %d out of %d", idx, len(names))
	}

	// Собираем сведения; переключать KDE умеет.
	info := contracts.LayoutInfo{Current: names[idx].Short, CanSwitch: true, Source: sourceKDE}
	for _, n := range names {
		info.Available = append(info.Available, n.Short)
	}
	return info, nil
}

// Switch переключает раскладку на первую с коротким именем name.
func (l *Layouts) Switch(ctx context.Context, name string) error {
	// Находим индекс раскладки.
	names, err := l.c.list(ctx)
	if err != nil {
		return fmt.Errorf("kde: get layouts: %w", err)
	}
	for i, n := range names {
		if n.Short != name {
			continue
		}

		// Переключаем; KWin возвращает false, если переключение не удалось.
		ok, err := l.c.set(ctx, uint32(i))
		if err != nil {
			return fmt.Errorf("kde: set layout: %w", err)
		}
		if !ok {
			return errors.New("kde: layout switch rejected")
		}
		return nil
	}
	return fmt.Errorf("kde: layout %q is not configured", name)
}

// dbusClient — настоящие вызовы D-Bus.
type dbusClient struct {
	obj dbus.BusObject
}

// list вызывает getLayoutsList.
func (d dbusClient) list(ctx context.Context) ([]LayoutName, error) {
	var out []LayoutName
	err := d.obj.CallWithContext(ctx, iface+".getLayoutsList", 0).Store(&out)
	return out, err
}

// current вызывает getLayout.
func (d dbusClient) current(ctx context.Context) (uint32, error) {
	var idx uint32
	err := d.obj.CallWithContext(ctx, iface+".getLayout", 0).Store(&idx)
	return idx, err
}

// set вызывает setLayout.
func (d dbusClient) set(ctx context.Context, index uint32) (bool, error) {
	var ok bool
	err := d.obj.CallWithContext(ctx, iface+".setLayout", 0, index).Store(&ok)
	return ok, err
}

// Проверка на этапе компиляции, что Layouts реализует контракт.
var _ contracts.LayoutProvider = (*Layouts)(nil)
