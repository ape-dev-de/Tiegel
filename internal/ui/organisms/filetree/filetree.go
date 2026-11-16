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

	"gioui.org/layout"

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

func (ExpandEvent) isTreeEvent() {}
func (OpenEvent) isTreeEvent()   {}
func (SelectEvent) isTreeEvent() {}

// Tree is the file tree organism. A click selects a row, a double click on
// a file opens it, a double click on a directory or its arrow toggles it.
type Tree struct {
	// Root is the project directory; it is shown as the first row.
	Root *Node
	// Empty is shown without Root.
	Empty string

	expanded map[string]bool
	selected string
	rows     []*molecules.FileRow
	flat     []flatRow
	list     molecules.ScrollList
}

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
	}
	t.expanded[p] = on
}

// Selected returns the selected path.
func (t *Tree) Selected() string { return t.selected }

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
}

// Update processes clicks and returns the resulting events.
func (t *Tree) Update(gtx layout.Context) []Event {
	t.flatten()
	var out []Event
	for i, fr := range t.flat {
		clicked, toggled := t.rows[i].Update(gtx)
		n := fr.node
		if !clicked && !toggled {
			continue
		}
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
		r.Glyph = Glyph(n)
		r.Detail = ""
		if n.Dir && r.Expanded && !n.Loaded {
			r.Detail = "loading…"
		}
		if n.Library && fr.depth == 1 {
			r.Detail = "library"
		}
		return r.Layout(gtx, th)
	})
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
