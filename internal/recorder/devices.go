package recorder

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// Выбор устройств для записи (FR-REC-2): у каждого подключённого устройства — своя галочка
// (RecordSettings.Devices, ключ «VID:PID имя»), а классы RecordSettings.Kinds решают за устройства
// без своего выбора, в том числе подключённые позже. Устройства показываются по категориям;
// собственные устройства mKey — в «Виртуальных», без галочки: mKey их не читает.

// catOrder — порядок категорий в выборе «что записывать».
var catOrder = []string{
	contracts.RecordCatKeyboards, contracts.RecordCatGamepads, contracts.RecordCatTouch,
	contracts.RecordCatOther, contracts.RecordCatVirtual,
}

// deviceKey — ключ выбора устройства: «2dc8:310a 8BitDo Ultimate 2C Wireless Controller». Одинаков
// после переподключения и перезагрузки (путь eventN меняется, а VID:PID и имя — нет).
func deviceKey(info ev.Info) string {
	return fmt.Sprintf("%04x:%04x %s", info.ID.Vendor, info.ID.Product, info.Name)
}

// category — категория устройства для показа: созданные программами — «виртуальные»; иначе
// по классам — геймпады важнее клавиатуры (у многих геймпадов есть «клавиатурный» интерфейс).
func category(d contracts.InputDevice) string {
	if d.Virtual {
		return contracts.RecordCatVirtual
	}
	has := func(kinds ...ev.Kind) bool {
		return slices.ContainsFunc(d.Kinds, func(k ev.Kind) bool { return slices.Contains(kinds, k) })
	}
	switch {
	case has(ev.KindGamepad, ev.KindJoystick):
		return contracts.RecordCatGamepads
	case has(ev.KindKeyboard, ev.KindMouse, ev.KindTouchpad):
		return contracts.RecordCatKeyboards
	case has(ev.KindTouchscreen, ev.KindTablet):
		return contracts.RecordCatTouch
	}
	return contracts.RecordCatOther
}

// selected сообщает, записывается ли устройство при настройках s: свой выбор из Devices, иначе —
// если хоть один его класс есть в Kinds. Устройства, созданные программами (удалённый доступ,
// эмуляторы), без своей галочки не записываются: они часто называют себя сразу клавиатурой
// и геймпадом и попадали бы в запись неожиданно.
func selected(s contracts.RecordSettings, d contracts.InputDevice) (sel, explicit bool) {
	if v, ok := s.Devices[deviceKey(d.Info)]; ok {
		return v, true
	}
	if d.Virtual {
		return false, false
	}
	return slices.ContainsFunc(d.Kinds, func(k ev.Kind) bool { return slices.Contains(s.Kinds, string(k)) }), false
}

// RecordDevices возвращает подключённые устройства для выбора «что записывать» по категориям
// (в каждой — по имени) с тем, будут ли они записываться (contracts.Recorder). Свои устройства
// mKey — в «Виртуальных», не выбраны и без галочки.
func (m *Module) RecordDevices() []contracts.RecordDevice {
	if m.input == nil {
		return []contracts.RecordDevice{}
	}
	s := m.RecordSettings()

	// Подключённые устройства и свои устройства mKey.
	out := []contracts.RecordDevice{}
	add := func(d contracts.InputDevice, own bool) {
		kinds := make([]string, len(d.Kinds))
		for i, k := range d.Kinds {
			kinds[i] = string(k)
		}
		rd := contracts.RecordDevice{
			Key: deviceKey(d.Info), Name: d.Info.Name, Path: d.Info.Path, Kinds: kinds,
			ID:       fmt.Sprintf("%04x:%04x", d.Info.ID.Vendor, d.Info.ID.Product),
			Category: category(d), Virtual: d.Virtual || own, Own: own,
		}
		if own {
			rd.Category = contracts.RecordCatVirtual
		} else {
			rd.Selected, rd.Explicit = selected(s, d)
		}
		out = append(out, rd)
	}
	for _, d := range m.input.Devices() {
		add(d, false)
	}
	for _, d := range m.input.OwnDevices() {
		add(d, true)
	}

	// Порядок показа: категория, затем имя.
	slices.SortStableFunc(out, func(a, b contracts.RecordDevice) int {
		return cmp.Or(
			cmp.Compare(slices.Index(catOrder, a.Category), slices.Index(catOrder, b.Category)),
			strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			strings.Compare(a.Path, b.Path),
		)
	})
	return out
}
