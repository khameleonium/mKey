package evdev

// Kind — класс устройства ввода, определённый эвристически по его возможностям.
type Kind string

// Классы устройств (FR-DEV-1).
const (
	// KindKeyboard — клавиатура (есть буквенные клавиши).
	KindKeyboard Kind = "keyboard"
	// KindMouse — мышь (относительные оси X/Y и левая кнопка).
	KindMouse Kind = "mouse"
	// KindTouchpad — тачпад (мультитач, указатель, инструмент «палец»).
	KindTouchpad Kind = "touchpad"
	// KindTouchscreen — сенсорный экран (прямой ввод, INPUT_PROP_DIRECT).
	KindTouchscreen Kind = "touchscreen"
	// KindTablet — графический планшет (перо).
	KindTablet Kind = "tablet"
	// KindGamepad — геймпад со стандартной раскладкой (BTN_SOUTH…).
	KindGamepad Kind = "gamepad"
	// KindJoystick — джойстик или геймпад без стандартной раскладки.
	KindJoystick Kind = "joystick"
	// KindOther — прочее: кнопки питания, мультимедийные пульты, датчики и т.п.
	KindOther Kind = "other"
)

// Classify определяет классы устройства по его возможностям.
// Устройство может относиться к нескольким классам (например, клавиатура со встроенным тачпадом);
// результат никогда не пустой — в крайнем случае KindOther.
func Classify(c Capabilities) []Kind {
	var kinds []Kind

	// Клавиатура: есть типичные буквенные клавиши и пробел.
	if c.Has(EvKey, KeyA) && c.Has(EvKey, KeyZ) && c.Has(EvKey, KeySpace) {
		kinds = append(kinds, KindKeyboard)
	}

	// Мышь: относительные оси X/Y и левая кнопка.
	if c.Has(EvRel, RelX) && c.Has(EvRel, RelY) && c.Has(EvKey, BtnLeft) {
		kinds = append(kinds, KindMouse)
	}

	// Абсолютные указательные устройства различаем по инструменту и свойствам.
	absXY := c.Has(EvAbs, AbsX) && c.Has(EvAbs, AbsY)
	mt := c.Has(EvAbs, AbsMtPositionX)
	switch {
	case c.Has(EvKey, BtnToolPen) && absXY:
		kinds = append(kinds, KindTablet)
	case c.HasProp(InputPropDirect) && (mt || absXY) && c.Has(EvKey, BtnTouch):
		kinds = append(kinds, KindTouchscreen)
	case (mt || absXY) && c.Has(EvKey, BtnToolFinger) && c.Has(EvKey, BtnTouch):
		kinds = append(kinds, KindTouchpad)
	}

	// Геймпад со стандартной раскладкой или джойстик.
	switch {
	case c.Has(EvKey, BtnSouth):
		kinds = append(kinds, KindGamepad)
	case c.Has(EvKey, BtnTrigger) || (absXY && !c.Has(EvKey, BtnTouch) && !c.Has(EvKey, BtnToolPen) && !c.HasProp(InputPropDirect)):
		kinds = append(kinds, KindJoystick)
	}

	// Ничего не подошло — прочее устройство.
	if len(kinds) == 0 {
		kinds = append(kinds, KindOther)
	}
	return kinds
}
