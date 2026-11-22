#!/bin/sh
# Renders the images of README.md from the screen's own previews with
# made-up data (mudi-screen -preview), so they show what the panel shows,
# pixel for pixel:
#
#   sh docs/screenshots/make.sh FONTS_DIR
#
# FONTS_DIR holds default_medium.ttf, default_semibold.ttf and
# default_bold.ttf: the router's own (/etc/gl_screen/language/ttf) or IBM
# Plex Sans Medium, SemiBold and Bold (https://github.com/IBM/plex) under
# those names. Needs Go.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
fonts=$(CDPATH='' cd -- "${1:?usage: sh docs/screenshots/make.sh FONTS_DIR}" && pwd)
out=$root/docs/screenshots
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$root" && go build -ldflags "-X main.version=$(cat package/VERSION)" -o "$work/mudi-screen" .)
(cd "$out" && go build -o "$work/frames" frames.go)

# shot NAME LANG PAGE [FLAGS...]: one preview, $work/NAME.png
shot() {
	name=$1 lang=$2 page=$3
	shift 3
	"$work/mudi-screen" -fonts "$fonts" -apps "$root/apps" -lang "$lang" -page "$page" \
		-preview "$work/$name.png" "$@"
}

# The still pictures.
set -- \
	lock:en:lock home:en:home passcode:en:pin notification:en:notify \
	network:en:system:net wifi:en:system:wifi keyboard:en:system:keyboard \
	sms:en:system:sms message:en:system:sms:demo1 \
	settings-ru:ru:system:settings \
	traffic:en:traffic:main mobile:en:traffic:mobile devices:en:traffic:devices \
	services:en:traffic:services hosts:en:traffic:hosts connections:en:traffic:flows \
	device:en:traffic:dev:aa:bb:cc:00:00:01
for spec; do
	name=${spec%%:*} rest=${spec#*:}
	lang=${rest%%:*} page=${rest#*:}
	shot "$name" "$lang" "$page"
	"$work/frames" scale "$work/$name.png" "$out/$name.png"
done

# The animation: unlock, open the traffic analyzer, go back. A transition
# lasts about 0.3 s and eases out, as on the panel; 10 frames of 30 ms each.
steps="0.1 0.2 0.3 0.4 0.5 0.6 0.7 0.8 0.9 1"
anim() { # KIND PAGE: frames of a transition, listed for frames gif
	for t in $steps; do
		e=$(awk -v t="$t" 'BEGIN { printf "%.4f", 1 - (1 - t) ^ 3 }')
		shot "a-$1-$t" en "$2" -anim "$1" -progress "$e"
		printf ' %s:3' "$work/a-$1-$t.png"
	done
}
frames=$(
	printf '%s' "$work/lock.png:120"
	anim up home
	printf ' %s' "$work/home.png:110"
	anim push traffic:main
	printf ' %s' "$work/traffic.png:200"
	anim pop traffic:main
	printf ' %s' "$work/home.png:90"
)
# shellcheck disable=SC2086 # one word per frame
"$work/frames" gif "$out/demo.gif" $frames
ls -l "$out"
