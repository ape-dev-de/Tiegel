// Package version holds build metadata injected at link time.
//
// The release scripts set the variables with
//
//	go build -ldflags "-X github.com/ape-dev-de/tiegel/internal/version.Version=v1.2.3 \
//	  -X github.com/ape-dev-de/tiegel/internal/version.Commit=abc1234 \
//	  -X github.com/ape-dev-de/tiegel/internal/version.Date=2026-01-02T03:04:05Z"
//
// A plain `go build` leaves the defaults in place.
package version

import "fmt"

// Version is the release version (usually the output of `git describe --tags`).
var Version = "dev"

// Commit is the abbreviated git commit hash of the build.
var Commit = "unknown"

// Date is the build timestamp in RFC 3339 format (UTC).
var Date = "unknown"

// String returns a human readable one-line description of the build.
func String() string {
	return fmt.Sprintf("Tiegel %s (commit %s, built %s)", Version, Commit, Date)
}

// IsDev reports whether the binary was built without an injected version.
func IsDev() bool {
	return Version == "" || Version == "dev"
}
