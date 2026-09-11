#!/usr/bin/env bash
# Build step for `herdr plugin install`: fetch the prebuilt picker binary.
# The version comes from the manifest, so it works in a shallow clone.
set -euo pipefail

repo="reobin/herdr-link-hints"

fail() {
  echo "herdr-link-hints: $1" >&2
  echo "Build from source instead: go build -trimpath -o picker ." >&2
  exit 1
}

version="$(sed -n 's/^version = "\(.*\)"/\1/p' herdr-plugin.toml | head -1)"
[[ -n "$version" ]] || fail "no version in herdr-plugin.toml"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s)" ;;
esac

case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  armv7l | armv6l | armhf | arm) arch=arm ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

asset="picker-${os}-${arch}"
base="https://github.com/${repo}/releases/download/v${version}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --retry 3 -o "$tmp/$asset" "$base/$asset" ||
  fail "no prebuilt $asset for v$version"
curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/checksums.txt" ||
  fail "no checksums.txt for v$version"

expected="$(awk -v name="$asset" '$2 == name {print $1}' "$tmp/checksums.txt")"
[[ -n "$expected" ]] || fail "$asset is missing from checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
[[ "$actual" == "$expected" ]] || fail "checksum mismatch for $asset"

install -m 755 "$tmp/$asset" picker
