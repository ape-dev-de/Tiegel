// Package templates provides window layouts composed of organisms. The
// Workbench is Tiegel's main window: an optional menu bar and toolbar
// (header) on top, the editor area in the middle, tool window panels
// docked left, right and below it, an icon stripe along the left and the
// right edge and exactly one footer (the status bar). Modal overlays
// (choosers, dialogs) are drawn on a scrim above everything.
//
// Every side holds up to two panels: the primary one and a split one
// (left/right: stacked below the primary panel; bottom: right of it).
// The stripes show one icon button per tool window, grouped by slot:
//
//	left stripe:  top = left primary, then left split; bottom = bottom primary
//	right stripe: top = right primary, then right split; bottom = bottom split
//
// There is no bottom stripe; bottom tool windows are reached through the
// bottom groups of the side stripes.
//
// Each bottom corner is owned either by the side panels (they span the
// full height, default) or by the bottom panel (it spans under the side
// panels of that corner, Workbench.BottomFull). Tool windows are moved by
// dragging their stripe button or panel header onto a slot, the bottom
// edge (full width) or another panel's header (as tab); see D474.
//
// Templates only arrange: the host passes the content of every area as a
// layout.Widget (Slots) and receives the user's layout intentions as
// events (D71): stripe button clicks, "move to" drags, hide, options and
// maximize clicks in panel headers and resized panels.
package templates

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Side is a docking side of the workbench.
type Side int

// Docking sides.
const (
	SideLeft Side = iota
	SideRight
	SideBottom
	numSides
)

// StripeGroup selects the panel slot of a side a tool window opens in.
type StripeGroup uint8

// Stripe groups.
const (
	// GroupPrimary windows open in the side's primary panel (left/right:
	// upper part, bottom: left part).
	GroupPrimary StripeGroup = iota
	// GroupSplit windows open in the side's split panel (left/right:
	// lower part, bottom: right part).
	GroupSplit
	numGroups
)

// StripeButton is a tool window button in a stripe.
type StripeButton struct {
	ID    string
	Title string
	// Glyph is the icon; without one the first letter of Title is shown.
	Glyph *atoms.Glyph
	// Badge is a counter or short state ("3", "paused"); the button
	// shows a dot in the kind's color and the tooltip the text.
	Badge     string
	BadgeKind atoms.BadgeKind
	// Active marks the window shown in its panel.
	Active bool
	// Group is the slot of the side the window opens in. For SideBottom
	// it also selects the stripe: GroupPrimary buttons sit in the bottom
	// group of the left stripe, GroupSplit buttons in the bottom group of
	// the right stripe.
	Group StripeGroup
	// Shortcut is shown in the tooltip next to the title ("Cmd+1").
	Shortcut string
}

// Panel is the content of a side panel; a nil Content closes the panel.
type Panel struct {
	// ID is the tool window shown (reported with header events).
	ID    string
	Title string
	// Content fills the panel below its header.
	Content layout.Widget
	// Actions (optional) are extra header controls drawn left of the
	// standard maximize, options and hide buttons; they get the header
	// height as maximum and are centered vertically.
	Actions layout.Widget
	// Tabs (optional) are the tool windows sharing the panel; with two or
	// more the header shows them as tabs instead of the title (ID is the
	// selected one). A click on another tab reports a SelectEvent.
	Tabs []PanelTab
}

// PanelTab is a tool window in the tab strip of a panel header.
type PanelTab struct {
	ID, Title string
}

// Slots are the contents of the workbench areas; nil slots are left out.
type Slots struct {
	// Menu is an optional bar above everything (in-app menu bar).
	Menu layout.Widget
	// Header is the toolbar row below the menu bar. The template draws
	// its background and bottom border, gives it the full width minus a
	// horizontal padding of Space.S on both sides and a height of
	// Size.ToolbarH (constraints: Min.X = Max.X, Min.Y = 0) and centers
	// it vertically.
	Header layout.Widget
	// Editor is the editor area (tab rows and editors of all groups).
	Editor layout.Widget
	// Panels holds the primary panel per side (index Side).
	Panels [numSides]Panel
	// Splits holds the split panel per side (GroupSplit windows); with
	// both panels of a side open, the side is divided (left/right:
	// stacked, bottom: side by side).
	Splits [numSides]Panel
	// Status is the footer (one row: breadcrumb and status widgets).
	Status layout.Widget
	// Overlay is drawn on a scrim above everything (modal).
	Overlay layout.Widget
	// OverlayCentered centers the overlay vertically (dialogs); otherwise
	// it sits near the top (choosers).
	OverlayCentered bool
}

// Event is reported by Workbench.Update.
type Event interface{ isWorkbenchEvent() }

// ToolEvent: a stripe button was clicked (the host shows the window or,
// if it is shown, hides it).
type ToolEvent struct{ ID string }

// ToolMenuEvent: a stripe button was right-clicked or the options button
// of a panel header was clicked; Pos is the pointer position in window
// coordinates (stripe) or the position below the options button relative
// to the panel (header).
type ToolMenuEvent struct {
	ID  string
	Pos image.Point
}

// HideEvent: the hide button of a panel header was clicked.
type HideEvent struct{ ID string }

// MoveEvent: a stripe button or panel header was dragged to another side
// or slot. With Tab, the window joins the panel of that slot as a tab;
// with FullWidth (dropped on the bottom edge) the bottom panel should
// take both bottom corners.
type MoveEvent struct {
	ID        string
	Side      Side
	Group     StripeGroup
	Tab       bool
	FullWidth bool
}

// SelectEvent: another tab of a panel header was clicked.
type SelectEvent struct{ ID string }

// ResizeEvent: a divider was dragged. Without Split, Size is the new
// share of the side (Workbench.Sizes); with Split, it is the share of the
// primary panel within the divided side (Workbench.SplitSizes). The
// Workbench field is already updated.
type ResizeEvent struct {
	Side  Side
	Size  float32
	Split bool
}

// MaximizeEvent: a panel was maximized (it fills the area of editor and
// panels) or restored, by its header button or a double click on the
// header. Workbench.Maximized is already updated.
type MaximizeEvent struct {
	ID        string
	Maximized bool
}

// PanelPressEvent: the pointer was pressed inside the panel showing ID
// (the host tracks the active tool window, e.g. for Shift+Escape).
type PanelPressEvent struct{ ID string }

// DismissEvent: the scrim around an overlay was clicked.
type DismissEvent struct{}

func (ToolEvent) isWorkbenchEvent()       {}
func (ToolMenuEvent) isWorkbenchEvent()   {}
func (HideEvent) isWorkbenchEvent()       {}
func (MoveEvent) isWorkbenchEvent()       {}
func (SelectEvent) isWorkbenchEvent()     {}
func (ResizeEvent) isWorkbenchEvent()     {}
func (MaximizeEvent) isWorkbenchEvent()   {}
func (PanelPressEvent) isWorkbenchEvent() {}
func (DismissEvent) isWorkbenchEvent()    {}

// Default shares.
const (
	defaultSideShare   = 0.22
	defaultBottomShare = 0.32
	defaultSplitShare  = 0.5
)

// Workbench is the main window template.
type Workbench struct {
	// Stripes are the tool window buttons per side (index Side), in
	// display order; StripeButton.Group sorts them into slots.
	Stripes [numSides][]StripeButton
	// Sizes are the panel shares per side: of the main area width (left,
	// right) and of the editor column height (bottom); 0 = default.
	// Dragging a divider updates them and reports a ResizeEvent.
	Sizes [numSides]float32
	// SplitSizes are the shares of the primary panel of a divided side
	// (0 = half).
	SplitSizes [numSides]float32
	// Maximized is the ID of the panel filling the main area ("" = none).
	// The header button and a double click on the header toggle it; it
	// is cleared when the panel is no longer shown.
	Maximized string
	// HideStripes hides both stripes (e.g. a distraction-free mode).
	HideStripes bool
	// BottomFull selects the owner of each bottom corner: true = the
	// bottom panel spans under the side panels of that side, false = the
	// side panels span the full height (default).
	BottomFull [numCorners]bool

	// splits: index side = between editor and side, numSides+side =
	// between primary and split panel of side.
	splits  [2 * numSides]molecules.Split
	dragged [2 * numSides]bool
	btns    map[string]*stripeBtn
	headers [numSides][numGroups]panelHeader
	press   [numSides][numGroups]int // panel press tags
	panelID [numSides][numGroups]string
	titles  [numSides][numGroups]string
	pending []Event

	// Geometry of the last layout (window coordinates) for drops and
	// previews.
	size    image.Point
	main    image.Rectangle
	geom    geometry
	open    openSlots
	bar     int
	metric  unit.Metric
	headerH unit.Dp
	drag    *dragState
	dragTag *int // the handler that owns drag

	scrim, block int
}

// slotRef names a panel slot.
type slotRef struct {
	side  Side
	group StripeGroup
}

// stripeBtn is the state of one stripe button.
type stripeBtn struct {
	click  widget.Clickable
	tag    int
	origin image.Point // window position of the last layout
	at     slotRef     // the slot the button belongs to
	drag   bool        // the last press became a drag (no click)
	seen   bool
	since  time.Time // hover start (tooltip)
}

// panelHeader is the state of a panel header.
type panelHeader struct {
	options, hide, max widget.Clickable
	tips               [3]atoms.Tooltip
	dbl                gesture.Click
	optPos             image.Point
	tag                int       // press, drag and release on the header
	spans              []tabSpan // tabs of the last layout
}

// tabSpan is the horizontal extent of a header tab.
type tabSpan struct {
	id, title string
	x0, x1    int
}

var (
	glyphMaximize = atoms.NewGlyph("panel-maximize", icons.NavigationFullscreen)
	glyphRestore  = atoms.NewGlyph("panel-restore", icons.NavigationFullscreenExit)
	glyphHide     = atoms.NewGlyph("panel-hide", icons.ContentRemove)
)

func (w *Workbench) btn(id string) *stripeBtn {
	if w.btns == nil {
		w.btns = map[string]*stripeBtn{}
	}
	b := w.btns[id]
	if b == nil {
		b = &stripeBtn{}
		w.btns[id] = b
	}
	return b
}

// Size returns the share of side (default if unset).
func (w *Workbench) Size(side Side) float32 {
	if s := w.Sizes[side]; s > 0 {
		return s
	}
	if side == SideBottom {
		return defaultBottomShare
	}
	return defaultSideShare
}

// SplitSize returns the share of the primary panel of a divided side
// (default if unset).
func (w *Workbench) SplitSize(side Side) float32 {
	if s := w.SplitSizes[side]; s > 0 && s < 1 {
		return s
	}
	return defaultSplitShare
}

// Update returns the user's layout intentions since the last frame.
func (w *Workbench) Update(gtx layout.Context) []Event {
	out := w.pending
	w.pending = nil
	w.dragKeys(gtx)
	for side := range numSides {
		for _, sb := range w.Stripes[side] {
			b := w.btn(sb.ID)
			// Pointer first: a release that ends a drag is no click.
			out = append(out, w.stripePointer(gtx, sb, b)...)
			if b.click.Clicked(gtx) && !b.drag {
				out = append(out, ToolEvent{ID: sb.ID})
			}
		}
		for g := range numGroups {
			// Events are read for every slot, open or not, so the
			// filters exist before a panel's first frame.
			id := w.panelID[side][g]
			h := &w.headers[side][g]
			out = append(out, w.headerPointer(gtx, slotRef{side, g}, id, h)...)
			if h.options.Clicked(gtx) && id != "" {
				out = append(out, ToolMenuEvent{ID: id, Pos: h.optPos})
			}
			if h.hide.Clicked(gtx) && id != "" {
				out = append(out, HideEvent{ID: id})
			}
			if h.max.Clicked(gtx) && id != "" {
				out = append(out, w.toggleMaximized(id))
			}
			for {
				ev, ok := h.dbl.Update(gtx.Source)
				if !ok {
					break
				}
				if ev.Kind == gesture.KindClick && ev.NumClicks == 2 && id != "" {
					out = append(out, w.toggleMaximized(id))
				}
			}
			for {
				ev, ok := gtx.Event(pointer.Filter{Target: &w.press[side][g], Kinds: pointer.Press})
				if !ok {
					break
				}
				if pe, ok := ev.(pointer.Event); ok && pe.Kind == pointer.Press && id != "" {
					out = append(out, PanelPressEvent{ID: id})
				}
			}
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &w.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if pe, ok := ev.(pointer.Event); ok && pe.Kind == pointer.Press {
			out = append(out, DismissEvent{})
		}
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &w.block, Kinds: pointer.Press}); !ok {
			break
		}
	}
	return out
}

// toggleMaximized maximizes panel id or restores it.
func (w *Workbench) toggleMaximized(id string) Event {
	if w.Maximized == id {
		w.Maximized = ""
		return MaximizeEvent{ID: id}
	}
	w.Maximized = id
	return MaximizeEvent{ID: id, Maximized: true}
}

// stripePointer handles right clicks and drags of a stripe button.
func (w *Workbench) stripePointer(gtx layout.Context, sb StripeButton, b *stripeBtn) []Event {
	var out []Event
	r := w.pointerDrag(gtx, &b.tag, b.at, b.origin, func(f32.Point) (string, string) { return sb.ID, sb.Title })
	if r.menu != nil {
		out = append(out, ToolMenuEvent{ID: sb.ID, Pos: *r.menu})
	}
	if r.move != nil {
		out = append(out, *r.move)
	}
	switch {
	case r.released:
		b.drag = r.dragged
	case w.dragTag == &b.tag:
		b.drag = w.drag != nil && w.drag.moved
	}
	return out
}

// headerPointer handles drags of a panel header (the shown window or the
// pressed tab), clicks on tabs and right clicks.
func (w *Workbench) headerPointer(gtx layout.Context, at slotRef, id string, h *panelHeader) []Event {
	// A closed panel starts no drag but still ends one that began on it.
	tabAt := func(x float32) (tabSpan, bool) {
		for _, sp := range h.spans {
			if int(x) >= sp.x0 && int(x) < sp.x1 {
				return sp, true
			}
		}
		return tabSpan{}, false
	}
	r := w.pointerDrag(gtx, &h.tag, at, w.geom.panels[at.side][at.group].Min, func(pos f32.Point) (string, string) {
		if id == "" {
			return "", ""
		}
		if sp, ok := tabAt(pos.X); ok {
			return sp.id, sp.title
		}
		return id, w.titles[at.side][at.group]
	})
	var out []Event
	if r.menu != nil && id != "" {
		out = append(out, ToolMenuEvent{ID: id, Pos: *r.menu})
	}
	if r.move != nil {
		out = append(out, *r.move)
	}
	if r.released && !r.dragged && id != "" {
		if sp, ok := tabAt(r.pos.X); ok && sp.id != id {
			out = append(out, SelectEvent{ID: sp.id})
		}
	}
	return out
}

// Layout fills the window.
func (w *Workbench) Layout(gtx layout.Context, th *tokens.Theme, s Slots) layout.Dimensions {
	size := gtx.Constraints.Max
	w.size = size
	gtx.Constraints = layout.Exact(size)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Background)
	shown := map[string]bool{}
	w.open = openSlots{}
	for side := range numSides {
		for g, p := range [numGroups]Panel{s.Panels[side], s.Splits[side]} {
			w.panelID[side][g], w.titles[side][g] = "", ""
			if p.Content != nil {
				w.panelID[side][g], w.titles[side][g] = p.ID, p.Title
				w.open[side][g] = true
				shown[p.ID] = true
			}
		}
	}
	w.metric, w.headerH = gtx.Metric, th.Size.PanelHeaderH
	w.bar = max(1, gtx.Dp(th.Size.BorderWidth))
	if !shown[w.Maximized] {
		w.Maximized = ""
	}
	for _, b := range w.btns {
		b.seen = false
	}
	y := 0
	if s.Menu != nil {
		mg := gtx
		mg.Constraints = layout.Constraints{Min: image.Pt(size.X, 0), Max: size}
		y = s.Menu(mg).Size.Y
	}
	if s.Header != nil {
		y += w.layoutHeaderSlot(gtx, th, s.Header, y, size.X)
	}
	statusH := 0
	var statusCall op.CallOp
	if s.Status != nil {
		rec := op.Record(gtx.Ops)
		sg := gtx
		sg.Constraints = layout.Constraints{Min: image.Pt(size.X, 0), Max: size}
		statusH = s.Status(sg).Size.Y
		statusCall = rec.Stop()
	}
	stripeW := 0
	if !w.HideStripes {
		stripeW = gtx.Dp(th.Size.StripeW)
	}
	bottomY := max(y, size.Y-statusH)
	w.main = image.Rect(stripeW, y, max(stripeW, size.X-stripeW), bottomY)
	if !w.HideStripes {
		w.layoutStripe(gtx, th, SideLeft, image.Rect(0, y, stripeW, bottomY))
		w.layoutStripe(gtx, th, SideRight, image.Rect(size.X-stripeW, y, size.X, bottomY))
	}
	w.layoutMain(gtx, th, s, w.main)
	if s.Status != nil {
		t := op.Offset(image.Pt(0, size.Y-statusH)).Push(gtx.Ops)
		statusCall.Add(gtx.Ops)
		t.Pop()
	}
	w.layoutDragPreview(gtx, th)
	for id, b := range w.btns {
		if !b.seen {
			delete(w.btns, id)
		}
	}
	if s.Overlay != nil {
		w.layoutOverlay(gtx, th, s)
	}
	return layout.Dimensions{Size: size}
}

// layoutHeaderSlot draws the toolbar row at y and returns its height.
func (w *Workbench) layoutHeaderSlot(gtx layout.Context, th *tokens.Theme, header layout.Widget, y, width int) int {
	h := gtx.Dp(th.Size.ToolbarH)
	t := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
	defer t.Pop()
	draw.Rect(gtx.Ops, image.Rect(0, 0, width, h), th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(th.Size.BorderWidth), width, h), th.Color.Border)
	pad := gtx.Dp(th.Space.S)
	inner := max(0, width-2*pad)
	hg := gtx
	hg.Constraints = layout.Constraints{Min: image.Pt(inner, 0), Max: image.Pt(inner, h)}
	rec := op.Record(gtx.Ops)
	d := header(hg)
	call := rec.Stop()
	c := clip.Rect{Max: image.Pt(width, h)}.Push(gtx.Ops)
	off := op.Offset(image.Pt(pad, max(0, (h-d.Size.Y)/2))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	c.Pop()
	return h
}

// layoutMain draws the panels and the editor area into r and records
// their rectangles.
func (w *Workbench) layoutMain(gtx layout.Context, th *tokens.Theme, s Slots, r image.Rectangle) {
	var maxAt *slotRef
	if w.Maximized != "" {
		for side := range numSides {
			for g := range numGroups {
				if w.open[side][g] && w.panelID[side][g] == w.Maximized {
					maxAt = &slotRef{side, StripeGroup(g)}
				}
			}
		}
	}
	tree := w.plan(w.open, w.BottomFull, maxAt)
	w.geom = geometry{}
	rects(tree, r, w.bar, gtx.Metric, &w.geom)
	t := op.Offset(r.Min).Push(gtx.Ops)
	defer t.Pop()
	mg := gtx
	mg.Constraints = layout.Exact(r.Size())
	w.render(th, s, tree)(mg)
}

// track records a dragged share (key: see Workbench.splits) and reports
// it once the drag ended.
func (w *Workbench) track(key int, share float32) {
	side, split := Side(key%int(numSides)), key >= int(numSides)
	cur, set := w.Size(side), &w.Sizes[side]
	if split {
		cur, set = w.SplitSize(side), &w.SplitSizes[side]
	}
	// The ratio only changes through drag input (the host's sizes are
	// applied before the split processes its events).
	changed := math.Abs(float64(share-cur)) >= 0.001
	if changed {
		*set = share
	}
	switch {
	case w.splits[key].Dragging():
		w.dragged[key] = true
	case w.dragged[key] || changed:
		w.dragged[key] = false
		w.pending = append(w.pending, ResizeEvent{Side: side, Size: *set, Split: split})
	}
}

// panel draws a side panel: header and content.
func (w *Workbench) panel(th *tokens.Theme, side Side, g StripeGroup, p Panel) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		atoms.Surface{Kind: atoms.SurfacePanel}.Paint(gtx, th)
		size := gtx.Constraints.Max
		d := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.layoutPanelHeader(gtx, th, side, g, p) }),
			layout.Flexed(1, p.Content),
		)
		pass := pointer.PassOp{}.Push(gtx.Ops)
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &w.press[side][g])
		area.Pop()
		pass.Pop()
		return d
	}
}

// layoutPanelHeader draws a panel header: title left, then the panel's
// actions and the maximize, options and hide buttons right-aligned.
func (w *Workbench) layoutPanelHeader(gtx layout.Context, th *tokens.Theme, side Side, g StripeGroup, p Panel) layout.Dimensions {
	h := gtx.Dp(th.Size.PanelHeaderH)
	width := gtx.Constraints.Max.X
	size := image.Pt(width, h)
	hd := &w.headers[side][g]
	// Double clicks anywhere on the header (below the buttons) toggle
	// maximize; pressing and moving drags the window (or the pressed tab).
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	hd.dbl.Add(gtx.Ops)
	event.Op(gtx.Ops, &hd.tag)
	area.Pop()
	draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(th.Size.BorderWidth), width, h), th.Color.Border)

	btn := gtx.Dp(th.Size.ActionButton)
	gap := gtx.Dp(th.Space.XXS)
	right := gtx.Dp(th.Space.XS)
	maxGlyph, maxTip := glyphMaximize, "Maximize"
	if w.Maximized == p.ID {
		maxGlyph, maxTip = glyphRestore, "Restore"
	}
	type action struct {
		c   *widget.Clickable
		g   *atoms.Glyph
		tip string
	}
	acts := []action{{&hd.max, maxGlyph, maxTip}, {&hd.options, atoms.GlyphMore, "Options"}, {&hd.hide, glyphHide, "Hide"}}
	x := width - right - len(acts)*btn - (len(acts)-1)*gap
	for i, a := range acts {
		t := op.Offset(image.Pt(x+i*(btn+gap), (h-btn)/2)).Push(gtx.Ops)
		bg := gtx
		bg.Constraints = layout.Exact(image.Pt(btn, btn))
		tip := &hd.tips[i]
		tip.Text = a.tip
		tip.Layout(bg, th, func(gtx layout.Context) layout.Dimensions {
			return iconButton(gtx, th, a.c, a.g, btn)
		})
		t.Pop()
	}
	// The options menu opens below its button, relative to the panel.
	hd.optPos = image.Pt(x+btn+gap, h)
	if p.Actions != nil {
		ag := gtx
		ag.Constraints = layout.Constraints{Max: image.Pt(max(0, x-gap), h)}
		rec := op.Record(gtx.Ops)
		d := p.Actions(ag)
		call := rec.Stop()
		x -= d.Size.X + gap
		t := op.Offset(image.Pt(max(0, x), (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
	pad := gtx.Dp(th.Space.M)
	hd.spans = hd.spans[:0]
	if len(p.Tabs) > 1 {
		w.layoutHeaderTabs(gtx, th, hd, p, max(0, x-gap), h)
		return layout.Dimensions{Size: size}
	}
	// Title: single line, vertically centered, cut before the buttons.
	tg := gtx
	tg.Constraints = layout.Constraints{Max: image.Pt(max(0, x-pad-gap), h)}
	rec := op.Record(gtx.Ops)
	td := atoms.Label{Text: p.Title, Role: atoms.RoleStrong, MaxLines: 1}.Layout(tg, th)
	call := rec.Stop()
	t := op.Offset(image.Pt(pad, (h-td.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// layoutHeaderTabs draws the tool windows of a tabbed panel as tabs in
// the header up to x and records their extents.
func (w *Workbench) layoutHeaderTabs(gtx layout.Context, th *tokens.Theme, hd *panelHeader, p Panel, limit, h int) {
	c := clip.Rect{Max: image.Pt(limit, h)}.Push(gtx.Ops)
	defer c.Pop()
	padX := gtx.Dp(th.Space.M)
	mark := gtx.Dp(th.Shell().TabMark)
	x := gtx.Dp(th.Space.XS)
	for _, tab := range p.Tabs {
		if x >= limit {
			break
		}
		active := tab.ID == p.ID
		lbl := atoms.Label{Text: tab.Title, Role: atoms.RoleBody, Color: th.Color.TextMuted, MaxLines: 1}
		if active {
			lbl.Role, lbl.Color = atoms.RoleStrong, th.Color.Text
		}
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(max(0, limit-x-2*padX), h)}
		rec := op.Record(gtx.Ops)
		ld := lbl.Layout(lg, th)
		call := rec.Stop()
		tw := ld.Size.X + 2*padX
		if active {
			draw.Rect(gtx.Ops, image.Rect(x, h-mark, x+tw, h), th.Color.Accent)
		}
		t := op.Offset(image.Pt(x+padX, (h-ld.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		if !active {
			area := clip.Rect(image.Rect(x, 0, x+tw, h)).Push(gtx.Ops)
			pointer.CursorPointer.Add(gtx.Ops)
			area.Pop()
		}
		hd.spans = append(hd.spans, tabSpan{id: tab.ID, title: tab.Title, x0: x, x1: x + tw})
		x += tw
	}
}

// iconButton draws a square icon button of px pixels with a rounded
// hover background.
func iconButton(gtx layout.Context, th *tokens.Theme, c *widget.Clickable, g *atoms.Glyph, px int) layout.Dimensions {
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(px, px)
		col := th.Color.TextMuted
		if c.Hovered() {
			draw.RRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(th.Radius.M), th.Color.SurfaceHover)
			col = th.Color.Text
		}
		layout.Center.Layout(gtx, atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}.Fn(th))
		area := clip.Rect{Max: sz}.Push(gtx.Ops)
		pointer.CursorPointer.Add(gtx.Ops)
		area.Pop()
		return layout.Dimensions{Size: sz}
	})
}

// stripeEntry is a button placed in a stripe.
type stripeEntry struct {
	sb StripeButton
	at slotRef
}

// stripeGroups returns the top groups (primary, split) and the bottom
// group of the stripe at side (left or right).
func (w *Workbench) stripeGroups(side Side) (top [numGroups][]stripeEntry, bottom []stripeEntry) {
	for _, sb := range w.Stripes[side] {
		g := min(sb.Group, GroupSplit)
		top[g] = append(top[g], stripeEntry{sb, slotRef{side, g}})
	}
	want := GroupPrimary
	if side == SideRight {
		want = GroupSplit
	}
	for _, sb := range w.Stripes[SideBottom] {
		if min(sb.Group, GroupSplit) == want {
			bottom = append(bottom, stripeEntry{sb, slotRef{SideBottom, want}})
		}
	}
	return top, bottom
}

// layoutStripe draws the icon buttons of the stripe at side into r
// (window coordinates).
func (w *Workbench) layoutStripe(gtx layout.Context, th *tokens.Theme, side Side, r image.Rectangle) {
	draw.Rect(gtx.Ops, r, th.Color.SurfaceAlt)
	bw := gtx.Dp(th.Size.BorderWidth)
	if side == SideLeft {
		draw.Rect(gtx.Ops, image.Rect(r.Max.X-bw, r.Min.Y, r.Max.X, r.Max.Y), th.Color.Border)
	} else {
		draw.Rect(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Min.X+bw, r.Max.Y), th.Color.Border)
	}
	clipR := clip.Rect(r).Push(gtx.Ops)
	defer clipR.Pop()
	bs := gtx.Dp(th.Size.StripeButton)
	margin := max(0, (r.Dx()-bs)/2)
	gap := gtx.Dp(th.Space.XS)
	sepH := gtx.Dp(th.Space.S) + bw
	top, bottom := w.stripeGroups(side)
	place := func(e stripeEntry, y int) {
		b := w.btn(e.sb.ID)
		b.seen, b.at = true, e.at
		b.origin = image.Pt(r.Min.X+margin, y)
		t := op.Offset(b.origin).Push(gtx.Ops)
		w.layoutStripeButton(gtx, th, side, e.sb, b, bs)
		t.Pop()
	}
	y := r.Min.Y + margin
	for g, list := range top {
		if g > 0 && len(list) > 0 && len(top[0]) > 0 {
			// A short rule separates the split group.
			mid := y - gap + sepH/2
			draw.Rect(gtx.Ops, image.Rect(r.Min.X+r.Dx()/4, mid, r.Max.X-r.Dx()/4, mid+bw), th.Color.Border)
			y += sepH
		}
		for _, e := range list {
			place(e, y)
			y += bs + gap
		}
	}
	// The bottom group ends at the stripe bottom; it never overlaps the
	// top groups (a too small window clips it instead).
	by := max(y+sepH, r.Max.Y-margin-len(bottom)*bs-max(0, len(bottom)-1)*gap)
	for _, e := range bottom {
		place(e, by)
		by += bs + gap
	}
}

// layoutStripeButton draws one square icon button of bs pixels.
func (w *Workbench) layoutStripeButton(gtx layout.Context, th *tokens.Theme, side Side, sb StripeButton, b *stripeBtn, bs int) {
	size := image.Pt(bs, bs)
	gtx.Constraints = layout.Exact(size)
	b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(th.Radius.M)
		col := th.Color.TextMuted
		switch {
		case sb.Active:
			draw.RRect(gtx.Ops, image.Rectangle{Max: size}, rr, th.Color.Selection)
			col = th.Color.Text
		case b.click.Hovered() || (w.Dragging() && w.drag.id == sb.ID):
			draw.RRect(gtx.Ops, image.Rectangle{Max: size}, rr, th.Color.SurfaceHover)
			col = th.Color.Text
		}
		if sb.Glyph != nil {
			layout.Center.Layout(gtx, atoms.Icon{Glyph: sb.Glyph, Size: th.Size.StripeIcon, Color: col}.Fn(th))
		} else {
			letter := ""
			for _, r := range sb.Title {
				letter = string(r)
				break
			}
			lg := gtx
			lg.Constraints.Min = image.Point{}
			rec := op.Record(gtx.Ops)
			ld := atoms.Label{Text: letter, Role: atoms.RoleStrong, Color: col, MaxLines: 1}.Layout(lg, th)
			draw.Centered(gtx.Ops, size, ld.Size, rec.Stop())
		}
		if sb.Badge != "" {
			dot := gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)
			ring := gtx.Dp(th.Space.XXS)
			cx, cy := bs-dot/2-ring, dot/2+ring
			draw.Circle(gtx.Ops, cx, cy, dot/2+ring, th.Color.SurfaceAlt)
			draw.Circle(gtx.Ops, cx, cy, dot/2, badgeColor(th, sb.BadgeKind))
		}
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &b.tag)
		pointer.CursorPointer.Add(gtx.Ops)
		area.Pop()
		return layout.Dimensions{Size: size}
	})
	w.stripeTooltip(gtx, th, side, sb, b, bs)
}

// stripeTooltip shows title, badge and shortcut beside a hovered stripe
// button (towards the window center) after atoms.TooltipDelay.
func (w *Workbench) stripeTooltip(gtx layout.Context, th *tokens.Theme, side Side, sb StripeButton, b *stripeBtn, bs int) {
	if !b.click.Hovered() || w.Dragging() {
		b.since = time.Time{}
		return
	}
	if b.since.IsZero() {
		b.since = gtx.Now
	}
	if due := b.since.Add(atoms.TooltipDelay); gtx.Now.Before(due) {
		gtx.Execute(op.InvalidateCmd{At: due})
		return
	}
	text := sb.Title
	if sb.Badge != "" {
		text += " (" + sb.Badge + ")"
	}
	bubble := atoms.TooltipBubble{Text: text, Shortcut: sb.Shortcut}
	bg := gtx
	bg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(th.Shell().TooltipMaxW), draw.Unbounded)}
	m := op.Record(gtx.Ops)
	bd := draw.Measure(bg, bubble.Fn(th))
	x := bs + gtx.Dp(th.Space.S)
	if side == SideRight {
		x = -bd.Size.X - gtx.Dp(th.Space.S)
	}
	off := op.Offset(image.Pt(x, (bs-bd.Size.Y)/2)).Push(gtx.Ops)
	bubble.Layout(bg, th)
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

// badgeColor is the dot color of a stripe badge.
func badgeColor(th *tokens.Theme, k atoms.BadgeKind) color.NRGBA {
	switch k {
	case atoms.BadgeAccent:
		return th.Color.Accent
	case atoms.BadgeSuccess:
		return th.Color.Success
	case atoms.BadgeWarning:
		return th.Color.Warning
	case atoms.BadgeDanger:
		return th.Color.Danger
	case atoms.BadgeInfo:
		return th.Color.Info
	}
	return th.Color.TextMuted
}

func (w *Workbench) layoutOverlay(gtx layout.Context, th *tokens.Theme, s Slots) {
	size := gtx.Constraints.Max
	m := op.Record(gtx.Ops)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, tokens.WithAlpha(th.Color.Background, 150))
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &w.scrim)
	area.Pop()
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, size.X-2*gtx.Dp(th.Space.L)), max(0, size.Y-2*gtx.Dp(th.Space.L)))}
	rec := op.Record(gtx.Ops)
	d := s.Overlay(cg)
	call := rec.Stop()
	y := gtx.Dp(th.Space.XXL) * 2
	if s.OverlayCentered {
		y = (size.Y - d.Size.Y) / 2
	}
	t := op.Offset(image.Pt((size.X-d.Size.X)/2, max(0, y))).Push(gtx.Ops)
	// The overlay itself blocks clicks from reaching the scrim.
	inner := clip.Rect{Max: d.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &w.block)
	inner.Pop()
	call.Add(gtx.Ops)
	t.Pop()
	op.Defer(gtx.Ops, m.Stop())
}
