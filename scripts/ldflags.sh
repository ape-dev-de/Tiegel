#!/usr/bin/env bash
# Prints the -X linker flags that embed version information into Tiegel
# (package internal/version). Used by scripts/macos/build-app.sh and the
# release workflow.
#
#   go build -ldflags "-s -w $(scripts/ldflags.sh)" ./cmd/tiegel
#
# Environment:
#   TIEGEL_VERSION  override the version (default: git describe --tags --always --dirty)
#   TIEGEL_COMMIT   override the commit  (default: git rev-parse --short HEAD)
#   TIEGEL_DATE     override the date    (default: commit time, UTC; reproducible)
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
	exit 0
fi

cd "$(dirname "$0")/.."
pkg="github.com/ape-dev-de/tiegel/internal/version"

version="${TIEGEL_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
commit="${TIEGEL_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
if [[ -z "${TIEGEL_DATE:-}" ]]; then
	ts="$(git log -1 --format=%ct 2>/dev/null || date +%s)"
	TIEGEL_DATE="$(date -u -r "$ts" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$ts" +%Y-%m-%dT%H:%M:%SZ)"
fi

echo "-X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${TIEGEL_DATE}"
