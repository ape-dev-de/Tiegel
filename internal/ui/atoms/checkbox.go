package atoms

import (
	"image"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// CheckState is the tri-state value of a Checkbox.
type CheckState int

// Checkbox states. Indeterminate means "partially checked", e.g. a file
// whose hunks are only partly staged in the commit dialog.
const (
	Unchecked CheckState = iota
	Checked
	Indeterminate
)

// String returns the state name.
func (s CheckState) String() string {
	switch s {
	case Checked:
		return "checked"
	case Indeterminate:
		return "indeterminate"
	}
	return "unchecked"
}

// Next returns the state after a user click: unchecked ↔ checked, and an
// indeterminate box becomes checked.
func (s CheckState) Next() CheckState {
	if s == Checked {
		return Unchecked
	}
	return Checked
}

// Checkbox is a stateful tri-state checkbox with an optional label. The
// user can only toggle between checked and unchecked; Indeterminate is set
// by the program (e.g. derived from child states).
type Checkbox struct {
	State    CheckState
	Label    string
	Disabled bool

	click widget.Clickable
}

// NewCheckbox returns a checkbox with label and initial state.
func NewCheckbox(label string, state CheckState) *Checkbox {
	return &Checkbox{Label: label, State: state}
}

// Checked reports whether the state is Checked.
func (c *Checkbox) Checked() bool { return c.State == Checked }

// Update processes clicks and reports whether the state changed. Layout
// calls Update too; call it first if you need to react before drawing.
func (c *Checkbox) Update(gtx layout.Context) bool {
	if c.Disabled {
		return false
	}
	changed := false
	for c.click.Clicked(gtx) {
		c.State = c.State.Next()
		changed = true
	}
	return changed
}

// Layout draws the box and its label.
func (c *Checkbox) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	c.Update(gtx)
	if c.Disabled {
		gtx = gtx.Disabled()
	}
	return c.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.layoutBox(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.Label == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.S}.Layout(gtx, Text(c.Label).SingleLine().Fn(th))
			}),
		)
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		semantic.CheckBox.Add(gtx.Ops)
		semantic.SelectedOp(c.State == Checked).Add(gtx.Ops)
		if c.Label != "" {
			semantic.LabelOp(c.Label).Add(gtx.Ops)
		}
		return d
	})
}

func (c *Checkbox) layoutBox(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	px := gtx.Dp(th.Size.Checkbox)
	// The hit area is a full row tall so small boxes stay easy to hit.
	outer := image.Pt(px, max(px, gtx.Dp(th.Size.RowH)-gtx.Dp(th.Space.S)))
	off := image.Pt(0, (outer.Y-px)/2)
	r := image.Rectangle{Min: off, Max: off.Add(image.Pt(px, px))}
	rad := gtx.Dp(th.Radius.S)
	col := th.Color
	on := c.State != Unchecked
	hover := c.click.Hovered() && !c.Disabled

	switch {
	case c.Disabled && on:
		draw.RRect(gtx.Ops, r, rad, col.TextDisabled)
	case c.Disabled:
		draw.RRect(gtx.Ops, r, rad, col.SurfaceAlt)
		draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), col.Border)
	case on:
		fill := col.Accent
		if hover {
			fill = col.AccentHover
		}
		draw.RRect(gtx.Ops, r, rad, fill)
	default:
		draw.RRect(gtx.Ops, r, rad, col.Surface)
		border := col.BorderStrong
		if hover {
			border = col.Accent
		}
		draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), border)
	}
	if gtx.Focused(&c.click) {
		ring := r.Inset(-gtx.Dp(2))
		draw.Border(gtx.Ops, ring, rad+gtx.Dp(2), gtx.Dp(1.5), col.Focus)
	}

	mark := col.OnAccent
	if c.Disabled {
		mark = col.Surface
	}
	switch c.State {
	case Checked:
		cgtx := gtx
		cgtx.Constraints = layout.Exact(image.Pt(px, px))
		t := opOffset(gtx, off)
		ic := Icon{Glyph: GlyphCheck, Size: th.Size.Checkbox, Color: mark}
		ic.Layout(cgtx, th)
		t.Pop()
	case Indeterminate:
		w := px * 9 / 16
		h := max(2, gtx.Dp(2))
		cx, cy := r.Min.X+px/2, r.Min.Y+px/2
		draw.RRect(gtx.Ops, image.Rect(cx-w/2, cy-h/2, cx-w/2+w, cy-h/2+h), h/2, mark)
	}
	return layout.Dimensions{Size: outer, Baseline: outer.Y / 4}
}
