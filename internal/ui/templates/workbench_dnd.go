package templates

import (
	"image"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Drag and drop of tool windows (D474): a stripe button or a panel
// header (or one of its tabs) is pressed and moved further than
// tokens.ShellSizes.DragThreshold; from then on the template shows a
// translucent preview of the rectangle the window would occupy and, on
// release, reports a MoveEvent. Escape cancels the drag; a press without movement stays a
// click. A drop in the strip along the bottom edge of the main area
// (tokens.ShellSizes.FullWidthStrip) docks the window at the bottom with
// the bottom panel spanning the full width.

// dropTarget is where a dragged window would go.
type dropTarget struct {
	at slotRef
	// tab: join the panel in at as a tab; full: the bottom panel takes
	// both bottom corners.
	tab, full bool
}

// dragState is the state of a pressed stripe button or panel header.
type dragState struct {
	id, title string
	from      slotRef
	// origin is the window position of the pressed handler; start the
	// press position in handler coordinates.
	origin image.Point
	start  f32.Point
	// active: moved past the threshold; moved: was active at some point
	// (suppresses the click on release); cancelled: Escape was pressed,
	// the rest of the gesture is ignored.
	active, moved, cancelled bool
	pos                      image.Point // window position
	target                   dropTarget
	hasTarget                bool
}

// pressDrag starts tracking a possible drag of window id from slot from;
// pos is the press position relative to origin (window coordinates of
// the handler).
func (w *Workbench) pressDrag(id, title string, from slotRef, origin image.Point, pos f32.Point) {
	w.drag = &dragState{id: id, title: title, from: from, origin: origin, start: pos,
		pos: origin.Add(image.Pt(int(pos.X), int(pos.Y)))}
}

// moveDrag follows the pointer (handler coordinates); threshold is the
// drag distance in pixels.
func (w *Workbench) moveDrag(pos f32.Point, threshold int) {
	d := w.drag
	if d == nil || d.cancelled {
		return
	}
	delta := pos.Sub(d.start)
	if !d.active && delta.X*delta.X+delta.Y*delta.Y > float32(threshold*threshold) {
		d.active, d.moved = true, true
	}
	d.pos = d.origin.Add(image.Pt(int(pos.X), int(pos.Y)))
	if d.active {
		d.target, d.hasTarget = w.dropAt(d.pos)
	}
}

// releaseDrag ends the gesture at pos (handler coordinates) and returns
// the move it results in, if any, and whether the press became a drag.
func (w *Workbench) releaseDrag(pos f32.Point, threshold int) (ev *MoveEvent, moved bool) {
	d := w.drag
	if d == nil {
		return nil, false
	}
	w.moveDrag(pos, threshold)
	w.drag = nil
	if !d.active || !d.hasTarget {
		return nil, d.moved
	}
	t := d.target
	switch {
	case t.tab && w.panelID[t.at.side][t.at.group] == d.id:
		return nil, true // into its own panel
	case !t.tab && !t.full && t.at == d.from:
		return nil, true // dropped where it is
	}
	return &MoveEvent{ID: d.id, Side: t.at.side, Group: t.at.group, Tab: t.tab, FullWidth: t.full}, true
}

// idOrEmpty returns the dragged window ("" without drag).
func (d *dragState) idOrEmpty() string {
	if d == nil {
		return ""
	}
	return d.id
}

// cancelDrag drops the preview (Escape); the gesture is ignored until
// the button is released.
func (w *Workbench) cancelDrag() {
	if d := w.drag; d != nil && d.active {
		d.active, d.cancelled, d.hasTarget = false, true, false
	}
}

// PanelRect returns the window rectangle of the panel showing tool
// window id in the last layout (empty if it is not shown).
func (w *Workbench) PanelRect(id string) image.Rectangle {
	for side := range numSides {
		for g := range numGroups {
			if id != "" && w.panelID[side][g] == id {
				return w.geom.panels[side][g]
			}
		}
	}
	return image.Rectangle{}
}

// EditorRect returns the window rectangle of the editor area (tabs and
// editors) in the last layout.
func (w *Workbench) EditorRect() image.Rectangle { return w.geom.editor }

// Dragging reports whether a tool window is being dragged.
func (w *Workbench) Dragging() bool { return w.drag != nil && w.drag.active }

// dragKeys cancels an active drag on Escape.
func (w *Workbench) dragKeys(gtx layout.Context) {
	if !w.Dragging() {
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			return
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			w.cancelDrag()
		}
	}
}

// dragResult is what pointerDrag observed in one frame.
type dragResult struct {
	move *MoveEvent
	// released: the button went up; dragged: the press had become a drag
	// (no click); pos: the release position (handler coordinates).
	released, dragged bool
	pos               f32.Point
	// menu is the window position of a secondary press.
	menu *image.Point
}

// pointerDrag feeds the events of a drag source (stripe button or panel
// header) into the drag state. press returns the window being pressed
// (id and title) at a position, or "" to ignore the press.
func (w *Workbench) pointerDrag(gtx layout.Context, tag *int, from slotRef, origin image.Point,
	press func(pos f32.Point) (id, title string)) dragResult {
	thr := gtx.Dp(tokens.DefaultShell().DragThreshold)
	var r dragResult
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			return r
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch pe.Kind {
		case pointer.Press:
			if pe.Buttons.Contain(pointer.ButtonSecondary) {
				p := origin.Add(image.Pt(int(pe.Position.X), int(pe.Position.Y)))
				r.menu = &p
				continue
			}
			if id, title := press(pe.Position); id != "" {
				w.pressDrag(id, title, from, origin, pe.Position)
				w.dragTag = tag
			}
		case pointer.Drag:
			if w.dragTag == tag {
				w.moveDrag(pe.Position, thr)
			}
		case pointer.Release:
			if w.dragTag != tag {
				continue
			}
			w.dragTag = nil
			m, moved := w.releaseDrag(pe.Position, thr)
			if m != nil {
				r.move = m
			}
			r.released, r.dragged, r.pos = true, moved, pe.Position
		case pointer.Cancel:
			if w.dragTag == tag {
				w.drag, w.dragTag = nil, nil
			}
		}
	}
}

// dropAt returns the drop target at p (window coordinates), in this
// order: the full-width strip along the bottom edge (left half: bottom
// primary, right half: bottom split), the header of an open panel (join
// as tab), the side zones (see dropZone).
func (w *Workbench) dropAt(p image.Point) (dropTarget, bool) {
	m := w.main
	if m.Empty() {
		return dropTarget{}, false
	}
	if p.Y >= m.Max.Y-w.metric.Dp(tokens.DefaultShell().FullWidthStrip) {
		g := GroupPrimary
		if p.X >= (m.Min.X+m.Max.X)/2 {
			g = GroupSplit
		}
		return dropTarget{at: slotRef{SideBottom, g}, full: true}, true
	}
	hh := w.metric.Dp(w.headerH)
	for side := range numSides {
		for g := range numGroups {
			r := w.geom.panels[side][g]
			// The own panel is no tab target (the side zones apply).
			if id := w.panelID[side][g]; id == "" || id == w.drag.idOrEmpty() || r.Empty() {
				continue
			}
			if p.In(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+hh)) {
				return dropTarget{at: slotRef{side, StripeGroup(g)}, tab: true}, true
			}
		}
	}
	for _, at := range []slotRef{
		{SideLeft, GroupPrimary}, {SideLeft, GroupSplit},
		{SideRight, GroupPrimary}, {SideRight, GroupSplit},
		{SideBottom, GroupPrimary}, {SideBottom, GroupSplit},
	} {
		if p.In(w.dropZone(at)) {
			return dropTarget{at: at}, true
		}
	}
	return dropTarget{}, false
}

// dropZone is the area of the main rectangle (extended over the stripes)
// that drops into slot at: the outer fifth left and right (upper three
// fifths primary, lower two fifths split), the lower fifth in between
// (left half primary, right half split).
func (w *Workbench) dropZone(at slotRef) image.Rectangle {
	m := w.main
	if m.Empty() {
		return image.Rectangle{}
	}
	fx, fy := m.Dx()/5, m.Dy()/5
	splitY := m.Min.Y + m.Dy()*3/5
	const far = 1 << 20 // the stripes count as part of the zone
	switch at {
	case slotRef{SideLeft, GroupPrimary}:
		return image.Rect(-far, m.Min.Y, m.Min.X+fx, splitY)
	case slotRef{SideLeft, GroupSplit}:
		return image.Rect(-far, splitY, m.Min.X+fx, m.Max.Y)
	case slotRef{SideRight, GroupPrimary}:
		return image.Rect(m.Max.X-fx, m.Min.Y, far, splitY)
	case slotRef{SideRight, GroupSplit}:
		return image.Rect(m.Max.X-fx, splitY, far, m.Max.Y)
	case slotRef{SideBottom, GroupPrimary}:
		return image.Rect(m.Min.X+fx, m.Max.Y-fy, m.Min.X+m.Dx()/2, m.Max.Y)
	case slotRef{SideBottom, GroupSplit}:
		return image.Rect(m.Min.X+m.Dx()/2, m.Max.Y-fy, m.Max.X-fx, m.Max.Y)
	}
	return image.Rectangle{}
}

// previewRect is the rectangle the dragged window would occupy after a
// drop at t: the target panel for tabs, otherwise the slot's rectangle in
// the layout that results from the move (window coordinates).
func (w *Workbench) previewRect(d *dragState, t dropTarget) image.Rectangle {
	if t.tab {
		return w.geom.panels[t.at.side][t.at.group]
	}
	open := w.open
	if w.panelID[d.from.side][d.from.group] == d.id {
		open[d.from.side][d.from.group] = false
	}
	open[t.at.side][t.at.group] = true
	full := w.BottomFull
	if t.full {
		full = [numCorners]bool{true, true}
	}
	var g geometry
	rects(w.plan(open, full, nil), w.main, w.bar, w.metric, &g)
	return g.panels[t.at.side][t.at.group]
}

// layoutDragPreview draws the drop preview and a label with the dragged
// window's title next to the pointer.
func (w *Workbench) layoutDragPreview(gtx layout.Context, th *tokens.Theme) {
	d := w.drag
	if d == nil || !d.active {
		return
	}
	if d.hasTarget {
		r := w.previewRect(d, d.target)
		fill := uint8(56)
		if d.target.tab {
			fill = 24 // the panel stays readable; the header band marks it
		}
		draw.Rect(gtx.Ops, r, tokens.WithAlpha(th.Color.Accent, fill))
		bw := gtx.Dp(th.Shell().DropOutline)
		draw.Border(gtx.Ops, r, 0, bw, tokens.WithAlpha(th.Color.Accent, 200))
		if d.target.tab {
			// The header band marks "as tab".
			hh := gtx.Dp(w.headerH)
			draw.Rect(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+hh), tokens.WithAlpha(th.Color.Accent, 90))
		}
	}
	label := d.title
	switch {
	case !d.hasTarget:
	case d.target.tab:
		label += " · as tab"
	case d.target.full:
		label += " · full width"
	}
	bubble := atoms.TooltipBubble{Text: label}
	bg := gtx
	bg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(th.Shell().TooltipMaxW), draw.Unbounded)}
	rec := op.Record(gtx.Ops)
	bd := bubble.Layout(bg, th)
	call := rec.Stop()
	// Below right of the pointer, kept inside the window.
	gap := gtx.Dp(th.Shell().PointerGap)
	at := d.pos.Add(image.Pt(gap, gap))
	if at.X+bd.Size.X > w.size.X {
		at.X = d.pos.X - gap - bd.Size.X
	}
	if at.Y+bd.Size.Y > w.size.Y {
		at.Y = d.pos.Y - gap - bd.Size.Y
	}
	off := op.Offset(image.Pt(max(0, at.X), max(0, at.Y))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
}
