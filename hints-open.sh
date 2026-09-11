#!/usr/bin/env bash
# Action `herdr-link-hints.hints`: open the picker popup.
#
# This runs headless on the server, so the interactive part lives in the
# `picker` pane entrypoint. Placement and size come from the [[panes]]
# block in herdr-plugin.toml; HINTS_WIDTH and HINTS_HEIGHT override them.
set -uo pipefail

herdr_bin="${HERDR_BIN_PATH:-herdr}"

args=(plugin pane open --plugin herdr-link-hints --entrypoint picker --focus)
[[ -n "${HINTS_WIDTH:-}" ]] && args+=(--width "$HINTS_WIDTH")
[[ -n "${HINTS_HEIGHT:-}" ]] && args+=(--height "$HINTS_HEIGHT")

exec "$herdr_bin" "${args[@]}"
