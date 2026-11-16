package atoms

import (
	"image"
	"time"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TooltipDelay is how long the pointer must rest on a widget before its
// tooltip appears.
const TooltipDelay = 500 * time.Millisecond

// tooltipMaxWidth bounds the bubble width; longer text wraps.
const tooltipMaxWidth = unit.Dp(320)

// TooltipBubble is the visual part of a tooltip: a dark rounded box with
// text. It is a value component; Tooltip positions it on hover.
type TooltipBubble struct {
	Text string
}

// Layout draws the bubble.
func (b TooltipBubble) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	padX, padY := gtx.Dp(th.Space.S), gtx.Dp(th.Space.XS)
	cgtx := gtx
	maxW := min(gtx.Dp(tooltipMaxWidth), draw.Width(gtx, gtx.Dp(tooltipMaxWidth)))
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, maxW-2*padX), draw.Unbounded)}
	m := op.Record(gtx.Ops)
	td := Label{Text: b.Text, Role: RoleSmall, Color: th.Color.TooltipText}.Layout(cgtx, th)
	call := m.Stop()
	size := image.Pt(td.Size.X+2*padX, td.Size.Y+2*padY)
	draw.RRect(gtx.Ops, image.Rectangle{Max: size}, gtx.Dp(th.Radius.M), th.Color.TooltipBg)
	draw.Centered(gtx.Ops, size, td.Size, call)
	return layout.Dimensions{Size: size}
}

// Tooltip shows a TooltipBubble below its content after the pointer rested
// on it for TooltipDelay. It is a stateful wrapper: unlike leaf components
// its Layout takes the wrapped content as last argument.
type Tooltip struct {
	Text string
	// AlwaysVisible shows the bubble without hovering (catalog, tests).
	AlwaysVisible bool

	hover   gesture.Hover
	since   time.Time
	visible bool
}

// Visible reports whether the bubble was shown in the last frame.
func (t *Tooltip) Visible() bool { return t.visible }

// Layout draws content and, if due, the bubble on top of everything else.
func (t *Tooltip) Layout(gtx layout.Context, th *tokens.Theme, content layout.Widget) layout.Dimensions {
	d := content(gtx)

	hovered := t.hover.Update(gtx.Source)
	// Pass events through so the wrapped content (e.g. a button) still
	// receives clicks.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops)
	t.hover.Add(gtx.Ops)
	area.Pop()
	pass.Pop()

	t.visible = false
	switch {
	case t.AlwaysVisible:
		t.visible = true
	case hovered:
		if t.since.IsZero() {
			t.since = gtx.Now
		}
		if due := t.since.Add(TooltipDelay); gtx.Now.Before(due) {
			gtx.Execute(op.InvalidateCmd{At: due})
		} else {
			t.visible = true
		}
	default:
		t.since = time.Time{}
	}
	if !t.visible || t.Text == "" {
		return d
	}

	m := op.Record(gtx.Ops)
	off := op.Offset(image.Pt(0, d.Size.Y+gtx.Dp(th.Space.XS))).Push(gtx.Ops)
	bg := gtx
	bg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(tooltipMaxWidth), draw.Unbounded)}
	TooltipBubble{Text: t.Text}.Layout(bg, th)
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
	return d
}

// Fn adapts the bubble to a layout.Widget.
func (b TooltipBubble) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
}
