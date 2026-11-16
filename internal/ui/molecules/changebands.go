package molecules

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Band connects a line range on the left edge with a line range on the
// right edge of a ChangeBands strip. Coordinates are pixels relative to
// the strip's top; an empty range (Top == Bottom) narrows the band to a
// point (lines that exist only on the other side).
type Band struct {
	LeftTop, LeftBottom   int
	RightTop, RightBottom int
	Fill, Edge            color.NRGBA
	// Emphasis draws the edges thicker (e.g. the current change).
	Emphasis bool
}

// ChangeBands is the strip between the two columns of a side-by-side
// diff: every change block is drawn as a curved band from its lines on
// the left to its lines on the right. It fills the maximum constraints
// with the editor background; bands outside the strip are clipped.
type ChangeBands struct {
	Bands []Band
}

// Layout draws the strip.
func (c ChangeBands) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded || size.Y >= draw.Unbounded {
		size = gtx.Constraints.Min
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Editor.Gutter)
	w := float32(size.X)
	for _, b := range c.Bands {
		if max(b.LeftBottom, b.RightBottom) < -2 || min(b.LeftTop, b.RightTop) > size.Y+2 {
			continue
		}
		lt, lb := float32(b.LeftTop), float32(b.LeftBottom)
		rt, rb := float32(b.RightTop), float32(b.RightBottom)
		curve := func(p *clip.Path, x0, y0, x1, y1 float32) {
			p.CubeTo(f32.Pt(x0+(x1-x0)/2, y0), f32.Pt(x0+(x1-x0)/2, y1), f32.Pt(x1, y1))
		}
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(0, lt))
		curve(&p, 0, lt, w, rt)
		p.LineTo(f32.Pt(w, rb))
		curve(&p, w, rb, 0, lb)
		p.Close()
		paint.FillShape(gtx.Ops, b.Fill, clip.Outline{Path: p.End()}.Op())

		ew := float32(max(1, gtx.Dp(1)))
		if b.Emphasis {
			ew = float32(max(2, gtx.Dp(2)))
		}
		for _, e := range [][2]float32{{lt, rt}, {lb, rb}} {
			var s clip.Path
			s.Begin(gtx.Ops)
			s.MoveTo(f32.Pt(0, e[0]))
			curve(&s, 0, e[0], w, e[1])
			paint.FillShape(gtx.Ops, b.Edge, clip.Stroke{Path: s.End(), Width: ew}.Op())
		}
	}
	return layout.Dimensions{Size: size}
}

// AnnotationCell is one cell of an annotation gutter column (e.g. blame):
// an age bar at the left edge, a background tint and a single-line text.
// It fills the exact constraints.
type AnnotationCell struct {
	Text string
	// Tint is painted over the background (zero alpha: none).
	Tint color.NRGBA
	// Bar is the color of the 3dp bar at the left edge (zero alpha: none).
	Bar color.NRGBA
	// Muted draws the text in the secondary color.
	Muted bool
}

// Layout draws the cell.
func (a AnnotationCell) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded || size.Y >= draw.Unbounded {
		size = gtx.Constraints.Min
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, a.Tint)
	bar := gtx.Dp(3)
	draw.Rect(gtx.Ops, image.Rect(0, 0, bar, size.Y), a.Bar)
	if a.Text != "" {
		l := atoms.Label{Text: a.Text, Role: atoms.RoleSmall, MaxLines: 1}
		if !a.Muted {
			l.Color = th.Editor.LineNumberActive
		}
		inset := layout.Inset{Left: th.Space.S, Right: th.Space.XS}
		gtx.Constraints = layout.Exact(size)
		layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return inset.Layout(gtx, l.Fn(th))
		})
	}
	return layout.Dimensions{Size: size}
}
