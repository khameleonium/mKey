package contracts

import "mkey/internal/lib/evdev"

// Темы шины, которые публикует модуль ввода.
const (
	// TopicInputDeviceAdded — подключено (или стало доступно) устройство; Payload: InputDevice.
	TopicInputDeviceAdded = "input.device_added"
	// TopicInputDeviceRemoved — устройство отключено; Payload: InputDevice.
	TopicInputDeviceRemoved = "input.device_removed"
	// TopicInputAccessChanged — изменилась доступность устройств; Payload: InputStatus.
	TopicInputAccessChanged = "input.access_changed"
)

// InputDevice — физическое устройство ввода, которое видит mKey.
type InputDevice struct {
	// Info — сведения об устройстве (имя, VID:PID, возможности).
	Info evdev.Info `json:"info"`
	// Kinds — классы устройства (клавиатура, мышь, геймпад…).
	Kinds []evdev.Kind `json:"kinds"`
}

// InputEvent — событие физического устройства.
type InputEvent struct {
	// Device — путь устройства-источника, например "/dev/input/event3".
	Device string
	// Event — само событие.
	Event evdev.Event
}

// InputStatus — доступность физических устройств для mKey.
type InputStatus struct {
	// Open — число открытых (читаемых) устройств.
	Open int `json:"open"`
	// Denied — пути устройств, к которым нет доступа (нужно выдать права, SPEC §5.11).
	Denied []string `json:"denied,omitempty"`
}

// InputSource — источник событий физических устройств (модуль input).
// Собственные виртуальные устройства mKey в нём никогда не появляются (защита от петель).
type InputSource interface {
	// Devices возвращает открытые устройства, отсортированные по пути.
	Devices() []InputDevice
	// Status возвращает текущую доступность устройств.
	Status() InputStatus
	// Subscribe подписывает на события всех устройств. Канал имеет буфер buffer;
	// если подписчик не успевает читать, события для него отбрасываются.
	// Функция отписки закрывает канал.
	Subscribe(buffer int) (<-chan InputEvent, func())
}
