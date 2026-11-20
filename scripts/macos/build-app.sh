#!/usr/bin/env bash
# build-app.sh – builds Tiegel.app (macOS app bundle).
#
# Usage: scripts/macos/build-app.sh [options]
#
# Options:
#   --arch ARCH     universal (default), arm64 or amd64
#   --out DIR       output directory (default: dist); result: DIR/Tiegel.app
#   --bundle-id ID  CFBundleIdentifier (default: de.ape-dev.tiegel)
#   --pkg PKG       Go main package to bundle (default: ./cmd/tiegel)
#   --no-sign       skip signing (default: sign via sign.sh, ad-hoc without identity)
#   -h, --help      show this help
#
# Environment:
#   TIEGEL_VERSION        override version (default: git describe --tags --always --dirty)
#   TIEGEL_SIGN_IDENTITY  passed on to sign.sh ("Developer ID Application: …");
#                         unset → ad-hoc signature
#
# Requires Xcode command line tools (clang, lipo, sips, iconutil, plutil) and Go.
# The build is idempotent: an existing DIR/Tiegel.app is replaced.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MIN_MACOS="11.0" # Go 1.24 requires macOS 11 Big Sur; Gio's Metal backend is older.

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }

arch="universal"
out="$ROOT/dist"
bundle_id="de.ape-dev.tiegel"
pkg="./cmd/tiegel"
sign=1
while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch) arch="${2:?--arch needs a value}"; shift 2 ;;
	--out) out="${2:?--out needs a value}"; shift 2 ;;
	--bundle-id) bundle_id="${2:?--bundle-id needs a value}"; shift 2 ;;
	--pkg) pkg="${2:?--pkg needs a value}"; shift 2 ;;
	--no-sign) sign=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) echo "build-app: unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
done

case "$arch" in
universal) archs=(arm64 amd64) ;;
arm64 | amd64) archs=("$arch") ;;
*) echo "build-app: invalid --arch: $arch" >&2; exit 2 ;;
esac

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "build-app: must run on macOS" >&2
	exit 1
fi

mkdir -p "$out"
out="$(cd "$out" && pwd)"
cd "$ROOT"

# --- Versions --------------------------------------------------------------
full_version="${TIEGEL_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
if [[ "$full_version" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
	short_version="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
else
	short_version="0.0.0"
fi
build_version="$(git rev-list --count HEAD 2>/dev/null || echo 0)"
export TIEGEL_VERSION="$full_version"
ldflags="-s -w $("$ROOT/scripts/ldflags.sh")"

echo "==> Tiegel $full_version (CFBundleShortVersionString $short_version, build $build_version)"

# Use the Xcode shims (they select the macOS SDK) even if another clang/lipo
# (e.g. from conda or Homebrew) comes first in PATH.
CC=/usr/bin/clang
export SDKROOT="${SDKROOT:-$(xcrun --sdk macosx --show-sdk-path)}"
LIPO=/usr/bin/lipo

work="$out/.build"
rm -rf "$work"
mkdir -p "$work"

# --- Binaries ----------------------------------------------------------------
# Silence the harmless duplicate -lobjc warning of Xcode 15+ linkers.
extra_ld=""
if echo 'int main(){return 0;}' | "$CC" -x c - -Wl,-no_warn_duplicate_libraries -o /dev/null >/dev/null 2>&1; then
	extra_ld=" -Wl,-no_warn_duplicate_libraries"
fi

bins=()
for a in "${archs[@]}"; do
	echo "==> go build darwin/$a"
	env CGO_ENABLED=1 GOOS=darwin GOARCH="$a" CC="$CC" \
		MACOSX_DEPLOYMENT_TARGET="$MIN_MACOS" \
		CGO_CFLAGS="-O2 -mmacosx-version-min=$MIN_MACOS" \
		CGO_LDFLAGS="-mmacosx-version-min=$MIN_MACOS$extra_ld" \
		go build -trimpath -ldflags "$ldflags" -o "$work/Tiegel-$a" "$pkg"
	bins+=("$work/Tiegel-$a")
done

if [[ ${#bins[@]} -gt 1 ]]; then
	echo "==> lipo -create (${archs[*]})"
	"$LIPO" -create -output "$work/Tiegel" "${bins[@]}"
else
	cp "${bins[0]}" "$work/Tiegel"
fi
"$LIPO" -info "$work/Tiegel"

# --- Icon ----------------------------------------------------------------------
echo "==> icon"
iconset="$work/Tiegel.iconset"
mkdir -p "$iconset"
go run ./packaging/macos/mkicon -png "$work/icon_1024.png" -size 1024
for s in 16 32 128 256 512; do
	sips -z "$s" "$s" "$work/icon_1024.png" --out "$iconset/icon_${s}x${s}.png" >/dev/null
	d=$((s * 2))
	sips -z "$d" "$d" "$work/icon_1024.png" --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$work/Tiegel.icns"

# --- Bundle --------------------------------------------------------------------
app="$out/Tiegel.app"
echo "==> assemble $app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$work/Tiegel" "$app/Contents/MacOS/Tiegel"
cp "$work/Tiegel.icns" "$app/Contents/Resources/Tiegel.icns"
printf 'APPL????' >"$app/Contents/PkgInfo"
sed -e '/^<!--/,/^-->/d' \
	-e "s|@BUNDLE_ID@|$bundle_id|g" \
	-e "s|@SHORT_VERSION@|$short_version|g" \
	-e "s|@BUILD_VERSION@|$build_version|g" \
	-e "s|@FULL_VERSION@|$full_version|g" \
	-e "s|@MIN_MACOS@|$MIN_MACOS|g" \
	-e "s|@YEAR@|$(date +%Y)|g" \
	"$ROOT/packaging/macos/Info.plist.in" >"$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist" >/dev/null
if grep -q '@[A-Z_]*@' "$app/Contents/Info.plist"; then
	echo "build-app: unreplaced placeholder in Info.plist" >&2
	exit 1
fi
rm -rf "$work"

if [[ $sign -eq 1 ]]; then
	"$SCRIPT_DIR/sign.sh" "$app"
fi
echo "==> done: $app"
