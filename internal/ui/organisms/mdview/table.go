package mdview

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// layoutTable draws a table: column widths follow the content, capped so
// the table fits the width (cells wrap), a bold header row on a header
// surface, striped body rows, column alignment and grid lines.
func (v *View) layoutTable(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	ncol := len(n.b.Align)
	for _, row := range n.cells {
		ncol = max(ncol, len(row))
	}
	if ncol == 0 || len(n.cells) == 0 {
		return layout.Dimensions{Size: image.Pt(w, 0)}
	}
	padX, padY := gtx.Dp(th.Space.M), gtx.Dp(th.Space.XS)+gtx.Dp(th.Space.XXS)
	bw := max(1, gtx.Dp(th.Size.BorderWidth))
	avail := max(ncol, w-bw*(ncol+1)-2*padX*ncol)

	mins, nats := make([]int, ncol), make([]int, ncol)
	for _, row := range n.cells {
		for j, rt := range row {
			mn, nt := rt.natural(gtx, th, v)
			mins[j], nats[j] = max(mins[j], mn), max(nats[j], nt)
		}
	}
	widths := allocate(mins, nats, avail)

	// Row heights.
	empty := gtx.Sp(readerSize(th)) * 3 / 2
	heights := make([]int, len(n.cells))
	for i, row := range n.cells {
		h := empty
		for j, rt := range row {
			h = max(h, rt.measure(gtx, th, v, widths[j]).Y)
		}
		heights[i] = h + 2*padY
	}
	xs := make([]int, ncol+1) // left edge of each column's border
	for j := range ncol {
		xs[j+1] = xs[j] + bw + 2*padX + widths[j]
	}
	tw := xs[ncol] + bw
	ys := make([]int, len(heights)+1)
	for i, h := range heights {
		ys[i+1] = ys[i] + bw + h
	}
	th0 := ys[len(heights)] + bw

	// Backgrounds.
	for i := range n.cells {
		r := image.Rect(0, ys[i], tw, ys[i+1]+bw)
		switch {
		case i == 0:
			draw.Rect(gtx.Ops, r, th.Color.SurfaceAlt)
		case i%2 == 0:
			draw.Rect(gtx.Ops, r, th.Color.Surface)
		}
	}
	// Cells.
	col := c.color(th)
	for i, row := range n.cells {
		for j, rt := range row {
			align := text.Start
			if j < len(n.b.Align) {
				switch n.b.Align[j] {
				case markdown.AlignCenter:
					align = text.Middle
				case markdown.AlignRight:
					align = text.End
				}
			}
			cg := gtx
			cg.Constraints = layout.Constraints{Max: image.Pt(widths[j], draw.Unbounded)}
			t := op.Offset(image.Pt(xs[j]+bw+padX, ys[i]+bw+padY)).Push(gtx.Ops)
			rt.layout(cg, th, v, widths[j], col, align)
			t.Pop()
		}
	}
	// Grid.
	for _, x := range xs {
		draw.Rect(gtx.Ops, image.Rect(x, 0, x+bw, th0), th.Color.Border)
	}
	for _, y := range ys {
		draw.Rect(gtx.Ops, image.Rect(0, y, tw, y+bw), th.Color.Border)
	}
	base := bw + padY
	if len(n.cells[0]) > 0 {
		base += n.cells[0][0].firstBaseline()
	}
	return layout.Dimensions{Size: image.Pt(w, th0), Baseline: th0 - base}
}

// allocate distributes avail pixels to columns: the natural widths when
// they fit, otherwise the minimum widths plus the remaining space in
// proportion to how much each column wants beyond its minimum; when even
// the minimums do not fit, they are scaled down.
func allocate(mins, nats []int, avail int) []int {
	n := len(nats)
	out := make([]int, n)
	sumMin, sumNat := 0, 0
	for j := range n {
		mins[j] = max(1, min(mins[j], avail))
		nats[j] = max(nats[j], mins[j])
		sumMin += mins[j]
		sumNat += nats[j]
	}
	switch {
	case sumNat <= avail:
		copy(out, nats)
	case sumMin >= avail:
		for j := range n {
			out[j] = max(1, mins[j]*avail/sumMin)
		}
	default:
		extra, slack := avail-sumMin, sumNat-sumMin
		for j := range n {
			out[j] = mins[j] + (nats[j]-mins[j])*extra/slack
		}
	}
	return out
}
