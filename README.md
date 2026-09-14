# herdr-link-hints

Vimium-style keyboard link hints for [Herdr](https://herdr.dev).
Tap a key, get a hint code drawn beside every link on screen, type the
code to open it.

## Install

Needs Herdr 0.9.0+. A prebuilt binary is downloaded for macOS and Linux
on arm64 and amd64; no toolchain required.

```sh
herdr plugin install reobin/herdr-link-hints
```

Bind a key in `~/.config/herdr/config.toml`, then
`herdr server reload-config`:

```toml
[[keys.command]]
key = "prefix+f"
type = "plugin_action"
command = "herdr-link-hints.hints"
description = "hint links in focused pane"
```

## Use

Press the bound key. Every link on screen is underlined and gets a hint
code beside it, and the rest of the screen dims. Type the code; hints
that no longer match fade, and the match opens as soon as it is
unambiguous. Backspace edits, Enter opens a single match, Esc quits.

A small popup shows how many hints are left, and says `no match` when a
prefix has ruled them all out.

![demo](docs/demo.gif)

## Demo

`picker --demo` runs the picker over a fixed synthetic pane with no
Herdr connection. It renders the hint frames without showing them, so it
exercises the whole pick path from any terminal:

```sh
go build -trimpath -o picker .
echo a | ./picker --demo
```

`docs/demo.gif` plays a synthetic session: the pane at rest, a keypress,
hints easing in over the dimmed text, the typed code narrowing them to
one, the pick opening. Rebuild it after any
overlay or demo change:

```sh
./scripts/build-demo-gif.sh
```

Every layer is drawn at the gif's own resolution. `pane.txt` is set with
ImageMagick in JetBrains Mono (`DEMO_FONT` overrides the typeface), and the hints come from the plugin's own renderer through
`go run ./internal/demo/frames`, at the cell size the gif uses rather
than the goldens'. `pane.txt` is freshness-checked against `PaneLines()`,
so the background can never drift from the badge coordinates.

Refresh the goldens with `UPDATE_GOLDEN=1 go test ./internal/demo/`.
`TestRenderBudget` fails the build when a full-screen render exceeds
2s, or 10s under the race detector; `go test -bench . ./...` tracks render speed.

The hints are drawn with Herdr's pane graphics, which need a terminal
with Kitty graphics support, and in the colours the terminal reports for
itself. Without Kitty graphics there are no badges to draw on, so the
picker falls back to a code list.

## From source

Needs Go 1.24+.

```sh
go build -trimpath -o picker .
herdr plugin link /path/to/herdr-link-hints
```

## Settings

| Variable | Default | Effect |
| --- | --- | --- |
| `HINTS_WIDTH` | square of the cell | picker popup width |
| `HINTS_HEIGHT` | 5 | picker popup height |
| `HINTS_PLACEMENT` | popup | picker pane placement; only popup takes a size |
| `HINTS_NO_OBSERVE` | unset | skip the observe stream when set |
| `HINTS_NO_THEME_CACHE` | unset | skip the theme cache and probe the terminal live |
| `HINTS_DEBUG` | unset | log to stderr, or to the named file |
