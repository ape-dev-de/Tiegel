package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (https://semver.org).
//
// Build metadata is dropped because it does not affect precedence.
type Version struct {
	Major, Minor, Patch int
	// Pre holds the dot-separated pre-release identifiers (empty for releases).
	Pre []string
}

// describeSuffix matches the "-<n>-g<hash>[-dirty]" tail that `git describe`
// appends to builds made after a tag, or a bare "-dirty".
var describeSuffix = regexp.MustCompile(`(-[0-9]+-g[0-9a-f]+)?(-dirty)?$`)

var identRe = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

// ParseVersion parses a semantic version. A leading "v" is accepted, and
// missing minor/patch components default to zero ("v2" == "2.0.0").
//
// Output of `git describe --tags` is understood as well: the suffix
// "-<n>-g<hash>" and "-dirty" are treated as build metadata, so a build made
// after tag v1.2.3 compares equal to v1.2.3 instead of as a pre-release of it.
func ParseVersion(s string) (Version, error) {
	var v Version
	raw := strings.TrimSpace(s)
	str := strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V")
	if i := strings.IndexByte(str, '+'); i >= 0 {
		str = str[:i]
	}
	str = describeSuffix.ReplaceAllString(str, "")
	if str == "" {
		return v, fmt.Errorf("parse version %q: empty", raw)
	}

	core, pre, hasPre := strings.Cut(str, "-")
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("parse version %q: too many components", raw)
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := parseNumeric(p)
		if err != nil {
			return v, fmt.Errorf("parse version %q: %w", raw, err)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]

	if hasPre {
		for _, id := range strings.Split(pre, ".") {
			if !identRe.MatchString(id) {
				return v, fmt.Errorf("parse version %q: invalid pre-release identifier %q", raw, id)
			}
			v.Pre = append(v.Pre, id)
		}
	}
	return v, nil
}

func parseNumeric(p string) (int, error) {
	if p == "" {
		return 0, fmt.Errorf("empty numeric component")
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-numeric component %q", p)
		}
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0, fmt.Errorf("numeric component %q: %w", p, err)
	}
	return n, nil
}

// String formats the version without a "v" prefix.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// IsPrerelease reports whether v carries pre-release identifiers.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or +1 if v is lower than, equal to or higher than w,
// following semver precedence rules.
func (v Version) Compare(w Version) int {
	if c := cmpInt(v.Major, w.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, w.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, w.Patch); c != 0 {
		return c
	}
	// A release has higher precedence than any of its pre-releases.
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		if c := cmpIdent(v.Pre[i], w.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.Pre), len(w.Pre))
}

func cmpIdent(a, b string) int {
	an, aErr := parseNumeric(a)
	bn, bErr := parseNumeric(b)
	switch {
	case aErr == nil && bErr == nil:
		return cmpInt(an, bn)
	case aErr == nil:
		return -1 // numeric identifiers sort before alphanumeric ones
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
