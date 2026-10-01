package sni

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// Имена протокола com.canonical.dbusmenu.
const (
	menuIface = "com.canonical.dbusmenu"
	menuPath  = dbus.ObjectPath("/MenuBar")
)

// MenuItem — пункт меню значка.
type MenuItem struct {
	// Label — текст пункта; пусто вместе с Separator — разделитель.
	Label string
	// Separator — пункт является разделительной линией.
	Separator bool
	// Disabled — пункт виден, но не нажимается.
	Disabled bool
	// Checkable — пункт с галочкой (включён/выключен); Checked — галочка стоит.
	Checkable bool
	Checked   bool
	// Children — вложенное меню (подменю); у такого пункта OnClick не вызывается.
	Children []MenuItem
	// OnClick вызывается при выборе пункта (в отдельной горутине).
	OnClick func()
}

// layout — элемент дерева меню в формате протокола: (ia{sv}av).
type layout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

// itemProps — свойства одного пункта для GetGroupProperties: (ia{sv}).
type itemProps struct {
	ID    int32
	Props map[string]dbus.Variant
}

// menuEvent — событие пункта для EventGroup: (isvu).
type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

// menu — меню значка (с подменю). ID пунктов — номера при обходе дерева в глубину, начиная с 1
// (0 — корень); после замены пунктов (set) номера считаются заново, панель перечитывает меню.
type menu struct {
	conn *dbus.Conn
	// mu защищает items и revision; revision растёт при каждой смене пунктов.
	mu       sync.Mutex
	items    []MenuItem
	revision uint32
}

// newMenu создаёт меню с пунктами items.
func newMenu(conn *dbus.Conn, items []MenuItem) *menu {
	return &menu{conn: conn, items: items, revision: 1}
}

// export публикует объект меню: методы, свойства протокола и описание.
func (m *menu) export() error {
	// Методы протокола.
	obj := &menuObject{m: m}
	if err := m.conn.Export(obj, menuPath, menuIface); err != nil {
		return fmt.Errorf("sni: export menu: %w", err)
	}

	// Свойства протокола: версия 3, направление текста, обычный статус.
	props, err := prop.Export(m.conn, menuPath, map[string]map[string]*prop.Prop{
		menuIface: {
			"Version":       {Value: uint32(3), Emit: prop.EmitFalse},
			"TextDirection": {Value: "ltr", Emit: prop.EmitFalse},
			"Status":        {Value: "normal", Emit: prop.EmitFalse},
			"IconThemePath": {Value: []string{}, Emit: prop.EmitFalse},
		},
	})
	if err != nil {
		return fmt.Errorf("sni: export menu properties: %w", err)
	}

	// Описание объекта (его запрашивают, например, GNOME AppIndicator и waybar).
	node := &introspect.Node{
		Name: string(menuPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       menuIface,
				Methods:    introspect.Methods(obj),
				Properties: props.Introspection(menuIface),
				Signals: []introspect.Signal{
					{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}},
					{Name: "ItemsPropertiesUpdated", Args: []introspect.Arg{{Name: "updatedProps", Type: "a(ia{sv})"}, {Name: "removedProps", Type: "a(ias)"}}},
				},
			},
		},
	}
	if err := m.conn.Export(introspect.NewIntrospectable(node), menuPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("sni: export menu introspection: %w", err)
	}
	return nil
}

// set заменяет пункты и сообщает панели, что меню изменилось.
func (m *menu) set(items []MenuItem) {
	m.mu.Lock()
	m.items = items
	m.revision++
	rev := m.revision
	m.mu.Unlock()
	_ = m.conn.Emit(menuPath, menuIface+".LayoutUpdated", rev, int32(0))
}

// props возвращает свойства пункта в формате протокола.
func (it MenuItem) props() map[string]dbus.Variant {
	if it.Separator {
		return map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}
	}
	p := map[string]dbus.Variant{
		"label":   dbus.MakeVariant(it.Label),
		"enabled": dbus.MakeVariant(!it.Disabled),
	}
	if len(it.Children) > 0 {
		p["children-display"] = dbus.MakeVariant("submenu")
	}
	if it.Checkable {
		state := int32(0)
		if it.Checked {
			state = 1
		}
		p["toggle-type"] = dbus.MakeVariant("checkmark")
		p["toggle-state"] = dbus.MakeVariant(state)
	}
	return p
}

// tree — пункты меню по ID (обход в глубину с 1) и дети каждого (0 — корень).
type tree struct {
	items    map[int32]MenuItem
	children map[int32][]int32
}

// flatten нумерует пункты дерева в глубину: пункт, затем его подменю.
func flatten(items []MenuItem) tree {
	t := tree{items: map[int32]MenuItem{}, children: map[int32][]int32{}}
	next := int32(1)
	var walk func(parent int32, list []MenuItem)
	walk = func(parent int32, list []MenuItem) {
		for _, it := range list {
			id := next
			next++
			t.items[id] = it
			t.children[parent] = append(t.children[parent], id)
			walk(id, it.Children)
		}
	}
	walk(0, items)
	return t
}

// snapshot возвращает пронумерованные пункты и номер версии меню.
func (m *menu) snapshot() (tree, uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return flatten(m.items), m.revision
}

// buildLayout строит дерево меню от пункта id (0 — корень) со всеми вложенными пунктами.
func (t tree) buildLayout(id int32) layout {
	props := map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}
	if id != 0 {
		props = t.items[id].props()
	}
	l := layout{ID: id, Props: props, Children: []dbus.Variant{}}
	for _, c := range t.children[id] {
		l.Children = append(l.Children, dbus.MakeVariant(t.buildLayout(c)))
	}
	return l
}

// menuObject — методы протокола dbusmenu. Отдельный тип, чтобы на шину попали только они.
type menuObject struct{ m *menu }

// GetLayout возвращает дерево меню от пункта parentID (0 — корень) со всеми вложенными пунктами
// (глубину и фильтр свойств панели не ограничиваем: меню небольшое).
func (o *menuObject) GetLayout(parentID, _ int32, _ []string) (uint32, layout, *dbus.Error) {
	t, rev := o.m.snapshot()
	if _, ok := t.items[parentID]; parentID != 0 && !ok {
		return rev, t.buildLayout(0), dbus.MakeFailedError(fmt.Errorf("unknown menu item %d", parentID))
	}
	return rev, t.buildLayout(parentID), nil
}

// GetGroupProperties возвращает свойства нескольких пунктов (пустой список — всех).
func (o *menuObject) GetGroupProperties(ids []int32, _ []string) ([]itemProps, *dbus.Error) {
	t, _ := o.m.snapshot()
	var out []itemProps
	for id := int32(1); int(id) <= len(t.items); id++ {
		if len(ids) == 0 || containsID(ids, id) {
			out = append(out, itemProps{ID: id, Props: t.items[id].props()})
		}
	}
	return out, nil
}

// GetProperty возвращает одно свойство пункта.
func (o *menuObject) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	t, _ := o.m.snapshot()
	if it, ok := t.items[id]; ok {
		if v, ok := it.props()[name]; ok {
			return v, nil
		}
	}
	return dbus.MakeVariant(""), dbus.MakeFailedError(fmt.Errorf("no property %q for item %d", name, id))
}

// Event обрабатывает действие с пунктом: "clicked" вызывает OnClick (у подменю — нет).
func (o *menuObject) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	t, _ := o.m.snapshot()
	if it, ok := t.items[id]; ok && !it.Disabled && it.OnClick != nil && len(it.Children) == 0 {
		go it.OnClick()
	}
	return nil
}

// EventGroup обрабатывает несколько действий; возвращает ID неизвестных пунктов.
func (o *menuObject) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	t, _ := o.m.snapshot()
	var bad []int32
	for _, e := range events {
		if _, ok := t.items[e.ID]; !ok {
			bad = append(bad, e.ID)
			continue
		}
		_ = o.Event(e.ID, e.EventID, e.Data, e.Timestamp)
	}
	return bad, nil
}

// AboutToShow — меню скоро откроется; обновлять ничего не нужно.
func (o *menuObject) AboutToShow(_ int32) (bool, *dbus.Error) { return false, nil }

// AboutToShowGroup — то же для нескольких пунктов.
func (o *menuObject) AboutToShowGroup(_ []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

// containsID сообщает, есть ли id в списке ids.
func containsID(ids []int32, id int32) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
