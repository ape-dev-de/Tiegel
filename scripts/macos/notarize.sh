#!/usr/bin/env bash
# notarize.sh – submits a signed .dmg (or .app) to Apple's notary service and
# staples the ticket.
#
# Usage: scripts/macos/notarize.sh [--require] PATH
#
#   PATH       signed Tiegel.dmg or Tiegel.app (Developer ID, Hardened Runtime)
#   --require  fail instead of skipping when no credentials are configured
#
# Credentials (one of):
#   TIEGEL_NOTARY_PROFILE   keychain profile created once with
#                           xcrun notarytool store-credentials <profile> \
#                             --apple-id <id> --team-id <team> --password <app-specific>
#   TIEGEL_NOTARY_KEY       path to an App Store Connect API key (.p8) plus
#   TIEGEL_NOTARY_KEY_ID    its key ID and
#   TIEGEL_NOTARY_ISSUER    its issuer ID (used by the release workflow)
#   TIEGEL_KEYCHAIN         optional keychain holding the profile
#
# Without credentials the script prints a notice and exits 0, so local builds
# work without an Apple Developer account. Ad-hoc signed files cannot be
# notarized; they are skipped the same way.
set -euo pipefail

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }

require=0
target=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--require) require=1; shift ;;
	-h | --help) usage; exit 0 ;;
	-*) echo "notarize: unknown option: $1" >&2; usage >&2; exit 2 ;;
	*) target="${1%/}"; shift ;;
	esac
done
if [[ -z "$target" ]]; then
	usage >&2
	exit 2
fi
if [[ ! -e "$target" ]]; then
	echo "notarize: not found: $target" >&2
	exit 1
fi

skip() {
	echo "==> notarization skipped: $1"
	if [[ $require -eq 1 ]]; then
		exit 1
	fi
	exit 0
}

auth=()
if [[ -n "${TIEGEL_NOTARY_PROFILE:-}" ]]; then
	auth=(--keychain-profile "$TIEGEL_NOTARY_PROFILE")
	if [[ -n "${TIEGEL_KEYCHAIN:-}" ]]; then
		auth+=(--keychain "$TIEGEL_KEYCHAIN")
	fi
elif [[ -n "${TIEGEL_NOTARY_KEY:-}" && -n "${TIEGEL_NOTARY_KEY_ID:-}" && -n "${TIEGEL_NOTARY_ISSUER:-}" ]]; then
	auth=(--key "$TIEGEL_NOTARY_KEY" --key-id "$TIEGEL_NOTARY_KEY_ID" --issuer "$TIEGEL_NOTARY_ISSUER")
else
	skip "no credentials (set TIEGEL_NOTARY_PROFILE or TIEGEL_NOTARY_KEY/_KEY_ID/_ISSUER)"
fi

sig_info="$(codesign --display --verbose=2 "$target" 2>&1 || true)"
if [[ "$sig_info" == *"code object is not signed"* ]]; then
	skip "$target is not signed (run scripts/macos/sign.sh first)"
fi
if [[ "$sig_info" == *"Signature=adhoc"* ]]; then
	skip "$target is ad-hoc signed (set TIEGEL_SIGN_IDENTITY when signing)"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

submit="$target"
case "$target" in
*.app)
	submit="$work/$(basename "$target" .app).zip"
	ditto -c -k --keepParent "$target" "$submit"
	;;
*.dmg | *.zip | *.pkg) ;;
*) echo "notarize: unsupported file type: $target" >&2; exit 2 ;;
esac

echo "==> submitting $(basename "$submit") to the Apple notary service (waits for result)"
if ! xcrun notarytool submit "$submit" "${auth[@]}" --wait --output-format json >"$work/result.json"; then
	cat "$work/result.json" >&2 || true
	exit 1
fi
cat "$work/result.json"
status="$(plutil -extract status raw -o - "$work/result.json" 2>/dev/null || true)"
if [[ "$status" != "Accepted" ]]; then
	id="$(plutil -extract id raw -o - "$work/result.json" 2>/dev/null || true)"
	echo "notarize: status '$status'" >&2
	if [[ -n "$id" ]]; then
		xcrun notarytool log "$id" "${auth[@]}" >&2 || true
	fi
	exit 1
fi

case "$target" in
*.app | *.dmg | *.pkg)
	echo "==> stapling ticket to $target"
	xcrun stapler staple "$target"
	xcrun stapler validate "$target"
	;;
esac
case "$target" in
*.app) spctl --assess --type execute --verbose=2 "$target" || true ;;
*.dmg) spctl --assess --type open --context context:primary-signature --verbose=2 "$target" || true ;;
esac
echo "==> notarized: $target"
