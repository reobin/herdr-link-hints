# herdr-link-hints

Vimium-style keyboard link hints for [herdr](https://herdr.dev).
Tap a key, get a hint code drawn beside the links on screen, type the
code to open one.

![demo](docs/demo.gif)

## Install

Needs herdr 0.9.0+.

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

## Build from source

Needs Go 1.24+.

```sh
go build -trimpath -o picker .
herdr plugin link /path/to/herdr-link-hints
```

herdr 0.9.1 hides any image that touches a popup, so the badges tile
around the picker popup and none is drawn under it. Links there stay in
the list.

## License

[MIT](LICENSE)
