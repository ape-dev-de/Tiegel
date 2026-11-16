// Package fspath holds small, OS-aware helpers for host file paths that
// several packages need to agree on.
package fspath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// ValidName checks a single file or directory name that is about to be
// created on goos ("windows", "darwin", "linux", ...). It rejects empty
// names, "." and "..", path separators and NUL everywhere; on Windows also
// the characters <>:"|?*, control characters, a trailing dot or space and
// the reserved device names (CON, PRN, AUX, NUL, COM1–9, LPT1–9, also with
// an extension such as "nul.txt").
func ValidName(name, goos string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("invalid name %q", name)
	case strings.ContainsAny(name, "/\x00"):
		return fmt.Errorf("invalid name %q: contains a path separator or NUL", name)
	}
	if goos != "windows" {
		return nil
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"\|?*`, r) {
			return fmt.Errorf("invalid name %q: %q is not allowed on Windows", name, r)
		}
	}
	if last := name[len(name)-1]; last == '.' || last == ' ' {
		return fmt.Errorf("invalid name %q: Windows names must not end with a dot or space", name)
	}
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return fmt.Errorf("invalid name %q: reserved device name on Windows", name)
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return fmt.Errorf("invalid name %q: reserved device name on Windows", name)
	}
	return nil
}

// Within reports whether path lies inside root or is root itself. Both
// must be clean absolute paths; the comparison is lexical (resolve
// symlinks first where that matters).
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
