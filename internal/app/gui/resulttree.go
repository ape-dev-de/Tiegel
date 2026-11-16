package gui

import (
	"image"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// resultNode is an entry of a result tree (usages, find results,
// structure, type hierarchy).
type resultNode struct {
	// Name is the main text, Value a code preview, Detail a muted note.
	Name, Value, Detail string
	Glyph               *atoms.Glyph
	Children            []*resultNode
	Expanded            bool
	// Open runs on double click (or a single click with OpenOnClick).
	Open func()
}

// resultTree is a scrollable tree of resultNodes below an optional header
// line (title, summary and buttons) used by the Usages, Find, Structure
// and Hierarchy tool windows.
type resultTree struct {
	Roots []*resultNode
	// Header draws above the tree (may be nil).
	Header func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	// Empty is shown without roots.
	Empty string
	// OpenOnClick opens leaves with a single click (structure view).
	OpenOnClick bool

	list  molecules.ScrollList
	rows  map[*resultNode]*molecules.TreeRow
	flat  []flatNode
	sel   *resultNode
	dirty bool
}

type flatNode struct {
	n     *resultNode
	depth int
}

// set replaces the roots.
func (t *resultTree) set(roots []*resultNode) {
	t.Roots, t.dirty, t.sel = roots, true, nil
	t.rows = map[*resultNode]*molecules.TreeRow{}
	t.list.ScrollTo(0)
}

func (t *resultTree) flatten() {
	t.flat = t.flat[:0]
	var walk func(ns []*resultNode, d int)
	walk = func(ns []*resultNode, d int) {
		for _, n := range ns {
			t.flat = append(t.flat, flatNode{n, d})
			if n.Expanded {
				walk(n.Children, d+1)
			}
		}
	}
	walk(t.Roots, 0)
	t.dirty = false
}

// rowCount is the number of visible rows (tests).
func (t *resultTree) rowCount() int {
	if t.dirty || t.flat == nil {
		t.flatten()
	}
	return len(t.flat)
}

// layout draws the header and the tree.
func (t *resultTree) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if t.rows == nil {
		t.rows = map[*resultNode]*molecules.TreeRow{}
	}
	if t.dirty || t.flat == nil {
		t.flatten()
	}
	size := gtx.Constraints.Max
	bg := gtx
	bg.Constraints = layout.Exact(size)
	atoms.Surface{Kind: atoms.SurfacePanel}.Paint(bg, th)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if t.Header == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return t.Header(gtx, th)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(t.flat) == 0 {
				if t.Empty == "" {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
				return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted(t.Empty).Fn(th))
			}
			changed := false
			d := t.list.Layout(gtx, th, len(t.flat), func(gtx layout.Context, i int) layout.Dimensions {
				if i >= len(t.flat) {
					return layout.Dimensions{}
				}
				f := t.flat[i]
				r := t.rows[f.n]
				if r == nil {
					r = &molecules.TreeRow{}
					t.rows[f.n] = r
				}
				r.Name, r.Value, r.Detail, r.Glyph, r.Depth = f.n.Name, f.n.Value, f.n.Detail, f.n.Glyph, f.depth
				r.Expandable, r.Expanded, r.Selected = len(f.n.Children) > 0, f.n.Expanded, t.sel == f.n
				clicked, toggled, double := r.Update(gtx)
				if toggled {
					f.n.Expanded = r.Expanded
					changed = true
				}
				if clicked {
					t.sel = f.n
					if !r.Expandable && f.n.Open != nil && (double || t.OpenOnClick) {
						f.n.Open()
					}
				}
				return r.Layout(gtx, th)
			})
			if changed {
				t.dirty = true
			}
			return d
		}),
	)
}

// headerText is a header with a strong title and a muted summary.
// It is drawn with the theme of the frame (the theme may change).
func headerText(title, summary string, extra ...func(gtx layout.Context, th *tokens.Theme) layout.Dimensions) func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(atoms.Strong(title).SingleLine().Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if summary == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Muted(summary).SingleLine().Fn(th))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}),
		}
		for _, x := range extra {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return x(gtx, th) })
			}))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
}
