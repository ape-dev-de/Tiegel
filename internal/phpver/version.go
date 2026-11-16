package phpver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a PHP version. Missing components are zero, so "8.1" equals
// "8.1.0".
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses versions such as "8", "8.2", "8.3.12", "v8.1",
// "8.4.0RC1" or "8.3.0-dev". Anything after the numeric part is ignored.
func ParseVersion(s string) (Version, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	var parts [3]int
	n := 0
	for n < 3 {
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			break
		}
		v, err := strconv.Atoi(s[:i])
		if err != nil {
			return Version{}, fmt.Errorf("phpver: parse version %q: %w", orig, err)
		}
		parts[n] = v
		n++
		s = s[i:]
		if len(s) < 2 || s[0] != '.' || s[1] < '0' || s[1] > '9' {
			break
		}
		s = s[1:]
	}
	if n == 0 {
		return Version{}, fmt.Errorf("phpver: parse version %q: no version number", orig)
	}
	return Version{Major: parts[0], Minor: parts[1], Patch: parts[2]}, nil
}

// String returns "major.minor.patch".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Short returns "major.minor".
func (v Version) Short() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero reports whether v is the zero Version.
func (v Version) IsZero() bool { return v == Version{} }

// Compare returns -1, 0 or +1.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sign(v.Major - o.Major)
	case v.Minor != o.Minor:
		return sign(v.Minor - o.Minor)
	default:
		return sign(v.Patch - o.Patch)
	}
}

// Less reports whether v < o.
func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

func sign(i int) int {
	switch {
	case i < 0:
		return -1
	case i > 0:
		return 1
	}
	return 0
}
