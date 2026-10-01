package evdev

import (
	"os"
	"path/filepath"
	"strings"
)

// Links — постоянные имена устройства, которые создаёт udev в /dev/input/by-id и by-path.
// В отличие от eventN они не меняются после переподключения и перезагрузки (FR-DEV-6).
// На системах без udev (например, с mdev) папок может не быть — тогда поля пустые.
type Links struct {
	// ByID — имя по модели и серийному номеру ("usb-Logitech_USB_Optical_Mouse-event-mouse").
	ByID string `json:"by_id,omitempty"`
	// ByPath — имя по порту подключения ("pci-0000:00:14.0-usb-0:3.2:1.0-event-mouse").
	ByPath string `json:"by_path,omitempty"`
}

// ReadLinks читает постоянные имена устройств из папок <root>/by-id и <root>/by-path
// (root обычно "/dev/input") и возвращает их по пути устройства ("/dev/input/event6").
// Учитываются только ссылки на eventN; если на устройство ведёт несколько ссылок
// (by-path: «usb-…» и «usbv2-…»), берётся первая по алфавиту — результат всегда один и тот же.
func ReadLinks(root string) map[string]Links {
	out := map[string]Links{}
	for _, dir := range []string{"by-id", "by-path"} {
		// Папки может не быть (нет udev) — это не ошибка.
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}

		// Символические ссылки на eventN: имя ссылки — по устройству, на которое она ведёт.
		for _, e := range entries {
			target, err := os.Readlink(filepath.Join(root, dir, e.Name()))
			if err != nil || !strings.HasPrefix(filepath.Base(target), "event") {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, dir, target)
			}
			path := filepath.Clean(target)
			l := out[path]
			switch {
			case dir == "by-id" && (l.ByID == "" || e.Name() < l.ByID):
				l.ByID = e.Name()
			case dir == "by-path" && (l.ByPath == "" || e.Name() < l.ByPath):
				l.ByPath = e.Name()
			}
			out[path] = l
		}
	}
	return out
}
