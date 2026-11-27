package knowledge

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Checker verifies the claims of a description mechanically: referenced
// files must exist in the project, symbols must occur in them (or be
// found by Lookup in another file, which then replaces the file).
type Checker struct {
	// Root is the project root.
	Root string
	// Lookup finds the project relative files declaring a symbol
	// (Class::method, function, class); nil skips the index.
	Lookup func(symbol string) []string

	cache map[string][]string // file → lines
}

// Verify checks and fixes the references of d in place: file paths are
// made project relative, lines are set to the symbol's declaration,
// Verified is set on every reference. It returns the number of
// unverified references and notes for the validating agent.
func (c *Checker) Verify(d Doc) (unverified int, notes []string) {
	return c.VerifyRefs(d.refs())
}

// VerifyRefs checks and fixes references in place (see Verify).
// References without file and symbol are skipped.
func (c *Checker) VerifyRefs(refs []*Ref) (unverified int, notes []string) {
	for _, r := range refs {
		if strings.TrimSpace(r.File) == "" && strings.TrimSpace(r.Symbol) == "" {
			continue
		}
		label := r.Symbol
		if label == "" {
			label = r.File
		}
		ok, note := c.step(r)
		r.Verified = &ok
		if !ok {
			unverified++
		}
		if note != "" {
			notes = append(notes, label+": "+note)
		}
	}
	return unverified, notes
}

func (c *Checker) step(s *Ref) (bool, string) {
	s.File = c.rel(s.File)
	lines, exists := c.lines(s.File)
	short := ShortSymbol(s.Symbol)
	if exists && short == "" {
		return true, ""
	}
	if exists {
		if ln := findSymbol(lines, short); ln > 0 {
			if s.Line <= 0 || s.Line > len(lines) || !strings.Contains(lines[s.Line-1], short) {
				s.Line = ln
			}
			return true, ""
		}
	}
	if c.Lookup != nil && s.Symbol != "" {
		for _, f := range c.Lookup(s.Symbol) {
			f = c.rel(f)
			if ls, ok := c.lines(f); ok {
				if ln := findSymbol(ls, short); ln > 0 {
					note := fmt.Sprintf("%s is declared in %s, not in %s (fixed)", s.Symbol, f, s.File)
					s.File, s.Line = f, ln
					return true, note
				}
			}
		}
	}
	if !exists {
		s.Line = 0
		return false, fmt.Sprintf("file %s does not exist", s.File)
	}
	s.Line = 0
	return false, fmt.Sprintf("symbol %s not found in %s", s.Symbol, s.File)
}

// rel makes a path project relative (slash separated).
func (c *Checker) rel(f string) string {
	f = strings.TrimSpace(strings.TrimPrefix(f, "file://"))
	if i := strings.LastIndexByte(f, ':'); i > 1 && isDigits(f[i+1:]) {
		f = f[:i] // "src/a.php:12"
	}
	if filepath.IsAbs(f) {
		if r, err := filepath.Rel(c.Root, f); err == nil && !strings.HasPrefix(r, "..") {
			f = r
		}
	}
	return strings.TrimPrefix(path.Clean(filepath.ToSlash(f)), "./")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// lines reads a project file (cached); false when it does not exist or
// lies outside the project.
func (c *Checker) lines(rel string) ([]string, bool) {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return nil, false
	}
	if c.cache == nil {
		c.cache = map[string][]string{}
	}
	if l, ok := c.cache[rel]; ok {
		return l, l != nil
	}
	b, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(rel)))
	if err != nil {
		c.cache[rel] = nil
		return nil, false
	}
	l := strings.Split(string(b), "\n")
	c.cache[rel] = l
	return l, true
}

var symbolTrim = regexp.MustCompile(`\(.*$`)

// ShortSymbol returns the last name of a symbol ("App\Foo::bar()" →
// "bar", "App\Foo" → "Foo", "$x" → "x").
func ShortSymbol(sym string) string {
	sym = strings.TrimSpace(symbolTrim.ReplaceAllString(sym, ""))
	if i := strings.LastIndex(sym, "::"); i >= 0 {
		sym = sym[i+2:]
	}
	if i := strings.LastIndex(sym, "->"); i >= 0 {
		sym = sym[i+2:]
	}
	if i := strings.LastIndexByte(sym, '\\'); i >= 0 {
		sym = sym[i+1:]
	}
	return strings.TrimPrefix(sym, "$")
}

// findSymbol returns the 1-based line declaring name (function, class,
// const, property), else the first line mentioning it as a word, else 0.
func findSymbol(lines []string, name string) int {
	if name == "" {
		return 0
	}
	q := regexp.QuoteMeta(name)
	decl := regexp.MustCompile(`(?i)\b(function|class|interface|trait|enum|const|case)\s+&?` + q + `\b|\$` + q + `\b`)
	word := regexp.MustCompile(`\b` + q + `\b`)
	first := 0
	for i, l := range lines {
		if decl.MatchString(l) {
			return i + 1
		}
		if first == 0 && word.MatchString(l) {
			first = i + 1
		}
	}
	return first
}
