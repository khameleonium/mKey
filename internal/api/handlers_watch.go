package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// Монитор нажатий (FR-DEV-8, T7.1): поток событий физических устройств для окна программы
// («Устройства» → «Следить за нажатиями») и команды `mkey devices watch`. События только
// показываются, пока открыт поток, и никуда не сохраняются (NFR-8).

// watchEntry — одно событие монитора.
type watchEntry struct {
	// Time — время события (метка ядра).
	Time time.Time `json:"time"`
	// Device и DeviceName — путь и имя устройства.
	Device     string `json:"device"`
	DeviceName string `json:"device_name"`
	// Kind — вид: "key" (клавиша или кнопка), "axis" (ось геймпада/джойстика), "wheel" (колесо), "move" (мышь).
	Kind string `json:"kind"`
	// Name — имя для макросов ("A", "Mouse0", "South", "LX") или "#код", если имени нет.
	Name string `json:"name"`
	// Kernel — имя кода в ядре ("BTN_TRIGGER_HAPPY3"); Code — сам код.
	Kernel string `json:"kernel"`
	Code   uint16 `json:"code"`
	// Action — для клавиш "down" или "up"; Value — значение оси, колеса или сдвиг мыши по X.
	Action string `json:"action,omitempty"`
	Value  int32  `json:"value,omitempty"`
	// DY — сдвиг мыши по Y (для "move").
	DY int32 `json:"dy,omitempty"`
}

// axisInterval — не чаще одного сообщения об оси за это время: оси шлют сотни значений в секунду.
const axisInterval = 100 * time.Millisecond

// handleWatch — поток событий устройств (Server-Sent Events) до закрытия соединения.
// ?moves=1 — показывать и перемещения мыши (по умолчанию нет: их слишком много).
func (m *Module) handleWatch(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || m.svc.input == nil {
		m.unavailable(w, r)
		return
	}
	moves := r.URL.Query().Get("moves") == "1"

	// Заголовки потока.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": mkey watch\n\n")
	flusher.Flush()

	// Имена устройств по путям (обновляются, если встретилось новое устройство).
	names := map[string]string{}
	refresh := func() {
		for _, d := range m.svc.input.Devices() {
			names[d.Info.Path] = d.Info.Name
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
			if _, known := names[e.Device]; !known {
				refresh()
			}
			entry, show := describeEvent(e, names[e.Device], moves, lastAxis)
			if !show {
				continue
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: input\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// describeEvent переводит событие устройства в запись монитора. false — не показывать
// (служебное, автоповтор, слишком частое значение оси, перемещение мыши без ?moves=1).
func describeEvent(e contracts.InputEvent, deviceName string, moves bool, lastAxis map[string]time.Time) (watchEntry, bool) {
	en := watchEntry{Time: e.Event.Time, Device: e.Device, DeviceName: deviceName, Code: e.Event.Code, Kernel: ev.CodeName(e.Event.Type, e.Event.Code)}
	if en.Time.IsZero() {
		en.Time = time.Now()
	}
	switch e.Event.Type {
	// Клавиши и кнопки: нажатие и отпускание (автоповтор не показываем).
	case ev.EvKey:
		if e.Event.Value == ev.ValueRepeat {
			return en, false
		}
		en.Kind, en.Name, en.Action = "key", keyName(e.Event.Code), "down"
		if e.Event.Value == ev.ValueUp {
			en.Action = "up"
		}
		return en, true

	// Оси геймпадов и джойстиков: не чаще раза в axisInterval на ось.
	case ev.EvAbs:
		id := e.Device + "/" + strconv.Itoa(int(e.Event.Code))
		if en.Time.Sub(lastAxis[id]) < axisInterval {
			return en, false
		}
		lastAxis[id] = en.Time
		en.Kind, en.Value = "axis", e.Event.Value
		en.Name = "#" + strconv.Itoa(int(e.Event.Code))
		if n, ok := keys.AxisNameOf(e.Event.Code); ok {
			en.Name = n
		}
		return en, true

	// Колесо и (по желанию) перемещения мыши.
	case ev.EvRel:
		switch e.Event.Code {
		case ev.RelWheel, ev.RelHwheel:
			en.Kind, en.Value = "wheel", e.Event.Value
			en.Name = "Wheel"
			if e.Event.Code == ev.RelHwheel {
				en.Name = "HWheel"
			}
			return en, true
		case ev.RelX, ev.RelY:
			if !moves {
				return en, false
			}
			en.Kind, en.Name = "move", "Move"
			if e.Event.Code == ev.RelX {
				en.Value = e.Event.Value
			} else {
				en.DY = e.Event.Value
			}
			return en, true
		}
	}
	return en, false
}

// keyName возвращает имя клавиши для макросов или «#код», если имени нет.
func keyName(code uint16) string {
	if n, ok := keys.NameOf(code); ok {
		return n
	}
	return "#" + strconv.Itoa(int(code))
}
