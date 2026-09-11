#!/usr/bin/env bash
# Action `herdr-link-hints.hints`: open the picker pane.
#
# This runs headless on the server, so the interactive part lives in the
# `picker` pane entrypoint, which sizes the pane before it opens.
set -uo pipefail

exec ./picker --open
