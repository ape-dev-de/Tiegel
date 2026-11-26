package molecules

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ListRow is a clickable, selectable row for lists (commit log, branch
// list, change lists). It paints the hover and selection background and
// lays out arbitrary content, vertically centered, with a horizontal
// padding. The row fills the available width and is at least
// th.Size.RowH tall.
//
// It is a stateful wrapper component: keep it in your state (one per
// row) and pass the content to Layout.
//
// Gio delivers pointer events to nested handlers as well: a click on a
// control inside the content (checkbox, arrow) is also reported by the
// row. Process the inner controls first and ignore the row click when one
// of them reacted.
type ListRow struct {
	Selected bool
	// Indent is extra space before the content (tree levels).
	Indent unit.Dp

	click widget.Clickable
}

// Update reports clicks since the last frame: clicked for any click and
// double for a double click (clicked is true as well).
func (r *ListRow) Update(gtx layout.Context) (clicked, double bool) {
	clicked, double, _ = r.UpdateMods(gtx)
	return clicked, double
}

// UpdateMods is Update that also returns the modifiers held at the last
// click (e.g. Shift+click for an alternative action).
func (r *ListRow) UpdateMods(gtx layout.Context) (clicked, double bool, mods key.Modifiers) {
	for {
		c, ok := r.click.Update(gtx)
		if !ok {
			break
		}
		clicked = true
		mods = c.Modifiers
		if c.NumClicks >= 2 {
			double = true
		}
	}
	return clicked, double, mods
}

// Hovered reports whether the pointer is over the row.
func (r *ListRow) Hovered() bool { return r.click.Hovered() }

// Layout draws the row. Content is laid out with the row's inner width as
// maximum and no minimum; in a horizontally scrolling list (Wide) the
// maximum is unbounded and the row grows to the content's width. Call Update before Layout to observe clicks;
// Layout does not consume them.
func (r *ListRow) Layout(gtx layout.Context, th *tokens.Theme, content layout.Widget) layout.Dimensions {
	return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		wide := Wide(gtx)
		w := draw.Width(gtx, gtx.Dp(240))
		if wide {
			w = gtx.Constraints.Min.X
		}
		padL := gtx.Dp(th.Space.S) + gtx.Dp(r.Indent)
		padR := gtx.Dp(th.Space.S)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-padL-padR), draw.Unbounded)}
		if wide {
			cgtx.Constraints.Max.X = draw.Unbounded
		}
		m := op.Record(gtx.Ops)
		d := content(cgtx)
		call := m.Stop()
		if wide {
			w = max(w, padL+d.Size.X+padR)
		}
		h := max(gtx.Dp(th.Size.RowH), d.Size.Y)
		size := image.Pt(w, h)
		switch {
		case r.Selected:
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Selection)
		case r.click.Hovered():
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceHover)
		}
		t := op.Offset(image.Pt(padL, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: size, Baseline: h - (h-d.Size.Y)/2 - d.Size.Y + d.Baseline}
	})
}
