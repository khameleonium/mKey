package contracts

import (
	"context"
	"errors"
	"time"

	"mkey/internal/lib/evdev"
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
}
