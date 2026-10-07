#!/bin/sh
# Обновляет PKGBUILD mkey-bin под выпуск: версия и контрольные суммы из checksums.txt релиза,
# файлы ярлыка и иконки — рядом, и пишет .SRCINFO (то же, что makepkg --printsrcinfo, но без
# makepkg — скрипт работает на любой системе). Использование: packaging/aur/update.sh 0.9.0
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

# .SRCINFO — сведения о пакете для сайта AUR (по полям PKGBUILD).
url=https://github.com/khameleonium/mKey/releases/download/v${ver}
desc=$(sed -n 's/^pkgdesc="\(.*\)"$/\1/p' "$dir/PKGBUILD")
{
  printf 'pkgbase = mkey-bin\n'
  printf '\tpkgdesc = %s\n' "$desc"
  printf '\tpkgver = %s\n\tpkgrel = 1\n' "$ver"
  printf '\turl = https://github.com/khameleonium/mKey\n\tinstall = mkey.install\n'
  printf '\tarch = x86_64\n\tarch = aarch64\n\tlicense = MIT\n'
  printf '\tprovides = mkey\n\tconflicts = mkey\n'
  printf '\tsource = mkey.desktop\n\tsource = mkey.svg\n'
  printf '\tsha256sums = %s\n\tsha256sums = %s\n' "$desk" "$svg"
  printf '\tsource_x86_64 = %s/mkey_%s_linux_amd64.tar.gz\n\tsha256sums_x86_64 = %s\n' "$url" "$ver" "$amd"
  printf '\tsource_aarch64 = %s/mkey_%s_linux_arm64.tar.gz\n\tsha256sums_aarch64 = %s\n' "$url" "$ver" "$arm"
  printf '\npkgname = mkey-bin\n'
} > "$dir/.SRCINFO"
echo "PKGBUILD and .SRCINFO updated to ${ver}"
