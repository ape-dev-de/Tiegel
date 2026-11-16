package debug

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Variable is a node of the variables tree. Children are loaded lazily: a
// node with HasChildren but no Children triggers an ExpandEvent when the
// user expands it; the host fills Children (and Page/PageSize) later.
type Variable struct {
	Name      string
	FullName  string // expression addressing the value (property_get -n)
	Type      string // int, string, array, object, …
	ClassName string
	Value     string
	// HasChildren marks arrays and objects; NumChildren is their total.
	HasChildren bool
	NumChildren int
	Children    []*Variable
	// Page is the last loaded child page (0-based); PageSize is the page
	// size, 0 when all children are loaded at once.
	Page     int
	PageSize int
	// Loading shows a placeholder while the host fetches children.
	Loading bool
}

// HasMore reports whether further child pages can be loaded.
func (v *Variable) HasMore() bool {
	return v.HasChildren && v.PageSize > 0 && len(v.Children) > 0 && len(v.Children) < v.NumChildren
}

// Display returns the value as shown in the tree.
func (v *Variable) Display() string {
	switch {
	case v.Type == "object" && v.ClassName != "":
		return "{" + v.ClassName + "}"
	case v.Type == "array":
		return fmt.Sprintf("array(%d)", v.NumChildren)
	case v.Type == "string":
		return strconv.Quote(v.Value)
	case v.Type == "null":
		return "null"
	case v.Type == "uninitialized":
		return "uninitialized"
	}
	return v.Value
}

// key identifies v below parent; FullName when known.
func (v *Variable) key(parent string) string {
	if v.FullName != "" {
		return v.FullName
	}
	return parent + "\x00" + v.Name
}

// visRow is one visible line of the tree.
type visRow struct {
	key   string
	v     *Variable
	depth int
	kind  rowKind
}

type rowKind uint8

const (
	rowVar rowKind = iota
	rowLoading
	rowMore
)

// VariablesTree shows variables as an expandable tree with lazy children,
// paging ("load more") and copying values.
type VariablesTree struct {
	Roots []*Variable
	// Empty is shown when there are no variables.
	Empty string

	expanded map[string]bool
	rows     map[string]*molecules.TreeRow
	visible  []visRow
	selected string
	copy     atoms.IconButton
	list     molecules.ScrollList
}

// Expanded reports whether the node with FullName (or key) is expanded.
func (t *VariablesTree) Expanded(key string) bool { return t.expanded[key] }

// SetExpanded expands or collapses a node by FullName without an event.
func (t *VariablesTree) SetExpanded(key string, on bool) {
	if t.expanded == nil {
		t.expanded = map[string]bool{}
	}
	if on {
		t.expanded[key] = true
	} else {
		delete(t.expanded, key)
	}
}

// Selected returns the key (FullName) of the selected node.
func (t *VariablesTree) Selected() string { return t.selected }

func (t *VariablesTree) init() {
	if t.rows == nil {
		t.rows = map[string]*molecules.TreeRow{}
		t.expanded = map[string]bool{}
	}
}

// flatten computes the visible rows.
func (t *VariablesTree) flatten() {
	t.visible = t.visible[:0]
	var walk func(vs []*Variable, depth int, parent string)
	walk = func(vs []*Variable, depth int, parent string) {
		for _, v := range vs {
			k := v.key(parent)
			t.visible = append(t.visible, visRow{key: k, v: v, depth: depth})
			if !v.HasChildren || !t.expanded[k] {
				continue
			}
			if len(v.Children) == 0 {
				if v.Loading {
					t.visible = append(t.visible, visRow{key: k + "\x00loading", v: v, depth: depth + 1, kind: rowLoading})
				}
				continue
			}
			walk(v.Children, depth+1, k)
			if v.HasMore() {
				t.visible = append(t.visible, visRow{key: k + "\x00more", v: v, depth: depth + 1, kind: rowMore})
			}
		}
	}
	walk(t.Roots, 0, "")
}

func (t *VariablesTree) row(key string) *molecules.TreeRow {
	r, ok := t.rows[key]
	if !ok {
		r = &molecules.TreeRow{}
		t.rows[key] = r
	}
	return r
}

// Update processes clicks on the rows shown in the last frame.
func (t *VariablesTree) Update(gtx layout.Context) []Event {
	t.init()
	var out []Event
	if t.copy.Clicked(gtx) {
		for _, vr := range t.visible {
			if vr.key == t.selected && vr.kind == rowVar {
				out = append(out, CopyValueEvent{FullName: vr.v.FullName, Value: vr.v.Value})
			}
		}
	}
	for _, vr := range t.visible {
		r := t.row(vr.key)
		clicked, toggled, _ := r.Update(gtx)
		switch vr.kind {
		case rowMore:
			if clicked {
				out = append(out, ExpandEvent{FullName: vr.v.FullName, Page: vr.v.Page + 1})
			}
		case rowVar:
			if clicked {
				t.selected = vr.key
			}
			if toggled {
				t.SetExpanded(vr.key, r.Expanded)
				v := vr.v
				if r.Expanded && v.HasChildren && len(v.Children) == 0 && !v.Loading {
					out = append(out, ExpandEvent{FullName: v.FullName})
				}
			}
		}
	}
	return out
}

// Layout draws the tree; it fills the available space.
func (t *VariablesTree) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t.Update(gtx)
	t.flatten()
	t.gc()
	if len(t.visible) == 0 {
		return emptyState(gtx, th, t.Empty, "No variables")
	}
	t.copy.Glyph, t.copy.Description = atoms.GlyphCopy, "Copy value"
	t.copy.Size, t.copy.IconSize = th.Size.RowH-th.Space.XS, th.Size.IconSmall
	return t.list.Layout(gtx, th, len(t.visible), func(gtx layout.Context, i int) layout.Dimensions {
		vr := t.visible[i]
		r := t.row(vr.key)
		r.Depth, r.Trailing, r.Glyph = vr.depth, nil, nil
		r.NameColor, r.ValueColor, r.Selected = color.NRGBA{}, color.NRGBA{}, false
		switch vr.kind {
		case rowLoading:
			r.Name, r.Value, r.Detail, r.Expandable = "Loading…", "", "", false
			r.NameColor = th.Color.TextMuted
		case rowMore:
			r.Name, r.Value, r.Expandable = "Load more…", "", false
			r.Detail = fmt.Sprintf("%d of %d", len(vr.v.Children), vr.v.NumChildren)
			r.NameColor = th.Color.Accent
		default:
			v := vr.v
			r.Name, r.Value, r.Detail = v.Name, v.Display(), v.Type
			r.Expandable, r.Expanded = v.HasChildren, t.expanded[vr.key]
			r.Selected = vr.key == t.selected
			r.NameColor = th.Syntax.Property
			if strings.HasPrefix(v.Name, "$") {
				r.NameColor = th.Syntax.Variable
			}
			r.ValueColor = valueColor(th, v.Type)
			if r.Selected {
				r.Trailing = func(gtx layout.Context) layout.Dimensions { return t.copy.Layout(gtx, th) }
			}
		}
		return r.Layout(gtx, th)
	})
}

// gc drops widget state of rows that are no longer visible.
func (t *VariablesTree) gc() {
	if len(t.rows) <= 2*len(t.visible)+64 {
		return
	}
	keep := make(map[string]bool, len(t.visible))
	for _, vr := range t.visible {
		keep[vr.key] = true
	}
	for k := range t.rows {
		if !keep[k] {
			delete(t.rows, k)
		}
	}
}

// valueColor colors values like the editor colors literals.
func valueColor(th *tokens.Theme, typ string) color.NRGBA {
	switch typ {
	case "string":
		return th.Syntax.String
	case "int", "float":
		return th.Syntax.Number
	case "bool", "null", "uninitialized":
		return th.Syntax.Constant
	}
	return th.Editor.Text
}
