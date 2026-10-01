package contracts

import (
	"mkey/internal/lib/devmap"
	"mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// DeviceProfile — профиль устройства в точке расширения PointDeviceProfile (FR-DEV-7, ADR-0027):
// готовые имена кнопок и осей для модели. Встроенные профили регистрирует inspector, их могут
// добавлять и плагины; профили человека из папки профилей важнее зарегистрированных.
type DeviceProfile interface {
	Extension
	// Profile возвращает данные профиля.
	Profile() *devmap.Profile
}

// StaticProfile — готовая реализация DeviceProfile.
type StaticProfile struct {
	M ExtensionMeta
	P *devmap.Profile
}

// Meta возвращает метаданные профиля.
func (p StaticProfile) Meta() ExtensionMeta { return p.M }

// Profile возвращает данные профиля.
func (p StaticProfile) Profile() *devmap.Profile { return p.P }

// DeviceKey — клавиша или кнопка, возможно конкретного устройства: {A} — с любого устройства,
// {UnKey2.001} — только с устройства UnKey2 (FR-DEV-2).
type DeviceKey struct {
	keys.Key
	// Device — авто-ID устройства ("UnKey2"); пусто — любое устройство.
	Device string `json:"device,omitempty"`
}

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
	// ResolveKey находит кнопку устройства для макросов: device — авто-ID ("UnKey2", без учёта
	// регистра), button — номер ("001") или стандартное имя клавиши ("A"). Работает и для
	// отключённого устройства (по devices.yaml). Ошибка — *dsl.Error (dsl.unknown_device,
	// dsl.unknown_button).
	ResolveKey(device, button string) (DeviceKey, error)
	// DeviceOf возвращает авто-ID подключённого устройства по пути ("" — у него нет авто-ID).
	DeviceOf(path string) string
	// Rename даёт имя устройству (control == "") или его кнопке или оси (FR-DEV-3): device —
	// авто-ID, имя, путь, eventN или часть названия; control — номер ("001", "Axis01") или
	// текущее имя; name == "" — убрать имя. Авто-ID и номера остаются рабочими навсегда.
	// Устройству без авто-ID он выдаётся. Ошибка — *devmap.NameError.
	Rename(device, control, name string) error
	// ExportProfile составляет профиль из имён устройства (FR-DEV-5): YAML и предлагаемое имя
	// файла. Ошибка — *devmap.NameError (нет такого устройства, неоднозначно).
	ExportProfile(device string) (data []byte, filename string, err error)
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

// PlaceProfiles — папка профилей устройств человека (место «Где что лежит»).
const PlaceProfiles = "profiles"

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
	// DeviceName — имя устройства, данное человеком (FR-DEV-3); пусто — нет.
	DeviceName string `json:"device_name,omitempty"`
	// Profile — применённый профиль устройства ("builtin/…", "user/…"; FR-DEV-7); пусто — нет.
	Profile string `json:"profile,omitempty"`
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
	// Label — имя кнопки или оси без стандартного имени для макросов ("UnKey001",
	// "UnKey2.001", "UnKey.Axis01", после переименования — "Sega.Start"); пусто — у неё есть
	// Name или устройство без авто-ID.
	Label string `json:"label,omitempty"`
	// Number — номер кнопки или оси в devices.yaml ("001", "Axis01"; для переименования);
	// CustomName — имя, данное ей человеком ("Start"; пусто — нет).
	Number     string `json:"number,omitempty"`
	CustomName string `json:"custom_name,omitempty"`
}

// DeviceAxis — абсолютная ось с параметрами (struct input_absinfo).
type DeviceAxis struct {
	DeviceControl
	evdev.AbsInfo
}
