package output

import (
	"strings"

	"mkey/internal/contracts"
	ev "mkey/internal/lib/evdev"
)

// vendorMKey — условный идентификатор производителя виртуальных устройств mKey ("mk").
const vendorMKey = 0x6d6b

// keyboardSetup описывает виртуальную клавиатуру: все коды KEY_* (без BTN_*).
// Кнопки BTN_* не объявляются намеренно: libinput по ним решил бы, что это мышь или планшет.
func keyboardSetup() ev.Setup {
	// Отбираем все коды EV_KEY, у которых каноническое имя ядра начинается с KEY_.
	var keysList []uint16
	for code := uint16(1); code <= ev.KeyMax; code++ {
		if strings.HasPrefix(ev.CodeName(ev.EvKey, code), "KEY_") {
			keysList = append(keysList, code)
		}
	}
	return ev.Setup{
		Name: contracts.VirtualNamePrefix + "Keyboard",
		Phys: contracts.VirtualPhysPrefix + "keyboard",
		ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0001, Version: 1},
		Keys: keysList,
	}
}

// mouseSetup описывает виртуальную мышь: кнопки, перемещение и колёса (включая высокое разрешение).
func mouseSetup() ev.Setup {
	return ev.Setup{
		Name: contracts.VirtualNamePrefix + "Mouse",
		Phys: contracts.VirtualPhysPrefix + "mouse",
		ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0002, Version: 1},
		Keys: []uint16{ev.BtnLeft, ev.BtnRight, ev.BtnMiddle, ev.BtnSide, ev.BtnExtra, ev.BtnForward, ev.BtnBack, ev.BtnTask},
		Rels: []uint16{ev.RelX, ev.RelY, ev.RelWheel, ev.RelHwheel, ev.RelWheelHiRes, ev.RelHwheelHiRes},
	}
}
