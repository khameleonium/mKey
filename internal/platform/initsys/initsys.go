// Package initsys — способы автозапуска демона mKey при входе в систему
// (contracts.Autostart, FR-INST-2 шаг 5, FR-INST-7).
//
// Порядок предпочтения (выбирается первый подходящий):
//  1. xdg — стандартный автозапуск рабочих столов (~/.config/autostart/mkey.desktop):
//     KDE, GNOME, Cinnamon, Xfce, MATE, LXQt и др.; работает и без systemd;
//  2. systemd-user — пользовательская служба systemd, если сеанс ею управляет
//     (graphical-session.target активна): перезапускает демон при сбое;
//  3. конфиг композитора — Sway, Hyprland, river, labwc, niri: строка запуска в помеченном
//     блоке mKey (только с согласия пользователя; блок убирается при удалении);
//  4. manual — показать пользователю, что добавить в автозапуск самому.
//
// Все файлы пишутся через contracts.FileWriter — они попадают в манифест установки.
package initsys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"mkey/internal/contracts"
	"mkey/internal/lib/fileblock"
)

// All возвращает все встроенные способы автозапуска в порядке предпочтения.
func All() []contracts.Autostart {
	return []contracts.Autostart{
		xdg{},
		systemdUser{},
		compositor{id: "sway", file: "sway/config", comment: "#", line: func(exe string) string { return "exec " + strconv.Quote(exe) + " daemon" }},
		compositor{id: "hyprland", file: "hypr/hyprland.conf", comment: "#", line: func(exe string) string { return "exec-once = " + strconv.Quote(exe) + " daemon" }},
		compositor{id: "river", file: "river/init", comment: "#", line: func(exe string) string { return strconv.Quote(exe) + " daemon &" }},
		compositor{id: "labwc", file: "labwc/autostart", comment: "#", create: true, line: func(exe string) string { return strconv.Quote(exe) + " daemon >/dev/null 2>&1 &" }},
		compositor{id: "niri", file: "niri/config.kdl", comment: "//", line: func(exe string) string { return "spawn-at-startup " + strconv.Quote(exe) + " \"daemon\"" }},
		manual{},
	}
}

// Select возвращает первый подходящий способ.
func Select(all []contracts.Autostart, env contracts.AutostartEnv) contracts.Autostart {
	for _, a := range all {
		if a.Available(env) {
			return a
		}
	}
	return manual{}
}

// meta собирает метаданные способа с i18n-ключом имени.
func meta(id string) contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: id, NameKey: "platform.autostart." + id, Provider: "platform"}
}

// bareCompositors — окружения без стандартного автозапуска XDG.
var bareCompositors = []string{"sway", "hyprland", "river", "labwc", "niri", "dwl", "i3", "wayfire"}

// xdg — стандартный автозапуск рабочего стола.
type xdg struct{}

// Meta возвращает метаданные.
func (xdg) Meta() contracts.ExtensionMeta { return meta("xdg") }

// Available: графическая сессия полноценного рабочего стола (не «голый» композитор).
func (xdg) Available(env contracts.AutostartEnv) bool {
	return env.Session.Graphical() && env.Session.Desktop != "" && !slices.Contains(bareCompositors, env.Session.Compositor)
}

// Target возвращает путь файла автозапуска.
func (xdg) Target(env contracts.AutostartEnv) string {
	return filepath.Join(env.ConfigHome, "autostart", "mkey.desktop")
}

// Installed сообщает, есть ли файл автозапуска.
func (x xdg) Installed(env contracts.AutostartEnv) bool { return exists(x.Target(env)) }

// Install создаёт файл автозапуска.
func (x xdg) Install(_ context.Context, env contracts.AutostartEnv) error {
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=mKey\n" +
		"Comment=mKey input macros (background service)\n" +
		"Exec=" + strconv.Quote(env.Exe) + " daemon\n" +
		"Icon=mkey\n" +
		"Terminal=false\n" +
		"NoDisplay=true\n" +
		"X-GNOME-Autostart-enabled=true\n" +
		"X-KDE-autostart-after=panel\n"
	return env.Files.WriteFile(x.Target(env), []byte(entry), 0o644)
}

// Uninstall удаляет файл автозапуска.
func (x xdg) Uninstall(_ context.Context, env contracts.AutostartEnv) error {
	return env.Files.RemoveFile(x.Target(env))
}

// Start не поддерживается: рабочий стол запускает демон только при входе.
func (xdg) Start(context.Context, contracts.AutostartEnv) error { return contracts.ErrUnsupported }

// systemdUser — пользовательская служба systemd.
type systemdUser struct{}

// Meta возвращает метаданные.
func (systemdUser) Meta() contracts.ExtensionMeta { return meta("systemd-user") }

// Available: systemd управляет графическим сеансом пользователя.
func (systemdUser) Available(env contracts.AutostartEnv) bool {
	if env.Platform.Init != "systemd" || env.Runner == nil {
		return false
	}
	return env.Runner.Run(context.Background(), "systemctl", "--user", "is-active", "--quiet", "graphical-session.target") == nil
}

// Target возвращает путь файла службы.
func (systemdUser) Target(env contracts.AutostartEnv) string {
	return filepath.Join(env.ConfigHome, "systemd", "user", "mkey.service")
}

// Installed сообщает, есть ли файл службы.
func (s systemdUser) Installed(env contracts.AutostartEnv) bool { return exists(s.Target(env)) }

// Install создаёт службу и включает её для графического сеанса.
func (s systemdUser) Install(ctx context.Context, env contracts.AutostartEnv) error {
	unit := "[Unit]\n" +
		"Description=mKey input macros\n" +
		"PartOf=graphical-session.target\n" +
		"After=graphical-session.target\n\n" +
		"[Service]\n" +
		"ExecStart=" + strconv.Quote(env.Exe) + " daemon\n" +
		"Restart=on-failure\n" +
		"RestartSec=2\n\n" +
		"[Install]\n" +
		"WantedBy=graphical-session.target\n"
	if err := env.Files.WriteFile(s.Target(env), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := env.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return env.Runner.Run(ctx, "systemctl", "--user", "enable", "mkey.service")
}

// Uninstall выключает и удаляет службу.
func (s systemdUser) Uninstall(ctx context.Context, env contracts.AutostartEnv) error {
	_ = env.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", "mkey.service")
	if err := env.Files.RemoveFile(s.Target(env)); err != nil {
		return err
	}
	return env.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
}

// Start запускает службу (systemd будет перезапускать её при сбое).
func (systemdUser) Start(ctx context.Context, env contracts.AutostartEnv) error {
	return env.Runner.Run(ctx, "systemctl", "--user", "start", "mkey.service")
}

// compositor — строка запуска в конфиге композитора.
type compositor struct {
	// id — имя композитора (как в SessionInfo.Compositor) и ID способа.
	id string
	// file — путь конфига относительно $XDG_CONFIG_HOME.
	file string
	// comment — префикс комментария для маркеров блока.
	comment string
	// create — создать конфиг, если его нет (labwc: файл автозапуска необязателен).
	create bool
	// line — строка запуска для пути к mkey.
	line func(exe string) string
}

// Meta возвращает метаданные.
func (c compositor) Meta() contracts.ExtensionMeta { return meta(c.id) }

// Available: сеанс этого композитора и (если файл не создаётся) существующий конфиг.
func (c compositor) Available(env contracts.AutostartEnv) bool {
	return env.Session.Compositor == c.id && (c.create || exists(c.Target(env)))
}

// Target возвращает путь конфига.
func (c compositor) Target(env contracts.AutostartEnv) string {
	return filepath.Join(env.ConfigHome, c.file)
}

// Installed сообщает, есть ли в конфиге блок mKey.
func (c compositor) Installed(env contracts.AutostartEnv) bool {
	data, err := os.ReadFile(c.Target(env))
	return err == nil && fileblock.Has(string(data))
}

// Install добавляет блок mKey со строкой запуска.
func (c compositor) Install(_ context.Context, env contracts.AutostartEnv) error {
	return env.Files.SetBlock(c.Target(env), c.comment, []string{c.line(env.Exe)})
}

// Uninstall убирает блок mKey.
func (c compositor) Uninstall(_ context.Context, env contracts.AutostartEnv) error {
	return env.Files.RemoveBlock(c.Target(env), c.comment)
}

// Start не поддерживается: композитор выполняет строку только при запуске.
func (compositor) Start(context.Context, contracts.AutostartEnv) error {
	return contracts.ErrUnsupported
}

// manual — ручная настройка: пользователь добавляет команду в автозапуск сам.
type manual struct{}

// Meta возвращает метаданные.
func (manual) Meta() contracts.ExtensionMeta { return meta("manual") }

// Available: всегда (последний вариант).
func (manual) Available(contracts.AutostartEnv) bool { return true }

// Target возвращает команду, которую нужно добавить в автозапуск.
func (manual) Target(env contracts.AutostartEnv) string { return strconv.Quote(env.Exe) + " daemon" }

// Installed: mKey не может проверить ручную настройку.
func (manual) Installed(contracts.AutostartEnv) bool { return false }

// Install ничего не делает и возвращает команду для ручной настройки.
func (m manual) Install(_ context.Context, env contracts.AutostartEnv) error {
	return &contracts.ManualActionError{Command: m.Target(env)}
}

// Uninstall ничего не делает.
func (manual) Uninstall(context.Context, contracts.AutostartEnv) error { return nil }

// Start не поддерживается.
func (manual) Start(context.Context, contracts.AutostartEnv) error { return contracts.ErrUnsupported }

// exists сообщает, существует ли путь.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Describe возвращает понятное описание способа и того, что он изменит (для вывода в CLI).
func Describe(a contracts.Autostart, env contracts.AutostartEnv) string {
	return fmt.Sprintf("%s (%s)", a.Meta().ID, strings.TrimSpace(a.Target(env)))
}
