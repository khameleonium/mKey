// Package devaccess — способы выдачи доступа к устройствам ввода (contracts.DeviceAccess),
// которые выполняются от root в команде `mkey privileged` (SPEC §5.11, FR-INST-7, ADR-0004).
//
// Способы:
//   - uaccess — udev-правило с тегом uaccess: доступ получает пользователь активной
//     локальной сессии, перелогин не нужен (systemd-logind или elogind + udev);
//   - group — пользователь добавляется в группу input, udev-правило открывает /dev/uinput
//     для этой группы; нужен перелогин (udev без logind);
//   - mdev — то же через /etc/mdev.conf (Alpine и другие системы с mdev/mdevd).
//
// Каждый способ также включает модуль ядра uinput и его автозагрузку (путь конфигурации
// зависит от системы). Содержимое всех системных файлов зашито в код (SEC-6), все пути
// строятся от PrivilegedEnv.Root, а команды выполняются через PrivilegedEnv.Runner —
// поэтому поведение проверяется тестами во временном каталоге.
package devaccess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mkey/internal/contracts"
)

// Пути системных файлов, которые создаёт или меняет mKey.
const (
	// RulesPath — udev-правило mKey. Номер 70 обязателен: тег uaccess должен быть
	// выставлен до 73-seat-late.rules, который применяет ACL.
	RulesPath = "/etc/udev/rules.d/70-mkey.rules"
	// ModulesLoadPath — автозагрузка uinput (systemd, OpenRC modules-load, Void/runit).
	ModulesLoadPath = "/etc/modules-load.d/mkey.conf"
	// modulesDir — каталог, наличие которого означает поддержку modules-load.d.
	modulesDir = "/etc/modules-load.d"
	// EtcModulesPath — классический список модулей (Alpine, Debian без modules-load.d).
	EtcModulesPath = "/etc/modules"
	// MdevConfPath — конфигурация mdev/mdevd.
	MdevConfPath = "/etc/mdev.conf"
	// devUinput — файл устройства uinput.
	devUinput = "/dev/uinput"
)

// Маркеры блока mKey внутри общих файлов (/etc/modules, /etc/mdev.conf).
const (
	markBegin = "# >>> mKey >>> managed by mKey, do not edit"
	markEnd   = "# <<< mKey <<<"
)

// uaccessRules — правило для способа uaccess.
const uaccessRules = `# Managed by mKey. Removed by "mkey uninstall".
# Gives the user of the active local session access to virtual input (uinput)
# and to input devices, so mKey can read and send key presses without root.
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
SUBSYSTEM=="input", KERNEL=="event*", TAG+="uaccess"
`

// groupRules — правило для способа group.
const groupRules = `# Managed by mKey. Removed by "mkey uninstall".
# Gives members of the "input" group access to virtual input (uinput) and input devices.
KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"
SUBSYSTEM=="input", KERNEL=="event*", GROUP="input", MODE="0660"
`

// mdevLines — строки блока mKey в /etc/mdev.conf (ставятся в начало файла: у mdev побеждает первое совпадение).
var mdevLines = []string{
	"uinput root:input 0660",
	"event[0-9]+ root:input 0660 =input/",
}

// All возвращает все встроенные способы в порядке предпочтения.
func All() []contracts.DeviceAccess {
	return []contracts.DeviceAccess{uaccess{}, group{}, mdev{}}
}

// meta собирает метаданные способа с i18n-ключом имени.
func meta(id string) contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: id, NameKey: "platform.device_access." + id, Provider: "platform"}
}

// isUdev сообщает, используется ли udev (systemd-udevd или eudev).
func isUdev(info contracts.PlatformInfo) bool {
	return info.DeviceManager == "systemd-udevd" || info.DeviceManager == "eudev"
}

// uaccess — доступ через udev-тег uaccess.
type uaccess struct{}

// Meta возвращает метаданные способа.
func (uaccess) Meta() contracts.ExtensionMeta { return meta("uaccess") }

// Applicable: есть менеджер сеансов (он применяет ACL) и udev.
func (uaccess) Applicable(info contracts.PlatformInfo) bool {
	return info.Logind != "none" && isUdev(info)
}

// RequiresRelogin: ACL применяется сразу к активной сессии.
func (uaccess) RequiresRelogin() bool { return false }

// Install пишет правило, включает uinput и применяет правила к существующим устройствам.
func (uaccess) Install(ctx context.Context, env contracts.PrivilegedEnv) error {
	if err := writeFile(env, RulesPath, uaccessRules); err != nil {
		return err
	}
	if err := enableUinput(ctx, env); err != nil {
		return err
	}
	return reloadUdev(ctx, env)
}

// Installed сообщает, установлено ли правило mKey.
func (uaccess) Installed(root string) bool { return rulesInstalled(root) }

// Uninstall удаляет правило и автозагрузку uinput, перечитывает правила.
func (uaccess) Uninstall(ctx context.Context, env contracts.PrivilegedEnv) error {
	return uninstallUdev(ctx, env)
}

// group — доступ через группу input.
type group struct{}

// Meta возвращает метаданные способа.
func (group) Meta() contracts.ExtensionMeta { return meta("group") }

// Applicable: есть udev (запасной вариант, когда нет менеджера сеансов).
func (group) Applicable(info contracts.PlatformInfo) bool { return isUdev(info) }

// RequiresRelogin: членство в группе вступает в силу после нового входа в систему.
func (group) RequiresRelogin() bool { return true }

// Install пишет правило, добавляет пользователя в группу input, включает uinput.
func (group) Install(ctx context.Context, env contracts.PrivilegedEnv) error {
	// Правило udev для группы input.
	if err := writeFile(env, RulesPath, groupRules); err != nil {
		return err
	}

	// Группа input и членство пользователя (утилиты shadow: groupadd/usermod).
	if err := ensureGroupMember(ctx, env, []string{"groupadd", "-r", "input"}, []string{"usermod", "-aG", "input", env.User}); err != nil {
		return err
	}

	// Модуль uinput и применение правил.
	if err := enableUinput(ctx, env); err != nil {
		return err
	}
	return reloadUdev(ctx, env)
}

// Installed сообщает, установлено ли правило mKey.
func (group) Installed(root string) bool { return rulesInstalled(root) }

// Uninstall удаляет правило и автозагрузку uinput. Членство в группе input не отзывается:
// группа могла быть нужна пользователю и до mKey (учёт изменений — манифест, фаза 4).
func (group) Uninstall(ctx context.Context, env contracts.PrivilegedEnv) error {
	return uninstallUdev(ctx, env)
}

// mdev — доступ через /etc/mdev.conf и группу input.
type mdev struct{}

// Meta возвращает метаданные способа.
func (mdev) Meta() contracts.ExtensionMeta { return meta("mdev") }

// Applicable: используется mdev или mdevd.
func (mdev) Applicable(info contracts.PlatformInfo) bool {
	return info.DeviceManager == "mdev" || info.DeviceManager == "mdevd"
}

// RequiresRelogin: членство в группе вступает в силу после нового входа в систему.
func (mdev) RequiresRelogin() bool { return true }

// Install добавляет блок mKey в начало mdev.conf, группу input, включает uinput и
// заново применяет правила к существующим устройствам.
func (m mdev) Install(ctx context.Context, env contracts.PrivilegedEnv) error {
	// Блок в начале файла: у mdev срабатывает первое совпадение.
	if err := setBlock(env, MdevConfPath, mdevLines, true); err != nil {
		return err
	}

	// Группа input и членство пользователя (busybox: addgroup).
	if err := ensureGroupMember(ctx, env, []string{"addgroup", "-S", "input"}, []string{"addgroup", env.User, "input"}); err != nil {
		return err
	}

	// Модуль uinput и повторное применение правил.
	if err := enableUinput(ctx, env); err != nil {
		return err
	}
	return m.rescan(ctx, env)
}

// Installed сообщает, есть ли блок mKey в mdev.conf.
func (mdev) Installed(root string) bool {
	data, err := os.ReadFile(rooted(contracts.PrivilegedEnv{Root: root}, MdevConfPath))
	return err == nil && strings.Contains(string(data), markBegin)
}

// Uninstall убирает блок mKey из mdev.conf и автозагрузку uinput.
func (m mdev) Uninstall(ctx context.Context, env contracts.PrivilegedEnv) error {
	if err := removeBlock(env, MdevConfPath); err != nil {
		return err
	}
	if err := disableUinput(env); err != nil {
		return err
	}
	return m.rescan(ctx, env)
}

// rescan заново применяет правила mdev к существующим устройствам.
func (mdev) rescan(ctx context.Context, env contracts.PrivilegedEnv) error {
	if env.Platform.DeviceManager == "mdevd" {
		return env.Runner.Run(ctx, "mdevd-coldplug")
	}
	return env.Runner.Run(ctx, "mdev", "-s")
}

// rulesInstalled сообщает, есть ли udev-правило mKey в системе с корнем root.
func rulesInstalled(root string) bool {
	return exists(contracts.PrivilegedEnv{Root: root}, RulesPath)
}

// ExecRunner — исполнитель системных команд для настоящей системы (contracts.CommandRunner).
type ExecRunner struct{}

// Run выполняет команду и возвращает ошибку вместе с её выводом.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureGroupMember создаёт группу input при отсутствии и добавляет в неё пользователя.
func ensureGroupMember(ctx context.Context, env contracts.PrivilegedEnv, addGroup, addMember []string) error {
	// Пользователь обязателен: иначе некого добавлять в группу.
	if env.User == "" {
		return errors.New("devaccess: target user is unknown")
	}

	// Создаём группу, только если её нет в /etc/group.
	groups, err := os.ReadFile(rooted(env, "/etc/group"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !hasLinePrefix(groups, "input:") {
		if err := env.Runner.Run(ctx, addGroup[0], addGroup[1:]...); err != nil {
			return fmt.Errorf("create group input: %w", err)
		}
	}

	// Добавляем пользователя в группу.
	if err := env.Runner.Run(ctx, addMember[0], addMember[1:]...); err != nil {
		return fmt.Errorf("add %s to group input: %w", env.User, err)
	}
	return nil
}

// enableUinput загружает модуль uinput сейчас и настраивает его автозагрузку при старте системы.
func enableUinput(ctx context.Context, env contracts.PrivilegedEnv) error {
	// Автозагрузка не нужна, если uinput встроен в ядро.
	if !uinputBuiltin(env) {
		if exists(env, modulesDir) || !exists(env, EtcModulesPath) {
			if err := writeFile(env, ModulesLoadPath, "# Managed by mKey: virtual input for macros\nuinput\n"); err != nil {
				return err
			}
		} else if err := setBlock(env, EtcModulesPath, []string{"uinput"}, false); err != nil {
			return err
		}
	}

	// Загружаем модуль сейчас; ошибка допустима, если устройство уже есть (модуль встроен или загружен).
	if err := env.Runner.Run(ctx, "modprobe", "uinput"); err != nil && !exists(env, devUinput) {
		return fmt.Errorf("load uinput module: %w", err)
	}
	return nil
}

// disableUinput убирает автозагрузку uinput, настроенную mKey (сам модуль не выгружается).
func disableUinput(env contracts.PrivilegedEnv) error {
	if err := removeFile(env, ModulesLoadPath); err != nil {
		return err
	}
	return removeBlock(env, EtcModulesPath)
}

// uinputBuiltin сообщает, встроен ли uinput в ядро (по modules.builtin текущего ядра).
func uinputBuiltin(env contracts.PrivilegedEnv) bool {
	release, err := os.ReadFile(rooted(env, "/proc/sys/kernel/osrelease"))
	if err != nil {
		return false
	}
	builtin, err := os.ReadFile(rooted(env, filepath.Join("/lib/modules", strings.TrimSpace(string(release)), "modules.builtin")))
	if err != nil {
		return false
	}
	return bytes.Contains(builtin, []byte("/uinput.ko"))
}

// reloadUdev перечитывает правила udev и применяет их к уже существующим устройствам.
func reloadUdev(ctx context.Context, env contracts.PrivilegedEnv) error {
	if err := env.Runner.Run(ctx, "udevadm", "control", "--reload"); err != nil {
		return fmt.Errorf("reload udev rules: %w", err)
	}
	if err := env.Runner.Run(ctx, "udevadm", "trigger", "--subsystem-match=input", "--subsystem-match=misc"); err != nil {
		return fmt.Errorf("apply udev rules: %w", err)
	}
	// Дожидаемся обработки событий; ошибка ожидания не критична.
	_ = env.Runner.Run(ctx, "udevadm", "settle")
	return nil
}

// uninstallUdev удаляет правило и автозагрузку uinput, перечитывает правила udev.
func uninstallUdev(ctx context.Context, env contracts.PrivilegedEnv) error {
	if err := removeFile(env, RulesPath); err != nil {
		return err
	}
	if err := disableUinput(env); err != nil {
		return err
	}
	return reloadUdev(ctx, env)
}

// rooted строит путь внутри корня env.Root.
func rooted(env contracts.PrivilegedEnv, path string) string {
	if env.Root == "" || env.Root == "/" {
		return path
	}
	return filepath.Join(env.Root, path)
}

// exists сообщает, существует ли путь внутри корня.
func exists(env contracts.PrivilegedEnv, path string) bool {
	_, err := os.Stat(rooted(env, path))
	return err == nil
}

// writeFile записывает файл целиком (права 0644), создавая каталоги.
func writeFile(env contracts.PrivilegedEnv, path, content string) error {
	full := rooted(env, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// removeFile удаляет файл; отсутствие файла — не ошибка.
func removeFile(env contracts.PrivilegedEnv, path string) error {
	if err := os.Remove(rooted(env, path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// setBlock записывает блок mKey (строки между маркерами) в общий файл, заменяя прежний блок.
// prepend — поставить блок в начало файла, иначе в конец.
func setBlock(env contracts.PrivilegedEnv, path string, lines []string, prepend bool) error {
	// Читаем файл и убираем прежний блок mKey, если он был.
	data, err := os.ReadFile(rooted(env, path))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rest := stripBlock(string(data))

	// Собираем новый блок и ставим его в начало или в конец.
	block := markBegin + "\n" + strings.Join(lines, "\n") + "\n" + markEnd + "\n"
	var out string
	switch {
	case prepend:
		out = block + rest
	case rest == "" || strings.HasSuffix(rest, "\n"):
		out = rest + block
	default:
		out = rest + "\n" + block
	}
	return writeFile(env, path, out)
}

// removeBlock убирает блок mKey из общего файла; отсутствие файла или блока — не ошибка.
func removeBlock(env contracts.PrivilegedEnv, path string) error {
	data, err := os.ReadFile(rooted(env, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stripped := stripBlock(string(data))
	if stripped == string(data) {
		return nil
	}
	return writeFile(env, path, stripped)
}

// stripBlock возвращает текст без блока mKey (включая маркеры).
func stripBlock(s string) string {
	start := strings.Index(s, markBegin)
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], markEnd)
	if end < 0 {
		return s
	}
	end += start + len(markEnd)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return s[:start] + s[end:]
}

// hasLinePrefix сообщает, есть ли в тексте строка, начинающаяся с prefix.
func hasLinePrefix(data []byte, prefix string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
