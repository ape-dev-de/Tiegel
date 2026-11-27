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

// DefaultExclude are the directory names left out by default.
var DefaultExclude = []string{"vendor", "node_modules", "var", "cache", "storage", "tests", "test", "Tests", "fixtures", "migrations", ".git", ".idea"}

// Defaults of the plan limits.
const (
	DefaultMaxProcesses  = 30
	DefaultMaxComponents = 20
)

// ReadPSR4 reads autoload psr-4 of composer.json in root (relative
// directories, slash separated). A missing or broken file yields nil.
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

// FilterFiles keeps the PHP files (project relative, slash separated)
// that are in scope (empty: everything) and not below an excluded
// directory name – unless the scope names that directory.
func FilterFiles(files, scope, exclude []string) []string {
	if exclude == nil {
		exclude = DefaultExclude
	}
	var sc []string
	for _, s := range scope {
		s = strings.Trim(path.Clean(filepath.ToSlash(s)), "/")
		if s != "" {
			sc = append(sc, s)
		}
	}
	var out []string
	for _, f := range files {
		f = path.Clean(filepath.ToSlash(f))
		if !strings.EqualFold(path.Ext(f), ".php") || excluded(f, exclude, sc) || !inScope(f, sc) {
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
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

var unsafeRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slug derives a stable, file-name safe ID part.
func Slug(s string) string {
	id := strings.Trim(unsafeRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if id == "" {
		return "x"
	}
	if len(id) > 80 {
		id = id[:71] + "-" + HashString(s)[:8]
	}
	return id
}

// PlanInput is what the planner knows about the project.
type PlanInput struct {
	// Files are the PHP files in scope (FilterFiles).
	Files []string
	Facts Facts
	// Entries are the entry points (mechanical plus those the
	// entry-point pages added).
	Entries      []EntryPoint
	MaxProcesses int
}

// PlanOverview plans the overview page: its sources are the
// configuration files plus the facts themselves.
func PlanOverview(in PlanInput) Item {
	return Item{ID: "overview", Level: LevelOverview, Title: "Overview", MD: "overview.md", Files: in.Facts.ConfigFiles(),
		Inputs: map[string]string{"@facts": in.Facts.Hash()}}
}

// PlanEntryPoints plans one page per entry point kind found (or a single
// "discover" page when Tiegel found none).
func PlanEntryPoints(in PlanInput) []Item {
	byKind := map[string][]EntryPoint{}
	for _, e := range in.Entries {
		if e.Source == "agent" {
			continue // the pages' own additions do not plan pages
		}
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	var out []Item
	for _, k := range EntryKinds {
		eps := byKind[k]
		if len(eps) == 0 {
			continue
		}
		it := Item{ID: "entrypoints-" + k, Level: LevelEntryPoints, Title: KindTitle(k), MD: "entrypoints/" + k + ".md", Group: k, Entries: eps}
		b, _ := json.Marshal(eps)
		it.Inputs = map[string]string{"@entries": HashString(string(b))}
		for _, e := range eps {
			if !slices.Contains(it.Files, e.File) {
				it.Files = append(it.Files, e.File)
			}
		}
		sort.Strings(it.Files)
		out = append(out, it)
	}
	if len(out) == 0 {
		out = append(out, Item{ID: "entrypoints-discover", Level: LevelEntryPoints, Title: "Entry points", MD: "entrypoints/discover.md", Group: "discover",
			Files: in.Facts.ConfigFiles(), Inputs: map[string]string{"@facts": in.Facts.Hash()}})
	}
	return out
}

// ProcessKey groups an entry point into a process: its handler class,
// else its file.
func ProcessKey(e EntryPoint) string {
	if c := e.HandlerClass(); c != "" {
		return c
	}
	if c := SymbolClass(e.Handler); c != "" {
		return c // a resource controller class
	}
	return e.File
}

// PlanProcesses groups the entry points into processes (one per
// handler class or file), the biggest first, at most MaxProcesses.
// skipped counts the groups left out by the limit.
func PlanProcesses(in PlanInput) (items []Item, skipped int) {
	max := in.MaxProcesses
	if max <= 0 {
		max = DefaultMaxProcesses
	}
	groups := map[string][]EntryPoint{}
	var keys []string
	for _, e := range in.Entries {
		k := ProcessKey(e)
		if k == "" {
			continue
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], e)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	if len(keys) > max {
		skipped = len(keys) - max
		keys = keys[:max]
	}
	sort.Strings(keys)
	for _, k := range keys {
		eps := groups[k]
		short := k
		if i := strings.LastIndexAny(k, `\/`); i >= 0 {
			short = k[i+1:]
		}
		title := short + " (" + kindsOf(eps) + ")"
		it := Item{ID: "process-" + Slug(k), Level: LevelProcesses, Title: title, MD: "processes/" + Slug(k) + ".md", Group: k, Entries: eps}
		files := []string{}
		if cf := ClassFile(k, in.Facts.PSR4, in.Files); cf != "" {
			files = append(files, cf)
		}
		for _, e := range eps {
			if !slices.Contains(files, e.File) {
				files = append(files, e.File)
			}
		}
		sort.Strings(files)
		it.Files = files
		b, _ := json.Marshal(eps)
		it.Inputs = map[string]string{"@entries": HashString(string(b))}
		items = append(items, it)
	}
	return items, skipped
}

func kindsOf(eps []EntryPoint) string {
	var ks []string
	for _, e := range eps {
		t := strings.ToUpper(e.Kind)
		if e.Kind == KindAsync {
			t = "async"
		}
		if e.Kind == KindCron {
			t = "cron"
		}
		if e.Kind == KindPublic {
			t = "public"
		}
		if !slices.Contains(ks, t) {
			ks = append(ks, t)
		}
	}
	return strings.Join(ks, ", ")
}

// SymbolClass returns the class of a symbol ("App\X::run" → "App\X",
// "X->run" → "X"; a bare capitalised name is a class; functions yield
// "").
func SymbolClass(sym string) string {
	sym = strings.TrimSpace(symbolTrim.ReplaceAllString(sym, ""))
	sym = strings.ReplaceAll(sym, "->", "::")
	if i := strings.Index(sym, "::"); i > 0 {
		return strings.TrimPrefix(sym[:i], `\`)
	}
	sym = strings.TrimPrefix(sym, `\`)
	short := sym[strings.LastIndexByte(sym, '\\')+1:]
	if short != "" && short[0] >= 'A' && short[0] <= 'Z' && !strings.ContainsAny(short, " $") {
		return sym
	}
	return ""
}

// componentScore counts how the processes touch a class.
type componentScore struct {
	class     string
	processes []string
	steps     int
	files     map[string]int
}

// PlanComponents ranks the classes the validated process pages cite
// (distinct processes, then steps) and plans a page for the top max;
// handler classes of processes are left out (their process page covers
// them).
func PlanComponents(dir string, m *Manifest, max int) []Item {
	if max <= 0 {
		max = DefaultMaxComponents
	}
	handlers := map[string]bool{}
	scores := map[string]*componentScore{}
	for _, st := range m.Level(LevelProcesses) {
		handlers[shortClass(st.Group)] = true
		handlers[st.Group] = true
	}
	for _, st := range m.Level(LevelProcesses) {
		if st.Status != StatusDone || st.Data == "" {
			continue
		}
		var d ProcessDoc
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(st.Data)))
		if err != nil || json.Unmarshal(b, &d) != nil {
			continue
		}
		seen := map[string]bool{}
		for _, s := range d.Steps {
			c := SymbolClass(s.Symbol)
			if c == "" || s.Unverified() || handlers[c] || handlers[shortClass(c)] {
				continue
			}
			sc := scores[c]
			if sc == nil {
				sc = &componentScore{class: c, files: map[string]int{}}
				scores[c] = sc
			}
			sc.steps++
			if s.File != "" {
				sc.files[s.File]++
			}
			if !seen[c] {
				seen[c] = true
				sc.processes = append(sc.processes, st.Title)
			}
		}
	}
	// Merge short names into a fully qualified one of the same class.
	for c, sc := range scores {
		if strings.Contains(c, `\`) {
			continue
		}
		for c2, sc2 := range scores {
			if c2 != c && shortClass(c2) == c {
				sc2.steps += sc.steps
				for f, n := range sc.files {
					sc2.files[f] += n
				}
				for _, p := range sc.processes {
					if !slices.Contains(sc2.processes, p) {
						sc2.processes = append(sc2.processes, p)
					}
				}
				delete(scores, c)
				break
			}
		}
	}
	l := make([]*componentScore, 0, len(scores))
	for _, sc := range scores {
		l = append(l, sc)
	}
	sort.Slice(l, func(i, j int) bool {
		if len(l[i].processes) != len(l[j].processes) {
			return len(l[i].processes) > len(l[j].processes)
		}
		if l[i].steps != l[j].steps {
			return l[i].steps > l[j].steps
		}
		return l[i].class < l[j].class
	})
	if len(l) > max {
		l = l[:max]
	}
	var out []Item
	for _, sc := range l {
		file, best := "", 0
		for f, n := range sc.files {
			if n > best || n == best && f < file {
				file, best = f, n
			}
		}
		it := Item{ID: "component-" + Slug(sc.class), Level: LevelComponents, Title: shortClass(sc.class), MD: "components/" + Slug(sc.class) + ".md", Group: sc.class, UsedBy: sc.processes}
		if file != "" {
			it.Files = []string{file}
		}
		sort.Strings(it.UsedBy)
		it.Inputs = map[string]string{"@usedBy": HashString(strings.Join(it.UsedBy, "\x00"))}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func shortClass(c string) string { return c[strings.LastIndexByte(c, '\\')+1:] }

// LoadEntryDocs returns the entry points the validated entry-point pages
// added (not found mechanically), for the process plan.
func LoadEntryDocs(dir string, m *Manifest) []EntryPoint {
	var out []EntryPoint
	for _, st := range m.Level(LevelEntryPoints) {
		if st.Status != StatusDone || st.Data == "" {
			continue
		}
		var d EntryPointsDoc
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(st.Data)))
		if err != nil || json.Unmarshal(b, &d) != nil {
			continue
		}
		for _, e := range d.Entries {
			if e.Found || e.Handler.Unverified() {
				continue
			}
			kind := e.Kind
			if kind == "" {
				kind = st.Group
			}
			if !slices.Contains(EntryKinds, kind) {
				kind = KindHTTP
			}
			out = append(out, EntryPoint{Kind: kind, Name: e.Name, Handler: e.Handler.Symbol, File: e.Handler.File, Line: e.Handler.Line, Source: "agent"})
		}
	}
	return out
}

// MergeEntries appends the added entry points that the inventory does
// not have yet.
func MergeEntries(inventory, added []EntryPoint) []EntryPoint {
	out := slices.Clone(inventory)
	have := map[string]bool{}
	for _, e := range inventory {
		have[e.Kind+"|"+e.Name] = true
	}
	for _, e := range added {
		if e.Name != "" && !have[e.Kind+"|"+e.Name] {
			have[e.Kind+"|"+e.Name] = true
			out = append(out, e)
		}
	}
	return out
}
