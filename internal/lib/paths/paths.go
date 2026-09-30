// Package paths — каталоги mKey по стандарту XDG (SPEC §8) и безопасное создание личных каталогов.
//
//   - Config: $XDG_CONFIG_HOME/mkey или ~/.config/mkey — настройки и проекты;
//   - Data:   $XDG_DATA_HOME/mkey или ~/.local/share/mkey — записи, плагины, манифест;
//   - State:  $XDG_STATE_HOME/mkey или ~/.local/state/mkey — журнал работы;
//   - Runtime: каталог времени выполнения (сокет, токен) — определяется модулем platform
//     (platform/detect.RuntimeDir), потому что зависит от наличия logind.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// app — имя подкаталога mKey во всех XDG-каталогах.
const app = "mkey"

// Config возвращает каталог настроек.
func Config(getenv func(string) string) string {
	return xdg(getenv, "XDG_CONFIG_HOME", ".config")
}

// Data возвращает каталог данных.
func Data(getenv func(string) string) string {
	return xdg(getenv, "XDG_DATA_HOME", ".local/share")
}

// State возвращает каталог состояния (журналы).
func State(getenv func(string) string) string {
	return xdg(getenv, "XDG_STATE_HOME", ".local/state")
}

// xdg возвращает $env/mkey или ~/fallback/mkey.
func xdg(getenv func(string) string, env, fallback string) string {
	if d := getenv(env); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, app)
	}
	return filepath.Join(getenv("HOME"), fallback, app)
}

// EnsurePrivateDir создаёт каталог с правами 0700 и проверяет, что он принадлежит текущему
// пользователю и недоступен другим. Это защищает сокет и токен от чужих пользователей
// (важно для запасного каталога в /tmp, SEC-5).
func EnsurePrivateDir(dir string) error {
	// Создаём каталог, если его нет.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// Проверяем владельца и права.
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user (uid %d)", dir, st.Uid)
	}

	// Лишние права убираем (каталог мог быть создан раньше с другой маской).
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return errors.Join(fmt.Errorf("%s is accessible by other users", dir), err)
		}
	}
	return nil
}
