package sni

import (
	"image"
	"image/color"
	"testing"

	"github.com/godbus/dbus/v5"
)

// TestPixmapFromImage проверяет перевод картинки в ARGB32 в сетевом порядке байт.
func TestPixmapFromImage(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff})
	img.Set(1, 0, color.NRGBA{R: 0xff, A: 0x80})
	p := PixmapFromImage(img)
	if p.W != 2 || p.H != 1 || len(p.Data) != 8 {
		t.Fatalf("size: %+v", p)
	}
	if got := p.Data[:4]; got[0] != 0xff || got[1] != 0x11 || got[2] != 0x22 || got[3] != 0x33 {
		t.Fatalf("opaque pixel = %x", got)
	}
	// Полупрозрачный пиксель: цвет без предумножения на альфу.
	if got := p.Data[4:]; got[0] != 0x80 || got[1] != 0xff || got[2] != 0 {
		t.Fatalf("translucent pixel = %x", got)
	}
}

// TestMenuLayout проверяет дерево меню, свойства пунктов и обработку щелчков.
func TestMenuLayout(t *testing.T) {
	t.Parallel()
	clicked := make(chan int, 2)
	m := newMenu(nil, []MenuItem{
		{Label: "Open", OnClick: func() { clicked <- 1 }},
		{Separator: true},
		{Label: "Off", Disabled: true, OnClick: func() { clicked <- 3 }},
	})
	o := &menuObject{m: m}

	// Корень с тремя детьми; второй — разделитель, третий — выключен.
	rev, root, derr := o.GetLayout(0, -1, nil)
	if derr != nil || rev != 1 || root.ID != 0 || len(root.Children) != 3 {
		t.Fatalf("layout: rev=%d %+v %v", rev, root, derr)
	}
	sep := root.Children[1].Value().(layout)
	if sep.ID != 2 || sep.Props["type"].Value() != "separator" {
		t.Fatalf("separator: %+v", sep)
	}
	if v, _ := o.GetProperty(3, "enabled"); v.Value() != false {
		t.Fatalf("disabled item enabled = %v", v)
	}
	if props, _ := o.GetGroupProperties([]int32{1}, nil); len(props) != 1 || props[0].Props["label"].Value() != "Open" {
		t.Fatalf("group props: %+v", props)
	}

	// Щелчок по обычному пункту вызывает обработчик, по выключенному — нет; неизвестный пункт — в списке ошибок.
	bad, _ := o.EventGroup([]menuEvent{{ID: 3, EventID: "clicked"}, {ID: 1, EventID: "clicked", Data: dbus.MakeVariant(0)}, {ID: 9, EventID: "clicked"}})
	if len(bad) != 1 || bad[0] != 9 {
		t.Fatalf("bad ids: %v", bad)
	}
	if got := <-clicked; got != 1 {
		t.Fatalf("clicked %d", got)
	}
	select {
	case got := <-clicked:
		t.Fatalf("disabled item clicked: %d", got)
	default:
	}
}

// TestSubmenu проверяет подменю и пункты с галочкой: нумерацию в глубину, свойства, поддерево,
// щелчок по вложенному пункту и то, что щелчок по самому подменю ничего не вызывает.
func TestSubmenu(t *testing.T) {
	t.Parallel()
	clicked := make(chan string, 4)
	m := newMenu(nil, []MenuItem{
		{Label: "Projects", OnClick: func() { clicked <- "projects" }, Children: []MenuItem{
			{Label: "A", Checkable: true, Checked: true, OnClick: func() { clicked <- "A" }},
			{Label: "B", Checkable: true},
		}},
		{Label: "Quit", OnClick: func() { clicked <- "quit" }},
	})
	o := &menuObject{m: m}

	// Нумерация: Projects=1, A=2, B=3, Quit=4; у подменю — children-display, у пунктов — галочки.
	_, root, _ := o.GetLayout(0, -1, nil)
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d", len(root.Children))
	}
	sub := root.Children[0].Value().(layout)
	if sub.ID != 1 || sub.Props["children-display"].Value() != "submenu" || len(sub.Children) != 2 {
		t.Fatalf("submenu = %+v", sub)
	}
	a := sub.Children[0].Value().(layout)
	b := sub.Children[1].Value().(layout)
	if a.ID != 2 || a.Props["toggle-type"].Value() != "checkmark" || a.Props["toggle-state"].Value() != int32(1) || b.Props["toggle-state"].Value() != int32(0) {
		t.Errorf("checkmarks: %+v %+v", a.Props, b.Props)
	}
	if quit := root.Children[1].Value().(layout); quit.ID != 4 {
		t.Errorf("quit id = %d", quit.ID)
	}

	// Поддерево пункта и свойства всех пунктов.
	if _, l, err := o.GetLayout(1, -1, nil); err != nil || len(l.Children) != 2 {
		t.Errorf("subtree: %+v %v", l, err)
	}
	if props, _ := o.GetGroupProperties(nil, nil); len(props) != 4 {
		t.Errorf("group properties = %d", len(props))
	}

	// Щелчок по вложенному пункту вызывает его; по подменю — ничего.
	_ = o.Event(1, "clicked", dbus.MakeVariant(0), 0)
	_ = o.Event(2, "clicked", dbus.MakeVariant(0), 0)
	if got := <-clicked; got != "A" {
		t.Errorf("clicked %q, want A", got)
	}
	select {
	case got := <-clicked:
		t.Errorf("unexpected click %q", got)
	default:
	}
}

// TestMenuIDsAfterUpdate проверяет, что после обновления меню номера пунктов новые: подменю,
// появившееся на месте обычного пункта, получает номер, которого панель ещё не видела, а щелчок
// по номеру из старого меню не попадает в пункт нового.
func TestMenuIDsAfterUpdate(t *testing.T) {
	t.Parallel()
	clicked := make(chan string, 4)
	m := newMenu(nil, []MenuItem{
		{Label: "Record"},
		{Label: "Stop all", OnClick: func() { clicked <- "stop" }},
	})
	o := &menuObject{m: m}

	// Было: Record=1, Stop all=2. Стало: Record, подменю Replay (с записью), Stop all —
	// прежние пункты сохраняют номера, новое подменю получает новый.
	m.set([]MenuItem{
		{Label: "Record"},
		{Label: "Replay", Children: []MenuItem{{Label: "rec1", OnClick: func() { clicked <- "rec1" }}}},
		{Label: "Stop all", OnClick: func() { clicked <- "stop" }},
	})
	_, root, _ := o.GetLayout(0, -1, nil)
	replay := root.Children[1].Value().(layout)
	if replay.ID <= 2 || replay.Props["children-display"].Value() != "submenu" ||
		root.Children[0].Value().(layout).ID != 1 || root.Children[2].Value().(layout).ID != 2 {
		t.Fatalf("root = %+v", root)
	}

	// Пункт «Record» стал подменю — номер новый (вид пункта по номеру не меняется).
	m.set([]MenuItem{{Label: "Record", Children: []MenuItem{{Label: "x"}}}})
	if _, r2, _ := o.GetLayout(0, -1, nil); r2.Children[0].Value().(layout).ID == 1 {
		t.Fatal("submenu reused the id of a plain item")
	}
	m.set([]MenuItem{
		{Label: "Record"},
		{Label: "Replay", Children: []MenuItem{{Label: "rec1", OnClick: func() { clicked <- "rec1" }}}},
		{Label: "Stop all", OnClick: func() { clicked <- "stop" }},
	})

	// Щелчок по записи в подменю — запускает её (номер подменю и записи прежние).
	_, root, _ = o.GetLayout(0, -1, nil)
	replay = root.Children[1].Value().(layout)
	rec := replay.Children[0].Value().(layout)
	_ = o.Event(rec.ID, "clicked", dbus.MakeVariant(0), 0)
	if got := <-clicked; got != "rec1" {
		t.Fatalf("clicked %q, want rec1", got)
	}
	if props, _ := o.GetGroupProperties(nil, nil); len(props) != 4 {
		t.Errorf("group properties = %d", len(props))
	}
}

// TestSetSameMenu: те же пункты — номер ревизии прежний (панели не сообщается), но новые
// обработчики щелчков действуют.
func TestSetSameMenu(t *testing.T) {
	t.Parallel()
	clicked := make(chan string, 2)
	m := newMenu(nil, []MenuItem{{Label: "Go", OnClick: func() { clicked <- "old" }}})
	o := &menuObject{m: m}
	rev, _, _ := o.GetLayout(0, -1, nil)
	m.set([]MenuItem{{Label: "Go", OnClick: func() { clicked <- "new" }}})
	if again, _, _ := o.GetLayout(0, -1, nil); again != rev {
		t.Fatalf("revision %d → %d for the same menu", rev, again)
	}
	_ = o.Event(1, "clicked", dbus.MakeVariant(0), 0)
	if got := <-clicked; got != "new" {
		t.Fatalf("clicked %q, want new handler", got)
	}
	m.set([]MenuItem{{Label: "Go", Checkable: true, Checked: true}})
	if again, _, _ := o.GetLayout(0, -1, nil); again == rev {
		t.Fatal("changed menu kept its revision")
	}
}

// TestAboutToShow проверяет обновление меню перед открытием меню или подменю: пункты те же — меню
// не перестраивается; изменились (запись удалили) — меню заменяется, панель просят перечитать
// его, а оставшиеся пункты сохраняют номера (уже открытое подменю остаётся верным).
func TestAboutToShow(t *testing.T) {
	t.Parallel()
	recs := []string{"rec1", "rec2"}
	build := func() []MenuItem {
		sub := MenuItem{Label: "Replay"}
		for _, r := range recs {
			sub.Children = append(sub.Children, MenuItem{Label: r, OnClick: func() {}})
		}
		return []MenuItem{{Label: "Record"}, sub}
	}
	m := newMenu(nil, build())
	m.source = build
	o := &menuObject{m: m}

	// Ничего не изменилось — прежнее меню.
	if changed, _ := o.AboutToShow(0); changed {
		t.Fatal("unchanged menu rebuilt")
	}
	if _, rev := m.snapshot(); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}

	// Запись удалили: перед открытием подменю (номер 2) — новое меню; номера подменю и rec2 прежние.
	_, before, _ := o.GetLayout(0, -1, nil)
	oldReplay := before.Children[1].Value().(layout)
	oldRec2 := oldReplay.Children[1].Value().(layout).ID
	recs = []string{"rec2"}
	if changed, _ := o.AboutToShow(oldReplay.ID); !changed {
		t.Fatal("submenu not refreshed")
	}
	_, sub, err := o.GetLayout(oldReplay.ID, -1, nil)
	if err != nil || len(sub.Children) != 1 || sub.Children[0].Value().(layout).ID != oldRec2 ||
		sub.Children[0].Value().(layout).Props["label"].Value() != "rec2" {
		t.Fatalf("replay = %+v, %v", sub, err)
	}
	if upd, _, _ := o.AboutToShowGroup([]int32{0}); len(upd) != 0 {
		t.Fatalf("unchanged menu updates = %v", upd)
	}

	// Без источника меню не меняется.
	plain := &menuObject{m: newMenu(nil, build())}
	if changed, _ := plain.AboutToShow(0); changed {
		t.Fatal("menu without source changed")
	}
}
