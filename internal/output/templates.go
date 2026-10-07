package output

import (
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
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

// pointerRange — наибольшее значение абсолютных осей указателя. Композитор растягивает диапазон
// 0…pointerRange на весь рабочий стол (libinput: x_px = value × ширина / (pointerRange+1)).
const pointerRange = 32767

// pointerSetup описывает виртуальный указатель с абсолютными координатами («mKey Pointer»):
// оси X/Y и кнопки мыши — так libinput считает его мышью с абсолютным позиционированием
// (как планшет виртуальной машины), а не графическим планшетом или тач-экраном.
func pointerSetup() ev.Setup {
	axis := ev.AbsInfo{Minimum: 0, Maximum: pointerRange}
	return ev.Setup{
		Name: contracts.VirtualNamePrefix + "Pointer",
		Phys: contracts.VirtualPhysPrefix + "pointer",
		ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: vendorMKey, Product: 0x0003, Version: 1},
		Keys: []uint16{ev.BtnLeft, ev.BtnRight, ev.BtnMiddle},
		Abs:  map[uint16]ev.AbsInfo{ev.AbsX: axis, ev.AbsY: axis},
	}
}

// pointerCenter — значение оси, соответствующее середине рабочего стола при любом его размере
// (композитор растягивает диапазон осей на весь стол, поэтому размер знать не нужно).
const pointerCenter = (pointerRange + 1) / 2

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
