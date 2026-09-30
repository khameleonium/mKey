package keys

import ev "mkey/internal/lib/evdev"

// entry — строка таблицы: каноническое имя, код evdev и алиасы.
type entry struct {
	name    string
	code    uint16
	aliases []string
	// anySide — модификатор без стороны: отправляется левый, распознаётся любой.
	anySide bool
}

// keyboardTable — клавиатура и мышь (основное пространство имён).
// Порядок важен: при обратном поиске «код → имя» берётся первая строка с этим кодом,
// поэтому конкретные клавиши (LCtrl) идут раньше обобщённых (Ctrl).
var keyboardTable = []entry{
	// Буквы.
	{name: "A", code: ev.KeyA}, {name: "B", code: ev.KeyB}, {name: "C", code: ev.KeyC},
	{name: "D", code: ev.KeyD}, {name: "E", code: ev.KeyE}, {name: "F", code: ev.KeyF},
	{name: "G", code: ev.KeyG}, {name: "H", code: ev.KeyH}, {name: "I", code: ev.KeyI},
	{name: "J", code: ev.KeyJ}, {name: "K", code: ev.KeyK}, {name: "L", code: ev.KeyL},
	{name: "M", code: ev.KeyM}, {name: "N", code: ev.KeyN}, {name: "O", code: ev.KeyO},
	{name: "P", code: ev.KeyP}, {name: "Q", code: ev.KeyQ}, {name: "R", code: ev.KeyR},
	{name: "S", code: ev.KeyS}, {name: "T", code: ev.KeyT}, {name: "U", code: ev.KeyU},
	{name: "V", code: ev.KeyV}, {name: "W", code: ev.KeyW}, {name: "X", code: ev.KeyX},
	{name: "Y", code: ev.KeyY}, {name: "Z", code: ev.KeyZ},

	// Цифры верхнего ряда.
	{name: "0", code: ev.Key0}, {name: "1", code: ev.Key1}, {name: "2", code: ev.Key2},
	{name: "3", code: ev.Key3}, {name: "4", code: ev.Key4}, {name: "5", code: ev.Key5},
	{name: "6", code: ev.Key6}, {name: "7", code: ev.Key7}, {name: "8", code: ev.Key8},
	{name: "9", code: ev.Key9},

	// Функциональные клавиши.
	{name: "F1", code: ev.KeyF1}, {name: "F2", code: ev.KeyF2}, {name: "F3", code: ev.KeyF3},
	{name: "F4", code: ev.KeyF4}, {name: "F5", code: ev.KeyF5}, {name: "F6", code: ev.KeyF6},
	{name: "F7", code: ev.KeyF7}, {name: "F8", code: ev.KeyF8}, {name: "F9", code: ev.KeyF9},
	{name: "F10", code: ev.KeyF10}, {name: "F11", code: ev.KeyF11}, {name: "F12", code: ev.KeyF12},
	{name: "F13", code: ev.KeyF13}, {name: "F14", code: ev.KeyF14}, {name: "F15", code: ev.KeyF15},
	{name: "F16", code: ev.KeyF16}, {name: "F17", code: ev.KeyF17}, {name: "F18", code: ev.KeyF18},
	{name: "F19", code: ev.KeyF19}, {name: "F20", code: ev.KeyF20}, {name: "F21", code: ev.KeyF21},
	{name: "F22", code: ev.KeyF22}, {name: "F23", code: ev.KeyF23}, {name: "F24", code: ev.KeyF24},

	// Управляющие клавиши.
	{name: "Enter", code: ev.KeyEnter, aliases: []string{"Return"}},
	{name: "Esc", code: ev.KeyEsc, aliases: []string{"Escape"}},
	{name: "Tab", code: ev.KeyTab},
	{name: "Space", code: ev.KeySpace},
	{name: "Backspace", code: ev.KeyBackspace},
	{name: "CapsLock", code: ev.KeyCapslock},
	{name: "NumLock", code: ev.KeyNumlock},
	{name: "ScrollLock", code: ev.KeyScrolllock},
	{name: "PrintScreen", code: ev.KeySysrq, aliases: []string{"PrtSc", "SysRq"}},
	{name: "Pause", code: ev.KeyPause, aliases: []string{"Break"}},
	{name: "Menu", code: ev.KeyCompose, aliases: []string{"ContextMenu", "Apps"}},

	// Навигация.
	{name: "Insert", code: ev.KeyInsert, aliases: []string{"Ins"}},
	{name: "Delete", code: ev.KeyDelete, aliases: []string{"Del"}},
	{name: "Home", code: ev.KeyHome},
	{name: "End", code: ev.KeyEnd},
	{name: "PgUp", code: ev.KeyPageup, aliases: []string{"PageUp"}},
	{name: "PgDn", code: ev.KeyPagedown, aliases: []string{"PageDown"}},
	{name: "Up", code: ev.KeyUp},
	{name: "Down", code: ev.KeyDown},
	{name: "Left", code: ev.KeyLeft},
	{name: "Right", code: ev.KeyRight},

	// Модификаторы с конкретной стороной.
	{name: "LCtrl", code: ev.KeyLeftctrl, aliases: []string{"LeftCtrl", "LControl"}},
	{name: "RCtrl", code: ev.KeyRightctrl, aliases: []string{"RightCtrl", "RControl"}},
	{name: "LShift", code: ev.KeyLeftshift, aliases: []string{"LeftShift"}},
	{name: "RShift", code: ev.KeyRightshift, aliases: []string{"RightShift"}},
	{name: "LAlt", code: ev.KeyLeftalt, aliases: []string{"LeftAlt"}},
	{name: "RAlt", code: ev.KeyRightalt, aliases: []string{"RightAlt", "AltGr"}},
	{name: "LSuper", code: ev.KeyLeftmeta, aliases: []string{"LWin", "LMeta", "LeftSuper"}},
	{name: "RSuper", code: ev.KeyRightmeta, aliases: []string{"RWin", "RMeta", "RightSuper"}},

	// Модификаторы без стороны: при отправке — левый, при распознавании — любой.
	{name: "Ctrl", code: ev.KeyLeftctrl, aliases: []string{"Control"}, anySide: true},
	{name: "Shift", code: ev.KeyLeftshift, anySide: true},
	{name: "Alt", code: ev.KeyLeftalt, anySide: true},
	{name: "Super", code: ev.KeyLeftmeta, aliases: []string{"Win", "Meta"}, anySide: true},

	// Знаки (физические клавиши US-раскладки).
	{name: "Minus", code: ev.KeyMinus},
	{name: "Equal", code: ev.KeyEqual},
	{name: "LBracket", code: ev.KeyLeftbrace, aliases: []string{"LeftBracket"}},
	{name: "RBracket", code: ev.KeyRightbrace, aliases: []string{"RightBracket"}},
	{name: "Semicolon", code: ev.KeySemicolon},
	{name: "Apostrophe", code: ev.KeyApostrophe, aliases: []string{"Quote"}},
	{name: "Grave", code: ev.KeyGrave, aliases: []string{"Backquote"}},
	{name: "Backslash", code: ev.KeyBackslash},
	{name: "Comma", code: ev.KeyComma},
	{name: "Dot", code: ev.KeyDot, aliases: []string{"Period"}},
	{name: "Slash", code: ev.KeySlash},
	{name: "IntlBackslash", code: ev.Key102nd, aliases: []string{"102nd"}},

	// Цифровой блок.
	{name: "Num0", code: ev.KeyKp0}, {name: "Num1", code: ev.KeyKp1}, {name: "Num2", code: ev.KeyKp2},
	{name: "Num3", code: ev.KeyKp3}, {name: "Num4", code: ev.KeyKp4}, {name: "Num5", code: ev.KeyKp5},
	{name: "Num6", code: ev.KeyKp6}, {name: "Num7", code: ev.KeyKp7}, {name: "Num8", code: ev.KeyKp8},
	{name: "Num9", code: ev.KeyKp9},
	{name: "NumEnter", code: ev.KeyKpenter},
	{name: "NumPlus", code: ev.KeyKpplus},
	{name: "NumMinus", code: ev.KeyKpminus},
	{name: "NumMultiply", code: ev.KeyKpasterisk},
	{name: "NumDivide", code: ev.KeyKpslash},
	{name: "NumDot", code: ev.KeyKpdot},
	{name: "NumEqual", code: ev.KeyKpequal},

	// Мультимедиа.
	{name: "VolumeUp", code: ev.KeyVolumeup},
	{name: "VolumeDown", code: ev.KeyVolumedown},
	{name: "Mute", code: ev.KeyMute},
	{name: "PlayPause", code: ev.KeyPlaypause},
	{name: "Stop", code: ev.KeyStopcd},
	{name: "Next", code: ev.KeyNextsong},
	{name: "Prev", code: ev.KeyPrevioussong, aliases: []string{"Previous"}},
	{name: "BrightnessUp", code: ev.KeyBrightnessup},
	{name: "BrightnessDown", code: ev.KeyBrightnessdown},

	// Кнопки мыши: Mouse0 — левая, Mouse1 — правая, Mouse2 — средняя,
	// Mouse3 — «назад» (боковая), Mouse4 — «вперёд» (дополнительная).
	{name: "Mouse0", code: ev.BtnLeft, aliases: []string{"MouseLeft"}},
	{name: "Mouse1", code: ev.BtnRight, aliases: []string{"MouseRight"}},
	{name: "Mouse2", code: ev.BtnMiddle, aliases: []string{"MouseMiddle"}},
	{name: "Mouse3", code: ev.BtnSide, aliases: []string{"MouseBack"}},
	{name: "Mouse4", code: ev.BtnExtra, aliases: []string{"MouseForward"}},
	{name: "Mouse5", code: ev.BtnForward},
	{name: "Mouse6", code: ev.BtnBack},
	{name: "Mouse7", code: ev.BtnTask},
}

// gamepadTable — кнопки геймпада (доступны только с префиксом устройства).
// Алиасы A/B/X/Y — по расположению кнопок Xbox: X слева (West), Y сверху (North).
var gamepadTable = []entry{
	{name: "South", code: ev.BtnSouth, aliases: []string{"A"}},
	{name: "East", code: ev.BtnEast, aliases: []string{"B"}},
	{name: "West", code: ev.BtnWest, aliases: []string{"X"}},
	{name: "North", code: ev.BtnNorth, aliases: []string{"Y"}},
	{name: "LB", code: ev.BtnTl, aliases: []string{"L1"}},
	{name: "RB", code: ev.BtnTr, aliases: []string{"R1"}},
	{name: "LT", code: ev.BtnTl2, aliases: []string{"L2"}},
	{name: "RT", code: ev.BtnTr2, aliases: []string{"R2"}},
	{name: "LS", code: ev.BtnThumbl, aliases: []string{"L3"}},
	{name: "RS", code: ev.BtnThumbr, aliases: []string{"R3"}},
	{name: "Start", code: ev.BtnStart},
	{name: "Select", code: ev.BtnSelect, aliases: []string{"Back"}},
	{name: "Mode", code: ev.BtnMode, aliases: []string{"Guide", "Home"}},
	{name: "DPadUp", code: ev.BtnDpadUp},
	{name: "DPadDown", code: ev.BtnDpadDown},
	{name: "DPadLeft", code: ev.BtnDpadLeft},
	{name: "DPadRight", code: ev.BtnDpadRight},
}

// axisTable — оси геймпада (EV_ABS). Курки LT/RT как оси соответствуют ABS_Z/ABS_RZ (как у xpad).
var axisTable = []entry{
	{name: "LX", code: ev.AbsX},
	{name: "LY", code: ev.AbsY},
	{name: "RX", code: ev.AbsRx},
	{name: "RY", code: ev.AbsRy},
	{name: "LT", code: ev.AbsZ},
	{name: "RT", code: ev.AbsRz},
	{name: "DPadX", code: ev.AbsHat0x},
	{name: "DPadY", code: ev.AbsHat0y},
}

// sidePairs — пары «левый ↔ правый» для модификаторов без стороны.
var sidePairs = map[uint16]uint16{
	ev.KeyLeftctrl:  ev.KeyRightctrl,
	ev.KeyLeftshift: ev.KeyRightshift,
	ev.KeyLeftalt:   ev.KeyRightalt,
	ev.KeyLeftmeta:  ev.KeyRightmeta,
}
