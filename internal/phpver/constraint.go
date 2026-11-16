package phpver

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoLowerBound is returned by MinVersion for constraints that allow any
// version from 0 upwards (e.g. "*" or "<8.0").
var ErrNoLowerBound = errors.New("phpver: constraint has no lower bound")

// MinVersion returns the smallest version allowed by a Composer version
// constraint such as "^8.1", "~7.4.0", ">=8.0 <8.4", "^7.4 || ^8.0",
// "8.1 - 8.3", "8.2.*" or "8.1.0@dev".
//
// Operators: ^ ~ = == >= > <= < != and bare versions; wildcards "*" / "x";
// hyphen ranges; AND by space or comma; OR by "||" (or "|"). Upper bounds are
// ignored. For ">x" the next patch release is returned. Stability suffixes
// ("@dev", "-beta") are ignored.
func MinVersion(constraint string) (Version, error) {
	c := strings.TrimSpace(constraint)
	if c == "" {
		return Version{}, errors.New("phpver: empty constraint")
	}
	c = strings.ReplaceAll(c, "||", "|")
	var best Version
	found := false
	for _, alt := range strings.Split(c, "|") {
		v, ok, err := minOfConjunction(alt)
		if err != nil {
			return Version{}, fmt.Errorf("phpver: constraint %q: %w", constraint, err)
		}
		if !ok {
			// One alternative without lower bound makes the whole
			// constraint unbounded below.
			return Version{}, ErrNoLowerBound
		}
		if !found || v.Less(best) {
			best, found = v, true
		}
	}
	return best, nil
}

// minOfConjunction returns the largest lower bound of an AND-list.
func minOfConjunction(s string) (Version, bool, error) {
	toks := strings.Fields(strings.ReplaceAll(s, ",", " "))
	if len(toks) == 0 {
		return Version{}, false, errors.New("empty alternative")
	}
	// Re-attach operators written with a space: ">= 8.1".
	var atoms []string
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if strings.Trim(t, "<>=!^~") == "" && t != "-" && i+1 < len(toks) {
			t += toks[i+1]
			i++
		}
		atoms = append(atoms, t)
	}
	var lower Version
	bounded := false
	for i := 0; i < len(atoms); i++ {
		a := atoms[i]
		if i+2 < len(atoms) && atoms[i+1] == "-" {
			// Hyphen range "a - b": lower bound a.
			i += 2
			a = ">=" + a
		}
		v, ok, err := atomLower(a)
		if err != nil {
			return Version{}, false, err
		}
		if ok && (!bounded || lower.Less(v)) {
			lower, bounded = v, true
		}
	}
	return lower, bounded, nil
}

// atomLower returns the lower bound of a single constraint atom.
func atomLower(a string) (Version, bool, error) {
	if i := strings.IndexByte(a, '@'); i >= 0 {
		a = a[:i]
	}
	op := ""
	for _, p := range []string{">=", "<=", "==", "!=", "<>", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(a, p) {
			op, a = p, strings.TrimSpace(a[len(p):])
			break
		}
	}
	switch op {
	case "<", "<=", "!=", "<>":
		return Version{}, false, nil
	}
	a = strings.TrimPrefix(strings.TrimPrefix(a, "v"), "V")
	switch a {
	case "":
		return Version{}, false, errors.New("operator without version")
	case "*", "x", "X":
		return Version{}, false, nil
	}
	// Wildcards: "8.*", "8.1.x" -> cut at the wildcard.
	parts := strings.Split(a, ".")
	for i, p := range parts {
		if p == "*" || p == "x" || p == "X" {
			parts = parts[:i]
			break
		}
	}
	v, err := ParseVersion(strings.Join(parts, "."))
	if err != nil {
		return Version{}, false, err
	}
	if op == ">" {
		v.Patch++
	}
	return v, true, nil
}
