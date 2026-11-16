package molecules

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TableColumn describes a Table column. A column has either a fixed Width
// or a share (Weight) of the remaining width; a zero Weight counts as 1.
type TableColumn struct {
	Title  string
	Width  unit.Dp
	Weight float32
}

// TableCell lays out the cell at row, col. The context has the exact column
// width and a minimum height of one row.
type TableCell func(gtx layout.Context, row, col int) layout.Dimensions

// Table draws a header row and a scrolling list of rows with cells laid out
// by the caller. It fills the available width (and height when bounded).
type Table struct {
	Columns []TableColumn
	list    ScrollList
}

// ColumnWidths returns the pixel widths of the columns for a total width.
func (t *Table) ColumnWidths(gtx layout.Context, total int) []int {
	ws := make([]int, len(t.Columns))
	rest := total
	var weights float32
	for i, c := range t.Columns {
		if c.Width > 0 {
			ws[i] = gtx.Dp(c.Width)
			rest -= ws[i]
			continue
		}
		weights += weight(c)
	}
	rest = max(0, rest)
	used := 0
	last := -1
	for i, c := range t.Columns {
		if c.Width > 0 {
			continue
		}
		ws[i] = int(float32(rest) * weight(c) / weights)
		used += ws[i]
		last = i
	}
	if last >= 0 {
		ws[last] += rest - used // rounding remainder
	}
	return ws
}

func weight(c TableColumn) float32 {
	if c.Weight <= 0 {
		return 1
	}
	return c.Weight
}

// Layout draws the table with rows rows.
func (t *Table) Layout(gtx layout.Context, th *tokens.Theme, rows int, cell TableCell) layout.Dimensions {
	w := draw.Width(gtx, gtx.Dp(480))
	ws := t.ColumnWidths(gtx, w)
	header := t.layoutRow(gtx, th, ws, func(gtx layout.Context, col int) layout.Dimensions {
		return atoms.Label{Text: t.Columns[col].Title, Role: atoms.RoleSmall, MaxLines: 1}.Layout(gtx, th)
	}, true)
	lgtx := gtx
	lgtx.Constraints.Min = image.Pt(w, 0)
	lgtx.Constraints.Max.X = w
	if lgtx.Constraints.Max.Y < draw.Unbounded {
		lgtx.Constraints.Max.Y = max(0, lgtx.Constraints.Max.Y-header)
		lgtx.Constraints.Min.Y = max(0, gtx.Constraints.Min.Y-header)
	}
	off := op.Offset(image.Pt(0, header)).Push(gtx.Ops)
	d := t.list.Layout(lgtx, th, rows, func(gtx layout.Context, r int) layout.Dimensions {
		h := t.layoutRow(gtx, th, ws, func(gtx layout.Context, col int) layout.Dimensions { return cell(gtx, r, col) }, false)
		return layout.Dimensions{Size: image.Pt(w, h)}
	})
	off.Pop()
	return layout.Dimensions{Size: image.Pt(w, header+d.Size.Y)}
}

// layoutRow draws one row of cells and its bottom border; it returns the
// row height.
func (t *Table) layoutRow(gtx layout.Context, th *tokens.Theme, ws []int, cell func(gtx layout.Context, col int) layout.Dimensions, header bool) int {
	minH := gtx.Dp(th.Size.RowH)
	padX := gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)
	padY := gtx.Dp(th.Space.XXS)
	type rec struct {
		call op.CallOp
		size image.Point
	}
	recs := make([]rec, len(ws))
	h := minH
	for i, cw := range ws {
		cgtx := gtx
		inner := max(0, cw-2*padX)
		cgtx.Constraints = layout.Constraints{Min: image.Pt(inner, 0), Max: image.Pt(inner, draw.Unbounded)}
		m := op.Record(gtx.Ops)
		d := cell(cgtx, i)
		recs[i] = rec{m.Stop(), d.Size}
		h = max(h, d.Size.Y+2*padY)
	}
	w := 0
	for _, cw := range ws {
		w += cw
	}
	if header {
		draw.Rect(gtx.Ops, image.Rect(0, 0, w, h), th.Color.SurfaceAlt)
	}
	x := 0
	for i, r := range recs {
		t := op.Offset(image.Pt(x+padX, (h-r.size.Y)/2)).Push(gtx.Ops)
		r.call.Add(gtx.Ops)
		t.Pop()
		x += ws[i]
	}
	bw := max(1, gtx.Dp(th.Size.BorderWidth))
	draw.Rect(gtx.Ops, image.Rect(0, h-bw, w, h), th.Color.Border)
	return h
}
