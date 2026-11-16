#!/usr/bin/env bash
# sign.sh – code-signs Tiegel.app or a .dmg with the Hardened Runtime.
#
# Usage: scripts/macos/sign.sh PATH [PATH...]
#
#   PATH  a .app bundle or a .dmg image
#
# Environment:
#   TIEGEL_SIGN_IDENTITY  signing identity, e.g.
#                         "Developer ID Application: Jane Doe (TEAMID1234)"
#                         or its SHA-1 hash. Unset or empty → ad-hoc signature
#                         ("-"): runs locally, but cannot be notarized and
#                         Gatekeeper on other Macs will refuse it.
#   TIEGEL_KEYCHAIN       optional keychain to search for the identity
#
# Re-running is safe: existing signatures are replaced (--force).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENTITLEMENTS="$ROOT/packaging/macos/Tiegel.entitlements"

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }

if [[ $# -eq 0 ]]; then
	usage >&2
	exit 2
fi
if [[ "$1" == "-h" || "$1" == "--help" ]]; then
	usage
	exit 0
fi

identity="${TIEGEL_SIGN_IDENTITY:-}"
common=(--force)
if [[ -n "$identity" ]]; then
	common+=(--sign "$identity" --timestamp)
	echo "==> signing with identity: $identity"
else
	common+=(--sign - --timestamp=none)
	echo "==> TIEGEL_SIGN_IDENTITY not set: ad-hoc signature (local use only)"
fi
if [[ -n "${TIEGEL_KEYCHAIN:-}" ]]; then
	common+=(--keychain "$TIEGEL_KEYCHAIN")
fi

for target in "$@"; do
	if [[ ! -e "$target" ]]; then
		echo "sign: not found: $target" >&2
		exit 1
	fi
	case "$target" in
	*.app | *.app/)
		target="${target%/}"
		# Sign nested code first (inside-out), then the bundle itself.
		while IFS= read -r -d '' f; do
			if [[ "$(file -b "$f")" == *Mach-O* ]]; then
				codesign "${common[@]}" --options runtime "$f"
			fi
		done < <(find "$target/Contents" -type f -perm -u+x ! -path "*/MacOS/*" -print0)
		codesign "${common[@]}" --options runtime --entitlements "$ENTITLEMENTS" "$target"
		codesign --verify --strict --deep --verbose=2 "$target"
		codesign --display --verbose=2 "$target" 2>&1 | grep -E '^(Identifier|Format|Authority|Signature|TeamIdentifier|flags|Runtime)' || true
		;;
	*.dmg)
		codesign "${common[@]}" "$target"
		codesign --verify --verbose=2 "$target"
		;;
	*)
		echo "sign: unsupported file type (expect .app or .dmg): $target" >&2
		exit 2
		;;
	esac
done
