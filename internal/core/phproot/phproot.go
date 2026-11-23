// Package phproot finds the PHP project root of a project: the directory
// with composer.json that composer, vendor/, phpunit.xml, PSR-4 and the
// quality tools resolve from. It is the project root for most projects;
// a repository may keep its PHP project in a subfolder (project/,
// backend/ …). Candidates are the project root and directories up to two
// levels below it that hold a composer.json (D515). It does not import
// Gio.
package phproot

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// MaxDepth is the deepest level below the project root searched for a
// composer.json.
const MaxDepth = 2

// Reasons of a candidate (Candidate.Reasons).
const (
	ReasonComposer     = "composer.json"
	ReasonVendor       = "vendor/"
	ReasonPHPUnit      = "phpunit.xml"
	ReasonComposerLock = "composer.lock"
	ReasonProjectRoot  = "project root"
)

// Candidate is a directory that may be the PHP root.
type Candidate struct {
	// Path is project relative and slash separated; "." is the project
	// root.
	Path string `json:"path"`
	// Reasons list what was found there (Reason* constants).
	Reasons []string `json:"reasons"`
	// Recommended marks the best candidate (the automatic choice).
	Recommended bool `json:"recommended,omitempty"`

	score, depth int
}

// skipDirs are never searched.
var skipDirs = map[string]bool{"vendor": true, "node_modules": true}

// phpunitConfigs are the configuration files of PHPUnit.
var phpunitConfigs = []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"}

// Detect returns the candidates of the project at root, best first: a
// directory with vendor/ wins over one with phpunit.xml, over one with
// composer.lock; equal ones prefer the shallower path, then the name.
// Without any composer.json the result is empty.
func Detect(root string) []Candidate {
	var out []Candidate
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if c, ok := inspect(dir, rel, depth); ok {
			out = append(out, c)
		}
		if depth >= MaxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)] {
				continue
			}
			walk(filepath.Join(dir, name), path.Join(rel, name), depth+1)
		}
	}
	walk(root, ".", 0)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		return a.Path < b.Path
	})
	if len(out) > 0 {
		out[0].Recommended = true
	}
	return out
}

// inspect scores dir when it holds a composer.json.
func inspect(dir, rel string, depth int) (Candidate, bool) {
	if !isFile(filepath.Join(dir, "composer.json")) {
		return Candidate{}, false
	}
	c := Candidate{Path: rel, Reasons: []string{ReasonComposer}, depth: depth}
	if isDir(Vendor(dir)) {
		c.Reasons = append(c.Reasons, ReasonVendor)
		c.score += 4
	}
	for _, n := range phpunitConfigs {
		if isFile(filepath.Join(dir, n)) {
			c.Reasons = append(c.Reasons, ReasonPHPUnit)
			c.score += 2
			break
		}
	}
	if isFile(filepath.Join(dir, "composer.lock")) {
		c.Reasons = append(c.Reasons, ReasonComposerLock)
		c.score++
	}
	if depth == 0 {
		c.Reasons = append(c.Reasons, ReasonProjectRoot)
		// The project root's own composer.json always wins: the
		// behaviour of root-level projects does not change.
		c.score += 100
	}
	return c, true
}

// Resolve returns the effective PHP root (absolute) of the project at
// root: the setting (project relative, "" = automatic) when it names a
// directory inside the project, else the best candidate, else root.
// auto reports that the setting did not decide.
func Resolve(root, setting string) (abs string, auto bool) {
	if root == "" {
		return "", true
	}
	if d, ok := FromSetting(root, setting); ok {
		return d, false
	}
	if isFile(filepath.Join(root, "composer.json")) { // always the best
		return root, true
	}
	if cs := Detect(root); len(cs) > 0 {
		return Abs(root, cs[0].Path), true
	}
	return root, true
}

// FromSetting returns the directory the setting value names (project
// relative or absolute below root); ok is false for "" and for values
// that leave the project or name no directory.
func FromSetting(root, setting string) (string, bool) {
	s := strings.TrimSpace(setting)
	if s == "" || root == "" {
		return "", false
	}
	d := s
	if !filepath.IsAbs(d) {
		d = filepath.Join(root, filepath.FromSlash(d))
	}
	d = filepath.Clean(d)
	if !Within(root, d) || !isDir(d) {
		return "", false
	}
	return d, true
}

// Abs turns a candidate path into an absolute directory below root.
func Abs(root, rel string) string {
	if rel == "" || rel == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// Rel returns dir relative to root, slash separated ("." for root).
func Rel(root, dir string) string {
	r, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(r)
}

// Within reports whether p is dir or below it.
func Within(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !filepath.IsAbs(r) && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
