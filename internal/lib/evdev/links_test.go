package evdev

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadLinks проверяет чтение постоянных имён: только ссылки на eventN, из нескольких — первая
// по алфавиту, отсутствующие папки (система без udev) — не ошибка.
func TestReadLinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Папки by-id и by-path с относительными ссылками, как их создаёт udev.
	links := map[string]string{
		"by-id/usb-Mouse-event-mouse":                "../event6",
		"by-id/usb-Mouse-mouse":                      "../mouse2",
		"by-path/pci-0000:00:14.0-usbv2-0:3.2-event": "../event6",
		"by-path/pci-0000:00:14.0-usb-0:3.2-event":   "../event6",
		"by-path/platform-i8042-serio-0-event-kbd":   "../event3",
	}
	for name, target := range links {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}

	got := ReadLinks(root)
	ev6, ev3 := got[filepath.Join(root, "event6")], got[filepath.Join(root, "event3")]
	if ev6.ByID != "usb-Mouse-event-mouse" || ev6.ByPath != "pci-0000:00:14.0-usb-0:3.2-event" {
		t.Errorf("event6 = %+v", ev6)
	}
	if ev3.ByID != "" || ev3.ByPath != "platform-i8042-serio-0-event-kbd" {
		t.Errorf("event3 = %+v", ev3)
	}
	if len(got) != 2 {
		t.Errorf("got = %+v", got)
	}

	// Без папок — пустой результат.
	if len(ReadLinks(t.TempDir())) != 0 {
		t.Error("expected no links")
	}
}
