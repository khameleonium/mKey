package session

import "testing"

// TestDetect проверяет определение сессии и композитора для типичных окружений.
func TestDetect(t *testing.T) {
	t.Parallel()

	// Таблица: окружение → ожидаемые тип и композитор.
	cases := []struct {
		name            string
		env             map[string]string
		typ, compositor string
	}{
		{"kde wayland", map[string]string{"XDG_SESSION_TYPE": "wayland", "XDG_CURRENT_DESKTOP": "KDE", "WAYLAND_DISPLAY": "wayland-0"}, "wayland", "kde"},
		{"ubuntu gnome", map[string]string{"XDG_SESSION_TYPE": "wayland", "XDG_CURRENT_DESKTOP": "ubuntu:GNOME"}, "wayland", "gnome"},
		{"sway", map[string]string{"WAYLAND_DISPLAY": "wayland-1", "SWAYSOCK": "/run/user/1000/sway.sock"}, "wayland", "sway"},
		{"hyprland", map[string]string{"XDG_SESSION_TYPE": "wayland", "XDG_CURRENT_DESKTOP": "Hyprland", "HYPRLAND_INSTANCE_SIGNATURE": "abc"}, "wayland", "hyprland"},
		{"cinnamon x11", map[string]string{"XDG_SESSION_TYPE": "x11", "XDG_CURRENT_DESKTOP": "X-Cinnamon", "DISPLAY": ":0"}, "x11", "cinnamon"},
		{"bare x11", map[string]string{"DISPLAY": ":0"}, "x11", "unknown"},
		{"console", map[string]string{"XDG_SESSION_TYPE": "tty"}, "tty", "unknown"},
		{"nothing", map[string]string{}, "unknown", "unknown"},
	}

	// Прогоняем случаи с подменённым окружением.
	for _, c := range cases {
		got := Detect(func(k string) string { return c.env[k] })
		if got.Type != c.typ || got.Compositor != c.compositor {
			t.Errorf("%s: Detect = %s/%s, want %s/%s", c.name, got.Type, got.Compositor, c.typ, c.compositor)
		}
	}
}
