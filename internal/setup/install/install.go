// Package install — установка файлов mKey в систему пользователя и удаление его данных
// (T4.1, T4.5, FR-INST-1, FR-INST-5).
//
// Что устанавливается (всё записывается через манифест, см. internal/setup/manifest):
//   - программа: ~/.local/bin/mkey (копия запущенного файла). Если mKey установлен в систему
//     (пакетом .deb/.rpm/AUR или вручную в /usr/bin — запущенный файл вне домашней папки), копия
//     не делается: программой считается системный файл, его обновляет и удаляет менеджер пакетов;
//   - ярлык в меню приложений: ~/.local/share/applications/mkey.desktop;
//   - иконка: ~/.local/share/icons/hicolor/scalable/apps/mkey.svg.
//
// Данные mKey при удалении: ~/.local/share/mkey (кроме записей при «сохранить настройки»),
// ~/.local/state/mkey (журнал, переменные), каталог времени выполнения; ~/.config/mkey —
// только при полном удалении.
//
// Пакет ничего не спрашивает у пользователя: диалог — в cmd/mkey (setup, uninstall).
package install

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"mkey/internal/contracts"
	"mkey/internal/lib/buildinfo"
	"mkey/internal/lib/paths"
	"mkey/internal/setup/manifest"
)

// icon — иконка mKey для меню приложений.
//
//go:embed assets/mkey.svg
var icon []byte

// Env — пути установки для текущего пользователя.
type Env struct {
	// Home — домашний каталог.
	Home string
	// ConfigHome — $XDG_CONFIG_HOME или ~/.config.
	ConfigHome string
	// DataHome — $XDG_DATA_HOME или ~/.local/share.
	DataHome string
	// Config, Data, State — каталоги mKey (…/mkey).
	Config, Data, State string
	// Runtime — каталог времени выполнения mKey.
	Runtime string
	// Exe — путь к запущенному файлу mkey.
	Exe string
}

// NewEnv определяет пути по переменным окружения.
func NewEnv(getenv func(string) string, runtime, exe string) Env {
	home := getenv("HOME")
	xdg := func(env, fallback string) string {
		if d := getenv(env); d != "" && filepath.IsAbs(d) {
			return d
		}
		return filepath.Join(home, fallback)
	}
	return Env{
		Home:       home,
		ConfigHome: xdg("XDG_CONFIG_HOME", ".config"),
		DataHome:   xdg("XDG_DATA_HOME", ".local/share"),
		Config:     paths.Config(getenv),
		Data:       paths.Data(getenv),
		State:      paths.State(getenv),
		Runtime:    runtime,
		Exe:        exe,
	}
}

// SystemDesktopFile — ярлык, который ставят пакеты mKey (packaging/): если он есть, свой ярлык
// в меню не создаётся (иначе в меню было бы два mKey).
const SystemDesktopFile = "/usr/share/applications/mkey.desktop"

// System сообщает, что mKey установлен в систему: запущенный файл лежит вне домашней папки
// (/usr/bin/mkey из пакета). Тогда программа не копируется в ~/.local/bin.
func (e Env) System() bool {
	if e.Exe == "" || e.Home == "" {
		return false
	}
	rel, err := filepath.Rel(e.Home, e.Exe)
	return err != nil || rel == ".." || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator)
}

// BinPath возвращает путь установленной программы: ~/.local/bin/mkey или, при установке
// в систему, сам запущенный файл.
func (e Env) BinPath() string {
	if e.System() {
		return e.Exe
	}
	return filepath.Join(e.Home, ".local", "bin", "mkey")
}

// ManifestPath возвращает путь манифеста установки.
func (e Env) ManifestPath() string { return filepath.Join(e.Data, manifest.FileName) }

// DesktopPath возвращает путь ярлыка в меню приложений.
func (e Env) DesktopPath() string {
	return filepath.Join(e.DataHome, "applications", "mkey.desktop")
}

// IconPath возвращает путь иконки.
func (e Env) IconPath() string {
	return filepath.Join(e.DataHome, "icons", "hicolor", "scalable", "apps", "mkey.svg")
}

// Installed сообщает, установлен ли mKey (есть программа — в ~/.local/bin или в системе — и манифест).
func (e Env) Installed() bool {
	_, errBin := os.Stat(e.BinPath())
	_, errMan := os.Stat(e.ManifestPath())
	return errBin == nil && errMan == nil
}

// RunningInstalled сообщает, что запущена уже установленная копия программы.
func (e Env) RunningInstalled() bool {
	a, err1 := os.Stat(e.Exe)
	b, err2 := os.Stat(e.BinPath())
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// Files копирует программу в ~/.local/bin (если запущена не она) и добавляет ярлык и иконку в меню.
func Files(e Env, w contracts.FileWriter) error {
	// Программа: копия запущенного файла (обновление заменяет старую копию); установленную
	// в систему не копируем.
	if !e.System() && !e.RunningInstalled() {
		data, err := readFile(e.Exe)
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Exe, err)
		}
		if err := w.WriteFile(e.BinPath(), data, 0o755); err != nil {
			return fmt.Errorf("install program: %w", err)
		}
	}

	// Иконка и ярлык в меню приложений: открывают интерфейс mKey (в консольной сборке окна нет;
	// у установки пакетом ярлык уже есть).
	if !buildinfo.GUI {
		return nil
	}
	if _, err := os.Stat(SystemDesktopFile); err == nil && e.System() {
		return nil
	}
	if err := w.WriteFile(e.IconPath(), icon, 0o644); err != nil {
		return fmt.Errorf("install icon: %w", err)
	}
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=mKey\n" +
		"GenericName=Input macros\n" +
		"GenericName[ru]=Макросы ввода\n" +
		"Comment=Keyboard and mouse macros for Linux\n" +
		"Comment[ru]=Макросы клавиатуры и мыши для Linux\n" +
		"Exec=\"" + e.BinPath() + "\" gui\n" +
		"Icon=mkey\n" +
		"Terminal=false\n" +
		"Categories=Utility;\n" +
		"Keywords=macro;hotkey;autoclicker;keyboard;mouse;\n"
	if err := w.WriteFile(e.DesktopPath(), []byte(entry), 0o644); err != nil {
		return fmt.Errorf("install menu entry: %w", err)
	}
	return nil
}

// RemoveData удаляет данные mKey. keepConfig — сохранить настройки и проекты (~/.config/mkey)
// и записи (~/.local/share/mkey/recordings). Возвращает ошибки по каждому каталогу.
func RemoveData(e Env, keepConfig bool) []error {
	var errs []error
	remove := func(path string) {
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, err)
		}
	}

	// Состояние и каталог времени выполнения удаляются всегда.
	remove(e.State)
	remove(e.Runtime)

	// Данные: при сохранении настроек оставляем записи, остальное удаляем.
	if keepConfig {
		entries, err := os.ReadDir(e.Data)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		for _, en := range entries {
			if en.Name() != "recordings" {
				remove(filepath.Join(e.Data, en.Name()))
			}
		}
		return errs
	}
	remove(e.Data)
	remove(e.Config)
	return errs
}

// readFile читает файл целиком (запущенный исполняемый файл можно читать, пока он работает).
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}
