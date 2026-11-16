package index

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/ape-dev-de/tiegel/internal/core/gitignore"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func errNoTreeSitter() error { return syntax.ErrNoTreeSitter }

// libraryDirs hold third-party code: indexed (unless SkipLibraries) even
// when .gitignore lists them, and marked Library.
var libraryDirs = map[string]bool{"vendor": true}

// alwaysSkipped are never descended into.
var alwaysSkipped = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, ".idea": true}

type scanned struct {
	path    string
	library bool
	mtime   int64
	size    int64
}

func (ix *Index) matchExclude(rel, base string) bool {
	for _, p := range ix.opts.Exclude {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}

// scan lists the indexable files below the root.
func (ix *Index) scan(ctx context.Context) ([]scanned, error) {
	root := ix.opts.Root
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		if err == nil {
			err = fs.ErrInvalid
		}
		return nil, err
	}
	var ign gitignore.Set
	var out []scanned
	var walk func(rel string, library bool) error
	walk = func(rel string, library bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if !ix.opts.IncludeIgnored && !library {
			ign.Load(root, rel)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // unreadable directory: skip
		}
		for _, e := range entries {
			name := e.Name()
			if alwaysSkipped[name] {
				continue
			}
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			if ix.matchExclude(r, name) {
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				st, err := os.Stat(filepath.Join(dir, name))
				if err != nil || st.IsDir() { // never follow directory links (cycles)
					continue
				}
			}
			if e.IsDir() {
				lib := library || libraryDirs[name]
				if lib && !library && ix.opts.SkipLibraries {
					continue
				}
				if !lib && !ix.opts.IncludeIgnored && ign.Match(r, true) {
					continue
				}
				if err := walk(r, lib); err != nil {
					return err
				}
				continue
			}
			abs := filepath.Join(dir, name)
			if !ix.Accepts(name) {
				continue
			}
			if !library && !ix.opts.IncludeIgnored && ign.Match(r, false) {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Size() > ix.opts.MaxFileSize {
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				if st, err := os.Stat(abs); err == nil {
					info = st
				}
			}
			out = append(out, scanned{path: abs, library: library, mtime: info.ModTime().UnixNano(), size: info.Size()})
		}
		return nil
	}
	if err := walk("", false); err != nil {
		return nil, err
	}
	return out, nil
}
