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
		mkdir -p /usr/local/sbin
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

# Каждая система проверяется до конца; в конце — список упавших (ненулевой код, если есть).
failed=""
check() {
	if ! run_case "$@"; then
		failed="$failed $1"
	fi
}

# Void: runit + eudev (без logind в контейнере) → группа input. В урезанном образе нет утилит shadow
# (useradd, usermod), которые на настоящей системе входят в base-system, — доустанавливаем.
check ghcr.io/void-linux/void-glibc:latest \
	"xbps-install -Syu xbps >/dev/null && xbps-install -y shadow >/dev/null; useradd -m tester; mkdir -p /run/runit /run/udev /etc/modules-load.d" group
# Alpine: OpenRC + mdev → mdev.conf и группа input (busybox addgroup). Обработчик hotplug ядра
# (по нему узнаётся mdev) в контейнере не задать — вместо него заглушка демона mdevd (тот же способ).
check alpine:latest \
	"adduser -D tester; mkdir -p /run/openrc /usr/local/sbin; printf '#!/bin/sh\nexit 0\n' > /usr/local/sbin/mdevd; chmod +x /usr/local/sbin/mdevd; touch /etc/mdev.conf /etc/modules" mdev
# Artix: OpenRC + udev + elogind → uaccess.
check artixlinux/artixlinux:latest \
	"useradd -m tester; mkdir -p /run/openrc /run/udev /run/systemd/seats /etc/modules-load.d" uaccess
# Devuan: sysvinit + eudev + elogind → uaccess.
check devuan/devuan:latest \
	"useradd -m tester; mkdir -p /run/udev /run/systemd/seats /etc/modules-load.d" uaccess

if [ -n "$failed" ]; then
	echo "Не прошли:$failed" >&2
	exit 1
fi
echo "Все проверки пройдены."
