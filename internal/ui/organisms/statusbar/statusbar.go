// Package statusbar provides the window status bar organism: a single row
// of segments on the left (messages) and right (caret position, encoding,
// language, PHP version, interpreter, language server, VCS branch).
//
// Segments are plain data (Item) or arbitrary components (Item.Widget, e.g.
// runtime.StatusbarVersion); clickable items report ClickEvents with their
// ID so modules can add their own entries.
package statusbar

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Tone colors an item's text.
type Tone uint8

// Tones.
const (
	ToneNormal Tone = iota
	ToneMuted
	ToneWarning
	ToneError
	ToneAccent
)

// Item is one status bar segment.
type Item struct {
	ID      string
	Text    string
	Tooltip string
	Glyph   *atoms.Glyph
	Tone    Tone
	// Clickable items report ClickEvent.
	Clickable bool
	// Widget replaces Text/Glyph with a component (e.g. the PHP version
	// selector); it handles its own input.
	Widget ui.Component
}

// Event is reported by Bar.Update.
type Event interface{ isStatusEvent() }

// ClickEvent: a clickable item was clicked.
type ClickEvent struct{ ID string }

func (ClickEvent) isStatusEvent() {}

// Bar is the status bar organism.
type Bar struct {
	Left, Right []Item

	clicks map[string]*widget.Clickable
	since  map[string]time.Time
}

func (b *Bar) click(id string) *widget.Clickable {
	if b.clicks == nil {
		b.clicks = map[string]*widget.Clickable{}
	}
	c := b.clicks[id]
	if c == nil {
		c = new(widget.Clickable)
		b.clicks[id] = c
	}
	return c
}

// Update returns clicks on clickable items.
func (b *Bar) Update(gtx layout.Context) []Event {
	var out []Event
	for _, list := range [][]Item{b.Left, b.Right} {
		for _, it := range list {
			if it.Clickable && it.Widget == nil && b.click(it.ID).Clicked(gtx) {
				out = append(out, ClickEvent{ID: it.ID})
			}
		}
	}
	return out
}

// Height returns the bar height in pixels.
func Height(gtx layout.Context, th *tokens.Theme) int { return gtx.Dp(th.Size.RowH) }

// Layout draws the bar across the full width.
func (b *Bar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.Update(gtx)
	h := Height(gtx, th)
	w := draw.Width(gtx, gtx.Dp(400))
	size := image.Pt(w, h)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, gtx.Dp(th.Size.BorderWidth)), th.Color.Border)
	cg := gtx
	cg.Constraints = layout.Exact(size)
	children := make([]layout.FlexChild, 0, len(b.Left)+len(b.Right)+1)
	for _, it := range b.Left {
		children = append(children, layout.Rigid(b.item(th, it, false)))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Min}
	}))
	for _, it := range b.Right {
		children = append(children, layout.Rigid(b.item(th, it, true)))
	}
	layout.Flex{Alignment: layout.Middle}.Layout(cg, children...)
	return layout.Dimensions{Size: size}
}

func (b *Bar) item(th *tokens.Theme, it Item, right bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		if it.Widget != nil {
			return layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return it.Widget.Layout(gtx, th)
			})
		}
		content := func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				col := toneColor(th, it.Tone)
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if it.Glyph == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Right: th.Space.XS}.Layout(gtx, atoms.Icon{Glyph: it.Glyph, Size: th.Size.IconSmall, Color: col}.Fn(th))
					}),
					layout.Rigid(atoms.Label{Text: it.Text, Role: atoms.RoleSmall, Color: col, MaxLines: 1}.Fn(th)),
				)
			})
		}
		if !it.Clickable && it.Tooltip == "" {
			return content(gtx)
		}
		id := it.ID
		if id == "" {
			id = it.Text
		}
		c := b.click(id)
		d := c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			m := draw.Measure(gtx, content)
			if c.Hovered() && it.Clickable {
				draw.Rect(gtx.Ops, image.Rectangle{Max: m.Size}, th.Color.SurfaceHover)
			}
			return content(gtx)
		})
		b.tooltip(gtx, th, id, c.Hovered(), it.Tooltip, d.Size, right)
		return d
	}
}

// tooltip shows text above the item after it was hovered for
// atoms.TooltipDelay (the bar sits at the window bottom).
func (b *Bar) tooltip(gtx layout.Context, th *tokens.Theme, id string, hovered bool, text string, size image.Point, right bool) {
	if b.since == nil {
		b.since = map[string]time.Time{}
	}
	if !hovered || text == "" {
		delete(b.since, id)
		return
	}
	since, ok := b.since[id]
	if !ok {
		since = gtx.Now
		b.since[id] = since
	}
	if due := since.Add(atoms.TooltipDelay); gtx.Now.Before(due) {
		gtx.Execute(op.InvalidateCmd{At: due})
		return
	}
	m := op.Record(gtx.Ops)
	bg := gtx
	bg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(360), draw.Unbounded)}
	bd := draw.Measure(bg, atoms.TooltipBubble{Text: text}.Fn(th))
	x := 0
	if right {
		x = min(0, size.X-bd.Size.X)
	}
	off := op.Offset(image.Pt(x, -bd.Size.Y-gtx.Dp(th.Space.XS))).Push(gtx.Ops)
	atoms.TooltipBubble{Text: text}.Layout(bg, th)
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

func toneColor(th *tokens.Theme, t Tone) color.NRGBA {
	switch t {
	case ToneMuted:
		return th.Color.TextMuted
	case ToneWarning:
		return th.Color.Warning
	case ToneError:
		return th.Color.Danger
	case ToneAccent:
		return th.Color.Accent
	}
	return th.Color.Text
}
