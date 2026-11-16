// Package draw contains low-level painting helpers shared by the Tiegel
// component packages. It is internal: modules and plugins must use the
// components in atoms/molecules/organisms instead of drawing themselves.
package draw

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Unbounded is the threshold above which a constraint is treated as
// unbounded (layout.List uses a very large main-axis maximum).
const Unbounded = 1 << 20

// RRect fills r with radius rad (pixels, clamped to half the shorter side).
func RRect(ops *op.Ops, r image.Rectangle, rad int, c color.NRGBA) {
	if c.A == 0 || r.Empty() {
		return
	}
	rad = clampRadius(r, rad)
	paint.FillShape(ops, c, clip.UniformRRect(r, rad).Op(ops))
}

// Border strokes the inside of r with the given width and radius.
func Border(ops *op.Ops, r image.Rectangle, rad, width int, c color.NRGBA) {
	if c.A == 0 || r.Empty() || width <= 0 {
		return
	}
	rad = clampRadius(r, rad)
	w := float32(width)
	inner := clip.UniformRRect(r, rad)
	// Stroke centered on a rectangle inset by half the width so the border
	// stays inside r.
	half := width / 2
	in := image.Rectangle{Min: r.Min.Add(image.Pt(half, half)), Max: r.Max.Sub(image.Pt(width-half, width-half))}
	if in.Empty() {
		paint.FillShape(ops, c, inner.Op(ops))
		return
	}
	rr := clip.UniformRRect(in, max(0, rad-half))
	paint.FillShape(ops, c, clip.Stroke{Path: rr.Path(ops), Width: w}.Op())
}

// Rect fills r without rounding.
func Rect(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	if c.A == 0 || r.Empty() {
		return
	}
	paint.FillShape(ops, c, clip.Rect(r).Op())
}

// Circle fills a circle with center (cx, cy) and radius rad.
func Circle(ops *op.Ops, cx, cy, rad int, c color.NRGBA) {
	if c.A == 0 || rad <= 0 {
		return
	}
	r := image.Rect(cx-rad, cy-rad, cx+rad, cy+rad)
	paint.FillShape(ops, c, clip.Ellipse(r).Op(ops))
}

// Ring strokes a circle outline.
func Ring(ops *op.Ops, cx, cy, rad, width int, c color.NRGBA) {
	if c.A == 0 || rad <= 0 {
		return
	}
	r := image.Rect(cx-rad, cy-rad, cx+rad, cy+rad)
	paint.FillShape(ops, c, clip.Stroke{Path: clip.Ellipse(r).Path(ops), Width: float32(width)}.Op())
}

func clampRadius(r image.Rectangle, rad int) int {
	m := min(r.Dx(), r.Dy()) / 2
	return max(0, min(rad, m))
}

// TextOpts configures Text.
type TextOpts struct {
	Font      font.Font
	Size      unit.Sp
	Color     color.NRGBA
	MaxLines  int
	Alignment text.Alignment
}

// Text lays out a plain label. Zero MaxLines means unlimited.
func Text(gtx layout.Context, sh *text.Shaper, o TextOpts, s string) layout.Dimensions {
	m := op.Record(gtx.Ops)
	paint.ColorOp{Color: o.Color}.Add(gtx.Ops)
	mat := m.Stop()
	return widget.Label{MaxLines: o.MaxLines, Alignment: o.Alignment}.Layout(gtx, sh, o.Font, o.Size, s, mat)
}

// Measure lays out w with ops discarded and returns its dimensions.
func Measure(gtx layout.Context, w layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	m.Stop()
	return d
}

// Centered places a child of size inner centered within outer by offsetting
// call.
func Centered(ops *op.Ops, outer, inner image.Point, call op.CallOp) {
	off := image.Pt((outer.X-inner.X)/2, (outer.Y-inner.Y)/2)
	t := op.Offset(off).Push(ops)
	call.Add(ops)
	t.Pop()
}

// Width returns the width a block element should take: the maximum
// constraint, or fallback when the maximum is unbounded.
func Width(gtx layout.Context, fallback int) int {
	w := gtx.Constraints.Max.X
	if w >= Unbounded {
		w = max(fallback, gtx.Constraints.Min.X)
	}
	return w
}
