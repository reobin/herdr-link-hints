# herdr-link-hints

Vimium-style keyboard link hints for [Herdr](https://herdr.dev).
Tap a key, get a hint code drawn beside the links on screen, type the
code to open one.

## Install

Needs Herdr 0.9.0+. A prebuilt binary is downloaded for macOS and Linux
on arm64 and amd64; no toolchain required.

```sh
herdr plugin install reobin/herdr-link-hints
```

Every release binary carries a signed build provenance attestation. The
installed binary is `picker`, in the plugin directory; check where it came
from with:

```sh
gh attestation verify ./picker --repo reobin/herdr-link-hints \
  --signer-workflow reobin/herdr-link-hints/.github/workflows/release.yml
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

Press the bound key, type a hint code to open it. Esc quits.

![demo](docs/demo.gif)

## Build from source

Needs Go 1.24+.

```sh
go build -trimpath -o picker .
herdr plugin link /path/to/herdr-link-hints
```

## Settings

| Variable               | Default | Effect                                           |
| ---------------------- | ------- | ------------------------------------------------ |
| `HINTS_WIDTH`          | 14      | picker popup width                               |
| `HINTS_HEIGHT`         | 5       | picker popup height                              |
| `HINTS_PLACEMENT`      | popup   | picker pane placement; only popup takes a size   |
| `HINTS_NO_OBSERVE`     | unset   | skip the observe stream when set                 |
| `HINTS_NO_THEME_CACHE` | unset   | skip the theme cache and probe the terminal live |
| `HINTS_DEBUG`          | unset   | log to stderr, or to the named file              |

## License

[MIT](LICENSE).
