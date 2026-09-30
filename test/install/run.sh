#!/bin/sh
# Проверка выдачи и снятия доступа к устройствам на дистрибутивах без systemd (T4.7).
#
# Для каждого образа: копирует mkey в контейнер, от root выполняет
# `mkey privileged install-rules`, проверяет созданные файлы и группу, затем
# `mkey privileged uninstall-rules` и проверяет, что следов не осталось.
# В контейнере нет udev и модулей ядра, поэтому modprobe/udevadm/mdev заменяются заглушками:
# проверяется выбор способа, пути файлов и утилиты групп конкретного дистрибутива.
#
# Запуск (нужен docker или podman): make build && sh test/install/run.sh
set -eu

# Движок контейнеров.
ENGINE=$(command -v podman || command -v docker || true)
if [ -z "$ENGINE" ]; then
	echo "нужен podman или docker" >&2
	exit 2
fi
BIN=$(cd "$(dirname "$0")/../.." && pwd)/mkey
[ -x "$BIN" ] || { echo "сначала соберите: make build" >&2; exit 2; }

# Образ → команда подготовки (пользователь, заглушки, маркеры init-системы).
run_case() {
	image=$1
	prepare=$2
	expect=$3
	echo "=== $image (ожидается: $expect)"
	"$ENGINE" run --rm -v "$BIN:/usr/local/bin/mkey:ro" "$image" sh -euc "
		$prepare
		for t in modprobe udevadm mdev; do printf '#!/bin/sh\nexit 0\n' > /usr/local/sbin/\$t; chmod +x /usr/local/sbin/\$t; done
		export PATH=/usr/local/sbin:\$PATH SUDO_USER=tester
		mkey privileged install-rules
		case '$expect' in
			uaccess|group) grep -q mKey /etc/udev/rules.d/70-mkey.rules ;;
			mdev) grep -q '>>> mKey >>>' /etc/mdev.conf ;;
		esac
		case '$expect' in group|mdev) id tester | grep -q input ;; esac
		mkey privileged uninstall-rules
		! grep -qs mKey /etc/udev/rules.d/70-mkey.rules
		! grep -qs '>>> mKey >>>' /etc/mdev.conf /etc/modules
		echo OK
	"
}

# Void: runit + eudev (без logind в контейнере) → группа input.
run_case ghcr.io/void-linux/void-glibc:latest \
	"useradd -m tester; mkdir -p /run/runit /run/udev /etc/modules-load.d" group
# Alpine: OpenRC + mdev → mdev.conf и группа input (busybox addgroup).
run_case alpine:latest \
	"adduser -D tester; mkdir -p /run/openrc; echo /sbin/mdev > /proc/sys/kernel/hotplug 2>/dev/null || true; touch /etc/mdev.conf /etc/modules" mdev
# Artix: OpenRC + udev + elogind → uaccess.
run_case artixlinux/artixlinux:latest \
	"useradd -m tester; mkdir -p /run/openrc /run/udev /run/systemd/seats /etc/modules-load.d" uaccess
# Devuan: sysvinit + eudev + elogind → uaccess.
run_case devuan/devuan:latest \
	"useradd -m tester; mkdir -p /run/udev /run/systemd/seats /etc/modules-load.d" uaccess
echo "Все проверки пройдены."
