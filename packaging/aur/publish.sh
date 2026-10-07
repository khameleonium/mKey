#!/bin/sh
# Публикует пакет mkey-bin в AUR: клонирует его git-репозиторий AUR (первый раз — пустой), кладёт
# PKGBUILD, .SRCINFO, mkey.install, ярлык и иконку и отправляет. Перед этим — update.sh <версия>.
# Нужны аккаунт на aur.archlinux.org и SSH-ключ, добавленный в его настройки (ключ по умолчанию —
# ~/.ssh/aur_mkey; другой — переменная AUR_KEY). Использование: packaging/aur/publish.sh
set -eu
dir=$(cd "$(dirname "$0")" && pwd)
key=${AUR_KEY:-$HOME/.ssh/aur_mkey}
export GIT_SSH_COMMAND="ssh -i $key -o IdentitiesOnly=yes"

# Рабочая копия репозитория пакета в AUR.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git clone ssh://aur@aur.archlinux.org/mkey-bin.git "$tmp/mkey-bin"

# Файлы пакета и коммит с версией из .SRCINFO.
cp "$dir/PKGBUILD" "$dir/.SRCINFO" "$dir/mkey.install" "$dir/../mkey.desktop" "$dir/../mkey.svg" "$tmp/mkey-bin/"
cd "$tmp/mkey-bin"
git add PKGBUILD .SRCINFO mkey.install mkey.desktop mkey.svg
ver=$(sed -n 's/^\tpkgver = //p' .SRCINFO)
git -c user.name=khameleonium -c user.email=siriusx666@gmail.com commit -m "mkey-bin ${ver}"
git push origin HEAD:master
echo "Опубликовано: https://aur.archlinux.org/packages/mkey-bin (${ver})"
