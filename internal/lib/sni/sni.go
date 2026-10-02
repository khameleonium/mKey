// Package sni — значок в системном трее по протоколу StatusNotifierItem (KDE, XFCE, LXQt,
// Cinnamon, sway/waybar, GNOME с расширением AppIndicator) с меню по протоколу com.canonical.dbusmenu.
//
// Протоколы:
//   - https://www.freedesktop.org/wiki/Specifications/StatusNotifierItem/
//   - https://github.com/AyatanaIndicators/libdbusmenu/blob/master/libdbusmenu-glib/dbus-menu.xml
//
// Item экспортирует два объекта на сессионной шине D-Bus: сам значок (/StatusNotifierItem)
// и его меню (/MenuBar), и регистрирует значок у сервиса-«наблюдателя» трея
// (org.kde.StatusNotifierWatcher). Если панель трея перезапускается, значок регистрируется заново.
// Пакет не зависит от модулей mKey: что показывать и что делать по щелчку, решает вызывающий код.
package sni

import (
	"fmt"
	"image"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// Имена протокола StatusNotifierItem.
const (
	itemIface     = "org.kde.StatusNotifierItem"
	itemPath      = dbus.ObjectPath("/StatusNotifierItem")
	watcherName   = "org.kde.StatusNotifierWatcher"
	watcherPath   = dbus.ObjectPath("/StatusNotifierWatcher")
	watcherMethod = watcherName + ".RegisterStatusNotifierItem"
)

// Pixmap — картинка значка в формате протокола: ширина, высота и пиксели ARGB32
// в сетевом порядке байт (A, R, G, B), строка за строкой.
type Pixmap struct {
	W, H int32
	Data []byte
}

// PixmapFromImage переводит картинку в формат протокола.
func PixmapFromImage(img image.Image) Pixmap {
	b := img.Bounds()
	p := Pixmap{W: int32(b.Dx()), H: int32(b.Dy()), Data: make([]byte, 0, b.Dx()*b.Dy()*4)}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// RGBA() возвращает 16-битные компоненты с предумноженной альфой; протокол ждёт обычную.
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 {
				r, g, bl = r*0xffff/a, g*0xffff/a, bl*0xffff/a
			}
			p.Data = append(p.Data, byte(a>>8), byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	return p
}

// toolTip — подсказка значка в формате протокола: имя иконки, картинки, заголовок, текст.
type toolTip struct {
	Icon  string
	Pix   []Pixmap
	Title string
	Body  string
}

// Options — начальные свойства значка.
type Options struct {
	// ID — постоянный идентификатор приложения ("mkey").
	ID string
	// Title — название приложения.
	Title string
	// Icon — картинки значка в нескольких размерах (панель выберет подходящий).
	Icon []Pixmap
	// Tooltip и TooltipBody — подсказка при наведении.
	Tooltip, TooltipBody string
	// OnActivate вызывается при щелчке левой кнопкой по значку (в отдельной горутине).
	OnActivate func()
	// OnMenuShow вызывается перед тем, как панель откроет меню, и возвращает свежие пункты
	// (nil — оставить прежние). Так меню показывает то, что есть сейчас, даже если что-то
	// изменилось без ведома программы (например, файл записи удалили). Если пункты те же,
	// меню не перестраивается: номера пунктов в уже открытом меню остаются верными.
	OnMenuShow func() []MenuItem
}

// Item — значок в трее. Методы безопасны для вызова из разных горутин.
type Item struct {
	// conn — собственное подключение к сессионной шине (закрывается в Close, не мешая другим модулям).
	conn *dbus.Conn
	// busName — имя значка на шине, которое регистрируется у наблюдателя.
	busName string
	// props — свойства значка; menu — меню.
	props *prop.Properties
	menu  *menu
	// onActivate — обработчик щелчка по значку.
	onActivate func()

	// mu защищает closed; done закрывается при Close и останавливает слежение за наблюдателем.
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	wg     sync.WaitGroup
}

// New подключается к сессионной шине, публикует значок с меню items и регистрирует его в трее.
// Если трея сейчас нет (панель не запущена), значок появится, когда она запустится.
// Ошибка — нет сессионной шины D-Bus или не удалось опубликовать объекты.
func New(opts Options, items []MenuItem) (*Item, error) {
	// Собственное подключение: закрытие не затронет общие подключения программы.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("sni: connect session bus: %w", err)
	}
	it := &Item{
		conn:       conn,
		busName:    fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid()),
		onActivate: opts.OnActivate,
		done:       make(chan struct{}),
	}
	it.menu = newMenu(conn, items)
	it.menu.source = opts.OnMenuShow

	// Публикуем объекты; при ошибке закрываем подключение.
	if err := it.export(opts); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// Занимаем имя на шине и регистрируемся в трее; следим за перезапуском панели трея.
	if _, err := conn.RequestName(it.busName, dbus.NameFlagDoNotQueue); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sni: request name: %w", err)
	}
	if err := it.watchWatcher(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = it.register()
	return it, nil
}

// export публикует объект значка (свойства, методы, описание) и объект меню.
func (it *Item) export(opts Options) error {
	// Свойства значка по протоколу StatusNotifierItem.
	propsSpec := map[string]map[string]*prop.Prop{
		itemIface: {
			"Category":            {Value: "ApplicationStatus", Emit: prop.EmitFalse},
			"Id":                  {Value: opts.ID, Emit: prop.EmitFalse},
			"Title":               {Value: opts.Title, Emit: prop.EmitFalse},
			"Status":              {Value: "Active", Emit: prop.EmitFalse},
			"WindowId":            {Value: int32(0), Emit: prop.EmitFalse},
			"IconName":            {Value: "", Emit: prop.EmitFalse},
			"IconPixmap":          {Value: opts.Icon, Emit: prop.EmitFalse},
			"OverlayIconName":     {Value: "", Emit: prop.EmitFalse},
			"OverlayIconPixmap":   {Value: []Pixmap{}, Emit: prop.EmitFalse},
			"AttentionIconName":   {Value: "", Emit: prop.EmitFalse},
			"AttentionIconPixmap": {Value: []Pixmap{}, Emit: prop.EmitFalse},
			"AttentionMovieName":  {Value: "", Emit: prop.EmitFalse},
			"ToolTip":             {Value: toolTip{Pix: []Pixmap{}, Title: opts.Tooltip, Body: opts.TooltipBody}, Emit: prop.EmitFalse},
			"ItemIsMenu":          {Value: false, Emit: prop.EmitFalse},
			"Menu":                {Value: menuPath, Emit: prop.EmitFalse},
		},
	}
	props, err := prop.Export(it.conn, itemPath, propsSpec)
	if err != nil {
		return fmt.Errorf("sni: export item properties: %w", err)
	}
	it.props = props

	// Методы значка (щелчки) и описание объекта для панелей, которые его запрашивают.
	obj := &itemObject{it: it}
	if err := it.conn.Export(obj, itemPath, itemIface); err != nil {
		return fmt.Errorf("sni: export item: %w", err)
	}
	node := &introspect.Node{
		Name: string(itemPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       itemIface,
				Methods:    introspect.Methods(obj),
				Properties: props.Introspection(itemIface),
				Signals: []introspect.Signal{
					{Name: "NewIcon"}, {Name: "NewTitle"}, {Name: "NewToolTip"},
					{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
				},
			},
		},
	}
	if err := it.conn.Export(introspect.NewIntrospectable(node), itemPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("sni: export introspection: %w", err)
	}

	// Меню.
	return it.menu.export()
}

// watchWatcher следит за появлением наблюдателя трея (перезапуск панели) и регистрирует значок заново.
func (it *Item) watchWatcher() error {
	// Подписка на смену владельца имени наблюдателя.
	if err := it.conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherName),
	); err != nil {
		return fmt.Errorf("sni: watch tray: %w", err)
	}
	ch := make(chan *dbus.Signal, 8)
	it.conn.Signal(ch)

	// Новый владелец имени — панель трея (пере)запущена: регистрируемся у неё.
	it.wg.Add(1)
	go func() {
		defer it.wg.Done()
		for {
			select {
			case <-it.done:
				return
			case s, ok := <-ch:
				if !ok {
					return
				}
				if s.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(s.Body) == 3 {
					if owner, _ := s.Body[2].(string); owner != "" {
						_ = it.register()
					}
				}
			}
		}
	}()
	return nil
}

// register регистрирует значок у наблюдателя трея. Ошибка — трея сейчас нет.
func (it *Item) register() error {
	return it.conn.Object(watcherName, watcherPath).Call(watcherMethod, 0, it.busName).Err
}

// Registered сообщает, есть ли сейчас в сеансе панель трея, которая может показать значок.
func (it *Item) Registered() bool {
	var has bool
	err := it.conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, watcherName).Store(&has)
	return err == nil && has
}

// SetIcon меняет картинку значка.
func (it *Item) SetIcon(icon []Pixmap) {
	it.props.SetMust(itemIface, "IconPixmap", icon)
	it.emit("NewIcon")
}

// SetTooltip меняет подсказку при наведении.
func (it *Item) SetTooltip(title, body string) {
	it.props.SetMust(itemIface, "ToolTip", toolTip{Pix: []Pixmap{}, Title: title, Body: body})
	it.emit("NewToolTip")
}

// SetMenu заменяет пункты меню.
func (it *Item) SetMenu(items []MenuItem) {
	it.menu.set(items)
}

// emit отправляет сигнал значка (панель перечитает соответствующее свойство).
func (it *Item) emit(name string, args ...any) {
	_ = it.conn.Emit(itemPath, itemIface+"."+name, args...)
}

// Close убирает значок из трея и закрывает подключение к шине. Повторный вызов ничего не делает.
func (it *Item) Close() error {
	it.mu.Lock()
	if it.closed {
		it.mu.Unlock()
		return nil
	}
	it.closed = true
	it.mu.Unlock()

	// Останавливаем слежение и закрываем подключение: панель трея убирает значок сама,
	// когда имя значка исчезает с шины.
	close(it.done)
	err := it.conn.Close()
	it.wg.Wait()
	return err
}

// itemObject — методы объекта значка, которые вызывает панель трея.
// Отдельный тип, чтобы на шину попали только методы протокола.
type itemObject struct{ it *Item }

// Activate — щелчок левой кнопкой по значку.
func (o *itemObject) Activate(_, _ int32) *dbus.Error {
	if o.it.onActivate != nil {
		go o.it.onActivate()
	}
	return nil
}

// SecondaryActivate — щелчок средней кнопкой; ничего не делает.
func (o *itemObject) SecondaryActivate(_, _ int32) *dbus.Error { return nil }

// ContextMenu — запрос меню без dbusmenu; меню показывает панель по свойству Menu.
func (o *itemObject) ContextMenu(_, _ int32) *dbus.Error { return nil }

// Scroll — прокрутка колёсика над значком; ничего не делает.
func (o *itemObject) Scroll(_ int32, _ string) *dbus.Error { return nil }
