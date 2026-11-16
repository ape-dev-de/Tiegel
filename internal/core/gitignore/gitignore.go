// Package gitignore is a rough .gitignore matcher shared by the file tree
// (files.list) and the PHP symbol index. Git stays the authority for VCS
// operations.
package gitignore

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignoreRule is one line of a .gitignore file.
type ignoreRule struct {
	base     string // directory of the .gitignore, slash separated, relative to the root ("" for the root)
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool // pattern contains a slash: match against the path relative to base
}

// Set is a rough .gitignore implementation: it supports comments,
// negation, trailing "/" (directories only), leading "/" and inner slashes
// (anchored), "*", "?", "[...]" and "**/" prefixes and "/**" suffixes. Nested
// .gitignore files are honoured for their subtree. It is good enough for a
// project tree; Git itself stays the authority for VCS operations.
type Set struct {
	rules []ignoreRule
}

// Load reads dir/.gitignore (dir relative to root, slash separated) and adds
// its rules.
func (s *Set) Load(root, rel string) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel), ".gitignore"))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		s.Add(rel, sc.Text())
	}
}

// Add adds one .gitignore line of the file in directory base (relative to
// the root, slash separated; "" for the root).
func (s *Set) Add(base, line string) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	r := ignoreRule{base: base}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	}
	line = strings.TrimPrefix(line, `\`)
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	line = strings.TrimPrefix(line, "**/")
	if strings.Contains(line, "/") {
		r.anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	if line == "" {
		return
	}
	r.pattern = line
	s.rules = append(s.rules, r)
}

// Match reports whether rel (slash separated, relative to the root) is
// ignored. The last matching rule wins.
func (s *Set) Match(rel string, isDir bool) bool {
	ignored := false
	for _, r := range s.rules {
		if r.dirOnly && !isDir {
			continue
		}
		p := rel
		if r.base != "" {
			if !strings.HasPrefix(rel, r.base+"/") {
				continue
			}
			p = strings.TrimPrefix(rel, r.base+"/")
		}
		if r.matches(p) {
			ignored = !r.negate
		}
	}
	return ignored
}

func (r ignoreRule) matches(p string) bool {
	if !r.anchored {
		ok, _ := path.Match(r.pattern, path.Base(p))
		return ok
	}
	pat := r.pattern
	if strings.HasSuffix(pat, "/**") {
		prefix := strings.TrimSuffix(pat, "/**")
		ok, _ := path.Match(prefix, firstSegments(p, strings.Count(prefix, "/")+1))
		return ok && strings.Count(p, "/") > strings.Count(prefix, "/")
	}
	if i := strings.Index(pat, "/**/"); i >= 0 {
		head, tail := pat[:i], pat[i+4:]
		n := strings.Count(head, "/") + 1
		if ok, _ := path.Match(head, firstSegments(p, n)); !ok {
			return false
		}
		rest := strings.Split(p, "/")[n:]
		m := strings.Count(tail, "/") + 1
		for j := 0; j+m <= len(rest); j++ {
			if ok, _ := path.Match(tail, strings.Join(rest[j:j+m], "/")); ok {
				return true
			}
		}
		return false
	}
	ok, _ := path.Match(pat, p)
	return ok
}

func firstSegments(p string, n int) string {
	parts := strings.Split(p, "/")
	if n > len(parts) {
		n = len(parts)
	}
	return strings.Join(parts[:n], "/")
}
