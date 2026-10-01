package contracts

import "mkey/internal/lib/evdev"

// Inspector — подробные сведения об устройствах ввода (модуль inspector, FR-DEV-1): постоянные
// имена, все кнопки и оси с именами mKey и ядра. По ним человек узнаёт, как устройство
// называется в макросах и что оно умеет. Собственные виртуальные устройства mKey не показываются.
type Inspector interface {
	// Devices возвращает сведения обо всех открытых устройствах, по пути.
	Devices() []DeviceDetails
	// Find ищет устройства по ссылке ref: путь ("/dev/input/event6"), имя файла ("event6") или
	// постоянное имя by-id/by-path — точно, иначе часть названия (без учёта регистра).
	// Точное совпадение пути или имени файла даёт одно устройство; иначе — все подходящие.
	Find(ref string) []DeviceDetails
	// Label возвращает имя для макросов кнопки или оси без стандартного имени
	// ("UnKey001", "UnKey2.001", "UnKey.Axis01", FR-DEV-2); "" — у неё нет авто-ID.
	Label(path string, typ, code uint16) string
	// AutoIDMode возвращает режим авто-ID: "smart", "all" или "unusual".
	AutoIDMode() string
	// SetAutoIDMode меняет режим (сразу раздаёт имена подходящим устройствам); уже выданные
	// имена не отбираются. Ошибка — неизвестный режим.
	SetAutoIDMode(mode string) error
}

// TopicAutoIDsChanged — инспектор выдал устройствам новые авто-ID или узнал их снова
// (окно обновляет список устройств); Payload: nil.
const TopicAutoIDsChanged = "inspector.auto_ids_changed"

// PlaceDevices — файл devices.yaml с авто-ID устройств и кнопок (место «Где что лежит»).
const PlaceDevices = "devices"

// DeviceDetails — устройство ввода со всеми подробностями.
type DeviceDetails struct {
	InputDevice
	// Links — постоянные имена udev (by-id, by-path); пусты на системах без udev.
	evdev.Links
	// Bus — шина подключения словом ("USB", "Bluetooth", "i8042").
	Bus string `json:"bus"`
	// AutoID — авто-ID устройства (UnKey, UnKey2…, FR-DEV-2); пусто — устройство его не получило.
	AutoID string `json:"auto_id,omitempty"`
	// Keys — клавиши и кнопки (EV_KEY); Rel — относительные оси (мышь, колесо);
	// Axes — абсолютные оси с диапазонами (стики, курки, крестовины, тачпад).
	Keys []DeviceControl `json:"keys,omitempty"`
	Rel  []DeviceControl `json:"rel,omitempty"`
	Axes []DeviceAxis    `json:"axes,omitempty"`
	// Switches, LEDs, FF — переключатели (крышка ноутбука), индикаторы, виды отдачи (force feedback).
	Switches []DeviceControl `json:"switches,omitempty"`
	LEDs     []DeviceControl `json:"leds,omitempty"`
	FF       []DeviceControl `json:"ff,omitempty"`
	// Props — свойства устройства (INPUT_PROP_POINTER, INPUT_PROP_DIRECT…).
	Props []string `json:"props,omitempty"`
}

// DeviceControl — одна кнопка, ось или индикатор устройства.
type DeviceControl struct {
	// Code — код события; Kernel — имя кода в ядре ("KEY_A", "BTN_TRIGGER_HAPPY3").
	Code   uint16 `json:"code"`
	Kernel string `json:"kernel"`
	// Name — имя для макросов ("A", "Mouse0", "South", "LX"); пусто — у mKey имени нет
	// (такие кнопки получают авто-ID, FR-DEV-2).
	Name string `json:"name,omitempty"`
	// Label — авто-ID кнопки или оси без стандартного имени для макросов ("UnKey001",
	// "UnKey2.001", "UnKey.Axis01"); пусто — у неё есть Name или устройство без авто-ID.
	Label string `json:"label,omitempty"`
}

// DeviceAxis — абсолютная ось с параметрами (struct input_absinfo).
type DeviceAxis struct {
	DeviceControl
	evdev.AbsInfo
}
