#!/bin/sh
# Обновляет PKGBUILD mkey-bin под выпуск: версия и контрольные суммы из checksums.txt релиза,
# файлы ярлыка и иконки — рядом. Использование: packaging/aur/update.sh 0.9.0
set -eu
ver=${1:?usage: update.sh <version without v>}
dir=$(cd "$(dirname "$0")" && pwd)
sums=$(curl -fsSL "https://github.com/khameleonium/mKey/releases/download/v${ver}/checksums.txt")
amd=$(echo "$sums" | awk -v f="mkey_${ver}_linux_amd64.tar.gz" '$2==f{print $1}')
arm=$(echo "$sums" | awk -v f="mkey_${ver}_linux_arm64.tar.gz" '$2==f{print $1}')
[ -n "$amd" ] && [ -n "$arm" ] || { echo "checksums for $ver not found" >&2; exit 1; }
cp "$dir/../mkey.desktop" "$dir/../mkey.svg" "$dir/"
desk=$(sha256sum "$dir/mkey.desktop" | cut -d' ' -f1)
svg=$(sha256sum "$dir/mkey.svg" | cut -d' ' -f1)
sed -i -e "s/^pkgver=.*/pkgver=${ver}/" -e "s/^pkgrel=.*/pkgrel=1/" \
  -e "s/^sha256sums=.*/sha256sums=('${desk}' '${svg}')/" \
  -e "s/^sha256sums_x86_64=.*/sha256sums_x86_64=('${amd}')/" \
  -e "s/^sha256sums_aarch64=.*/sha256sums_aarch64=('${arm}')/" "$dir/PKGBUILD"
echo "PKGBUILD updated to ${ver}; run makepkg --printsrcinfo > .SRCINFO in the AUR repository"
