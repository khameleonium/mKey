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
