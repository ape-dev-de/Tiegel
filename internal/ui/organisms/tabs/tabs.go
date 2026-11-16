// Package tabs provides the editor tab bar organism: one molecules.Tab per
// open document with a modified marker and a close button, horizontally
// scrollable when the tabs do not fit.
package tabs

import (
	"image"

	"gioui.org/layout"

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

func (SelectEvent) isTabsEvent() {}
func (CloseEvent) isTabsEvent()  {}

// Bar is the tab bar organism.
type Bar struct {
	Items []Item
	// Active is the ID of the active tab.
	Active string

	tabs map[string]*molecules.Tab
	list layout.List
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
	}
	// Drop state of closed tabs.
	if len(b.tabs) > len(b.Items)+8 {
		keep := map[string]*molecules.Tab{}
		for _, it := range b.Items {
			keep[it.ID] = b.tabs[it.ID]
		}
		b.tabs = keep
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
		return t.Layout(gtx, th)
	})
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
