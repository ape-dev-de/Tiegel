package atoms

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Divider is a 1dp separator line in the border color. A horizontal divider
// spans the available width, a vertical one the available height. It is a
// value component.
type Divider struct {
	Vertical bool
	// Inset is the empty space on both ends of the line.
	Inset unit.Dp
}

// Layout draws the divider.
func (d Divider) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t := max(1, gtx.Dp(th.Size.BorderWidth))
	in := gtx.Dp(d.Inset)
	var size image.Point
	var r image.Rectangle
	if d.Vertical {
		h := gtx.Constraints.Max.Y
		if h >= draw.Unbounded {
			h = gtx.Constraints.Min.Y
		}
		size = image.Pt(t, h)
		r = image.Rect(0, in, t, h-in)
	} else {
		w := gtx.Constraints.Max.X
		if w >= draw.Unbounded {
			w = gtx.Constraints.Min.X
		}
		size = image.Pt(w, t)
		r = image.Rect(in, 0, w-in, t)
	}
	draw.Rect(gtx.Ops, r, th.Color.Border)
	return layout.Dimensions{Size: size}
}

// Fn adapts the divider to a layout.Widget.
func (d Divider) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return d.Layout(gtx, th) }
}
