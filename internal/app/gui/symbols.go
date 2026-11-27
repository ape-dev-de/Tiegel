package gui

import (
	"path"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Go to Class (Cmd+O), Go to Symbol (Cmd+Alt+O) and Search Everywhere
// (Shift Shift) are choosers over nav.symbols (index, else the language
// server's workspace symbols); Search Everywhere adds files and actions
// and has categories (Tab switches).

// Symbol chooser modes.
const (
	symClasses = iota
	symSymbols
	symEverywhere
)

// Search Everywhere categories.
var everywhereTabs = []string{"All", "Classes", "Files", "Symbols", "Actions"}

const (
	tabAll = iota
	tabClasses
	tabFiles
	tabSymbols
	tabActions
)

// symEntry is a chooser row.
type symEntry struct {
	target controller.NavTarget
	file   string // project-relative file
	action string
}

// symbolChooser is an open Go to Class/Symbol or Search Everywhere.
type symbolChooser struct {
	d       *navDock
	mode    int
	tab     int
	q       string
	entries []symEntry
	spec    *ChooserSpec
	files   int
}

// openSymbols opens a symbol chooser.
func (d *navDock) openSymbols(mode int) {
	v := d.v
	if !v.ctl.Project.Open {
		return
	}
	sc := &symbolChooser{d: d, mode: mode}
	title, hint := "Go to Class", "Class name (fuzzy, \\ for namespaces)"
	switch mode {
	case symSymbols:
		title, hint = "Go to Symbol", "Symbol name (Class::member for members)"
	case symEverywhere:
		title, hint = "Search Everywhere", "Classes, files, symbols and actions"
		v.ctl.LoadFileIndex()
	}
	spec := ChooserSpec{Title: title, Hint: hint, OnQuery: sc.query, OnChoose: sc.choose}
	if mode == symEverywhere {
		spec.Tabs = everywhereTabs
		spec.OnTab = func(i int, q string) []palette.Item {
			sc.tab = i
			return sc.query(q)
		}
	}
	v.OpenChooser(spec)
	sc.spec = v.custom
	d.sym = sc
	v.chooser.Items = sc.query("")
}

// active reports whether sc is the open chooser.
func (sc *symbolChooser) active() bool {
	v := sc.d.v
	return v.chooseMode == chooseCustom && v.custom == sc.spec && sc.spec != nil
}

// refresh shows newly arrived symbols (or files).
func (sc *symbolChooser) refresh() {
	if !sc.active() {
		return
	}
	v := sc.d.v
	sel := v.chooser.Selected
	v.chooser.Items = sc.items()
	v.chooser.Selected = min(sel, max(len(v.chooser.Items)-1, 0))
}

func (sc *symbolChooser) kinds() []string {
	if sc.mode == symClasses || sc.mode == symEverywhere && sc.tab == tabClasses {
		return []string{"type"}
	}
	return nil
}

// query starts the symbol search for q and returns what is known now.
func (sc *symbolChooser) query(q string) []palette.Item {
	sc.q = q
	n := sc.d.n
	if sc.needSymbols() && (n.Symbols.Query != q || !sameKinds(n.Symbols.Kinds, sc.kinds())) {
		n.SearchSymbols(q, sc.kinds(), 100)
	}
	return sc.items()
}

func (sc *symbolChooser) needSymbols() bool {
	return sc.mode != symEverywhere || sc.tab == tabAll || sc.tab == tabClasses || sc.tab == tabSymbols
}

func sameKinds(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// items builds the rows for the current query.
func (sc *symbolChooser) items() []palette.Item {
	d := sc.d
	v := d.v
	sc.entries = sc.entries[:0]
	sc.files = len(v.ctl.Files)
	var out []palette.Item
	add := func(e symEntry, it palette.Item) {
		sc.entries = append(sc.entries, e)
		out = append(out, it)
	}
	res := d.n.Symbols
	fresh := res.Query == sc.q && sameKinds(res.Kinds, sc.kinds())
	symbol := func(t controller.NavTarget, group string) {
		label := t.Name
		if t.Container != "" && !isClassKind(t.Kind) {
			label = shortFQN(t.Container, "") + "::" + t.Name
		}
		detail := t.Where()
		if isClassKind(t.Kind) && t.FQN != "" {
			detail = t.FQN + "  " + detail
		}
		if t.Library {
			detail += "  (library)"
		}
		add(symEntry{target: t}, palette.Item{Label: label, Detail: detail, Glyph: kindGlyph(t.Kind), Group: group})
	}
	limit := 100
	if sc.mode == symEverywhere && sc.tab == tabAll {
		limit = 6
	}
	if sc.mode != symEverywhere {
		if fresh {
			for _, t := range res.Items {
				symbol(t, "")
			}
		}
		sc.message(fresh, res)
		return out
	}
	// Search Everywhere.
	if fresh && (sc.tab == tabAll || sc.tab == tabClasses) {
		k := 0
		for _, t := range res.Items {
			if isClassKind(t.Kind) && k < limit {
				symbol(t, "Classes")
				k++
			}
		}
	}
	if sc.tab == tabAll || sc.tab == tabFiles {
		if sc.q != "" {
			for _, m := range controller.FilterFuzzy(sc.q, v.ctl.Files, limit) {
				p := v.ctl.Files[m.Index]
				dir := path.Dir(p)
				if dir == "." {
					dir = ""
				}
				add(symEntry{file: p}, palette.Item{Label: path.Base(p), Detail: dir, Glyph: fileGlyph(p), Group: "Files"})
			}
		}
	}
	if fresh && (sc.tab == tabAll || sc.tab == tabSymbols) {
		k := 0
		for _, t := range res.Items {
			if !isClassKind(t.Kind) && k < limit {
				symbol(t, "Symbols")
				k++
			}
		}
	}
	if (sc.tab == tabAll || sc.tab == tabActions) && sc.q != "" {
		k := 0
		for _, m := range v.Registry.Model().Find(sc.q, v.actionContext(""), nil, v.goos) {
			if !v.Registry.HasHandler(m.ID) || m.Disabled || k >= limit {
				continue
			}
			k++
			g := atoms.GlyphNamed(m.Icon)
			if g == nil {
				g = atoms.GlyphChevronRight
			}
			add(symEntry{action: m.ID}, palette.Item{Label: m.Label, Detail: keyLabel(v.goos, m.Shortcut), Glyph: g, Group: "Actions"})
		}
	}
	sc.message(fresh || !sc.needSymbols(), res)
	return out
}

// message sets the chooser's empty text and title suffix.
func (sc *symbolChooser) message(fresh bool, res controller.SymbolResult) {
	v := sc.d.v
	if !sc.active() {
		return
	}
	switch {
	case sc.q == "":
		v.chooser.Message = "Type to search"
	case !fresh:
		v.chooser.Message = "Searching…"
	case res.Err != "":
		v.chooser.Message = res.Err
	case !res.Complete || sc.d.n.Index.Busy():
		v.chooser.Message = "Nothing found yet (the index is still being built)"
	default:
		v.chooser.Message = ""
	}
}

func isClassKind(k string) bool {
	switch k {
	case "class", "interface", "trait", "enum":
		return true
	}
	return false
}

// choose opens the chosen row.
func (sc *symbolChooser) choose(i int, _ string) {
	d := sc.d
	v := d.v
	if sc.d.sym == sc {
		d.sym = nil
	}
	if i < 0 || i >= len(sc.entries) {
		return
	}
	e := sc.entries[i]
	cur := v.currentLocation()
	switch {
	case e.action != "":
		v.runAction(keymap.Action(e.action))
	case e.file != "":
		if cur.File != "" {
			v.ctl.Nav.Push(cur)
		}
		v.ctl.OpenFile(e.file, nil)
	default:
		d.n.OpenTarget(cur, e.target)
	}
}
