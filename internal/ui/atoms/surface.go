package atoms

import (
	"image"
	"image/color"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Surface fills its constraints with a palette color – the background of
// panels, toolbars and headers. It is a value component: it takes the
// maximum constraints (the minimum on an unbounded axis).
type Surface struct {
	// Color defaults to th.Color.Surface when its alpha is zero.
	Color color.NRGBA
}

// Layout fills the area.
func (s Surface) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded {
		size.X = gtx.Constraints.Min.X
	}
	if size.Y >= draw.Unbounded {
		size.Y = gtx.Constraints.Min.Y
	}
	c := s.Color
	if c.A == 0 {
		c = th.Color.Surface
	}
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, c)
	return layout.Dimensions{Size: size}
}

// Fn adapts the surface to a layout.Widget.
func (s Surface) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return s.Layout(gtx, th) }
}
