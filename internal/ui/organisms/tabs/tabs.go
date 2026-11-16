// Package tabs provides the editor tab bar organism: one molecules.Tab per
// open document with a modified marker and a close button, horizontally
// scrollable when the tabs do not fit.
package tabs

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Item is one tab.
type Item struct {
	// ID identifies the tab (e.g. the absolute file path).
	ID       string
	Title    string
	Tooltip  string
	Glyph    *atoms.Glyph
	Modified bool
}

// Event is reported by Bar.Update.
type Event interface{ isTabsEvent() }

// SelectEvent: a tab was clicked.
type SelectEvent struct{ ID string }

// CloseEvent: a tab's close button was clicked.
type CloseEvent struct{ ID string }

// ContextEvent: a tab was clicked with the secondary button (context
// menu); Pos is relative to the bar.
type ContextEvent struct {
	ID  string
	Pos image.Point
}

func (SelectEvent) isTabsEvent()  {}
func (CloseEvent) isTabsEvent()   {}
func (ContextEvent) isTabsEvent() {}

// Bar is the tab bar organism.
type Bar struct {
	Items []Item
	// Active is the ID of the active tab.
	Active string
	// Dimmed draws the active tab without the accent (the bar of an
	// inactive editor group).
	Dimmed bool

	tabs map[string]*molecules.Tab
	ctx  map[string]*tabCtx
	list layout.List
}

// tabCtx tracks secondary clicks of one tab.
type tabCtx struct {
	tag  int
	x, w int // offset and width of the tab in the bar (last layout)
}

func (b *Bar) ctxOf(id string) *tabCtx {
	if b.ctx == nil {
		b.ctx = map[string]*tabCtx{}
	}
	c := b.ctx[id]
	if c == nil {
		c = &tabCtx{}
		b.ctx[id] = c
	}
	return c
}

func (b *Bar) tab(id string) *molecules.Tab {
	if b.tabs == nil {
		b.tabs = map[string]*molecules.Tab{}
	}
	t := b.tabs[id]
	if t == nil {
		t = &molecules.Tab{}
		b.tabs[id] = t
	}
	return t
}

// Update returns the clicks since the last frame.
func (b *Bar) Update(gtx layout.Context) []Event {
	var out []Event
	for _, it := range b.Items {
		sel, closed := b.tab(it.ID).Update(gtx)
		switch {
		case closed:
			out = append(out, CloseEvent{ID: it.ID})
		case sel:
			out = append(out, SelectEvent{ID: it.ID})
		}
		c := b.ctxOf(it.ID)
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &c.tag, Kinds: pointer.Press})
			if !ok {
				break
			}
			if pe, ok := ev.(pointer.Event); ok && pe.Buttons.Contain(pointer.ButtonSecondary) {
				out = append(out, ContextEvent{ID: it.ID, Pos: image.Pt(c.x+int(pe.Position.X), int(pe.Position.Y))})
			}
		}
	}
	// Drop state of closed tabs.
	if len(b.tabs) > len(b.Items)+8 {
		keep := map[string]*molecules.Tab{}
		keepCtx := map[string]*tabCtx{}
		for _, it := range b.Items {
			keep[it.ID] = b.tabs[it.ID]
			keepCtx[it.ID] = b.ctx[it.ID]
		}
		b.tabs, b.ctx = keep, keepCtx
	}
	return out
}

// Layout draws the bar across the full width, th.Size.TabH high.
func (b *Bar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.Update(gtx)
	h := gtx.Dp(th.Size.TabH)
	w := draw.Width(gtx, gtx.Dp(400))
	size := image.Pt(w, h)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(th.Size.BorderWidth), w, h), th.Color.Border)
	lg := gtx
	lg.Constraints = layout.Exact(size)
	b.list.Axis = layout.Horizontal
	b.list.Layout(lg, len(b.Items), func(gtx layout.Context, i int) layout.Dimensions {
		it := b.Items[i]
		t := b.tab(it.ID)
		t.Title, t.Tooltip, t.Glyph, t.Modified = it.Title, it.Tooltip, it.Glyph, it.Modified
		t.Active = it.ID == b.Active
		gtx.Constraints.Min.Y = h
		d := t.Layout(gtx, th)
		if t.Active && b.Dimmed {
			// Inactive group: cover the accent marker.
			draw.Rect(gtx.Ops, image.Rect(0, d.Size.Y-gtx.Dp(2), d.Size.X, d.Size.Y), th.Editor.Background)
		}
		c := b.ctxOf(it.ID)
		c.w = d.Size.X
		pass := pointer.PassOp{}.Push(gtx.Ops)
		area := clip.Rect{Max: d.Size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &c.tag)
		area.Pop()
		pass.Pop()
		return d
	})
	// Tab offsets for context positions.
	x := 0
	for i := range b.Items {
		if i < b.list.Position.First {
			continue
		}
		c := b.ctxOf(b.Items[i].ID)
		c.x = x - b.list.Position.Offset
		x += c.w
	}
	return layout.Dimensions{Size: size}
}

// ScrollToActive scrolls the active tab into view on the next Layout.
func (b *Bar) ScrollToActive() {
	for i, it := range b.Items {
		if it.ID == b.Active {
			if i < b.list.Position.First || i >= b.list.Position.First+b.list.Position.Count {
				b.list.ScrollTo(i)
			}
			return
		}
	}
}
