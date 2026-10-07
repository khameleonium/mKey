package setup

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"

	ev "github.com/khameleonium/mKey/internal/lib/evdev"
)

// osProbe — sysProbe для настоящей системы.
type osProbe struct{}

// KernelRelease возвращает версию ядра через uname.
func (osProbe) KernelRelease() (string, error) {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "", err
	}
	return strings.TrimRight(string(u.Release[:]), "\x00"), nil
}

// Exists сообщает, существует ли путь.
func (osProbe) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// CanOpen проверяет доступ к файлу и сразу закрывает его.
// Запись — простое открытие: для /dev/uinput открытие без ioctl не создаёт устройство и ничего не отправляет.
// Чтение — открытие устройства так же, как это делает модуль input (с чтением сведений через ioctl):
// так диагностика видит не только нехватку прав, но и устройства, которые mKey не сможет разобрать.
func (osProbe) CanOpen(path string, write bool) error {
	// Проверка записи (uinput).
	if write {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	}

	// Проверка чтения устройства ввода.
	d, err := ev.Open(path)
	if err != nil {
		return err
	}
	return d.Close()
}

// ProcDevices читает список устройств ввода из /proc.
func (osProbe) ProcDevices() ([]ev.ProcDevice, error) { return ev.ReadProcDevices() }

// Executable возвращает путь к текущему исполняемому файлу.
func (osProbe) Executable() (string, error) { return os.Executable() }
