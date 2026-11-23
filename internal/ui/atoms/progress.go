package atoms

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ProgressPeriod is the duration of one sweep of an indeterminate
// progress bar.
const ProgressPeriod = 1200 * time.Millisecond

// ProgressBar is a thin rounded bar (D509). Value in [0,1] fills that
// fraction in the accent color; a negative Value is indeterminate: a
// segment sweeps across the track (the bar schedules its own frames,
// about 30 per second, while it is drawn). It is a value component.
type ProgressBar struct {
	// Value is the progress in [0,1]; < 0 means indeterminate.
	Value float32
	// Width is the bar width (0: the minimum width of the constraints,
	// or 80dp when that is 0). Height defaults to 4dp.
	Width, Height unit.Dp
	// Danger draws the fill in the danger color (failed task).
	Danger bool
}

// Indeterminate reports whether the bar sweeps instead of filling.
func (p ProgressBar) Indeterminate() bool { return p.Value < 0 }

// Layout draws the bar.
func (p ProgressBar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w := gtx.Dp(p.Width)
	if w == 0 {
		w = gtx.Constraints.Min.X
	}
	if w == 0 {
		w = gtx.Dp(80)
	}
	w = min(w, gtx.Constraints.Max.X)
	hdp := p.Height
	if hdp == 0 {
		hdp = 4
	}
	h := max(1, gtx.Dp(hdp))
	size := image.Pt(w, h)
	rad := h / 2
	draw.RRect(gtx.Ops, image.Rectangle{Max: size}, rad, th.Color.Border)
	fill := th.Color.Accent
	if p.Danger {
		fill = th.Color.Danger
	}
	if p.Value >= 0 {
		v := min(p.Value, 1)
		if fw := int(float32(w) * v); fw > 0 {
			draw.RRect(gtx.Ops, image.Rect(0, 0, max(fw, h), h), rad, fill)
		}
		return layout.Dimensions{Size: size}
	}
	p.sweep(gtx, size, rad, fill)
	return layout.Dimensions{Size: size}
}

// sweep draws the moving segment of an indeterminate bar, clipped to the
// track, and asks for the next frame.
func (p ProgressBar) sweep(gtx layout.Context, size image.Point, rad int, c color.NRGBA) {
	seg := max(size.X/3, size.Y)
	n := gtx.Now.UnixNano() % int64(ProgressPeriod)
	if n < 0 {
		n += int64(ProgressPeriod)
	}
	phase := float32(n) / float32(ProgressPeriod)
	x := int(phase*float32(size.X+seg)) - seg
	area := clip.UniformRRect(image.Rectangle{Max: size}, rad).Push(gtx.Ops)
	draw.RRect(gtx.Ops, image.Rect(x, 0, x+seg, size.Y), rad, c)
	area.Pop()
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(33 * time.Millisecond)})
}

// Fn adapts the bar to a layout.Widget.
func (p ProgressBar) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, th) }
}
