package molecules

import (
	"image"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// SplitPane lays out two widgets side by side (Axis Horizontal) or stacked
// (Axis Vertical) with a draggable 1dp divider between them. It fills its
// maximum constraints.
type SplitPane struct {
	Axis layout.Axis
	// Ratio is the share of the first pane in (0, 1); zero means 0.5.
	Ratio float32
	// MinSize is the minimum size of either pane; zero means 48dp.
	MinSize unit.Dp

	drag    gesture.Drag
	dragOff float32 // pointer position relative to the divider at press
}

// handleDp is the width of the invisible drag handle around the divider.
const handleDp = unit.Dp(6)

// Dragging reports whether the divider is being dragged.
func (s *SplitPane) Dragging() bool { return s.drag.Dragging() }

func (s *SplitPane) ratio() float32 {
	if s.Ratio <= 0 || s.Ratio >= 1 {
		return 0.5
	}
	return s.Ratio
}

// Layout draws first and second.
func (s *SplitPane) Layout(gtx layout.Context, th *tokens.Theme, first, second layout.Widget) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded {
		size.X = gtx.Constraints.Min.X
	}
	if size.Y >= draw.Unbounded {
		size.Y = gtx.Constraints.Min.Y
	}
	total := size.X
	if s.Axis == layout.Vertical {
		total = size.Y
	}
	bar := max(1, gtx.Dp(th.Size.BorderWidth))
	minPx := gtx.Dp(s.MinSize)
	if s.MinSize == 0 {
		minPx = gtx.Dp(48)
	}
	minPx = min(minPx, max(0, (total-bar)/2))
	pos := s.split(total, bar, minPx)

	// Dragging.
	handle := gtx.Dp(handleDp)
	axis := gesture.Horizontal
	if s.Axis == layout.Vertical {
		axis = gesture.Vertical
	}
	for {
		ev, ok := s.drag.Update(gtx.Metric, gtx.Source, axis)
		if !ok {
			break
		}
		coord := ev.Position.X
		if s.Axis == layout.Vertical {
			coord = ev.Position.Y
		}
		switch ev.Kind {
		case pointer.Press:
			s.dragOff = coord - float32(pos)
		case pointer.Drag:
			// Positions are in the pane's coordinates.
			p := coord - s.dragOff
			if total > bar {
				s.Ratio = clampRatio(p/float32(total-bar), total, bar, minPx)
			}
			pos = s.split(total, bar, minPx)
		}
	}

	firstSize, secondSize := image.Pt(pos, size.Y), image.Pt(size.X, size.Y-pos-bar)
	barRect := image.Rect(pos, 0, pos+bar, size.Y)
	secondOff := image.Pt(0, pos+bar)
	handleRect := image.Rect(0, pos-handle/2+bar/2, size.X, pos-handle/2+bar/2+handle)
	cursor := pointer.CursorRowResize
	if s.Axis == layout.Horizontal {
		secondSize = image.Pt(size.X-pos-bar, size.Y)
		secondOff = image.Pt(pos+bar, 0)
		handleRect = image.Rect(pos-handle/2+bar/2, 0, pos-handle/2+bar/2+handle, size.Y)
		cursor = pointer.CursorColResize
	} else {
		firstSize = image.Pt(size.X, pos)
		barRect = image.Rect(0, pos, size.X, pos+bar)
	}

	pane := func(off, sz image.Point, w layout.Widget) {
		t := op.Offset(off).Push(gtx.Ops)
		cl := clip.Rect{Max: sz}.Push(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints = layout.Exact(sz)
		w(cgtx)
		cl.Pop()
		t.Pop()
	}
	pane(image.Point{}, firstSize, first)
	pane(secondOff, secondSize, second)

	col := th.Color.Border
	if s.drag.Dragging() {
		col = th.Color.Accent
	}
	draw.Rect(gtx.Ops, barRect, col)

	hs := clip.Rect(handleRect).Push(gtx.Ops)
	cursor.Add(gtx.Ops)
	s.drag.Add(gtx.Ops)
	hs.Pop()
	return layout.Dimensions{Size: size}
}

// split returns the pixel position of the divider.
func (s *SplitPane) split(total, bar, minPx int) int {
	avail := max(0, total-bar)
	p := int(s.ratio()*float32(avail) + 0.5)
	return max(min(p, avail-minPx), min(minPx, avail))
}

func clampRatio(r float32, total, bar, minPx int) float32 {
	avail := float32(max(1, total-bar))
	lo, hi := float32(minPx)/avail, 1-float32(minPx)/avail
	if hi < lo {
		return 0.5
	}
	return max(lo, min(hi, r))
}
