package molecules

import (
	"image"
	"math"

	"gioui.org/gesture"
	"gioui.org/io/event"
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
	// Horizontal enables horizontal scrolling of wide rows (long paths):
	// elements are laid out with the view (or content) width as minimum
	// and an unbounded maximum (see Wide) and report their natural width;
	// the widest visible row sets the scroll range. A horizontal overlay
	// scrollbar, trackpad and Shift+wheel scroll the content.
	Horizontal bool

	bar   widget.Scrollbar
	hbar  widget.Scrollbar
	hgest gesture.Scroll
	// xoff is the horizontal scroll offset, contentW the widest row of
	// the last layout (pixels).
	xoff, contentW int
}

// Wide reports whether gtx lays out a row of a horizontally scrolling
// list: the maximum width is unbounded, the minimum is the width to fill.
// Rows then lay out their content at its natural width (no Flexed
// children, no truncation) and return max(Min.X, natural width).
// Insets and Flex children below the row keep the property (their maximum
// is the unbounded width minus a few pixels).
func Wide(gtx layout.Context) bool { return gtx.Constraints.Max.X >= draw.Unbounded/2 }

// XOffset returns the horizontal scroll offset in pixels.
func (s *ScrollList) XOffset() int { return s.xoff }

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
	if s.Horizontal {
		el = s.wideElement(gtx, size, el)
	}
	s.List.Layout(lg, n, el)
	cl.Pop()
	if s.Horizontal {
		s.layoutHorizontal(gtx, th, size)
	}

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

// wideElement wraps el for horizontal scrolling: the row gets the visible
// extent as minimum and no maximum, is shifted by the scroll offset and
// reports the view width to the list; the widest row is recorded.
func (s *ScrollList) wideElement(gtx layout.Context, size image.Point, el layout.ListElement) layout.ListElement {
	// The scroll handler sits on the list area, below the rows.
	maxX := max(0, s.contentW-size.X)
	if d := s.hgest.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Horizontal,
		pointer.ScrollRange{Min: -s.xoff, Max: maxX - s.xoff}, pointer.ScrollRange{}); d != 0 {
		s.xoff += d
	}
	s.xoff = min(max(0, s.xoff), maxX)
	event.Op(gtx.Ops, &s.hgest)
	s.contentW = 0
	// Rows fill at least the visible extent (selection backgrounds reach
	// the right edge); wider rows extend the content. Filling only what is
	// visible lets the range shrink again once wide rows go away.
	fill := size.X + s.xoff
	widest := 0
	return func(gtx layout.Context, i int) layout.Dimensions {
		gtx.Constraints.Min.X = fill
		gtx.Constraints.Max.X = draw.Unbounded
		t := op.Offset(image.Pt(-s.xoff, 0)).Push(gtx.Ops)
		d := el(gtx, i)
		t.Pop()
		widest = max(widest, d.Size.X)
		// The list reports the widest row seen in this frame; shrinking
		// rows take effect in the next frame.
		s.contentW = widest
		d.Size.X = size.X
		return d
	}
}

// layoutHorizontal draws the horizontal scrollbar and applies its drags.
func (s *ScrollList) layoutHorizontal(gtx layout.Context, th *tokens.Theme, size image.Point) {
	if s.contentW <= size.X {
		s.xoff = 0
		return
	}
	cw := float32(s.contentW)
	start := float32(s.xoff) / cw
	end := float32(s.xoff+size.X) / cw
	s.layoutTrack(gtx, th, &s.hbar, layout.Horizontal, size, start, end)
	if d := s.hbar.ScrollDistance(); d != 0 {
		s.xoff = min(max(0, s.xoff+int(math.Round(float64(d*cw)))), s.contentW-size.X)
		gtx.Execute(op.InvalidateCmd{})
	}
}

func (s *ScrollList) layoutBar(gtx layout.Context, th *tokens.Theme, size image.Point, start, end float32) {
	s.layoutTrack(gtx, th, &s.bar, layout.Vertical, size, start, end)
}

// layoutTrack draws an overlay scrollbar along the right (vertical) or
// bottom (horizontal) edge of size.
func (s *ScrollList) layoutTrack(gtx layout.Context, th *tokens.Theme, bar *widget.Scrollbar, axis layout.Axis, size image.Point, start, end float32) {
	bw := gtx.Dp(th.Space.S) + gtx.Dp(th.Space.XXS)
	track := image.Rect(size.X-bw, 0, size.X, size.Y)
	if axis == layout.Horizontal {
		// Leave the corner to the vertical bar.
		track = image.Rect(0, size.Y-bw, size.X-bw, size.Y)
	}
	bg := gtx
	bg.Constraints = layout.Exact(track.Size())
	t := op.Offset(track.Min).Push(gtx.Ops)
	bar.Update(bg, axis, start, end)

	area := clip.Rect{Max: track.Size()}.Push(gtx.Ops)
	bar.AddDrag(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area2 := clip.Rect{Max: track.Size()}.Push(gtx.Ops)
	bar.AddTrack(gtx.Ops)
	area2.Pop()
	pass.Pop()
	area.Pop()

	trackLen := track.Dy()
	if axis == layout.Horizontal {
		trackLen = track.Dx()
	}
	p0 := int(math.Round(float64(start) * float64(trackLen)))
	p1 := int(math.Round(float64(end) * float64(trackLen)))
	minLen := gtx.Dp(th.Size.RowH)
	if p1-p0 < minLen {
		p1 = p0 + minLen
	}
	if p1 > trackLen {
		p0, p1 = max(0, trackLen-(p1-p0)), trackLen
	}
	inset := gtx.Dp(th.Space.XXS)
	ind := image.Rect(inset, p0, bw-inset, p1)
	if axis == layout.Horizontal {
		ind = image.Rect(p0, inset, p1, bw-inset)
	}
	col := tokens.WithAlpha(th.Color.TextMuted, 0x60)
	if bar.IndicatorHovered() || bar.Dragging() {
		col = tokens.WithAlpha(th.Color.TextMuted, 0xA0)
	}
	draw.RRect(gtx.Ops, ind, min(ind.Dx(), ind.Dy())/2, col)
	pass = pointer.PassOp{}.Push(gtx.Ops)
	ia := clip.Rect(ind).Push(gtx.Ops)
	bar.AddIndicator(gtx.Ops)
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
