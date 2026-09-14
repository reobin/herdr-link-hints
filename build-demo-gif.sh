#!/usr/bin/env bash
# Rebuilds demo.gif as a little story: plain pane output, a keypress,
# hints fading in over dimmed text, narrowing to one match, opening it.
# pane.txt is freshness-checked against PaneLines(), so the background
# can never drift from the badge coordinates.
set -uo pipefail

FONT="${DEMO_FONT:-$HOME/Library/Fonts/JetBrainsMono-Regular.ttf}"
GOLDEN=internal/demo/testdata/golden
PANE=internal/demo/testdata/pane.txt
WORK="$PWD/.demo-frames"
FPS=10
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/seq"

bg() {
	magick -size 720x432 xc:black -font "$FONT" -pointsize 15 \
		-interline-spacing -3.5 -fill '#b8b8b8' -annotate +0+15 "@$PANE" "$WORK/bg.png"
}

caption() {
	magick -size 720x30 xc:black -font "$FONT" -pointsize 14 \
		-fill '#8a8a8a' -gravity center -annotate +0+2 "$2" "$1"
}

stage() {
	magick "$1" "$2" -append "$WORK/stage-$3.png"
}

fade() {
	magick "$WORK/stage-$1.png" "$WORK/stage-$2.png" \
		-define compose:args=33 -compose blend -composite "$WORK/fade-$1-$2-a.png"
	magick "$WORK/stage-$1.png" "$WORK/stage-$2.png" \
		-define compose:args=66 -compose blend -composite "$WORK/fade-$1-$2-b.png"
}

emit() {
	i=0
	while [ "$i" -lt "$2" ]; do
		printf -v n "%03d" "$((i + $3))"
		cp "$WORK/$1.png" "$WORK/seq/f$n.png"
		i=$((i + 1))
	done
	echo $((i + $3))
}

bg
caption "$WORK/cap-empty.png" " "
caption "$WORK/cap-hints.png" "prefix+f  |  6 links"
caption "$WORK/cap-narrow.png" "a  |  1 link"
caption "$WORK/cap-open.png" "opened https://c.io/x in demo"

magick "$WORK/bg.png" "$GOLDEN/full.png" -compose over -composite "$WORK/over-full.png"
magick "$WORK/bg.png" "$GOLDEN/narrowed.png" -compose over -composite "$WORK/over-narrowed.png"

stage "$WORK/bg.png" "$WORK/cap-empty.png" plain
stage "$WORK/over-full.png" "$WORK/cap-hints.png" full
stage "$WORK/over-narrowed.png" "$WORK/cap-narrow.png" narrowed
stage "$WORK/bg.png" "$WORK/cap-open.png" opened
fade plain full
fade full narrowed
fade narrowed opened

n=0
n=$(emit stage-plain 15 "$n")
n=$(emit fade-plain-full-a 2 "$n")
n=$(emit fade-plain-full-b 2 "$n")
n=$(emit stage-full 12 "$n")
n=$(emit fade-full-narrowed-a 2 "$n")
n=$(emit fade-full-narrowed-b 2 "$n")
n=$(emit stage-narrowed 12 "$n")
n=$(emit fade-narrowed-opened-a 2 "$n")
n=$(emit fade-narrowed-opened-b 2 "$n")
n=$(emit stage-opened 14 "$n")

ffmpeg -y -v error -framerate "$FPS" -i "$WORK/seq/f%03d.png" \
	-filter_complex "scale=1440:924:flags=neighbor,format=rgb24,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" \
	demo.gif
