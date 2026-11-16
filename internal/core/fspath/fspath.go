// Package fspath holds small, OS-aware helpers for host file paths that
// several packages need to agree on.
package fspath

import (
	"os"
	"path/filepath"
)

// IsAbs reports whether p must not be joined with a base directory: it is
// absolute (filepath.IsAbs) or rooted, i.e. starts with a path separator.
//
// The difference only matters on Windows, where `\x` and `/x` are not
// absolute (they lack a drive) but refer to the root of the current drive.
// Joining them with a project root (`C:\proj` + `\x` = `C:\proj\x`) would
// silently turn a path outside the project into one inside it.
func IsAbs(p string) bool {
	return filepath.IsAbs(p) || p != "" && os.IsPathSeparator(p[0])
}

// Resolve returns p cleaned if IsAbs(p), otherwise base joined with p
// (slash separators in p are accepted on every OS).
func Resolve(base, p string) string {
	if IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, filepath.FromSlash(p))
}
