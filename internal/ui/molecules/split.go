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

// Split divides its area into two panes separated by a draggable 1dp
// divider: side by side (default) or stacked (Vertical). Ratio is the
// share of the first pane (0…1, default 0.5); dragging the divider changes
// it. MinPane keeps both panes at least that many dp wide/tall.
//
// It is a stateful wrapper component: Layout takes both panes. Each pane
// gets tight constraints. Split fills its maximum constraints.
type Split struct {
	Ratio    float32
	Vertical bool
	// MinPane is the minimum size of each pane in dp (default 80).
	MinPane unit.Dp

	drag    gesture.Drag
	dragPos float32
	start   float32
}

// Layout draws both panes.
func (s *Split) Layout(gtx layout.Context, th *tokens.Theme, first, second layout.Widget) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded {
		size.X = gtx.Constraints.Min.X
	}
	if size.Y >= draw.Unbounded {
		size.Y = gtx.Constraints.Min.Y
	}
	if s.Ratio <= 0 || s.Ratio >= 1 {
		s.Ratio = 0.5
	}
	total := size.X
	if s.Vertical {
		total = size.Y
	}
	bar := max(1, gtx.Dp(th.Size.BorderWidth))
	grip := gtx.Dp(th.Space.S)
	s.update(gtx, total)

	minPane := s.MinPane
	if minPane <= 0 {
		minPane = 80
	}
	minPx := min(gtx.Dp(minPane), max(0, (total-bar)/2))
	firstPx := int(float32(total-bar) * s.Ratio)
	firstPx = max(minPx, min(firstPx, total-bar-minPx))
	secondPx := max(0, total-bar-firstPx)

	pane := func(off image.Point, sz image.Point, w layout.Widget) {
		pg := gtx
		pg.Constraints = layout.Exact(sz)
		t := op.Offset(off).Push(gtx.Ops)
		c := clip.Rect{Max: sz}.Push(gtx.Ops)
		w(pg)
		c.Pop()
		t.Pop()
	}
	var barRect, gripRect image.Rectangle
	if s.Vertical {
		pane(image.Point{}, image.Pt(size.X, firstPx), first)
		pane(image.Pt(0, firstPx+bar), image.Pt(size.X, secondPx), second)
		barRect = image.Rect(0, firstPx, size.X, firstPx+bar)
		gripRect = image.Rect(0, firstPx-grip/2, size.X, firstPx+bar+grip/2)
	} else {
		pane(image.Point{}, image.Pt(firstPx, size.Y), first)
		pane(image.Pt(firstPx+bar, 0), image.Pt(secondPx, size.Y), second)
		barRect = image.Rect(firstPx, 0, firstPx+bar, size.Y)
		gripRect = image.Rect(firstPx-grip/2, 0, firstPx+bar+grip/2, size.Y)
	}
	col := th.Color.Border
	if s.drag.Dragging() {
		col = th.Color.Accent
	}
	draw.Rect(gtx.Ops, barRect, col)

	c := clip.Rect(gripRect).Push(gtx.Ops)
	if s.Vertical {
		pointer.CursorRowResize.Add(gtx.Ops)
	} else {
		pointer.CursorColResize.Add(gtx.Ops)
	}
	s.drag.Add(gtx.Ops)
	c.Pop()
	return layout.Dimensions{Size: size}
}

func (s *Split) update(gtx layout.Context, total int) {
	axis := gesture.Horizontal
	if s.Vertical {
		axis = gesture.Vertical
	}
	for {
		e, ok := s.drag.Update(gtx.Metric, gtx.Source, axis)
		if !ok {
			break
		}
		pos := e.Position.X
		if s.Vertical {
			pos = e.Position.Y
		}
		switch e.Kind {
		case pointer.Press:
			s.dragPos, s.start = pos, s.Ratio
		case pointer.Drag:
			// Positions are relative to the Split origin (the grip area is
			// registered without an offset).
			if total > 0 {
				s.Ratio = s.start + (pos-s.dragPos)/float32(total)
				s.Ratio = max(0.05, min(0.95, s.Ratio))
			}
		}
	}
}
