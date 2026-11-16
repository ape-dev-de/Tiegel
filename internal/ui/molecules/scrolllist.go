package molecules

import (
	"image"
	"math"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ScrollList is a virtualized vertical list with an overlay scrollbar.
// Only the visible elements are laid out, so it handles hundreds of
// thousands of rows (diffs, logs). It fills the maximum constraints; give
// it a bounded height.
//
// It is a stateful component; Layout takes the element count and the
// element function like layout.List.
type ScrollList struct {
	// List is the underlying Gio list (Axis is forced to vertical).
	List layout.List
	bar  widget.Scrollbar
}

// Position returns the scroll position of the last layout.
func (s *ScrollList) Position() layout.Position { return s.List.Position }

// ScrollTo scrolls so that element i is the first visible one.
func (s *ScrollList) ScrollTo(i int) { s.List.ScrollTo(max(0, i)) }

// FirstVisible returns the index of the first visible element.
func (s *ScrollList) FirstVisible() int { return s.List.Position.First }

// LastVisible returns the index of the last (partially) visible element,
// or -1 before the first layout.
func (s *ScrollList) LastVisible() int {
	p := s.List.Position
	if p.Count == 0 {
		return -1
	}
	return p.First + p.Count - 1
}

// Layout draws the list and its scrollbar.
func (s *ScrollList) Layout(gtx layout.Context, th *tokens.Theme, n int, el layout.ListElement) layout.Dimensions {
	s.List.Axis = layout.Vertical
	size := image.Pt(draw.Width(gtx, gtx.Dp(240)), gtx.Constraints.Max.Y)
	if size.Y >= draw.Unbounded {
		size.Y = max(gtx.Constraints.Min.Y, gtx.Dp(200))
	}
	lg := gtx
	lg.Constraints = layout.Exact(size)
	cl := clip.Rect{Max: size}.Push(gtx.Ops)
	s.List.Layout(lg, n, el)
	cl.Pop()

	if n > 0 {
		start, end := viewport(s.List.Position, n, size.Y)
		if start > 0 || end < 1 {
			s.layoutBar(gtx, th, size, start, end)
		}
		if d := s.bar.ScrollDistance(); d != 0 {
			s.List.ScrollBy(d * float32(n))
		}
	}
	return layout.Dimensions{Size: size}
}

func (s *ScrollList) layoutBar(gtx layout.Context, th *tokens.Theme, size image.Point, start, end float32) {
	bw := gtx.Dp(th.Space.S) + gtx.Dp(th.Space.XXS)
	track := image.Rect(size.X-bw, 0, size.X, size.Y)
	bg := gtx
	bg.Constraints = layout.Exact(track.Size())
	t := op.Offset(track.Min).Push(gtx.Ops)
	s.bar.Update(bg, layout.Vertical, start, end)

	area := clip.Rect{Max: track.Size()}.Push(gtx.Ops)
	s.bar.AddDrag(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area2 := clip.Rect{Max: track.Size()}.Push(gtx.Ops)
	s.bar.AddTrack(gtx.Ops)
	area2.Pop()
	pass.Pop()
	area.Pop()

	trackLen := track.Dy()
	y0 := int(math.Round(float64(start) * float64(trackLen)))
	y1 := int(math.Round(float64(end) * float64(trackLen)))
	minLen := gtx.Dp(th.Size.RowH)
	if y1-y0 < minLen {
		y1 = y0 + minLen
	}
	if y1 > trackLen {
		y0, y1 = max(0, trackLen-(y1-y0)), trackLen
	}
	inset := gtx.Dp(th.Space.XXS)
	ind := image.Rect(inset, y0, bw-inset, y1)
	col := tokens.WithAlpha(th.Color.TextMuted, 0x60)
	if s.bar.IndicatorHovered() || s.bar.Dragging() {
		col = tokens.WithAlpha(th.Color.TextMuted, 0xA0)
	}
	draw.RRect(gtx.Ops, ind, ind.Dx()/2, col)
	pass = pointer.PassOp{}.Push(gtx.Ops)
	ia := clip.Rect(ind).Push(gtx.Ops)
	s.bar.AddIndicator(gtx.Ops)
	ia.Pop()
	pass.Pop()
	t.Pop()
}

// viewport estimates the visible fraction [start, end] of the content
// from the list position (the same estimate Gio's material list uses).
func viewport(lp layout.Position, n, viewH int) (start, end float32) {
	if lp.Length <= 0 || n <= 0 {
		return 0, 1
	}
	length := float32(lp.Length)
	per := length / float32(n)
	start = clamp01((float32(lp.First)*per + float32(lp.Offset)) / length)
	end = clamp01((float32(lp.First+lp.Count)*per + float32(lp.OffsetLast)) / length)
	vis := float32(viewH) / length
	if frac := end - start; frac < 1 {
		e := vis - frac
		start -= start / (1 - frac) * e
		end += (1 - end) / (1 - frac) * e
	}
	return clamp01(start), clamp01(end)
}

func clamp01(v float32) float32 {
	return float32(math.Max(0, math.Min(1, float64(v))))
}
