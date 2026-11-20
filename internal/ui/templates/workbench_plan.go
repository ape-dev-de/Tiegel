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
	// full: the panel's share (Workbench.Sizes) is a share of the whole
	// main area, not of the region the split divides; ratioFor converts
	// (U9: toggling a corner mode keeps the panel size).
	full bool
	a, b *node
}

// ratioFor returns the ratio of the split over total pixels (bar: the
// divider) when the main area is mainTotal pixels along the same axis.
func (n *node) ratioFor(total, mainTotal, bar int) float32 {
	if !n.full || total == mainTotal || total <= bar || mainTotal <= bar {
		return n.ratio
	}
	share := n.ratio
	if n.invert {
		share = 1 - share
	}
	r := share * float32(mainTotal-bar) / float32(total-bar)
	if n.invert {
		r = 1 - r
	}
	return min(max(r, 0.001), 0.999)
}

// shareOf converts a split ratio over total pixels back to the panel's
// share of the main area (the inverse of ratioFor, as Workbench.Sizes
// stores it).
func (n *node) shareOf(ratio float32, total, mainTotal, bar int) float32 {
	share := ratio
	if n.invert {
		share = 1 - share
	}
	if n.full && total != mainTotal && total > bar && mainTotal > bar {
		share = share * float32(total-bar) / float32(mainTotal-bar)
	}
	return share
}

// openSlots are the slots with a panel.
type openSlots [numSides][numGroups]bool

// plan builds the layout tree for the open slots and the corner owners
// (full: the bottom panel spans under the side panels of that corner).
// With a maximized slot (max != nil) the tree is that panel alone.
func (w *Workbench) plan(open openSlots, full [numCorners]bool, maxAt *slotRef) *node {
	if maxAt != nil {
		return &node{kind: nodePanel, at: *maxAt}
	}
	sh := tokens.DefaultShell()
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
			minPane: sh.MinOtherPane, a: panel(side, GroupPrimary), b: panel(side, GroupSplit)}
	}
	left, right, bottom := sideNode(SideLeft), sideNode(SideRight), sideNode(SideBottom)
	row := func(l, c, r *node) *node {
		if r != nil {
			c = &node{kind: nodeSplit, key: int(SideRight), ratio: 1 - w.Size(SideRight), minPane: sh.MinSidePane, invert: true, full: true, a: c, b: r}
		}
		if l != nil {
			c = &node{kind: nodeSplit, key: int(SideLeft), ratio: w.Size(SideLeft), minPane: sh.MinSidePane, full: true, a: l, b: c}
		}
		return c
	}
	col := func(top *node) *node {
		if bottom == nil {
			return top
		}
		return &node{kind: nodeSplit, key: int(SideBottom), vertical: true, ratio: 1 - w.Size(SideBottom), minPane: sh.MinOtherPane,
			invert: true, full: true, a: top, b: bottom}
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

// rects computes the rectangles of n placed in r, the main area; bar is
// the divider width in pixels. The arithmetic matches molecules.Split.
func rects(n *node, r image.Rectangle, bar int, m unit.Metric, g *geometry) {
	placeRects(n, r, r.Size(), bar, m, g)
}

func placeRects(n *node, r image.Rectangle, main image.Point, bar int, m unit.Metric, g *geometry) {
	switch n.kind {
	case nodeEditor:
		g.editor = r
	case nodePanel:
		g.panels[n.at.side][n.at.group] = r
	case nodeSplit:
		a, b := splitRect(r, main, n, bar, m)
		placeRects(n.a, a, main, bar, m, g)
		placeRects(n.b, b, main, bar, m, g)
	}
}

// axis returns the extent of p along the split's axis.
func (n *node) axis(p image.Point) int {
	if n.vertical {
		return p.Y
	}
	return p.X
}

// splitRect divides r like molecules.Split with the node's ratio (main
// is the size of the whole main area).
func splitRect(r image.Rectangle, main image.Point, n *node, bar int, m unit.Metric) (image.Rectangle, image.Rectangle) {
	total := n.axis(r.Size())
	ratio := n.ratioFor(total, n.axis(main), bar)
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	minPane := n.minPane
	if minPane <= 0 {
		minPane = tokens.DefaultShell().MinOtherPane
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
		total, mainTotal := n.axis(gtx.Constraints.Max), n.axis(w.main.Size())
		bar := max(1, gtx.Dp(th.Size.BorderWidth))
		ratio := n.ratioFor(total, mainTotal, bar)
		sp.Vertical, sp.MinPane, sp.Ratio = n.vertical, n.minPane, ratio
		d := sp.Layout(gtx, th, first, second)
		// Unchanged ratio: report the stored share exactly, so the
		// conversion's rounding never looks like a resize.
		share := n.shareOf(n.ratio, mainTotal, mainTotal, bar)
		if sp.Ratio != ratio {
			share = n.shareOf(sp.Ratio, total, mainTotal, bar)
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
