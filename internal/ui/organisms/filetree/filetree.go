// Package filetree provides the project file tree organism: a lazily
// loaded, scrollable tree of directories and files with icons, VCS status
// colors and keyboard-free mouse interaction (click selects, click on a
// directory or its arrow toggles, double click opens a file).
//
// Following the organism convention (D71) the host owns the data (Root and
// its children) and loads directories when it receives an ExpandEvent;
// which directories are expanded and which row is selected is UI state of
// the organism.
package filetree

import (
	"path"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Node is a directory or file of the tree.
type Node struct {
	Name string
	// Path identifies the node (project relative, slash separated).
	Path string
	Dir  bool
	// Loaded marks directories whose Children are known; expanding an
	// unloaded directory emits an ExpandEvent so the host loads it.
	Loaded   bool
	Children []*Node
	Status   molecules.FileStatus
	// Library marks vendor code (muted).
	Library bool
	Ignored bool
}

// Event is reported by Tree.Update.
type Event interface{ isTreeEvent() }

// ExpandEvent: a directory was expanded (Expanded true) or collapsed.
type ExpandEvent struct {
	Path     string
	Expanded bool
	// Loaded tells whether the host already provided the children.
	Loaded bool
}

// OpenEvent: a file was double clicked.
type OpenEvent struct{ Path string }

// SelectEvent: a row was selected.
type SelectEvent struct{ Path string }

// ContextEvent: a row (or, with Path "" and Background set, the empty
// area below the rows) was context clicked; the row is selected.
type ContextEvent struct {
	Path       string
	Dir        bool
	Background bool
}

// KeyEvent: a command key was pressed while the tree had the focus
// (Shift+F6, Delete, Backspace, Shortcut+C/X/V, F4); Path is the
// selected row.
type KeyEvent struct {
	Path      string
	Dir       bool
	Name      key.Name
	Modifiers key.Modifiers
}

func (ExpandEvent) isTreeEvent()  {}
func (OpenEvent) isTreeEvent()    {}
func (SelectEvent) isTreeEvent()  {}
func (ContextEvent) isTreeEvent() {}
func (KeyEvent) isTreeEvent()     {}

// Tree is the file tree organism. A click selects a row, a double click on
// a file opens it, a double click on a directory or its arrow toggles it.
type Tree struct {
	// Root is the project directory; it is shown as the first row.
	Root *Node
	// Empty is shown without Root.
	Empty string

	// GOOS enables Ctrl+click context clicks on "darwin".
	GOOS string
	// Icon (optional) returns the icon of a node (open: an expanded
	// directory), e.g. from internal/ui/fileicons; nil results and a nil
	// Icon fall back to Glyph and the default folder glyphs.
	Icon func(n *Node, open bool) *atoms.Glyph

	expanded map[string]bool
	selected string
	rows     []*molecules.FileRow
	areas    []molecules.ContextArea
	bg       molecules.ContextArea
	flat     []flatRow
	list     molecules.ScrollList
	focus    int
	focused  bool
	// reveal is a path to scroll into view once its row exists (the host
	// may still be loading its directories).
	reveal string
}

// Focused reports whether the tree has the keyboard focus.
func (t *Tree) Focused() bool { return t.focused }

// Focus requests the keyboard focus for the tree.
func (t *Tree) Focus(gtx layout.Context) { gtx.Execute(key.FocusCmd{Tag: &t.focus}) }

type flatRow struct {
	node  *Node
	depth int
}

// Expanded reports whether the directory at p is expanded.
func (t *Tree) Expanded(p string) bool { return t.expanded[p] }

// SetExpanded expands or collapses a directory (e.g. restoring state or
// revealing a file) without emitting events.
func (t *Tree) SetExpanded(p string, on bool) {
	if t.expanded == nil {
		t.expanded = map[string]bool{}
		if t.Root != nil {
			// The root starts expanded (as in flatten).
			t.expanded[t.Root.Path] = true
		}
	}
	t.expanded[p] = on
}

// Selected returns the selected path.
func (t *Tree) Selected() string { return t.selected }

// Reveal selects p, expands its parent directories and scrolls its row
// into view as soon as it is part of the tree (directories still loading
// are waited for).
func (t *Tree) Reveal(p string) {
	t.Select(p)
	t.reveal = p
}

// Select selects p and expands its parent directories.
func (t *Tree) Select(p string) {
	t.selected = p
	for d := path.Dir(p); d != "." && d != "/" && d != ""; d = path.Dir(d) {
		t.SetExpanded(d, true)
	}
}

func (t *Tree) flatten() {
	t.flat = t.flat[:0]
	if t.Root == nil {
		return
	}
	if t.expanded == nil {
		t.expanded = map[string]bool{t.Root.Path: true}
	}
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		t.flat = append(t.flat, flatRow{n, depth})
		if n.Dir && t.expanded[n.Path] {
			for _, ch := range n.Children {
				walk(ch, depth+1)
			}
		}
	}
	walk(t.Root, 0)
	for len(t.rows) < len(t.flat) {
		t.rows = append(t.rows, &molecules.FileRow{})
	}
	for len(t.areas) < len(t.flat) {
		t.areas = append(t.areas, molecules.ContextArea{})
	}
}

// Update processes clicks and keys and returns the resulting events.
func (t *Tree) Update(gtx layout.Context) []Event {
	t.flatten()
	var out []Event
	for i, fr := range t.flat {
		t.areas[i].GOOS = t.GOOS
		if _, ok := t.areas[i].Update(gtx); ok {
			if t.selected != fr.node.Path {
				t.selected = fr.node.Path
				out = append(out, SelectEvent{Path: fr.node.Path})
			}
			out = append(out, ContextEvent{Path: fr.node.Path, Dir: fr.node.Dir})
			t.Focus(gtx)
		}
	}
	t.bg.GOOS = t.GOOS
	if _, ok := t.bg.Update(gtx); ok && t.Root != nil && len(out) == 0 {
		out = append(out, ContextEvent{Path: t.Root.Path, Dir: true, Background: true})
	}
	out = append(out, t.keys(gtx)...)
	for i, fr := range t.flat {
		clicked, toggled := t.rows[i].Update(gtx)
		n := fr.node
		if !clicked && !toggled {
			continue
		}
		t.Focus(gtx)
		if t.selected != n.Path {
			t.selected = n.Path
			out = append(out, SelectEvent{Path: n.Path})
		}
		double := t.rows[i].DoubleClicked()
		switch {
		case n.Dir && toggled:
			on := !t.expanded[n.Path]
			t.expanded[n.Path] = on
			out = append(out, ExpandEvent{Path: n.Path, Expanded: on, Loaded: n.Loaded})
		case !n.Dir && double:
			out = append(out, OpenEvent{Path: n.Path})
		}
	}
	return out
}

// Layout draws the tree filling the available space.
func (t *Tree) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t.Update(gtx)
	t.flatten()
	if len(t.flat) == 0 {
		text := t.Empty
		if text == "" {
			text = "No project"
		}
		layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted(text).Fn(th))
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	size := gtx.Constraints.Max
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &t.focus)
	area.Pop()
	return t.bg.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = size
		d := t.layoutRows(gtx, th)
		d.Size = size
		return d
	})
}

func (t *Tree) layoutRows(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t.list.Horizontal = true
	t.scrollToReveal()
	return t.list.Layout(gtx, th, len(t.flat), func(gtx layout.Context, i int) layout.Dimensions {
		fr := t.flat[i]
		n := fr.node
		r := t.rows[i]
		r.Name, r.Dir, r.Depth = n.Name, n.Dir, fr.depth
		r.Expanded = t.expanded[n.Path]
		r.Selected = n.Path == t.selected
		r.Status = n.Status
		if n.Ignored && r.Status == molecules.StatusNone {
			r.Status = molecules.StatusIgnored
		}
		r.Glyph = nil
		if t.Icon != nil {
			r.Glyph = t.Icon(n, r.Expanded)
		}
		if r.Glyph == nil {
			r.Glyph = Glyph(n)
		}
		r.Detail = ""
		if n.Dir && r.Expanded && !n.Loaded {
			r.Detail = "loading…"
		}
		if n.Library && fr.depth == 1 {
			r.Detail = "library"
		}
		return t.areas[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.Layout(gtx, th) })
	})
}

// scrollToReveal scrolls a pending Reveal target into view.
func (t *Tree) scrollToReveal() {
	if t.reveal == "" {
		return
	}
	for i, fr := range t.flat {
		if fr.node.Path != t.reveal {
			continue
		}
		t.reveal = ""
		first, last := t.list.FirstVisible(), t.list.LastVisible()
		if last < 0 || i < first || i >= last {
			// Keep a few rows of context above the target.
			t.list.ScrollTo(i - 3)
		}
		return
	}
}

// treeKeys are the keys the focused tree handles.
var treeKeys = []key.Filter{
	{Name: key.NameUpArrow}, {Name: key.NameDownArrow}, {Name: key.NameLeftArrow}, {Name: key.NameRightArrow},
	{Name: key.NameReturn}, {Name: key.NameEnter}, {Name: key.NameF4},
	{Name: key.NameF6, Required: key.ModShift},
	{Name: key.NameDeleteForward},
	{Name: key.NameDeleteBackward, Optional: key.ModShortcut},
	{Name: "C", Required: key.ModShortcut}, {Name: "X", Required: key.ModShortcut}, {Name: "V", Required: key.ModShortcut},
}

// keys handles the keyboard while the tree is focused.
func (t *Tree) keys(gtx layout.Context) []Event {
	var out []Event
	filters := make([]event.Filter, 0, len(treeKeys)+1)
	filters = append(filters, key.FocusFilter{Target: &t.focus})
	for _, f := range treeKeys {
		f.Focus = &t.focus
		filters = append(filters, f)
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return out
		}
		switch e := ev.(type) {
		case key.FocusEvent:
			t.focused = e.Focus
		case key.Event:
			if e.State != key.Press {
				continue
			}
			out = append(out, t.key(e)...)
		}
	}
}

// key handles one key press.
func (t *Tree) key(e key.Event) []Event {
	cur := -1
	for i, fr := range t.flat {
		if fr.node.Path == t.selected {
			cur = i
		}
	}
	sel := func(i int) []Event {
		if i < 0 || i >= len(t.flat) || i == cur {
			return nil
		}
		t.selected = t.flat[i].node.Path
		return []Event{SelectEvent{Path: t.selected}}
	}
	var n *Node
	if cur >= 0 {
		n = t.flat[cur].node
	}
	switch e.Name {
	case key.NameUpArrow:
		return sel(max(cur-1, 0))
	case key.NameDownArrow:
		return sel(min(cur+1, len(t.flat)-1))
	case key.NameLeftArrow, key.NameRightArrow:
		if n == nil {
			return nil
		}
		on := e.Name == key.NameRightArrow
		if n.Dir && t.expanded[n.Path] != on {
			t.expanded[n.Path] = on
			return []Event{ExpandEvent{Path: n.Path, Expanded: on, Loaded: n.Loaded}}
		}
		if !on && cur > 0 {
			// Left on a collapsed entry selects its parent.
			parent := path.Dir(n.Path)
			for i := cur - 1; i >= 0; i-- {
				if p := t.flat[i].node.Path; p == parent || (parent == "." && i == 0) {
					return sel(i)
				}
			}
		}
		return nil
	case key.NameReturn, key.NameEnter:
		if n == nil {
			return nil
		}
		if n.Dir {
			on := !t.expanded[n.Path]
			t.expanded[n.Path] = on
			return []Event{ExpandEvent{Path: n.Path, Expanded: on, Loaded: n.Loaded}}
		}
		return []Event{OpenEvent{Path: n.Path}}
	}
	if n == nil {
		return nil
	}
	return []Event{KeyEvent{Path: n.Path, Dir: n.Dir, Name: e.Name, Modifiers: e.Modifiers}}
}

// Glyph returns the icon of a node by kind and file extension.
func Glyph(n *Node) *atoms.Glyph {
	if n.Dir {
		return nil // FileRow picks the folder glyphs
	}
	switch strings.ToLower(path.Ext(n.Name)) {
	case ".php", ".phtml", ".inc", ".js", ".ts", ".go", ".py", ".sh":
		return atoms.GlyphFileCode
	case ".md", ".txt", ".json", ".yml", ".yaml", ".xml", ".env", ".lock", ".ini", ".neon", ".dist":
		return atoms.GlyphFileText
	}
	if strings.HasPrefix(n.Name, ".env") {
		return atoms.GlyphFileText
	}
	return atoms.GlyphFile
}

// StatusFromGit maps a git state letter ("M", "A", "D", "?", "!", "U")
// to a file status.
func StatusFromGit(s string) molecules.FileStatus {
	switch s {
	case "M", "R", "C":
		return molecules.StatusModified
	case "A":
		return molecules.StatusAdded
	case "D":
		return molecules.StatusDeleted
	case "?":
		return molecules.StatusUntracked
	case "!":
		return molecules.StatusIgnored
	case "U":
		return molecules.StatusConflict
	}
	return molecules.StatusNone
}
