#!/usr/bin/env bash
# dmg.sh – packs Tiegel.app into a compressed disk image with an
# /Applications link for drag-and-drop installation.
#
# Usage: scripts/macos/dmg.sh [--app PATH] [--out FILE] [--no-sign]
#
#   --app PATH   app bundle (default: dist/Tiegel.app)
#   --out FILE   output image (default: dist/Tiegel-<version>.dmg, version
#                from the bundle's TiegelGitVersion)
#   --no-sign    do not sign the image (default: sign via sign.sh; ad-hoc
#                without TIEGEL_SIGN_IDENTITY)
#   -h, --help   show this help
#
# Re-running replaces an existing image.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }

app="$ROOT/dist/Tiegel.app"
dmg=""
sign=1
while [[ $# -gt 0 ]]; do
	case "$1" in
	--app) app="${2:?--app needs a value}"; shift 2 ;;
	--out) dmg="${2:?--out needs a value}"; shift 2 ;;
	--no-sign) sign=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) echo "dmg: unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
done
app="${app%/}"
if [[ ! -d "$app/Contents" ]]; then
	echo "dmg: app bundle not found: $app (run scripts/macos/build-app.sh first)" >&2
	exit 1
fi
if [[ -z "$dmg" ]]; then
	version="$(plutil -extract TiegelGitVersion raw -o - "$app/Contents/Info.plist" 2>/dev/null || echo dev)"
	dmg="$(dirname "$app")/Tiegel-${version}.dmg"
fi
mkdir -p "$(dirname "$dmg")"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
# ditto preserves signatures, extended attributes and symlinks.
ditto "$app" "$stage/$(basename "$app")"
ln -s /Applications "$stage/Applications"

echo "==> creating $dmg"
rm -f "$dmg"
hdiutil create -quiet -volname "Tiegel" -srcfolder "$stage" -fs HFS+ \
	-format UDZO -imagekey zlib-level=9 -ov "$dmg"
hdiutil verify -quiet "$dmg"

if [[ $sign -eq 1 ]]; then
	"$SCRIPT_DIR/sign.sh" "$dmg"
fi
echo "==> done: $dmg"
