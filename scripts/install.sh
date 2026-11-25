#!/usr/bin/env bash
# install.sh – builds Tiegel for this machine and installs it.
#
# Usage: scripts/install.sh
#
# macOS: builds Tiegel.app for the native architecture (ad-hoc signed unless
# TIEGEL_SIGN_IDENTITY is set) and copies it to APP_DIR (default
# /Applications), replacing an installed Tiegel.app.
# Other systems: builds the binary and copies it to BIN_DIR (default
# ~/.local/bin).
#
# Environment:
#   APP_DIR   target directory of Tiegel.app on macOS (default /Applications)
#   BIN_DIR   target directory of the binary elsewhere (default ~/.local/bin)
#   ARCH      macOS architecture: native (default), arm64, amd64 or universal
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if [[ "$(uname -s)" != "Darwin" ]]; then
	bin_dir="${BIN_DIR:-$HOME/.local/bin}"
	cd "$ROOT"
	export TIEGEL_VERSION="${TIEGEL_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
	mkdir -p "$bin_dir"
	echo "==> go build → $bin_dir/tiegel"
	go build -trimpath -ldflags "-s -w $("$ROOT/scripts/ldflags.sh")" -o "$bin_dir/tiegel.new" ./cmd/tiegel
	mv -f "$bin_dir/tiegel.new" "$bin_dir/tiegel"
	echo "==> installed: $bin_dir/tiegel"
	exit 0
fi

arch="${ARCH:-native}"
if [[ "$arch" == "native" ]]; then
	case "$(uname -m)" in
	arm64) arch=arm64 ;;
	x86_64) arch=amd64 ;;
	*) arch=universal ;;
	esac
fi
app_dir="${APP_DIR:-/Applications}"

build="$(mktemp -d)"
trap 'rm -rf "$build"' EXIT
"$SCRIPT_DIR/macos/build-app.sh" --arch "$arch" --out "$build"

if [[ ! -d "$app_dir" ]]; then
	echo "install: $app_dir does not exist" >&2
	exit 1
fi
if [[ ! -w "$app_dir" ]]; then
	echo "install: $app_dir is not writable (try APP_DIR=\$HOME/Applications make install)" >&2
	exit 1
fi
target="$app_dir/Tiegel.app"
if pgrep -f "$target/Contents/MacOS/Tiegel" >/dev/null 2>&1; then
	echo "==> note: Tiegel is running; restart it to use the new version"
fi
echo "==> install $target"
rm -rf "$target"
ditto "$build/Tiegel.app" "$target"
echo "==> installed: $target"
