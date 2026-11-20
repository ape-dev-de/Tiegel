// Package projectroles derives the roles of project folders (source,
// test, resource and library roots) for the project tree's folder icons.
// Source and test roots come from the autoload and autoload-dev sections
// of composer.json (psr-4, psr-0 and classmap directories), the library
// root from config.vendor-dir; without them the conventional top-level
// names are used. It does not import Gio.
package projectroles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Role is the role of a folder.
type Role string

// Folder roles.
const (
	Source   Role = "source"
	Test     Role = "test"
	Resource Role = "resource"
	Library  Role = "library"
)

// Origin tells where a role comes from.
type Origin string

// Origins of a role.
const (
	// FromComposer: autoload/autoload-dev or config.vendor-dir.
	FromComposer Origin = "composer"
	// FromName: a conventional top-level folder name (fallback).
	FromName Origin = "name"
)

// Root is a folder with a role.
type Root struct {
	// Path is project relative and slash separated, without trailing
	// slash.
	Path   string `json:"path"`
	Role   Role   `json:"role"`
	Origin Origin `json:"origin"`
}

// Conventional top-level names used when composer.json declares no root
// of that role.
var (
	sourceNames   = []string{"src", "app", "lib"}
	testNames     = []string{"tests", "test"}
	resourceNames = []string{"resources", "assets", "public"}
)

// ErrComposer wraps an unreadable or invalid composer.json; the roots
// derived from names are returned with it.
var ErrComposer = errors.New("composer.json")

// Detect returns the folder roles of the project at root, sorted by path.
// Only existing directories are reported. A path claimed by several
// sections keeps the first role in the order autoload (source),
// autoload-dev (test), vendor-dir (library). An invalid composer.json is
// reported as ErrComposer together with the name-based roots.
func Detect(root string) ([]Root, error) {
	c, cerr := readComposer(root)
	var out []Root
	seen := map[string]bool{}
	add := func(dir string, role Role, origin Origin) {
		dir = norm(dir)
		if dir == "" || seen[dir] || !isDir(root, dir) {
			return
		}
		seen[dir] = true
		out = append(out, Root{Path: dir, Role: role, Origin: origin})
	}
	has := map[Role]bool{}
	if c != nil {
		for _, d := range c.Autoload.dirs() {
			add(d, Source, FromComposer)
		}
		for _, d := range c.AutoloadDev.dirs() {
			add(d, Test, FromComposer)
		}
		vendor := "vendor"
		if c.Config.VendorDir != "" {
			vendor = c.Config.VendorDir
		}
		add(vendor, Library, FromComposer)
		for _, r := range out {
			has[r.Role] = true
		}
	}
	for _, g := range []struct {
		role  Role
		names []string
	}{{Source, sourceNames}, {Test, testNames}, {Resource, resourceNames}} {
		if has[g.role] {
			continue
		}
		for _, n := range g.names {
			add(n, g.role, FromName)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, cerr
}

// Lookup returns the role of the folder rel (project relative, slash
// separated) in roots.
func Lookup(roots []Root, rel string) (Role, bool) {
	rel = norm(rel)
	for _, r := range roots {
		if r.Path == rel {
			return r.Role, true
		}
	}
	return "", false
}

type autoload struct {
	PSR4     map[string]json.RawMessage `json:"psr-4"`
	PSR0     map[string]json.RawMessage `json:"psr-0"`
	Classmap []string                   `json:"classmap"`
}

// dirs returns the directories of the section in a stable order.
func (a autoload) dirs() []string {
	var out []string
	for _, m := range []map[string]json.RawMessage{a.PSR4, a.PSR0} {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var one string
			var many []string
			if json.Unmarshal(m[k], &one) == nil {
				out = append(out, one)
			} else if json.Unmarshal(m[k], &many) == nil {
				out = append(out, many...)
			}
		}
	}
	return append(out, a.Classmap...)
}

type composer struct {
	Autoload    autoload `json:"autoload"`
	AutoloadDev autoload `json:"autoload-dev"`
	Config      struct {
		VendorDir string `json:"vendor-dir"`
	} `json:"config"`
}

func readComposer(root string) (*composer, error) {
	data, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrComposer, err)
	}
	var c composer
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrComposer, err)
	}
	return &c, nil
}

// norm makes d a clean project-relative slash path; "" for the root and
// for paths leaving the project.
func norm(d string) string {
	d = strings.TrimSpace(strings.ReplaceAll(d, `\`, "/"))
	if d == "" || path.IsAbs(d) || filepath.IsAbs(d) {
		return ""
	}
	d = path.Clean(d)
	if d == "." || d == ".." || strings.HasPrefix(d, "../") {
		return ""
	}
	return d
}

func isDir(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}
