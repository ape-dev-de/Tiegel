// Package templates provides window layouts composed of organisms. The
// Workbench is Tiegel's main window: project tree on the left, editor tabs
// and editor in the middle, a collapsible tool window area at the bottom
// (Problems, Git, Debug, … – the host registers the tabs), an optional
// panel on the right and the status bar. Modal overlays (choosers,
// dialogs) are drawn on a scrim above everything.
//
// Templates only arrange: the host passes the content of every area as a
// layout.Widget (Slots) and receives the user's layout intentions as
// events (D71).
package templates

import (
	"image"

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

// ToolTab is a tab of the bottom tool window area.
type ToolTab struct {
	ID    string
	Title string
	Glyph *atoms.Glyph
	// Badge is a counter or short state ("3", "●").
	Badge     string
	BadgeKind atoms.BadgeKind
}

// Slots are the contents of the workbench areas; nil slots are left out.
type Slots struct {
	Sidebar, Tabs, Find, Editor, Tool, Right, Status layout.Widget
	// Overlay is drawn on a scrim above everything (modal).
	Overlay layout.Widget
	// OverlayCentered centers the overlay vertically (dialogs); otherwise
	// it sits near the top (choosers).
	OverlayCentered bool
}

// Event is reported by Workbench.Update.
type Event interface{ isWorkbenchEvent() }

// ToolEvent: a tool tab was clicked (the host opens it or, if it is the
// open one, collapses the area).
type ToolEvent struct{ ID string }

// DismissEvent: the scrim around an overlay was clicked.
type DismissEvent struct{}

func (ToolEvent) isWorkbenchEvent()    {}
func (DismissEvent) isWorkbenchEvent() {}

// Workbench is the main window template.
type Workbench struct {
	// SidebarVisible shows the project tree.
	SidebarVisible bool
	// ToolTabs are the tool windows; ActiveTool is the shown one and
	// ToolOpen expands the area.
	ToolTabs   []ToolTab
	ActiveTool string
	ToolOpen   bool
	// RightVisible shows the right panel.
	RightVisible bool

	side, bottom, right molecules.Split
	toolBtns            map[string]*widget.Clickable
	scrim, block        int
	inited              bool
}

func (w *Workbench) init() {
	if w.inited {
		return
	}
	w.inited = true
	w.side.Ratio, w.side.MinPane = 0.22, 160
	w.bottom.Ratio, w.bottom.Vertical, w.bottom.MinPane = 0.68, true, 80
	w.right.Ratio, w.right.MinPane = 0.72, 200
	w.toolBtns = map[string]*widget.Clickable{}
}

// ToolShare returns the share of the editor column taken by the open tool
// area (0..1).
func (w *Workbench) ToolShare() float32 {
	w.init()
	return 1 - w.bottom.Ratio
}

// SetToolShare sets the share of the editor column taken by the tool area
// (clamped to 0.1..0.9); the user can still drag the divider.
func (w *Workbench) SetToolShare(share float32) {
	w.init()
	w.bottom.Ratio = 1 - min(max(share, 0.1), 0.9)
}

func (w *Workbench) btn(id string) *widget.Clickable {
	b := w.toolBtns[id]
	if b == nil {
		b = new(widget.Clickable)
		w.toolBtns[id] = b
	}
	return b
}

// Update returns tool tab clicks and scrim clicks.
func (w *Workbench) Update(gtx layout.Context) []Event {
	w.init()
	var out []Event
	for _, t := range w.ToolTabs {
		if w.btn(t.ID).Clicked(gtx) {
			out = append(out, ToolEvent{ID: t.ID})
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

// Layout fills the window.
func (w *Workbench) Layout(gtx layout.Context, th *tokens.Theme, s Slots) layout.Dimensions {
	w.init()
	size := gtx.Constraints.Max
	gtx.Constraints = layout.Exact(size)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Background)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			main := func(gtx layout.Context) layout.Dimensions { return w.layoutCenter(gtx, th, s) }
			if s.Right != nil && w.RightVisible {
				inner := main
				main = func(gtx layout.Context) layout.Dimensions {
					return w.right.Layout(gtx, th, inner, panel(th, atoms.SurfacePanel, s.Right))
				}
			}
			if s.Sidebar != nil && w.SidebarVisible {
				return w.side.Layout(gtx, th, panel(th, atoms.SurfacePanel, s.Sidebar), main)
			}
			return main(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.Status == nil {
				return layout.Dimensions{}
			}
			return s.Status(gtx)
		}),
	)
	if s.Overlay != nil {
		w.layoutOverlay(gtx, th, s)
	}
	return layout.Dimensions{Size: size}
}

// panel fills the area with a surface and lays out content in it.
func panel(th *tokens.Theme, kind atoms.SurfaceKind, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		atoms.Surface{Kind: kind}.Paint(gtx, th)
		return content(gtx)
	}
}

func (w *Workbench) layoutCenter(gtx layout.Context, th *tokens.Theme, s Slots) layout.Dimensions {
	editorArea := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(orEmpty(s.Tabs)),
			layout.Rigid(orEmpty(s.Find)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				atoms.Surface{Kind: atoms.SurfaceEditor}.Paint(gtx, th)
				return orEmpty(s.Editor)(gtx)
			}),
		)
	}
	header := func(gtx layout.Context) layout.Dimensions { return w.layoutToolHeader(gtx, th) }
	if len(w.ToolTabs) == 0 {
		return editorArea(gtx)
	}
	if !w.ToolOpen || s.Tool == nil {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, editorArea),
			layout.Rigid(header),
		)
	}
	return w.bottom.Layout(gtx, th, editorArea, func(gtx layout.Context) layout.Dimensions {
		atoms.Surface{Kind: atoms.SurfacePanel}.Paint(gtx, th)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(header),
			layout.Flexed(1, s.Tool),
		)
	})
}

func orEmpty(wd layout.Widget) layout.Widget {
	if wd == nil {
		return func(layout.Context) layout.Dimensions { return layout.Dimensions{} }
	}
	return wd
}

// layoutToolHeader draws the strip with the tool window tabs.
func (w *Workbench) layoutToolHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	h := gtx.Dp(th.Size.RowH + th.Space.XS)
	width := gtx.Constraints.Max.X
	draw.Rect(gtx.Ops, image.Rect(0, 0, width, h), th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, 0, width, gtx.Dp(th.Size.BorderWidth)), th.Color.Border)
	cg := gtx
	cg.Constraints = layout.Exact(image.Pt(width, h))
	children := make([]layout.FlexChild, 0, len(w.ToolTabs))
	for _, t := range w.ToolTabs {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := w.btn(t.ID)
			active := w.ToolOpen && t.ID == w.ActiveTool
			return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				content := func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.M, Right: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						col := th.Color.TextMuted
						if active {
							col = th.Color.Text
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if t.Glyph == nil {
									return layout.Dimensions{}
								}
								return layout.Inset{Right: th.Space.XS}.Layout(gtx, atoms.Icon{Glyph: t.Glyph, Size: th.Size.IconSmall, Color: col}.Fn(th))
							}),
							layout.Rigid(atoms.Label{Text: t.Title, Role: atoms.RoleSmall, Color: col}.Fn(th)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if t.Badge == "" {
									return layout.Dimensions{}
								}
								return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: t.Badge, Kind: t.BadgeKind}.Fn(th))
							}),
						)
					})
				}
				gtx.Constraints.Min.Y = h
				m := op.Record(gtx.Ops)
				d := layout.Center.Layout(gtx, content)
				call := m.Stop()
				switch {
				case active:
					draw.Rect(gtx.Ops, image.Rectangle{Max: d.Size}, th.Color.Selection)
					draw.Rect(gtx.Ops, image.Rect(0, d.Size.Y-gtx.Dp(2), d.Size.X, d.Size.Y), th.Color.Accent)
				case b.Hovered():
					draw.Rect(gtx.Ops, image.Rectangle{Max: d.Size}, th.Color.SurfaceHover)
				}
				call.Add(gtx.Ops)
				pointer.CursorPointer.Add(gtx.Ops)
				return d
			})
		}))
	}
	layout.Flex{Alignment: layout.Middle}.Layout(cg, children...)
	return layout.Dimensions{Size: image.Pt(width, h)}
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
