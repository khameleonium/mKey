package contracts

import (
	"errors"

	"mkey/internal/lib/evdev"
)

// ErrNotGrabbed возвращается при отправке в passthrough-устройство, которое сейчас не захвачено.
var ErrNotGrabbed = errors.New("device is not grabbed")

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
	// SetHandler устанавливает синхронный обработчик событий всех устройств (модуль hotkeys).
	// Он вызывается прямо в потоке чтения и должен работать быстро (NFR-1).
	SetHandler(h InputHandler)
	// SetGrabPolicy задаёт, какие устройства захватывать (FR-HK-2); nil — не захватывать никакие.
	// Захват включается только когда на устройстве не зажата ни одна клавиша.
	SetGrabPolicy(policy func(InputDevice) bool)
	// Inject отправляет события в passthrough-копию захваченного устройства device
	// (например, чтобы отпустить модификаторы для приложений); ErrNotGrabbed, если устройство не захвачено.
	Inject(device string, events ...evdev.Event) error
	// GrabSuspended сообщает, что перехват отключён после экстренной остановки (SEC-1).
	GrabSuspended() bool
	// ResumeGrab снова разрешает перехват после экстренной остановки.
	ResumeGrab()
	// EmergencyStop выполняет экстренную остановку так же, как сочетание клавиш (SEC-1):
	// снимает перехват, останавливает макросы, отпускает клавиши и приостанавливает mKey.
	// reason — источник для журнала ("tray", "api").
	EmergencyStop(reason string)
}

// InputHandler — синхронный обработчик событий физических устройств.
type InputHandler interface {
	// HandleInput вызывается для каждого события каждого устройства. grabbed — устройство захвачено,
	// и событие попадёт в систему только через passthrough. Обработчик может изменить событие
	// (переназначение клавиши) и вернуть drop = true, чтобы не передавать его в систему
	// (для незахваченных устройств drop игнорируется).
	HandleInput(device string, e *evdev.Event, grabbed bool) (drop bool)
}
