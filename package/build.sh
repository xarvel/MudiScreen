#!/bin/sh
# Build the MudiScreen release into dist/:
#   mudi-screen_<version>_aarch64_cortex-a53.ipk   (opkg install, or LuCI upload)
#   install.sh                                    one-line installer for the router
#   SHA256SUMS
# Needs Go; the binary is static, so any host builds it.
# MUDI_SCREEN_BASE_URL is where install.sh downloads the .ipk from (the release
# page of the hosting).
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
pkg=$root/package
ver=$(cat "$pkg/VERSION")
arch=aarch64_cortex-a53
ipk=mudi-screen_${ver}_${arch}.ipk
out=$root/dist
base_url=${MUDI_SCREEN_BASE_URL:-https://github.com/xarvel/MudiScreen/releases/download/v$ver}
# Reproducible archives: fixed timestamps, owners and order.
epoch=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct 2>/dev/null || date +%s)}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/data/usr/bin" "$work/control" "$out"

(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
	-ldflags="-s -w -X main.version=$ver" -o "$work/data/usr/bin/mudi-screen" .)
cp -R "$pkg/files/." "$work/data/"
mkdir -p "$work/data/usr/share/mudi-screen/apps"
cp -R "$root/apps/traffic" "$work/data/usr/share/mudi-screen/apps/"
rm -f "$work/data/usr/share/mudi-screen/apps/traffic/demo.json"
cp -R "$root/i18n" "$work/data/usr/share/mudi-screen/"
# The terms, the notice and the licenses of what the binary links in.
doc=$work/data/usr/share/doc/mudi-screen
mkdir -p "$doc"
cp "$root/LICENSE" "$root/NOTICE" "$pkg/THIRD_PARTY.md" "$doc/"
cp -R "$pkg/licenses" "$doc/"

find "$work/data" -type d -exec chmod 0755 {} +
find "$work/data" -type f -exec chmod 0644 {} +
chmod 0755 "$work/data/usr/bin/mudi-screen" "$work/data/usr/libexec/mudi-screen/run" \
	"$work/data/usr/sbin/mudi-screen-mode" "$work/data/etc/init.d/mudi-screen" "$work/data/etc/init.d/mudi-screen-traffic"

size=$(du -sk "$work/data" | cut -f1)
sed -e "s/@VERSION@/$ver/" -e "s/@SIZE@/$((size * 1024))/" "$pkg/control/control.in" >"$work/control/control"
for f in conffiles postinst prerm; do
	cp "$pkg/control/$f" "$work/control/$f"
done
chmod 0644 "$work/control/control" "$work/control/conffiles"
chmod 0755 "$work/control/postinst" "$work/control/prerm"

mktar() { # dir → gzip'd tar on stdout, the way OpenWrt's ipkg-build packs
	tar --format=gnu --numeric-owner --owner=0 --group=0 --sort=name \
		--mtime="@$epoch" -C "$1" -cf - . | gzip -9n
}
mktar "$work/data" >"$work/data.tar.gz"
mktar "$work/control" >"$work/control.tar.gz"
echo 2.0 >"$work/debian-binary"
touch -d "@$epoch" "$work/debian-binary" "$work/data.tar.gz" "$work/control.tar.gz"
tar --format=gnu --numeric-owner --owner=0 --group=0 --mtime="@$epoch" -C "$work" \
	-cf - ./debian-binary ./data.tar.gz ./control.tar.gz | gzip -9n >"$out/$ipk"
sum=$(sha256sum "$out/$ipk" | cut -d' ' -f1)
sed -e "s|@VERSION@|$ver|g" -e "s|@BASE_URL@|$base_url|g" -e "s|@SHA256@|$sum|g" \
	"$pkg/install.sh.in" >"$out/install.sh"
chmod 0755 "$out/install.sh"
touch -d "@$epoch" "$out/install.sh"
(cd "$out" && sha256sum "$ipk" install.sh >SHA256SUMS)

echo "== $out/$ipk"
tar -xzOf "$out/$ipk" ./data.tar.gz | tar -tzvf - | awk '{print $1, $3, $6}'
