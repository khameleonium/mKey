package evdev

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// ProcDevicesPath — файл ядра со списком всех устройств ввода.
// Он доступен на чтение всем пользователям, поэтому позволяет показать список устройств
// ещё до того, как пользователь выдал mKey права на /dev/input/event* (диагностика, мастер).
const ProcDevicesPath = "/proc/bus/input/devices"

// ProcDevice — описание устройства из /proc/bus/input/devices.
type ProcDevice struct {
	// ID — шина, производитель, модель, версия (строка "I:").
	ID ID `json:"id"`
	// Name — имя устройства (строка "N:").
	Name string `json:"name"`
	// Phys — физический путь (строка "P:").
	Phys string `json:"phys,omitempty"`
	// Sysfs — путь в sysfs (строка "S:").
	Sysfs string `json:"sysfs,omitempty"`
	// Uniq — уникальный идентификатор (строка "U:").
	Uniq string `json:"uniq,omitempty"`
	// Handlers — обработчики ядра (строка "H:"), например ["sysrq", "kbd", "event3"].
	Handlers []string `json:"handlers,omitempty"`
}

// EventPath возвращает путь /dev/input/eventN устройства или пустую строку, если обработчика evdev нет.
func (p ProcDevice) EventPath() string {
	for _, h := range p.Handlers {
		if strings.HasPrefix(h, "event") {
			return "/dev/input/" + h
		}
	}
	return ""
}

// ReadProcDevices читает и разбирает /proc/bus/input/devices.
func ReadProcDevices() ([]ProcDevice, error) {
	f, err := os.Open(ProcDevicesPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseProcDevices(f)
}

// ParseProcDevices разбирает содержимое /proc/bus/input/devices: блоки строк
// вида "X: ...", разделённые пустыми строками.
func ParseProcDevices(r io.Reader) ([]ProcDevice, error) {
	var (
		devs []ProcDevice
		cur  *ProcDevice
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		// Пустая строка завершает блок текущего устройства.
		if line == "" {
			if cur != nil {
				devs = append(devs, *cur)
				cur = nil
			}
			continue
		}
		if cur == nil {
			cur = &ProcDevice{}
		}

		// Разбираем строку по её префиксу.
		prefix, rest, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch prefix {
		case "I":
			cur.ID = parseProcID(rest)
		case "N":
			cur.Name = unquote(strings.TrimPrefix(rest, "Name="))
		case "P":
			cur.Phys = strings.TrimPrefix(rest, "Phys=")
		case "S":
			cur.Sysfs = strings.TrimPrefix(rest, "Sysfs=")
		case "U":
			cur.Uniq = strings.TrimPrefix(rest, "Uniq=")
		case "H":
			cur.Handlers = strings.Fields(strings.TrimPrefix(rest, "Handlers="))
		}
	}

	// Последний блок может не заканчиваться пустой строкой.
	if cur != nil {
		devs = append(devs, *cur)
	}
	return devs, sc.Err()
}

// parseProcID разбирает строку "Bus=0003 Vendor=046d Product=c52b Version=0111".
func parseProcID(s string) ID {
	var id ID
	for _, field := range strings.Fields(s) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(val, 16, 16)
		if err != nil {
			continue
		}
		switch key {
		case "Bus":
			id.Bustype = uint16(n)
		case "Vendor":
			id.Vendor = uint16(n)
		case "Product":
			id.Product = uint16(n)
		case "Version":
			id.Version = uint16(n)
		}
	}
	return id
}

// unquote убирает обрамляющие двойные кавычки, если они есть.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
