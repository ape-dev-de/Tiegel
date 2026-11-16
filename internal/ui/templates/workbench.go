// Package templates provides window layouts composed of organisms. The
// Workbench is Tiegel's main window: the editor area in the middle, tool
// window panels docked left, right and below it, stripes with one button
// per tool window along the left, right and bottom edge, an optional menu
// bar on top and the status bar. Modal overlays (choosers, dialogs) are
// drawn on a scrim above everything.
//
// Templates only arrange: the host passes the content of every area as a
// layout.Widget (Slots) and receives the user's layout intentions as
// events (D71): stripe button clicks, "move to" drags, hide and options
// clicks in panel headers and resized panels.
package templates

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

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

// StripeButton is a tool window button in a stripe.
type StripeButton struct {
	ID    string
	Title string
	Glyph *atoms.Glyph
	// Badge is a counter or short state ("3", "paused").
	Badge     string
	BadgeKind atoms.BadgeKind
	// Active marks the window shown in its panel.
	Active bool
}

// Panel is the content of a side panel; a nil Content closes the panel.
type Panel struct {
	// ID is the tool window shown (reported with header events).
	ID    string
	Title string
	// Content fills the panel below its header.
	Content layout.Widget
}

// Slots are the contents of the workbench areas; nil slots are left out.
type Slots struct {
	// Menu is an optional bar above everything (in-app menu bar).
	Menu layout.Widget
	// Editor is the editor area (tab rows and editors of all groups).
	Editor layout.Widget
	// Panels holds the open panel per side (index Side).
	Panels [numSides]Panel
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
// coordinates.
type ToolMenuEvent struct {
	ID  string
	Pos image.Point
}

// HideEvent: the hide button of a panel header was clicked.
type HideEvent struct{ ID string }

// MoveEvent: a stripe button was dragged to another side.
type MoveEvent struct {
	ID   string
	Side Side
}

// ResizeEvent: the divider of a panel was dragged; Size is the new
// share (Workbench.Sizes is already updated).
type ResizeEvent struct {
	Side Side
	Size float32
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
func (ResizeEvent) isWorkbenchEvent()     {}
func (PanelPressEvent) isWorkbenchEvent() {}
func (DismissEvent) isWorkbenchEvent()    {}

// Workbench is the main window template.
type Workbench struct {
	// Stripes are the tool window buttons per side (index Side).
	Stripes [numSides][]StripeButton
	// Sizes are the panel shares per side: of the window width (left,
	// right) and of the editor column height (bottom); 0 = default.
	// Dragging a divider updates them and reports a ResizeEvent.
	Sizes [numSides]float32
	// HideStripes hides all stripes (e.g. a distraction-free mode).
	HideStripes bool

	splits  [numSides]molecules.Split
	dragged [numSides]bool
	btns    map[string]*stripeBtn
	headers [numSides]panelHeader
	press   [numSides]int // panel press tags
	panelID [numSides]string
	pending []Event

	// Geometry of the last layout (window coordinates) for drops.
	stripeRect [numSides]image.Rectangle
	size       image.Point
	dragging   string
	dragTarget Side
	dragOver   bool

	scrim, block int
}

// stripeBtn is the state of one stripe button.
type stripeBtn struct {
	click  widget.Clickable
	tag    int
	origin image.Point // window position of the last layout
	side   Side
	press  f32.Point
	drag   bool
	seen   bool
}

// panelHeader is the state of a panel header.
type panelHeader struct {
	options, hide widget.Clickable
	optPos        image.Point
}

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
		return 0.32
	}
	return 0.22
}

// Update returns the user's layout intentions since the last frame.
func (w *Workbench) Update(gtx layout.Context) []Event {
	out := w.pending
	w.pending = nil
	for side := range numSides {
		for _, sb := range w.Stripes[side] {
			b := w.btn(sb.ID)
			if b.click.Clicked(gtx) && !b.drag {
				out = append(out, ToolEvent{ID: sb.ID})
			}
			out = append(out, w.stripePointer(gtx, sb.ID, b)...)
		}
		h := &w.headers[side]
		if id := w.panelID[side]; id != "" {
			if h.options.Clicked(gtx) {
				out = append(out, ToolMenuEvent{ID: id, Pos: h.optPos})
			}
			if h.hide.Clicked(gtx) {
				out = append(out, HideEvent{ID: id})
			}
			for {
				ev, ok := gtx.Event(pointer.Filter{Target: &w.press[side], Kinds: pointer.Press})
				if !ok {
					break
				}
				if pe, ok := ev.(pointer.Event); ok && pe.Kind == pointer.Press {
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

// stripePointer handles right clicks and drags of a stripe button.
func (w *Workbench) stripePointer(gtx layout.Context, id string, b *stripeBtn) []Event {
	var out []Event
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &b.tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		win := b.origin.Add(image.Pt(int(pe.Position.X), int(pe.Position.Y)))
		switch pe.Kind {
		case pointer.Press:
			b.press, b.drag = pe.Position, false
			if pe.Buttons.Contain(pointer.ButtonSecondary) {
				out = append(out, ToolMenuEvent{ID: id, Pos: win})
			}
		case pointer.Drag:
			if !b.drag && pe.Position.Sub(b.press).X*pe.Position.Sub(b.press).X+pe.Position.Sub(b.press).Y*pe.Position.Sub(b.press).Y > float32(gtx.Dp(8)*gtx.Dp(8)) {
				b.drag = true
			}
			if b.drag {
				w.dragging = id
				w.dragTarget, w.dragOver = w.dropSide(win)
			}
		case pointer.Release, pointer.Cancel:
			if b.drag && pe.Kind == pointer.Release {
				if side, ok := w.dropSide(win); ok && side != b.side {
					out = append(out, MoveEvent{ID: id, Side: side})
				}
			}
			w.dragging, w.dragOver = "", false
			if pe.Kind == pointer.Cancel {
				b.drag = false
			}
		}
	}
	return out
}

// dropSide returns the side whose drop zone contains p (window
// coordinates): the outer fifth of the window width left and right, the
// lower fifth below.
func (w *Workbench) dropSide(p image.Point) (Side, bool) {
	sz := w.size
	switch {
	case sz.X == 0 || sz.Y == 0:
		return 0, false
	case p.X < sz.X/5:
		return SideLeft, true
	case p.X > sz.X-sz.X/5:
		return SideRight, true
	case p.Y > sz.Y-sz.Y/5:
		return SideBottom, true
	}
	return 0, false
}

// Layout fills the window.
func (w *Workbench) Layout(gtx layout.Context, th *tokens.Theme, s Slots) layout.Dimensions {
	size := gtx.Constraints.Max
	w.size = size
	gtx.Constraints = layout.Exact(size)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Background)
	for side := range numSides {
		w.panelID[side] = ""
		if s.Panels[side].Content != nil {
			w.panelID[side] = s.Panels[side].ID
		}
	}
	for _, b := range w.btns {
		b.seen = false
	}
	y := 0
	if s.Menu != nil {
		rec := op.Record(gtx.Ops)
		mg := gtx
		mg.Constraints = layout.Constraints{Min: image.Pt(size.X, 0), Max: size}
		d := s.Menu(mg)
		call := rec.Stop()
		call.Add(gtx.Ops)
		y = d.Size.Y
	}
	statusH := 0
	var statusCall op.CallOp
	if s.Status != nil {
		rec := op.Record(gtx.Ops)
		sg := gtx
		sg.Constraints = layout.Constraints{Min: image.Pt(size.X, 0), Max: size}
		d := s.Status(sg)
		statusCall = rec.Stop()
		statusH = d.Size.Y
	}
	stripeV := 0
	stripeH := 0
	if !w.HideStripes {
		stripeV = gtx.Dp(th.Size.RowH + th.Space.XS)
		stripeH = gtx.Dp(th.Size.RowH)
	}
	bottomY := size.Y - statusH - stripeH
	mainRect := image.Rect(stripeV, y, size.X-stripeV, bottomY)
	if !w.HideStripes {
		w.layoutStripe(gtx, th, SideLeft, image.Rect(0, y, stripeV, bottomY))
		w.layoutStripe(gtx, th, SideRight, image.Rect(size.X-stripeV, y, size.X, bottomY))
		w.layoutStripe(gtx, th, SideBottom, image.Rect(0, bottomY, size.X, bottomY+stripeH))
	}
	w.layoutMain(gtx, th, s, mainRect)
	if s.Status != nil {
		t := op.Offset(image.Pt(0, size.Y-statusH)).Push(gtx.Ops)
		statusCall.Add(gtx.Ops)
		t.Pop()
	}
	if w.dragging != "" && w.dragOver {
		w.layoutDropZone(gtx, th, mainRect)
	}
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

// layoutMain draws the panels and the editor area into r.
func (w *Workbench) layoutMain(gtx layout.Context, th *tokens.Theme, s Slots, r image.Rectangle) {
	t := op.Offset(r.Min).Push(gtx.Ops)
	defer t.Pop()
	mg := gtx
	mg.Constraints = layout.Exact(r.Size())
	editorCol := func(gtx layout.Context) layout.Dimensions {
		editor := func(gtx layout.Context) layout.Dimensions {
			atoms.Surface{Kind: atoms.SurfaceEditor}.Paint(gtx, th)
			if s.Editor == nil {
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			return s.Editor(gtx)
		}
		if s.Panels[SideBottom].Content == nil {
			return editor(gtx)
		}
		sp := &w.splits[SideBottom]
		sp.Vertical, sp.MinPane = true, 80
		sp.Ratio = 1 - w.Size(SideBottom)
		d := sp.Layout(gtx, th, editor, w.panel(th, SideBottom, s.Panels[SideBottom]))
		w.track(SideBottom, 1-sp.Ratio)
		return d
	}
	inner := editorCol
	if s.Panels[SideRight].Content != nil {
		inner = func(gtx layout.Context) layout.Dimensions {
			sp := &w.splits[SideRight]
			sp.MinPane = 160
			sp.Ratio = 1 - w.Size(SideRight)
			d := sp.Layout(gtx, th, editorCol, w.panel(th, SideRight, s.Panels[SideRight]))
			w.track(SideRight, 1-sp.Ratio)
			return d
		}
	}
	if s.Panels[SideLeft].Content != nil {
		sp := &w.splits[SideLeft]
		sp.MinPane = 160
		sp.Ratio = w.Size(SideLeft)
		sp.Layout(mg, th, w.panel(th, SideLeft, s.Panels[SideLeft]), inner)
		w.track(SideLeft, sp.Ratio)
		return
	}
	inner(mg)
}

// track records a dragged share and reports it once the drag ended.
func (w *Workbench) track(side Side, share float32) {
	// The ratio only changes through drag input (the host's Sizes are
	// applied before the split processes its events).
	changed := math.Abs(float64(share-w.Size(side))) >= 0.001
	if changed {
		w.Sizes[side] = share
	}
	switch {
	case w.splits[side].Dragging():
		w.dragged[side] = true
	case w.dragged[side] || changed:
		w.dragged[side] = false
		w.pending = append(w.pending, ResizeEvent{Side: side, Size: w.Size(side)})
	}
}

// panel draws a side panel: header (title, options, hide) and content.
func (w *Workbench) panel(th *tokens.Theme, side Side, p Panel) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		atoms.Surface{Kind: atoms.SurfacePanel}.Paint(gtx, th)
		size := gtx.Constraints.Max
		d := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.layoutHeader(gtx, th, side, p) }),
			layout.Flexed(1, p.Content),
		)
		pass := pointer.PassOp{}.Push(gtx.Ops)
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &w.press[side])
		area.Pop()
		pass.Pop()
		return d
	}
}

// layoutHeader draws a panel header.
func (w *Workbench) layoutHeader(gtx layout.Context, th *tokens.Theme, side Side, p Panel) layout.Dimensions {
	h := gtx.Dp(th.Size.RowH + th.Space.XS)
	width := gtx.Constraints.Max.X
	draw.Rect(gtx.Ops, image.Rect(0, 0, width, h), th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(th.Size.BorderWidth), width, h), th.Color.Border)
	hd := &w.headers[side]
	cg := gtx
	cg.Constraints = layout.Exact(image.Pt(width, h))
	iconBtn := func(c *widget.Clickable, g *atoms.Glyph) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(h, h)
				if c.Hovered() {
					draw.Rect(gtx.Ops, image.Rectangle{Max: sz}, th.Color.SurfaceHover)
				}
				gtx.Constraints = layout.Exact(sz)
				layout.Center.Layout(gtx, atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Fn(th))
				pointer.CursorPointer.Add(gtx.Ops)
				return layout.Dimensions{Size: sz}
			})
		})
	}
	layout.Flex{Alignment: layout.Middle}.Layout(cg,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Strong(p.Title).Fn(th))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, h)}
		}),
		iconBtn(&hd.options, atoms.GlyphMore),
		iconBtn(&hd.hide, atoms.GlyphRemove),
	)
	// The options menu opens below its button (window coordinates are
	// not known here; the host positions menus relative to the panel).
	hd.optPos = image.Pt(width-2*h, h)
	return layout.Dimensions{Size: image.Pt(width, h)}
}

// layoutStripe draws the buttons of side into r (window coordinates).
func (w *Workbench) layoutStripe(gtx layout.Context, th *tokens.Theme, side Side, r image.Rectangle) {
	w.stripeRect[side] = r
	draw.Rect(gtx.Ops, r, th.Color.SurfaceAlt)
	bw := gtx.Dp(th.Size.BorderWidth)
	switch side {
	case SideLeft:
		draw.Rect(gtx.Ops, image.Rect(r.Max.X-bw, r.Min.Y, r.Max.X, r.Max.Y), th.Color.Border)
	case SideRight:
		draw.Rect(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Min.X+bw, r.Max.Y), th.Color.Border)
	case SideBottom:
		draw.Rect(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+bw), th.Color.Border)
	}
	pos := r.Min
	if side == SideBottom {
		pos.X += gtx.Dp(th.Space.XS)
	} else {
		pos.Y += gtx.Dp(th.Space.XS)
	}
	clipR := clip.Rect(r).Push(gtx.Ops)
	defer clipR.Pop()
	for _, sb := range w.Stripes[side] {
		b := w.btn(sb.ID)
		b.seen, b.side, b.origin = true, side, pos
		t := op.Offset(pos).Push(gtx.Ops)
		d := w.layoutButton(gtx, th, side, sb, b, r)
		t.Pop()
		if side == SideBottom {
			pos.X += d.Size.X
		} else {
			pos.Y += d.Size.Y + gtx.Dp(th.Space.XXS)
		}
	}
}

// layoutButton draws one stripe button; vertical stripes rotate the
// label (left: bottom-up, right: top-down).
func (w *Workbench) layoutButton(gtx layout.Context, th *tokens.Theme, side Side, sb StripeButton, b *stripeBtn, r image.Rectangle) layout.Dimensions {
	thick := r.Dy()
	if side != SideBottom {
		thick = r.Dx()
	}
	col := th.Color.TextMuted
	if sb.Active {
		col = th.Color.Text
	}
	vertical := side != SideBottom
	// The icon stays upright; on vertical stripes it sits above the
	// rotated label.
	icon := func(gtx layout.Context) layout.Dimensions {
		if sb.Glyph == nil {
			return layout.Dimensions{}
		}
		return atoms.Icon{Glyph: sb.Glyph, Size: th.Size.IconSmall, Color: col}.Layout(gtx, th)
	}
	iconLen, gap := 0, gtx.Dp(th.Space.XS)
	var iconCall op.CallOp
	if vertical && sb.Glyph != nil {
		rec := op.Record(gtx.Ops)
		d := icon(gtx)
		iconCall = rec.Stop()
		iconLen = d.Size.Y
	}
	// Record the horizontal content.
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(0, thick), Max: image.Pt(1<<20, thick)}
	rec := op.Record(gtx.Ops)
	cd := layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(cg, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if vertical || sb.Glyph == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: th.Space.XS}.Layout(gtx, icon)
			}),
			layout.Rigid(atoms.Label{Text: sb.Title, Role: atoms.RoleSmall, Color: col}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if sb.Badge == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: sb.Badge, Kind: sb.BadgeKind}.Fn(th))
			}),
		)
	})
	content := rec.Stop()
	length := cd.Size.X
	size := image.Pt(length, thick)
	head := 0 // space above the rotated label (icon)
	if vertical {
		if iconLen > 0 {
			head = gtx.Dp(th.Space.S) + iconLen + gap - gtx.Dp(th.Space.S)/2
		}
		size = image.Pt(thick, head+length)
	}
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg := image.Rectangle{Max: size}
		switch {
		case sb.Active:
			draw.Rect(gtx.Ops, bg, th.Color.Selection)
		case b.click.Hovered() || w.dragging == sb.ID:
			draw.Rect(gtx.Ops, bg, th.Color.SurfaceHover)
		}
		// Accent marker on the panel side of the button.
		if sb.Active {
			m := gtx.Dp(2)
			switch side {
			case SideLeft:
				draw.Rect(gtx.Ops, image.Rect(0, 0, m, size.Y), th.Color.Accent)
			case SideRight:
				draw.Rect(gtx.Ops, image.Rect(size.X-m, 0, size.X, size.Y), th.Color.Accent)
			default:
				draw.Rect(gtx.Ops, image.Rect(0, size.Y-m, size.X, size.Y), th.Color.Accent)
			}
		}
		if iconLen > 0 {
			t := op.Offset(image.Pt((thick-iconLen)/2, gtx.Dp(th.Space.S))).Push(gtx.Ops)
			iconCall.Add(gtx.Ops)
			t.Pop()
		}
		var aff f32.Affine2D
		switch side {
		case SideLeft:
			aff = f32.Affine2D{}.Rotate(f32.Point{}, -math.Pi/2).Offset(f32.Pt(0, float32(head+length)))
		case SideRight:
			aff = f32.Affine2D{}.Rotate(f32.Point{}, math.Pi/2).Offset(f32.Pt(float32(thick), float32(head)))
		}
		tr := op.Affine(aff).Push(gtx.Ops)
		content.Add(gtx.Ops)
		tr.Pop()
		area := clip.Rect(bg).Push(gtx.Ops)
		event.Op(gtx.Ops, &b.tag)
		pointer.CursorPointer.Add(gtx.Ops)
		area.Pop()
		return layout.Dimensions{Size: size}
	})
}

// layoutDropZone highlights the side a dragged button would move to.
func (w *Workbench) layoutDropZone(gtx layout.Context, th *tokens.Theme, main image.Rectangle) {
	r := main
	switch w.dragTarget {
	case SideLeft:
		r.Max.X = r.Min.X + main.Dx()/5
	case SideRight:
		r.Min.X = r.Max.X - main.Dx()/5
	case SideBottom:
		r.Min.Y = r.Max.Y - main.Dy()/5
	}
	draw.Rect(gtx.Ops, r, tokens.WithAlpha(th.Color.Accent, 60))
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
