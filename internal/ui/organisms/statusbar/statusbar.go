// Package statusbar provides the window footer organism: one row with a
// breadcrumb (project › directory › file) and messages on the left and
// status widgets on the right (PHP version, interpreter, caret position,
// line separator, encoding, indentation, VCS branch, Xdebug, language
// server).
//
// Segments are plain data (Item, Crumb) or arbitrary components
// (Item.Widget, e.g. runtime.StatusbarVersion); clickable items report
// ClickEvents with their ID, crumbs CrumbEvents with their index, so
// modules can add their own entries. Text uses the body size and every
// segment is a single line with a hit target of the full bar height.
package statusbar

import (
	"image"
	"image/color"
	"strconv"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
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

// Crumb is one segment of the breadcrumb (left end of the bar).
type Crumb struct {
	Text string
	// Glyph is an optional icon before the text (e.g. the file type).
	Glyph   *atoms.Glyph
	Tooltip string
}

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

// CrumbEvent: the breadcrumb segment Path[Index] was clicked.
type CrumbEvent struct{ Index int }

func (ClickEvent) isStatusEvent() {}
func (CrumbEvent) isStatusEvent() {}

// Bar is the status bar organism.
type Bar struct {
	// Path is the breadcrumb drawn first on the left; the segments are
	// separated by chevrons, the last one (the file) is emphasized.
	Path []Crumb
	// Left items follow the breadcrumb, Right items are right-aligned.
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

// crumbID is the click ID of breadcrumb segment i.
func crumbID(i int) string { return "\x00crumb:" + strconv.Itoa(i) }

// Update returns clicks on clickable items and breadcrumb segments.
func (b *Bar) Update(gtx layout.Context) []Event {
	var out []Event
	for i := range b.Path {
		if b.click(crumbID(i)).Clicked(gtx) {
			out = append(out, CrumbEvent{Index: i})
		}
	}
	for _, list := range [][]Item{b.Left, b.Right} {
		for _, it := range list {
			if it.Clickable && it.Widget == nil && b.click(it.ID).Clicked(gtx) {
				out = append(out, ClickEvent{ID: it.ID})
			}
		}
	}
	return out
}

// Height returns the bar height in pixels: Size.StatusH, or more if the
// body text (user font size) needs it.
func Height(gtx layout.Context, th *tokens.Theme) int {
	line := gtx.Sp(th.Text.Body) * 7 / 5 // body line height
	return max(gtx.Dp(th.Size.StatusH), line+2*gtx.Dp(th.Space.XS))
}

// Layout draws the bar across the full width.
func (b *Bar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	h := Height(gtx, th)
	w := draw.Width(gtx, gtx.Dp(400))
	size := image.Pt(w, h)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, gtx.Dp(th.Size.BorderWidth)), th.Color.Border)
	// Segments are as tall as the bar minus a small margin, so hover
	// backgrounds line up and the whole height is clickable.
	margin := gtx.Dp(th.Space.XXS)
	itemH := h - 2*margin
	pad := gtx.Dp(th.Space.XS)
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(w-2*pad, 0), Max: image.Pt(w-2*pad, itemH)}
	children := make([]layout.FlexChild, 0, 2*len(b.Path)+len(b.Left)+len(b.Right)+1)
	for i, c := range b.Path {
		if i > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return separator(gtx, th, itemH)
			}))
		}
		last := i == len(b.Path)-1
		tone := ToneMuted
		if last {
			tone = ToneNormal
		}
		it := Item{ID: crumbID(i), Text: c.Text, Glyph: c.Glyph, Tooltip: c.Tooltip, Tone: tone, Clickable: true}
		children = append(children, layout.Rigid(b.item(th, it, false, itemH)))
	}
	for _, it := range b.Left {
		children = append(children, layout.Rigid(b.item(th, it, false, itemH)))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
	}))
	for _, it := range b.Right {
		children = append(children, layout.Rigid(b.item(th, it, true, itemH)))
	}
	t := op.Offset(image.Pt(pad, margin)).Push(gtx.Ops)
	layout.Flex{Alignment: layout.Middle}.Layout(cg, children...)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// separator draws the chevron between breadcrumb segments.
func separator(gtx layout.Context, th *tokens.Theme, h int) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	rec := op.Record(gtx.Ops)
	d := atoms.Icon{Glyph: atoms.GlyphChevronRight, Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Layout(gtx, th)
	call := rec.Stop()
	size := image.Pt(d.Size.X, h)
	draw.Centered(gtx.Ops, size, d.Size, call)
	return layout.Dimensions{Size: size}
}

// item draws one segment: h pixels tall, content centered.
func (b *Bar) item(th *tokens.Theme, it Item, right bool, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		gtx.Constraints.Max.Y = h
		if it.Widget != nil {
			rec := op.Record(gtx.Ops)
			d := layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return it.Widget.Layout(gtx, th)
			})
			call := rec.Stop()
			size := image.Pt(d.Size.X, h)
			draw.Centered(gtx.Ops, size, d.Size, call)
			return layout.Dimensions{Size: size}
		}
		col := toneColor(th, it.Tone)
		content := func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if it.Glyph == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Right: th.Space.XS}.Layout(gtx, atoms.Icon{Glyph: it.Glyph, Size: th.Size.IconSmall, Color: col}.Fn(th))
				}),
				layout.Rigid(atoms.Label{Text: it.Text, Role: atoms.RoleBody, Color: col, MaxLines: 1}.Fn(th)),
			)
		}
		padX := gtx.Dp(th.Space.S)
		box := func(gtx layout.Context, hover bool) layout.Dimensions {
			cg := gtx
			cg.Constraints.Max.X = max(0, gtx.Constraints.Max.X-2*padX)
			rec := op.Record(gtx.Ops)
			cd := content(cg)
			call := rec.Stop()
			size := image.Pt(cd.Size.X+2*padX, h)
			if hover {
				draw.RRect(gtx.Ops, image.Rectangle{Max: size}, gtx.Dp(th.Radius.M), th.Color.SurfaceHover)
			}
			draw.Centered(gtx.Ops, size, cd.Size, call)
			return layout.Dimensions{Size: size}
		}
		if !it.Clickable && it.Tooltip == "" {
			return box(gtx, false)
		}
		id := it.ID
		if id == "" {
			id = it.Text
		}
		c := b.click(id)
		d := c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			d := box(gtx, c.Hovered() && it.Clickable)
			if it.Clickable {
				area := clip.Rect{Max: d.Size}.Push(gtx.Ops)
				pointer.CursorPointer.Add(gtx.Ops)
				area.Pop()
			}
			return d
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
