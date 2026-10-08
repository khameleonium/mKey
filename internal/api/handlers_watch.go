package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/lib/keys"
)

// Монитор нажатий (FR-DEV-8, T7.1): поток событий физических устройств для окна программы
// («Устройства» → «Следить за нажатиями») и команды `mkey devices watch`. Демон события только
// показывает, пока открыт поток, и никуда их не сохраняет (NFR-8); в файл журнал попадает только
// по явной просьбе человека — кнопкой «Сохранить в файл» в окне или `mkey devices watch --out`.

// watchEntry — одно событие монитора.
type watchEntry struct {
	// Time — время события (метка ядра).
	Time time.Time `json:"time"`
	// Device и DeviceName — путь и имя устройства.
	Device     string `json:"device"`
	DeviceName string `json:"device_name"`
	// Kind — вид: "key" (клавиша или кнопка), "axis" (ось геймпада/джойстика), "wheel" (колесо),
	// "move" (сдвиг мыши или трекпойнта), "touch" (положение пальца или пера).
	Kind string `json:"kind"`
	// Group — что показывать (галочки окна, `--show`): "keys" (всегда), "wheel", "axes", "moves", "touch".
	Group string `json:"group"`
	// Name — имя для макросов ("A", "Mouse0", "South", "LX") или "#код", если имени нет.
	Name string `json:"name"`
	// Kernel — имя кода в ядре ("BTN_TRIGGER_HAPPY3"); Code — сам код.
	Kernel string `json:"kernel"`
	Code   uint16 `json:"code"`
	// Labeled — Name — авто-ID или имя, данное человеком ({UnKey001}, {Sega.Start}): кнопку можно
	// переименовать (FR-DEV-4).
	Labeled bool `json:"labeled,omitempty"`
	// Action — для клавиш "down" или "up"; Value — значение оси, колеса или сдвиг мыши по X.
	Action string `json:"action,omitempty"`
	Value  int32  `json:"value,omitempty"`
	// DY — сдвиг мыши по Y (для "move").
	DY int32 `json:"dy,omitempty"`
}

// axisInterval — не чаще одного сообщения об оси за это время: оси шлют сотни значений в секунду.
const axisInterval = 100 * time.Millisecond

// Группы событий монитора (кроме клавиш и кнопок — они видны всегда).
const (
	// watchWheel — колёсико и прокрутка.
	watchWheel = "wheel"
	// watchAxes — стики, курки и другие оси геймпадов и джойстиков.
	watchAxes = "axes"
	// watchMoves — сдвиги указателя от мыши и трекпойнта.
	watchMoves = "moves"
	// watchTouch — касания тачпада, сенсорного экрана и пера планшета.
	watchTouch = "touch"
)

// watchShow — какие группы событий показывать; клавиши и кнопки показываются всегда.
type watchShow map[string]bool

// parseWatchShow читает группы из запроса: ?show=wheel,axes,moves,touch (пусто — только клавиши
// и кнопки). Без ?show — прежнее поведение: всё, кроме перемещений мыши, а с ?moves=1 — и они.
// Неизвестные группы пропускаются.
func parseWatchShow(r *http.Request) watchShow {
	q := r.URL.Query()
	if !q.Has("show") {
		return watchShow{watchWheel: true, watchAxes: true, watchTouch: true, watchMoves: q.Get("moves") == "1"}
	}
	show := watchShow{}
	for _, g := range strings.Split(q.Get("show"), ",") {
		show[strings.TrimSpace(g)] = true
	}
	return show
}

// watchDevice — что монитору нужно знать об устройстве: имя и сенсорное ли оно (тачпад,
// сенсорный экран, планшет — их оси и «касания» относятся к группе touch).
type watchDevice struct {
	name  string
	touch bool
}

// isTouch — устройство сенсорное: тачпад, сенсорный экран или графический планшет.
func isTouch(kinds []ev.Kind) bool {
	return slices.ContainsFunc(kinds, func(k ev.Kind) bool {
		return k == ev.KindTouchpad || k == ev.KindTouchscreen || k == ev.KindTablet
	})
}

// handleWatch — поток событий устройств (Server-Sent Events) до закрытия соединения.
// ?show=wheel,axes,moves,touch — какие группы событий показывать кроме клавиш и кнопок
// (parseWatchShow; без него — всё, кроме перемещений мыши, ?moves=1 — и они);
// ?device=<ссылка> — только устройства по ссылке (путь, event6, постоянное имя или часть
// названия, как у «Подробнее»); не нашлось ни одного — 404 api.device_not_found.
func (m *Module) handleWatch(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || m.svc.input == nil {
		m.unavailable(w, r)
		return
	}
	show := parseWatchShow(r)

	// Фильтр по устройству (nil — все устройства).
	var only map[string]bool
	if ref := r.URL.Query().Get("device"); ref != "" {
		if only = m.watchDevices(ref); len(only) == 0 {
			m.writeError(w, r, http.StatusNotFound, "api.device_not_found", map[string]string{"ref": ref})
			return
		}
	}

	// Заголовки потока.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": mkey watch\n\n")
	flusher.Flush()

	// Имена и вид устройств по путям (обновляются, если встретилось новое устройство).
	names := map[string]watchDevice{}
	refresh := func() {
		for _, d := range m.svc.input.Devices() {
			names[d.Info.Path] = watchDevice{name: d.Info.Name, touch: isTouch(d.Kinds)}
		}
	}
	refresh()

	// События до разрыва соединения; раз в 15 с — комментарий, чтобы соединение не закрылось.
	events, unsub := m.svc.input.Subscribe(1024)
	defer unsub()
	lastAxis := map[string]time.Time{}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-events:
			if !ok {
				return
			}
			if only != nil && !only[e.Device] {
				continue
			}
			if _, known := names[e.Device]; !known {
				refresh()
			}
			entry, ok := describeEvent(e, names[e.Device], show, lastAxis)
			if !ok {
				continue
			}
			m.labelEntry(&entry, e)
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: input\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// watchDevices находит пути устройств по ссылке ref для фильтра монитора: через инспектор (путь,
// event6, постоянное имя, часть названия), а без него — по пути, имени файла или части названия
// среди открытых устройств. Пусто — ничего не нашлось.
func (m *Module) watchDevices(ref string) map[string]bool {
	out := map[string]bool{}

	// Инспектор знает и постоянные имена.
	if m.svc.inspect != nil {
		for _, d := range m.svc.inspect.Find(ref) {
			out[d.Info.Path] = true
		}
		return out
	}

	// Без инспектора: путь или имя файла точно, иначе часть названия без учёта регистра.
	lower := strings.ToLower(ref)
	for _, d := range m.svc.input.Devices() {
		if d.Info.Path == ref || strings.TrimPrefix(d.Info.Path, "/dev/input/") == ref ||
			strings.Contains(strings.ToLower(d.Info.Name), lower) {
			out[d.Info.Path] = true
		}
	}
	return out
}

// labelEntry подставляет авто-ID устройства (UnKey001, FR-DEV-2) вместо «#код» у кнопки или оси
// без стандартного имени, если инспектор его выдал.
func (m *Module) labelEntry(entry *watchEntry, e contracts.InputEvent) {
	if !strings.HasPrefix(entry.Name, "#") || m.svc.inspect == nil {
		return
	}
	if l := m.svc.inspect.Label(e.Device, e.Event.Type, e.Event.Code); l != "" {
		entry.Name, entry.Labeled = l, true
	}
}

// describeEvent переводит событие устройства dev в запись монитора. false — не показывать
// (служебное, автоповтор, слишком частое значение оси, группа не выбрана в show).
func describeEvent(e contracts.InputEvent, dev watchDevice, show watchShow, lastAxis map[string]time.Time) (watchEntry, bool) {
	en := watchEntry{Time: e.Event.Time, Device: e.Device, DeviceName: dev.name, Code: e.Event.Code, Kernel: ev.CodeName(e.Event.Type, e.Event.Code), Group: "keys"}
	if en.Time.IsZero() {
		en.Time = time.Now()
	}

	// throttled — значение оси пришло раньше axisInterval после прошлого показанного.
	throttled := func() bool {
		id := e.Device + "/" + strconv.Itoa(int(e.Event.Code))
		if en.Time.Sub(lastAxis[id]) < axisInterval {
			return true
		}
		lastAxis[id] = en.Time
		return false
	}

	switch e.Event.Type {
	// Клавиши и кнопки: нажатие и отпускание (автоповтор не показываем). У сенсорных устройств
	// «касание» и «инструмент» (палец, перо, два пальца…) — часть касаний, а не кнопки.
	case ev.EvKey:
		if e.Event.Value == ev.ValueRepeat {
			return en, false
		}
		if dev.touch && touchKey(e.Event.Code) {
			en.Group = watchTouch
		}
		en.Kind, en.Name, en.Action = "key", keyName(e.Event.Code), "down"
		if e.Event.Value == ev.ValueUp {
			en.Action = "up"
		}
		return en, show.has(en.Group)

	// Оси сенсорных устройств: только положение (X, Y), не чаще раза в axisInterval на ось;
	// служебные (номер пальца, площадь касания) не показываем.
	case ev.EvAbs:
		if dev.touch {
			en.Group, en.Kind, en.Value = watchTouch, "touch", e.Event.Value
			switch e.Event.Code {
			case ev.AbsX, ev.AbsMtPositionX:
				en.Name = "X"
			case ev.AbsY, ev.AbsMtPositionY:
				en.Name = "Y"
			default:
				return en, false
			}
			return en, show[watchTouch] && !throttled()
		}

		// Оси геймпадов и джойстиков: не чаще раза в axisInterval на ось.
		if !show[watchAxes] || throttled() {
			return en, false
		}
		en.Group, en.Kind, en.Value = watchAxes, "axis", e.Event.Value
		en.Name = "#" + strconv.Itoa(int(e.Event.Code))
		if n, ok := keys.AxisNameOf(e.Event.Code); ok {
			en.Name = n
		}
		return en, true

	// Колесо и сдвиги указателя (мышь, трекпойнт).
	case ev.EvRel:
		switch e.Event.Code {
		case ev.RelWheel, ev.RelHwheel:
			en.Group, en.Kind, en.Value = watchWheel, "wheel", e.Event.Value
			en.Name = "Wheel"
			if e.Event.Code == ev.RelHwheel {
				en.Name = "HWheel"
			}
			return en, show[watchWheel]
		case ev.RelX, ev.RelY:
			en.Group, en.Kind, en.Name = watchMoves, "move", "Move"
			if e.Event.Code == ev.RelX {
				en.Value = e.Event.Value
			} else {
				en.DY = e.Event.Value
			}
			return en, show[watchMoves]
		}
	}
	return en, false
}

// has — показывать ли группу g; клавиши и кнопки ("keys") показываются всегда.
func (s watchShow) has(g string) bool {
	return g == "keys" || s[g]
}

// touchKey — код «касания» или «инструмента» сенсорного устройства: BTN_TOOL_PEN…BTN_TOOL_QUINTTAP
// (0x140–0x148), BTN_TOUCH (0x14a), BTN_TOOL_DOUBLETAP…QUADTAP (0x14d–0x14f). Кнопки пера
// (BTN_STYLUS*, 0x149, 0x14b, 0x14c) — настоящие кнопки (linux/input-event-codes.h).
func touchKey(code uint16) bool {
	return (code >= ev.BtnToolPen && code <= ev.BtnToolQuinttap) || code == ev.BtnTouch || (code >= ev.BtnToolDoubletap && code <= ev.BtnToolQuadtap)
}

// keyName возвращает имя клавиши для макросов или «#код», если имени нет.
func keyName(code uint16) string {
	if n, ok := keys.NameOf(code); ok {
		return n
	}
	return "#" + strconv.Itoa(int(code))
}
