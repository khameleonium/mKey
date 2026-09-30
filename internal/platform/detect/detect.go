// Package detect определяет сведения о системе для модуля platform (FR-INST-7).
//
// Все обращения к системе идут через интерфейс Probe, поэтому определение можно
// проверить тестами для любой системы (systemd, Void/runit, Alpine/OpenRC+mdev…)
// без запуска на ней.
package detect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mkey/internal/contracts"
)

// Probe — доступ к системе, нужный для определения.
type Probe interface {
	// Exists сообщает, существует ли путь.
	Exists(path string) bool
	// ReadFile читает файл целиком.
	ReadFile(path string) ([]byte, error)
	// LookPath ищет программу в PATH.
	LookPath(name string) bool
	// Getenv возвращает переменную окружения.
	Getenv(name string) string
	// UID возвращает uid текущего пользователя.
	UID() int
}

// OS — Probe для настоящей системы. Root — корень файловой системы ("" или "/" — настоящий).
type OS struct {
	// Root — префикс путей (для тестов и chroot).
	Root string
}

// Exists сообщает, существует ли путь.
func (o OS) Exists(path string) bool {
	_, err := os.Stat(o.path(path))
	return err == nil
}

// ReadFile читает файл целиком.
func (o OS) ReadFile(path string) ([]byte, error) { return os.ReadFile(o.path(path)) }

// LookPath ищет программу в PATH и в стандартных системных каталогах
// (у пользовательских сессий /usr/sbin может не быть в PATH).
func (o OS) LookPath(name string) bool {
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	for _, dir := range []string{"/usr/bin", "/usr/sbin", "/bin", "/sbin", "/usr/local/bin"} {
		if o.Exists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// Getenv возвращает переменную окружения процесса.
func (OS) Getenv(name string) string { return os.Getenv(name) }

// UID возвращает uid текущего процесса.
func (OS) UID() int { return os.Getuid() }

// path добавляет префикс Root к пути.
func (o OS) path(p string) string {
	if o.Root == "" || o.Root == "/" {
		return p
	}
	return filepath.Join(o.Root, p)
}

// Detect собирает сведения о системе.
func Detect(p Probe) contracts.PlatformInfo {
	info := contracts.PlatformInfo{}

	// Система инициализации, менеджер сеансов и менеджер устройств.
	info.Init = detectInit(p)
	info.Logind = detectLogind(p, info.Init)
	info.DeviceManager = detectDeviceManager(p, info.Init)

	// Дистрибутив и пакетный менеджер по /etc/os-release.
	osr := parseOSRelease(p)
	info.DistroID = osr["ID"]
	info.DistroName = osr["PRETTY_NAME"]
	info.PackageManager = packageManager(osr["ID"], osr["ID_LIKE"])

	// Средства повышения прав.
	info.HasPkexec = p.LookPath("pkexec")
	info.HasSudo = p.LookPath("sudo")
	info.HasDoas = p.LookPath("doas")

	// Каталог времени выполнения: XDG_RUNTIME_DIR или запасной /tmp/mkey-UID.
	info.RuntimeDir, info.RuntimeDirFallback = RuntimeDir(p.Getenv, p.UID())
	return info
}

// RuntimeDir возвращает каталог для сокета и токена mKey и признак, что это запасной каталог.
// Без logind/elogind переменная XDG_RUNTIME_DIR может быть не задана.
func RuntimeDir(getenv func(string) string, uid int) (dir string, fallback bool) {
	if d := getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "mkey"), false
	}
	return fmt.Sprintf("/tmp/mkey-%d", uid), true
}

// detectInit определяет систему инициализации по характерным каталогам в /run и имени PID 1.
func detectInit(p Probe) string {
	// Надёжные маркеры работающих систем инициализации.
	switch {
	case p.Exists("/run/systemd/system"):
		return "systemd"
	case p.Exists("/run/openrc"):
		return "openrc"
	case p.Exists("/run/runit") || p.Exists("/run/runit.stopit"):
		return "runit"
	case p.Exists("/run/dinitctl"):
		return "dinit"
	case p.Exists("/run/s6") || p.Exists("/run/s6-rc"):
		return "s6"
	}

	// Иначе — по имени процесса PID 1.
	comm, err := p.ReadFile("/proc/1/comm")
	if err != nil {
		return "unknown"
	}
	switch name := strings.TrimSpace(string(comm)); name {
	case "systemd":
		return "systemd"
	case "runit":
		return "runit"
	case "dinit":
		return "dinit"
	case "s6-svscan":
		return "s6"
	case "init":
		return "sysvinit"
	default:
		return "unknown"
	}
}

// detectLogind определяет менеджер сеансов: оба (systemd-logind и elogind) создают /run/systemd/seats.
func detectLogind(p Probe, initSys string) string {
	if !p.Exists("/run/systemd/seats") {
		return "none"
	}
	if initSys == "systemd" {
		return "systemd-logind"
	}
	return "elogind"
}

// detectDeviceManager определяет менеджер устройств: udev (systemd-udevd или eudev) или mdev/mdevd.
func detectDeviceManager(p Probe, initSys string) string {
	// udev создаёт /run/udev; в системе с systemd это systemd-udevd, иначе — eudev
	// (или отдельно собранный udev из systemd — ведёт себя так же, как systemd-udevd).
	if p.Exists("/run/udev") {
		if initSys == "systemd" || p.LookPath("systemd-udevd") || p.Exists("/usr/lib/systemd/systemd-udevd") {
			return "systemd-udevd"
		}
		return "eudev"
	}

	// mdev регистрируется обработчиком hotplug ядра; mdevd работает как демон с netlink.
	if hp, err := p.ReadFile("/proc/sys/kernel/hotplug"); err == nil && bytes.Contains(hp, []byte("mdev")) {
		return "mdev"
	}
	if p.LookPath("mdevd") {
		return "mdevd"
	}
	return "unknown"
}

// parseOSRelease разбирает /etc/os-release (или /usr/lib/os-release) в карту «ключ → значение».
func parseOSRelease(p Probe) map[string]string {
	out := map[string]string{}
	data, err := p.ReadFile("/etc/os-release")
	if err != nil {
		if data, err = p.ReadFile("/usr/lib/os-release"); err != nil {
			return out
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		out[key] = strings.Trim(val, `"'`)
	}
	return out
}

// packageManager выбирает пакетный менеджер по ID и ID_LIKE дистрибутива.
func packageManager(id, idLike string) string {
	// Сопоставление семейств дистрибутивов с пакетными менеджерами.
	families := map[string]string{
		"debian": "apt", "ubuntu": "apt", "linuxmint": "apt", "pop": "apt", "elementary": "apt", "devuan": "apt",
		"fedora": "dnf", "rhel": "dnf", "centos": "dnf", "rocky": "dnf", "almalinux": "dnf", "nobara": "dnf",
		"arch": "pacman", "manjaro": "pacman", "artix": "pacman", "endeavouros": "pacman", "cachyos": "pacman",
		"opensuse": "zypper", "suse": "zypper", "opensuse-tumbleweed": "zypper", "opensuse-leap": "zypper",
		"void": "xbps", "alpine": "apk", "postmarketos": "apk", "gentoo": "emerge", "chimera": "apk",
	}

	// Сначала точный ID, затем «похожие» из ID_LIKE по порядку.
	for _, candidate := range append([]string{id}, strings.Fields(idLike)...) {
		if pm, ok := families[strings.ToLower(candidate)]; ok {
			return pm
		}
	}
	return ""
}
