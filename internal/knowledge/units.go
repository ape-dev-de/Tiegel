package knowledge

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Unit is one piece of work of the pipeline: a directory (usually a
// PSR-4 namespace) with its PHP files.
type Unit struct {
	// ID is stable and file-name safe ("src-billing").
	ID string `json:"id"`
	// Title is the namespace ("App\Billing") or the directory.
	Title string `json:"title"`
	// Dir is the project relative directory ("." for the root).
	Dir string `json:"dir"`
	// Files are the project relative PHP files (sorted).
	Files []string `json:"files"`
	// Bytes is the size of all files.
	Bytes int64 `json:"bytes"`
}

// PlanOptions configure PlanUnits.
type PlanOptions struct {
	// PSR4 maps namespace prefixes to directories (from composer.json).
	PSR4 map[string][]string
	// Scope restricts the plan to these project relative directories
	// (empty: everything).
	Scope []string
	// Exclude are directory names skipped anywhere (vendor, tests …).
	Exclude []string
	// MaxFiles splits a unit with more files into its subdirectories
	// (default 40).
	MaxFiles int
}

// DefaultExclude are the directory names left out by default.
var DefaultExclude = []string{"vendor", "node_modules", "var", "cache", "storage", "tests", "test", "Tests", "fixtures", "migrations", ".git", ".idea"}

// ReadPSR4 reads autoload and autoload-dev psr-4 of composer.json in
// root (relative directories, slash separated). A missing or broken file
// yields nil.
func ReadPSR4(root string) map[string][]string {
	b, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if err != nil {
		return nil
	}
	var c struct {
		Autoload struct {
			PSR4 map[string]json.RawMessage `json:"psr-4"`
		} `json:"autoload"`
	}
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	out := map[string][]string{}
	for ns, raw := range c.Autoload.PSR4 {
		var one string
		var many []string
		if json.Unmarshal(raw, &one) == nil {
			many = []string{one}
		} else if json.Unmarshal(raw, &many) != nil {
			continue
		}
		for _, d := range many {
			d = strings.Trim(path.Clean(filepath.ToSlash(d)), "/")
			if d == "" {
				d = "."
			}
			out[ns] = append(out[ns], d)
		}
	}
	return out
}

// PlanUnits groups PHP files (project relative, slash separated) into
// units: each top-level directory below a PSR-4 source directory (or
// below the project root) is a unit; units with more than MaxFiles files
// are split into their subdirectories.
func PlanUnits(files []string, o PlanOptions) []Unit {
	if o.MaxFiles <= 0 {
		o.MaxFiles = 40
	}
	if o.Exclude == nil {
		o.Exclude = DefaultExclude
	}
	type root struct{ dir, ns string }
	var roots []root
	for ns, dirs := range o.PSR4 {
		for _, d := range dirs {
			roots = append(roots, root{d, strings.TrimSuffix(ns, `\`)})
		}
	}
	// Longest directory first: nested source roots win.
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i].dir) != len(roots[j].dir) {
			return len(roots[i].dir) > len(roots[j].dir)
		}
		return roots[i].dir < roots[j].dir
	})
	var scope []string
	for _, s := range o.Scope {
		s = strings.Trim(path.Clean(filepath.ToSlash(s)), "/")
		if s != "" {
			scope = append(scope, s)
		}
	}
	groups := map[string][]string{} // unit dir → files
	ns := map[string]string{}       // unit dir → namespace title
	for _, f := range files {
		f = path.Clean(filepath.ToSlash(f))
		if !strings.EqualFold(path.Ext(f), ".php") || excluded(f, o.Exclude, scope) || !inScope(f, scope) {
			continue
		}
		base, title := ".", ""
		for _, r := range roots {
			if r.dir == "." || strings.HasPrefix(f, r.dir+"/") {
				base, title = r.dir, r.ns
				break
			}
		}
		rel := f
		if base != "." {
			rel = strings.TrimPrefix(f, base+"/")
		}
		dir := base
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			dir = path.Join(base, rel[:i])
			if title != "" {
				title += `\` + rel[:i]
			}
		}
		groups[dir] = append(groups[dir], f)
		if title != "" {
			ns[dir] = title
		}
	}
	var units []Unit
	var add func(dir string, fs []string)
	add = func(dir string, fs []string) {
		if len(fs) > o.MaxFiles {
			sub := map[string][]string{}
			var own []string
			for _, f := range fs {
				rel := strings.TrimPrefix(f, dir+"/")
				if dir == "." {
					rel = f
				}
				if i := strings.IndexByte(rel, '/'); i >= 0 {
					sub[path.Join(dir, rel[:i])] = append(sub[path.Join(dir, rel[:i])], f)
				} else {
					own = append(own, f)
				}
			}
			if len(sub) > 0 {
				for d, l := range sub {
					if t := ns[dir]; t != "" {
						ns[d] = t + `\` + path.Base(d)
					}
					add(d, l)
				}
				if len(own) > 0 {
					units = append(units, newUnit(dir, ns[dir], own))
				}
				return
			}
		}
		units = append(units, newUnit(dir, ns[dir], fs))
	}
	for dir, fs := range groups {
		add(dir, fs)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	// IDs must be unique (a split unit keeps its own files under its ID).
	seen := map[string]int{}
	for i := range units {
		if n := seen[units[i].ID]; n > 0 {
			units[i].ID += "-" + string(rune('a'+n))
		}
		seen[units[i].ID]++
	}
	return units
}

func newUnit(dir, title string, files []string) Unit {
	files = slices.Clone(files)
	sort.Strings(files)
	if title == "" {
		title = dir
		if dir == "." {
			title = "(project root)"
		}
	}
	return Unit{ID: UnitID(dir), Title: title, Dir: dir, Files: files}
}

var unsafeRE = regexp.MustCompile(`[^a-z0-9]+`)

// UnitID derives a stable, file-name safe ID from a directory.
func UnitID(dir string) string {
	if dir == "." || dir == "" {
		return "root"
	}
	id := strings.Trim(unsafeRE.ReplaceAllString(strings.ToLower(dir), "-"), "-")
	if id == "" {
		return "unit"
	}
	return id
}

// excluded reports whether a directory component of f is excluded –
// unless the user's scope names that directory explicitly.
func excluded(f string, ex []string, scope []string) bool {
	parts := strings.Split(f, "/")
	for i, p := range parts[:len(parts)-1] {
		if !slices.Contains(ex, p) {
			continue
		}
		dir := strings.Join(parts[:i+1], "/")
		named := false
		for _, s := range scope {
			if s == dir || strings.HasPrefix(s, dir+"/") {
				named = true
			}
		}
		if !named {
			return true
		}
	}
	return false
}

func inScope(f string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == "." || f == s || strings.HasPrefix(f, s+"/") {
			return true
		}
	}
	return false
}
