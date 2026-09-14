#!/usr/bin/env bash
# Rebuilds docs/demo.gif: a terminal window showing the synthetic demo pane, a
# keypress, hints easing in over the dimmed text, the typed code narrowing
# them to one, and the pick opening. Every layer
# is rendered at the gif's own resolution, so nothing is upscaled: the
# pane text with ImageMagick, the hints by the plugin's own renderer
# through internal/demo/frames. pane.txt is freshness-checked against
# PaneLines(), so the background can never drift from the badge
# coordinates.
set -euo pipefail

# The layer paths below are repo-relative, so the script runs from anywhere.
cd "$(dirname "${BASH_SOURCE[0]}")/.."

FONT="${DEMO_FONT:-$HOME/Library/Fonts/JetBrainsMono-Regular.ttf}"
PANE=internal/demo/testdata/pane.txt
WORK="$PWD/.demo-frames"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/seq"

# JetBrains Mono advances 0.6em, so a 30px face fills an 18px cell; the
# baseline sits where a 22px cap height centres in the 36px row.
CELL_W=18
CELL_H=36
POINT=30
BASELINE=28

# The frames command writes the hint layers and demo.env, which carries
# COLS, ROWS, BG, FG, and the first code and URL from the Go side.
go run ./internal/demo/frames -out "$WORK" -cell-width "$CELL_W" -cell-height "$CELL_H"
mv "$WORK/full.png" "$WORK/hints-full.png"
mv "$WORK/narrowed.png" "$WORK/hints-narrowed.png"
. "$WORK/demo.env"

FPS=25
PANE_W=$((COLS * CELL_W))
PANE_H=$((ROWS * CELL_H))

PAD=24
TITLE_H=44
MARGIN_X=48
MARGIN_TOP=40
CAPTION_H=64
WIN_W=$((PANE_W + 2 * PAD))
WIN_H=$((PANE_H + 2 * PAD + TITLE_H))
CANVAS_W=$((WIN_W + 2 * MARGIN_X))
CANVAS_H=$((WIN_H + MARGIN_TOP + CAPTION_H))
PANE_X=$((MARGIN_X + PAD))
PANE_Y=$((MARGIN_TOP + TITLE_H + PAD))
RADIUS=14

DIM='#565f89'
BORDER='#2f334d'
BACKDROP='#0c0d14'
KEYCAP_BG='#24283b'
KEYCAP_BORDER='#3b4261'

ease() {
	awk -v i="$1" -v n="$2" 'BEGIN { t = i / n; if (t < 0) t = 0; if (t > 1) t = 1; printf "%.4f", t * t * (3 - 2 * t) }'
}

lerp() {
	awk -v a="$1" -v b="$2" -v t="$3" 'BEGIN { printf "%.4f", a + (b - a) * t }'
}

pct() {
	awk -v t="$1" 'BEGIN { printf "%d", t * 100 + 0.5 }'
}

# literal escapes what -annotate would otherwise interpret: % escapes,
# backslash sequences, and a leading @ that reads a file.
literal() {
	local text=${1//\\/\\\\}
	text=${text//%/%%}
	printf '%s' "${text/#@/\\@}"
}

text_layer() {
	local out=$1 size=$2 font=$3 color=$4
	shift 4
	local args=()
	for spec in "$@"; do
		IFS='|' read -r col row text <<<"$spec"
		args+=(-annotate "+$((col * CELL_W))+$((row * CELL_H + BASELINE))" "$(literal "$text")")
	done
	magick -size "${size}" xc:none -font "$font" -pointsize "$POINT" -fill "$color" "${args[@]}" "$out"
}

pane_text() {
	local specs=() row=0 line
	while IFS= read -r line; do
		[ -n "$line" ] && specs+=("0|$row|$line")
		row=$((row + 1))
	done <"$PANE"
	text_layer "$WORK/text.png" "${PANE_W}x${PANE_H}" "$FONT" "$FG" "${specs[@]}"
	magick -size "${PANE_W}x${PANE_H}" "xc:$BG" "$WORK/text.png" -composite "$WORK/pane.png"
}

cursor() {
	local col=2 row=$((ROWS - 1))
	magick -size "${PANE_W}x${PANE_H}" xc:none -fill "$FG" \
		-draw "rectangle $((col * CELL_W)),$((row * CELL_H + 4)) $(((col + 1) * CELL_W - 1)),$(((row + 1) * CELL_H - 3))" \
		"$WORK/cursor.png"
}

window() {
	local x0=$MARGIN_X y0=$MARGIN_TOP x1=$((MARGIN_X + WIN_W - 1)) y1=$((MARGIN_TOP + WIN_H - 1))
	magick -size "${CANVAS_W}x${CANVAS_H}" "xc:$BACKDROP" \
		\( -size "${CANVAS_W}x${CANVAS_H}" xc:none -fill 'rgba(0,0,0,0.6)' \
		-draw "roundrectangle $x0,$((y0 + 18)) $x1,$((y1 + 18)) $RADIUS,$RADIUS" -blur 0x28 \) -composite \
		-fill "$BG" -stroke "$BORDER" -strokewidth 1 \
		-draw "roundrectangle $x0,$y0 $x1,$y1 $RADIUS,$RADIUS" \
		-stroke none -fill '#ff5f57' -draw "circle $((x0 + 26)),$((y0 + 22)) $((x0 + 32)),$((y0 + 22))" \
		-fill '#febc2e' -draw "circle $((x0 + 48)),$((y0 + 22)) $((x0 + 54)),$((y0 + 22))" \
		-fill '#28c840' -draw "circle $((x0 + 70)),$((y0 + 22)) $((x0 + 76)),$((y0 + 22))" \
		-font "$FONT" -pointsize 17 -fill "$DIM" -gravity North -annotate "+0+$((y0 + 13))" "herdr" \
		-gravity NorthWest "$WORK/window.png"
}

keycap() {
	local name=$1 label=$2 w h
	magick -background none -font "$FONT" -pointsize 24 -fill "$FG" "label:$label" \
		-bordercolor none -border 18x10 "$WORK/key-label.png"
	read -r w h <<<"$(magick "$WORK/key-label.png" -format '%w %h' info:)"
	magick -size "${w}x${h}" xc:none -fill "$KEYCAP_BG" -stroke "$KEYCAP_BORDER" -strokewidth 2 \
		-draw "roundrectangle 2,2 $((w - 3)),$((h - 3)) 9,9" \
		"$WORK/key-label.png" -composite "$WORK/key-$name.png"
}

caption() {
	magick -size "${CANVAS_W}x${CAPTION_H}" xc:none -font "$FONT" -pointsize 22 -fill "$DIM" \
		-gravity Center -annotate +0+0 "$2" "$WORK/cap-$1.png"
}

# frame composes one gif frame from its layer states. Every alpha is a
# 0..1 opacity; an overlay blend of 0 is the full hint set, 1 the
# narrowed one.
frame() {
	local n=$1 cursor_on=$2 hint_alpha=$3 hint_blend=$4
	local key=$5 key_alpha=$6 key_scale=$7 cap=$8 cap_alpha=$9 cap_prev=${10} cap_prev_alpha=${11}
	local args=("$WORK/window.png" "$WORK/pane.png" -geometry "+$PANE_X+$PANE_Y" -composite)
	if [ "$cursor_on" = 1 ]; then
		args+=("$WORK/cursor.png" -geometry "+$PANE_X+$PANE_Y" -composite)
	fi
	if awk -v a="$hint_alpha" 'BEGIN { exit !(a > 0) }'; then
		args+=(\( "$WORK/hints-full.png" "$WORK/hints-narrowed.png"
			-define "compose:args=$(pct "$hint_blend")" -compose blend -composite
			-channel A -evaluate multiply "$hint_alpha" +channel \)
			-compose over -geometry "+$PANE_X+$PANE_Y" -composite)
	fi
	if [ -n "$key" ] && awk -v a="$key_alpha" 'BEGIN { exit !(a > 0) }'; then
		args+=(\( -size "${PANE_W}x${PANE_H}" xc:none
			\( "$WORK/key-$key.png" -resize "$(pct "$key_scale")%" \)
			-gravity SouthEast -geometry +36+28 -composite -gravity NorthWest
			-channel A -evaluate multiply "$key_alpha" +channel \)
			-geometry "+$PANE_X+$PANE_Y" -composite)
	fi
	local cap_y=$((MARGIN_TOP + WIN_H))
	if [ -n "$cap_prev" ] && awk -v a="$cap_prev_alpha" 'BEGIN { exit !(a > 0) }'; then
		args+=(\( "$WORK/cap-$cap_prev.png" -channel A -evaluate multiply "$cap_prev_alpha" +channel \)
			-geometry "+0+$cap_y" -composite)
	fi
	if [ -n "$cap" ] && awk -v a="$cap_alpha" 'BEGIN { exit !(a > 0) }'; then
		args+=(\( "$WORK/cap-$cap.png" -channel A -evaluate multiply "$cap_alpha" +channel \)
			-geometry "+0+$cap_y" -composite)
	fi
	magick "${args[@]}" "$WORK/seq/f$(printf '%04d' "$n").png"
}

# The timeline in frames at 25fps. Each beat names the frame it starts on.
T_KEY1=30      # prefix+f keycap pops
T_HINTS=36     # hints ease in
T_KEY1_OUT=62  # keycap fades
T_KEY2=84      # code keycap pops
T_NARROW=90    # hints narrow to the match
T_OPEN=110     # everything clears, the pick opens
T_CAP_OUT=168  # closing caption fades so the loop lands on frame 0
T_END=180

D_POP=6
D_FADE=12
D_BLEND=8
D_CAP=10
BLINK=26

blink() {
	[ $(($1 % BLINK)) -lt 16 ] && echo 1 || echo 0
}

fade_in() {
	ease $(($1 - $2)) "$3"
}

fade_out() {
	lerp 1 0 "$(ease $(($1 - $2)) "$3")"
}

render() {
	local n cursor_on hint_alpha hint_blend key key_alpha key_scale
	local cap cap_alpha cap_prev cap_prev_alpha
	for ((n = 0; n < T_END; n++)); do
		cursor_on=$(blink "$n")
		hint_alpha=0 hint_blend=0 key= key_alpha=0 key_scale=1
		cap= cap_alpha=0 cap_prev= cap_prev_alpha=0

		if [ "$n" -ge "$T_HINTS" ] && [ "$n" -lt "$T_OPEN" ]; then
			cursor_on=0
			hint_alpha=$(fade_in "$n" "$T_HINTS" "$D_FADE")
		elif [ "$n" -ge "$T_OPEN" ]; then
			hint_alpha=$(fade_out "$n" "$T_OPEN" "$D_FADE")
			hint_blend=1
			awk -v a="$hint_alpha" 'BEGIN { exit !(a > 0) }' && cursor_on=0
		fi
		if [ "$n" -ge "$T_NARROW" ] && [ "$n" -lt "$T_OPEN" ]; then
			hint_blend=$(fade_in "$n" "$T_NARROW" "$D_BLEND")
		fi

		if [ "$n" -ge "$T_KEY1" ] && [ "$n" -lt "$T_KEY2" ]; then
			key=prefix
			key_alpha=$(fade_in "$n" "$T_KEY1" "$D_POP")
			key_scale=$(lerp 0.88 1 "$key_alpha")
			if [ "$n" -ge "$T_KEY1_OUT" ]; then
				key_alpha=$(fade_out "$n" "$T_KEY1_OUT" "$D_FADE")
			fi
		elif [ "$n" -ge "$T_KEY2" ]; then
			key=code
			key_alpha=$(fade_in "$n" "$T_KEY2" "$D_POP")
			key_scale=$(lerp 0.88 1 "$key_alpha")
			if [ "$n" -ge "$T_OPEN" ]; then
				key_alpha=$(fade_out "$n" "$T_OPEN" "$D_FADE")
			fi
		fi

		if [ "$n" -lt "$T_HINTS" ]; then
			cap=links cap_alpha=1
			[ "$n" -lt "$D_CAP" ] && cap_alpha=$(fade_in "$n" 0 "$D_CAP")
		elif [ "$n" -lt "$T_NARROW" ]; then
			cap=hints cap_alpha=$(fade_in "$n" "$T_HINTS" "$D_CAP")
			cap_prev=links cap_prev_alpha=$(fade_out "$n" "$T_HINTS" "$D_CAP")
		elif [ "$n" -lt "$T_OPEN" ]; then
			cap=typed cap_alpha=$(fade_in "$n" "$T_NARROW" "$D_CAP")
			cap_prev=hints cap_prev_alpha=$(fade_out "$n" "$T_NARROW" "$D_CAP")
		else
			cap=opened cap_alpha=$(fade_in "$n" "$T_OPEN" "$D_CAP")
			cap_prev=typed cap_prev_alpha=$(fade_out "$n" "$T_OPEN" "$D_CAP")
			[ "$n" -ge "$T_CAP_OUT" ] && cap_alpha=$(fade_out "$n" "$T_CAP_OUT" "$D_CAP")
		fi

		frame "$n" "$cursor_on" "$hint_alpha" "$hint_blend" \
			"$key" "$key_alpha" "$key_scale" "$cap" "$cap_alpha" "$cap_prev" "$cap_prev_alpha"
	done
}

pane_text
cursor
window
keycap prefix "prefix + f"
keycap code "$CODE0"
caption links "six links on screen"
caption hints "every link gets a hint code, the rest dims"
caption typed "type the code"
caption opened "opened $URL0"
render

ffmpeg -y -v error -framerate "$FPS" -i "$WORK/seq/f%04d.png" \
	-filter_complex "format=rgb24,split[s0][s1];[s0]palettegen=max_colors=256:stats_mode=diff[p];[s1][p]paletteuse=dither=sierra2_4a:diff_mode=rectangle" \
	-loop 0 docs/demo.gif
