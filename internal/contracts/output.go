package contracts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"mkey/internal/lib/dsl"
	"mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/lib/project"
)

// ErrOutputUnavailable возвращается, когда виртуальное устройство нельзя создать
// (нет модуля uinput или прав на /dev/uinput). Пользователю нужно пройти настройку прав.
var ErrOutputUnavailable = errors.New("virtual input is unavailable")

// Префиксы, по которым mKey узнаёт собственные виртуальные устройства и не читает их (SPEC §4.1).
const (
	// VirtualNamePrefix — начало имени каждого виртуального устройства mKey.
	VirtualNamePrefix = "mKey "
	// VirtualPhysPrefix — начало физического пути каждого виртуального устройства mKey.
	VirtualPhysPrefix = "mkey/"
)

// VirtualDevice — виртуальное устройство mKey, в которое отправляются нажатия.
// Каждое устройство помнит, какие клавиши на нём зажаты, чтобы их можно было гарантированно отпустить.
type VirtualDevice interface {
	// Name возвращает имя устройства в системе, например "mKey Keyboard".
	Name() string
	// Press зажимает клавишу или кнопку code (EV_KEY).
	Press(ctx context.Context, code uint16) error
	// Release отпускает клавишу или кнопку code.
	Release(ctx context.Context, code uint16) error
	// Tap нажимает code, держит hold и отпускает. Клавиша отпускается даже при отмене ctx.
	Tap(ctx context.Context, code uint16, hold time.Duration) error
	// Emit отправляет произвольные события; SYN_REPORT в конце добавляется автоматически.
	Emit(ctx context.Context, events ...evdev.Event) error
	// Held возвращает коды зажатых сейчас клавиш.
	Held() []uint16
	// ReleaseAll отпускает все зажатые клавиши. Работает всегда, без контекста.
	ReleaseAll() error
}

// OutputStatus — доступность виртуального ввода.
type OutputStatus struct {
	// Available — виртуальные устройства созданы и готовы.
	Available bool `json:"available"`
	// Error — причина недоступности (пусто, если всё в порядке).
	Error string `json:"error,omitempty"`
}

// VirtualDevices — менеджер виртуальных устройств (модуль output).
type VirtualDevices interface {
	// Keyboard возвращает основную виртуальную клавиатуру (ErrOutputUnavailable, если её нельзя создать).
	Keyboard() (VirtualDevice, error)
	// Mouse возвращает основную виртуальную мышь (ErrOutputUnavailable, если её нельзя создать).
	Mouse() (VirtualDevice, error)
	// Status возвращает доступность виртуального ввода.
	Status() OutputStatus
	// ReleaseAll отпускает все зажатые клавиши на всех виртуальных устройствах (SEC-2).
	ReleaseAll() error
	// CenterPointer ставит указатель мыши в центр рабочего стола — калибровка перед записью
	// и воспроизведением (FR-REC-5). Работает в любом окружении (X11 и Wayland): через
	// виртуальное устройство с абсолютными координатами, как у графического планшета.
	CenterPointer(ctx context.Context) error
}

// VirtualDeviceManager — виртуальные устройства проектов (модуль output, FR-VD-1, -2): геймпады,
// джойстик, тач-экран и т.п., описанные в разделе virtual_devices включённых проектов. Устройство
// создаётся, когда проект включают, и исчезает, когда выключают.
type VirtualDeviceManager interface {
	// Device возвращает виртуальное устройство проекта по имени ("pad2", без учёта регистра).
	// Ошибка — ErrUnknownVirtual (нет такого) или ErrOutputUnavailable (создать не удалось).
	Device(name string) (VirtualDevice, error)
	// Resolve находит кнопку или ось устройства по имени для макросов: "South", "LB",
	// "BTN_TRIGGER", "LX". axis — это ось ({pad2.LX=0.5}). Ошибка — ErrUnknownVirtual или
	// ErrUnknownControl (у устройства нет такой кнопки или оси).
	Resolve(device, control string) (code uint16, axis bool, err error)
	// ResolveIn — то же по описанию устройства из проекта (устройство может быть ещё не создано:
	// проект выключен). Ошибка — ErrUnknownControl или ошибка описания.
	ResolveIn(v project.VirtualDevice, control string) (code uint16, axis bool, err error)
	// Validate проверяет описание устройства из проекта: шаблон и набор кнопок и осей.
	Validate(v project.VirtualDevice) error
	// List возвращает виртуальные устройства включённых проектов.
	List() []VirtualDeviceInfo
	// Templates возвращает шаблоны устройств (xbox360, ds4…) по порядку.
	Templates() []string
	// TemplateInfo возвращает кнопки и оси шаблона id именами для макросов (для мастеров окна);
	// false — шаблона нет или его состав задаёт проект (custom).
	TemplateInfo(id string) (VirtualTemplateInfo, bool)
	// Clone создаёт временную копию устройства с возможностями setup (для повтора записи геймпада
	// или сенсорного экрана, T12.2): имя в системе — "mKey <name>" (mKey его не читает). close
	// отпускает всё нажатое и уничтожает копию.
	Clone(name string, setup evdev.Setup) (dev VirtualDevice, close func() error, err error)
}

// VirtualTemplateInfo — состав шаблона виртуального устройства: кнопки ("South", "LB", "LT",
// "DPadUp") и оси ("LX", "RY") — как их пишут в макросах и привязках ({pad2.South}).
type VirtualTemplateInfo struct {
	ID      string   `json:"id"`
	Buttons []string `json:"buttons"`
	Axes    []string `json:"axes"`
}

// TouchSetter — сенсорный экран (виртуальное устройство шаблона touchscreen): одно касание
// пальцем. Координаты — доли экрана 0…1 (0,0 — левый верхний угол). TouchDown начинает касание
// (или переносит палец, если он уже касается), TouchMove двигает палец, TouchUp отрывает его
// (без касания — ничего не делает). ReleaseAll устройства тоже отрывает палец.
type TouchSetter interface {
	TouchDown(ctx context.Context, x, y float64) error
	TouchMove(ctx context.Context, x, y float64) error
	TouchUp(ctx context.Context) error
}

// TemplateTouchscreen — шаблон виртуального сенсорного экрана.
const TemplateTouchscreen = "touchscreen"

// ScreenInfo — размер рабочего стола в пикселях (для координат касаний в пикселях). Модуль
// desktop берёт его из настроек (modules.desktop.screen), адаптеры окружений (фаза 8) — сами.
// ErrUnsupported — размер неизвестен.
type ScreenInfo interface {
	ScreenSize(ctx context.Context) (width, height int, err error)
}

// AxisSetter — устройство с осями (виртуальный геймпад, джойстик): SetAxis ставит ось code в
// положение value — от −1 до 1 (стики, крестовина) или от 0 до 1 (курки).
type AxisSetter interface {
	SetAxis(ctx context.Context, code uint16, value float64) error
}

// VirtualDeviceInfo — виртуальное устройство проекта для окна и CLI.
type VirtualDeviceInfo struct {
	// Name — имя в макросах; Template — шаблон; Project — проект, который его описывает.
	Name     string `json:"name"`
	Template string `json:"template"`
	Project  string `json:"project"`
	// SystemName — имя в системе ("mKey pad2"); Node — файл устройства (/dev/input/eventN).
	SystemName string `json:"system_name"`
	Node       string `json:"node,omitempty"`
	// Error — почему устройство не создано (пусто — работает).
	Error string `json:"error,omitempty"`
}

var (
	// ErrUnknownVirtual — нет виртуального устройства с таким именем (проект выключен или нет такого).
	ErrUnknownVirtual = errors.New("unknown virtual device")
	// ErrUnknownControl — у виртуального устройства нет такой кнопки или оси.
	ErrUnknownControl = errors.New("unknown button or axis of the virtual device")
)

// BindingTarget — куда передаётся нажатие привязки (FR-VD-3): кнопка или ось устройства.
type BindingTarget struct {
	// Device — "keyboard"/"mouse" (устройства mKey) или имя виртуального устройства проекта
	// (в нижнем регистре).
	Device string
	// Code — код кнопки (EV_KEY, в кодах макросов) или оси (EV_ABS, если Axis).
	Code uint16
	Axis bool
	// Value — положение оси, пока нажата кнопка-источник (для оси).
	Value float64
}

// ParseBindingTo разбирает цель привязки to ("{pad2.South}", "{pad2.LX}", "{Space}"), без проверки
// настроек (их проверяет CompileBinding). own — виртуальные устройства того же проекта (они могут
// быть ещё не созданы); остальные имена ищутся среди включённых проектов (vd; nil — виртуальных
// устройств нет). Ошибка — *dsl.Error: dsl.unknown_device, dsl.unknown_button, dsl.unknown_key.
func ParseBindingTo(to string, own []project.VirtualDevice, vd VirtualDeviceManager) (BindingTarget, error) {
	// Одна клавиша или кнопка в скобках.
	refs, err := dsl.ParseHotkey(strings.TrimSpace(to))
	if err != nil {
		return BindingTarget{}, err
	}
	if len(refs) != 1 {
		return BindingTarget{}, dsl.NewError(dsl.Pos{}, dsl.ErrBadHotkey)
	}
	ref := refs[0]

	// Клавиша или кнопка мыши mKey.
	var t BindingTarget
	switch {
	case ref.Device == "" && ref.Code != nil:
		t = BindingTarget{Device: dsl.DeviceKeyboard, Code: *ref.Code}
	case ref.Device == "":
		k, ok := keys.Lookup(ref.Name)
		if !ok {
			return BindingTarget{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownKey, "name", ref.Name)
		}
		t = BindingTarget{Device: dsl.DeviceKeyboard, Code: k.Code}
		if k.Code >= evdev.BtnMouse && k.Code < evdev.BtnJoystick {
			t.Device = dsl.DeviceMouse
		}
	default:
		// Виртуальное устройство: своё (из того же проекта) или другого включённого проекта.
		var code uint16
		var axis bool
		err := fmt.Errorf("%w: %q", ErrUnknownVirtual, ref.Device)
		if i := slices.IndexFunc(own, func(v project.VirtualDevice) bool { return strings.EqualFold(v.Name, ref.Device) }); i >= 0 && vd != nil {
			code, axis, err = vd.ResolveIn(own[i], ref.Name)
		} else if vd != nil {
			code, axis, err = vd.Resolve(ref.Device, ref.Name)
		}
		switch {
		case errors.Is(err, ErrUnknownVirtual):
			return BindingTarget{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownDevice, "device", ref.Device)
		case err != nil:
			return BindingTarget{}, dsl.NewError(dsl.Pos{}, dsl.ErrUnknownButton, "device", ref.Device, "button", ref.Name)
		}
		t = BindingTarget{Device: strings.ToLower(ref.Device), Code: code, Axis: axis}
	}

	return t, nil
}
