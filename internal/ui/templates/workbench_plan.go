package templates

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The main area (between the stripes, below the header, above the footer)
// is a tree of splits with the editor and the panels as leaves. The same
// tree drives the layout (molecules.Split per inner node) and the
// geometry the template needs for drops and previews, so both always
// agree.

// Corner is a bottom corner of the main area.
type Corner int

// Bottom corners.
const (
	CornerLeft Corner = iota
	CornerRight
	numCorners
)

// nodeKind is the kind of a layout tree node.
type nodeKind uint8

const (
	nodeEditor nodeKind = iota
	nodePanel
	nodeSplit
)

// node is a layout tree node.
type node struct {
	kind nodeKind
	at   slotRef // nodePanel
	// nodeSplit: key into Workbench.splits, orientation, share of a,
	// minimum pane size; invert: the tracked share is 1-ratio (the panel
	// is the second pane).
	key      int
	vertical bool
	ratio    float32
	minPane  unit.Dp
	invert   bool
	a, b     *node
}

// Minimum pane sizes.
const (
	minSidePane  = unit.Dp(160)
	minOtherPane = unit.Dp(80)
)

// openSlots are the slots with a panel.
type openSlots [numSides][numGroups]bool

// plan builds the layout tree for the open slots and the corner owners
// (full: the bottom panel spans under the side panels of that corner).
// With a maximized slot (max != nil) the tree is that panel alone.
func (w *Workbench) plan(open openSlots, full [numCorners]bool, maxAt *slotRef) *node {
	if maxAt != nil {
		return &node{kind: nodePanel, at: *maxAt}
	}
	panel := func(side Side, g StripeGroup) *node { return &node{kind: nodePanel, at: slotRef{side, g}} }
	sideNode := func(side Side) *node {
		p, q := open[side][GroupPrimary], open[side][GroupSplit]
		switch {
		case !p && !q:
			return nil
		case !q:
			return panel(side, GroupPrimary)
		case !p:
			return panel(side, GroupSplit)
		}
		return &node{kind: nodeSplit, key: int(numSides + side), vertical: side != SideBottom, ratio: w.SplitSize(side),
			minPane: minOtherPane, a: panel(side, GroupPrimary), b: panel(side, GroupSplit)}
	}
	left, right, bottom := sideNode(SideLeft), sideNode(SideRight), sideNode(SideBottom)
	row := func(l, c, r *node) *node {
		if r != nil {
			c = &node{kind: nodeSplit, key: int(SideRight), ratio: 1 - w.Size(SideRight), minPane: minSidePane, invert: true, a: c, b: r}
		}
		if l != nil {
			c = &node{kind: nodeSplit, key: int(SideLeft), ratio: w.Size(SideLeft), minPane: minSidePane, a: l, b: c}
		}
		return c
	}
	col := func(top *node) *node {
		if bottom == nil {
			return top
		}
		return &node{kind: nodeSplit, key: int(SideBottom), vertical: true, ratio: 1 - w.Size(SideBottom), minPane: minOtherPane,
			invert: true, a: top, b: bottom}
	}
	ed := &node{kind: nodeEditor}
	fl, fr := full[CornerLeft], full[CornerRight]
	switch {
	case fl && fr:
		return col(row(left, ed, right))
	case fl:
		return row(nil, col(row(left, ed, nil)), right)
	case fr:
		return row(left, col(row(nil, ed, right)), nil)
	}
	return row(left, col(ed), right)
}

// geometry holds the rectangles of a laid out tree (window coordinates).
type geometry struct {
	editor image.Rectangle
	panels [numSides][numGroups]image.Rectangle
}

// rects computes the rectangles of n placed in r; bar is the divider
// width in pixels. The arithmetic matches molecules.Split.
func rects(n *node, r image.Rectangle, bar int, m unit.Metric, g *geometry) {
	switch n.kind {
	case nodeEditor:
		g.editor = r
	case nodePanel:
		g.panels[n.at.side][n.at.group] = r
	case nodeSplit:
		a, b := splitRect(r, n, bar, m)
		rects(n.a, a, bar, m, g)
		rects(n.b, b, bar, m, g)
	}
}

// splitRect divides r like molecules.Split with the node's ratio.
func splitRect(r image.Rectangle, n *node, bar int, m unit.Metric) (image.Rectangle, image.Rectangle) {
	ratio := n.ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	total := r.Dx()
	if n.vertical {
		total = r.Dy()
	}
	minPane := n.minPane
	if minPane <= 0 {
		minPane = minOtherPane
	}
	minPx := min(m.Dp(minPane), max(0, (total-bar)/2))
	first := int(float32(total-bar) * ratio)
	first = max(minPx, min(first, total-bar-minPx))
	if n.vertical {
		return image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+first), image.Rect(r.Min.X, r.Min.Y+first+bar, r.Max.X, r.Max.Y)
	}
	return image.Rect(r.Min.X, r.Min.Y, r.Min.X+first, r.Max.Y), image.Rect(r.Min.X+first+bar, r.Min.Y, r.Max.X, r.Max.Y)
}

// render returns the widget of n.
func (w *Workbench) render(th *tokens.Theme, s Slots, n *node) layout.Widget {
	switch n.kind {
	case nodeEditor:
		return func(gtx layout.Context) layout.Dimensions {
			atoms.Surface{Kind: atoms.SurfaceEditor}.Paint(gtx, th)
			if s.Editor == nil {
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			return s.Editor(gtx)
		}
	case nodePanel:
		return w.panel(th, n.at.side, n.at.group, slotPanel(s, n.at))
	}
	first, second := w.render(th, s, n.a), w.render(th, s, n.b)
	return func(gtx layout.Context) layout.Dimensions {
		sp := &w.splits[n.key]
		sp.Vertical, sp.MinPane, sp.Ratio = n.vertical, n.minPane, n.ratio
		d := sp.Layout(gtx, th, first, second)
		share := sp.Ratio
		if n.invert {
			share = 1 - share
		}
		w.track(n.key, share)
		return d
	}
}

// slotPanel returns the panel of slot at.
func slotPanel(s Slots, at slotRef) Panel {
	if at.group == GroupSplit {
		return s.Splits[at.side]
	}
	return s.Panels[at.side]
}
