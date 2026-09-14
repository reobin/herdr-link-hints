#!/usr/bin/env bash
# Rebuilds demo.gif from the golden overlay frames composited over the
# synthetic pane text. pane.txt is freshness-checked against PaneLines(),
# so the background can never drift from the badge coordinates.
set -uo pipefail

FONT="${DEMO_FONT:-$HOME/Library/Fonts/JetBrainsMono-Regular.ttf}"
GOLDEN=internal/demo/testdata/golden
PANE=internal/demo/testdata/pane.txt
WORK="$PWD/.demo-frames"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK"

magick -size 720x432 xc:black -font "$FONT" -pointsize 15 \
	-interline-spacing -3.5 -fill '#b8b8b8' -annotate +0+15 "@$PANE" "$WORK/bg.png"
for s in full narrowed typed; do
	magick "$WORK/bg.png" "$GOLDEN/$s.png" -compose over -composite "$WORK/frame-$s.png"
done
ffmpeg -y -v error \
	-loop 1 -framerate 1 -t 1 -i "$WORK/frame-full.png" \
	-loop 1 -framerate 1 -t 1 -i "$WORK/frame-narrowed.png" \
	-loop 1 -framerate 1 -t 1 -i "$WORK/frame-typed.png" \
	-filter_complex "[0:v][1:v][2:v]concat=n=3:v=1:a=0,scale=1440:864:flags=neighbor,format=rgb24,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" \
	demo.gif
