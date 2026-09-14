# herdr-link-hints

Vimium-style keyboard link hints for [Herdr](https://herdr.dev).
Tap a key, get a hint code drawn beside every link in the focused pane,
type the code to open it.

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

Press the bound key. Every link in the focused pane is underlined and
gets a hint code beside it, and the rest of that pane dims. Other panes
are left alone. Type the code; hints that no longer match fade, and the
match opens as soon as it is unambiguous. Backspace edits, Enter opens a
single match, Esc quits.

A small popup shows how many hints are left, and says `no match` when a
prefix has ruled them all out.

The hints are drawn with Herdr's pane graphics, which need a terminal
with Kitty graphics support, and in the colours the terminal reports for
itself. Without Kitty graphics the plugin falls back to a popup listing
the links, and works the same way otherwise.

## From source

Needs Go 1.24+.

```sh
go build -trimpath -o picker .
herdr plugin link /path/to/herdr-link-hints
```
