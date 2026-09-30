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

// menu — плоское меню значка. ID пункта — его номер в списке плюс 1 (0 — корень).
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
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(it.Label),
		"enabled": dbus.MakeVariant(!it.Disabled),
	}
}

// snapshot возвращает копию пунктов и номер версии меню.
func (m *menu) snapshot() ([]MenuItem, uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MenuItem(nil), m.items...), m.revision
}

// buildLayout строит дерево меню: корень (ID 0) с пунктами-детьми.
func buildLayout(items []MenuItem) layout {
	root := layout{ID: 0, Props: map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}}
	for i, it := range items {
		root.Children = append(root.Children, dbus.MakeVariant(layout{ID: int32(i + 1), Props: it.props(), Children: []dbus.Variant{}}))
	}
	return root
}

// menuObject — методы протокола dbusmenu. Отдельный тип, чтобы на шину попали только они.
type menuObject struct{ m *menu }

// GetLayout возвращает дерево меню (глубина и фильтр свойств не нужны для плоского меню).
func (o *menuObject) GetLayout(parentID, _ int32, _ []string) (uint32, layout, *dbus.Error) {
	items, rev := o.m.snapshot()
	root := buildLayout(items)
	if parentID == 0 {
		return rev, root, nil
	}
	// Запрос поддерева пункта: у пунктов нет детей.
	if parentID > 0 && int(parentID) <= len(items) {
		return rev, layout{ID: parentID, Props: items[parentID-1].props(), Children: []dbus.Variant{}}, nil
	}
	return rev, root, dbus.MakeFailedError(fmt.Errorf("unknown menu item %d", parentID))
}

// GetGroupProperties возвращает свойства нескольких пунктов (пустой список — всех).
func (o *menuObject) GetGroupProperties(ids []int32, _ []string) ([]itemProps, *dbus.Error) {
	items, _ := o.m.snapshot()
	var out []itemProps
	for i, it := range items {
		id := int32(i + 1)
		if len(ids) == 0 || containsID(ids, id) {
			out = append(out, itemProps{ID: id, Props: it.props()})
		}
	}
	return out, nil
}

// GetProperty возвращает одно свойство пункта.
func (o *menuObject) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	items, _ := o.m.snapshot()
	if id >= 1 && int(id) <= len(items) {
		if v, ok := items[id-1].props()[name]; ok {
			return v, nil
		}
	}
	return dbus.MakeVariant(""), dbus.MakeFailedError(fmt.Errorf("no property %q for item %d", name, id))
}

// Event обрабатывает действие с пунктом: "clicked" вызывает OnClick.
func (o *menuObject) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	items, _ := o.m.snapshot()
	if id >= 1 && int(id) <= len(items) {
		if it := items[id-1]; !it.Disabled && it.OnClick != nil {
			go it.OnClick()
		}
	}
	return nil
}

// EventGroup обрабатывает несколько действий; возвращает ID неизвестных пунктов.
func (o *menuObject) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	items, _ := o.m.snapshot()
	var bad []int32
	for _, e := range events {
		if e.ID < 1 || int(e.ID) > len(items) {
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
