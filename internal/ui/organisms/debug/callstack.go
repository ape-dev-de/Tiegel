package debug

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Frame is one call stack entry.
type Frame struct {
	Level int    // 0 is the current frame
	Where string // function, e.g. "App\\Mailer->send" or "{main}"
	File  string // host path
	Line  int
}

// CallStackList shows the call stack; clicking a frame selects it.
type CallStackList struct {
	Frames []Frame
	// Selected is the level of the selected frame.
	Selected int
	// Empty is shown when there are no frames.
	Empty string

	rows []molecules.TreeRow
	list molecules.ScrollList
}

func (c *CallStackList) sync() {
	if len(c.rows) != len(c.Frames) {
		c.rows = make([]molecules.TreeRow, len(c.Frames))
	}
}

// Update returns a FrameSelectEvent when a frame was clicked.
func (c *CallStackList) Update(gtx layout.Context) []Event {
	c.sync()
	var out []Event
	for i := range c.rows {
		if clicked, _, _ := c.rows[i].Update(gtx); clicked {
			f := c.Frames[i]
			c.Selected = f.Level
			out = append(out, FrameSelectEvent{Level: f.Level, File: f.File, Line: f.Line})
		}
	}
	return out
}

// Layout draws the list; it fills the available space.
func (c *CallStackList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	c.Update(gtx)
	if len(c.Frames) == 0 {
		return emptyState(gtx, th, c.Empty, "Not paused")
	}
	return c.list.Layout(gtx, th, len(c.Frames), func(gtx layout.Context, i int) layout.Dimensions {
		f := c.Frames[i]
		r := &c.rows[i]
		r.Name, r.Detail = f.Where, location(f.File, f.Line)
		r.Selected = f.Level == c.Selected
		r.Glyph, r.GlyphColor = nil, th.Color.TextMuted
		if f.Level == 0 {
			r.Glyph, r.GlyphColor = atoms.GlyphChevronRight, th.Color.Accent
		}
		return r.Layout(gtx, th)
	})
}

// emptyState draws a muted single-line placeholder (ellipsis when it
// does not fit) centered in the available space.
func emptyState(gtx layout.Context, th *tokens.Theme, text, fallback string) layout.Dimensions {
	if text == "" {
		text = fallback
	}
	size := gtx.Constraints.Max
	if size.Y > unbounded {
		size.Y = gtx.Dp(th.Size.RowH) * 2
	}
	if size.X > unbounded {
		size.X = gtx.Constraints.Min.X
	}
	pad := gtx.Dp(th.Space.M)
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, size.X-2*pad), size.Y)}
	m := op.Record(gtx.Ops)
	d := atoms.Muted(text).SingleLine().Layout(cg, th)
	call := m.Stop()
	// Centered horizontally; vertically centered in short areas, a
	// little below the top in tall ones (the eye starts there).
	y := min((size.Y-d.Size.Y)/2, 4*pad)
	t := op.Offset(image.Pt(max(pad, (size.X-d.Size.X)/2), max(0, y))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// unbounded is the threshold above which a constraint counts as unbounded
// (layout.List passes huge maxima on its main axis).
const unbounded = 1 << 20
