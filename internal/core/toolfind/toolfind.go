// Package toolfind finds project tools and configuration files on disk for
// setting candidates (D522): Composer projects (also nested ones like
// "project/composer.json" with a custom vendor-dir), their bin scripts
// (vendor/bin/phpunit, phpstan, …), PHAR files and configuration files
// (phpunit.xml, phpstan.neon, …). It only reads the local file system.
package toolfind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDepth is the subfolder depth searched below the project root.
const DefaultDepth = 2

// skip are folders never searched (compared lowercased).
var skip = map[string]bool{"vendor": true, "node_modules": true, ".git": true, ".idea": true, "var": true, "cache": true, "storage": true, "tmp": true}

// ComposerProject is a directory with a composer.json.
type ComposerProject struct {
	// Dir is the absolute directory.
	Dir string `json:"dir"`
	// VendorDir and BinDir are absolute (composer config vendor-dir and
	// bin-dir; defaults vendor and vendor/bin).
	VendorDir string `json:"vendorDir"`
	BinDir    string `json:"binDir"`
}

// ReadComposer returns the Composer project of dir; ok is false without a
// composer.json. An unreadable composer.json uses the defaults.
func ReadComposer(dir string) (ComposerProject, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return ComposerProject{}, false
	}
	var cj struct {
		Config struct {
			VendorDir string `json:"vendor-dir"`
			BinDir    string `json:"bin-dir"`
		} `json:"config"`
	}
	_ = json.Unmarshal(data, &cj)
	p := ComposerProject{Dir: dir, VendorDir: filepath.Join(dir, "vendor")}
	if v := strings.TrimSpace(cj.Config.VendorDir); v != "" {
		p.VendorDir = abs(dir, strings.ReplaceAll(v, "{$vendor-dir}", "vendor"))
	}
	p.BinDir = filepath.Join(p.VendorDir, "bin")
	if b := strings.TrimSpace(cj.Config.BinDir); b != "" {
		b = strings.ReplaceAll(b, "{$vendor-dir}", p.VendorDir)
		p.BinDir = abs(dir, b)
	}
	return p, true
}

func abs(dir, p string) string {
	p = filepath.FromSlash(strings.TrimRight(p, "/"))
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// Dirs returns first, root and the subfolders of root up to depth (0:
// DefaultDepth), breadth first, without vendor, node_modules, hidden and
// cache folders and without duplicates. first may be empty.
func Dirs(root, first string, depth int) []string {
	if depth <= 0 {
		depth = DefaultDepth
	}
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	add(first)
	add(root)
	level := []string{root}
	for i := 0; i < depth; i++ {
		var next []string
		for _, dir := range level {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				n := e.Name()
				if !e.IsDir() || strings.HasPrefix(n, ".") || skip[strings.ToLower(n)] {
					continue
				}
				sub := filepath.Join(dir, n)
				next = append(next, sub)
				add(sub)
			}
		}
		level = next
	}
	return out
}

// ComposerProjects returns the Composer projects among Dirs(root, phpRoot,
// depth): the PHP root first.
func ComposerProjects(root, phpRoot string, depth int) []ComposerProject {
	var out []ComposerProject
	for _, d := range Dirs(root, phpRoot, depth) {
		if p, ok := ReadComposer(d); ok {
			out = append(out, p)
		}
	}
	return out
}

// Found is a file found by Executables or Files.
type Found struct {
	// Path is absolute; Rel is relative to the project root (slashes).
	Path string `json:"path"`
	Rel  string `json:"rel"`
	// InPHPRoot is true for files of the PHP root (its own folder or its
	// Composer bin dir): the natural choice.
	InPHPRoot bool `json:"inPhpRoot"`
}

func found(root, phpRoot, p string, inPHPRoot bool) Found {
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = p
	}
	return Found{Path: p, Rel: filepath.ToSlash(rel), InPHPRoot: inPHPRoot}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Executables finds the scripts called names in the bin dirs of the
// Composer projects (PHP root first) and "<name>.phar" files in their
// folders. The result is ordered: PHP root first, then by path.
func Executables(root, phpRoot string, names ...string) []Found {
	if phpRoot == "" {
		phpRoot = root
	}
	seen := map[string]bool{}
	var out []Found
	add := func(p string, in bool) {
		if !seen[p] && isFile(p) {
			seen[p] = true
			out = append(out, found(root, phpRoot, p, in))
		}
	}
	for _, cp := range ComposerProjects(root, phpRoot, 0) {
		in := cp.Dir == phpRoot
		for _, n := range names {
			add(filepath.Join(cp.BinDir, n), in)
			add(filepath.Join(cp.Dir, n+".phar"), in)
			add(filepath.Join(cp.BinDir, n+".phar"), in)
		}
	}
	for _, n := range names {
		add(filepath.Join(phpRoot, n+".phar"), true)
		add(filepath.Join(root, n+".phar"), root == phpRoot)
	}
	sortFound(out)
	return out
}

// Files finds files called names (in order of preference) in the PHP
// root, the project root and its subfolders (Dirs). The result is
// ordered: PHP root first, then by depth and path.
func Files(root, phpRoot string, names ...string) []Found {
	if phpRoot == "" {
		phpRoot = root
	}
	var out []Found
	for _, d := range Dirs(root, phpRoot, 0) {
		for _, n := range names {
			if p := filepath.Join(d, n); isFile(p) {
				out = append(out, found(root, phpRoot, p, d == phpRoot))
			}
		}
	}
	sortFound(out)
	return out
}

func sortFound(out []Found) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InPHPRoot != out[j].InPHPRoot {
			return out[i].InPHPRoot
		}
		di, dj := strings.Count(out[i].Rel, "/"), strings.Count(out[j].Rel, "/")
		if di != dj {
			return di < dj
		}
		return false
	})
}

// Shells returns the login shells listed in /etc/shells (or file) that
// exist, in file order.
func Shells(file string) []string {
	if file == "" {
		file = "/etc/shells"
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || seen[line] || !isFile(line) {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}
