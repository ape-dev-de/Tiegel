package molecules

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BreakpointRow is one entry of the breakpoint list: enable checkbox,
// breakpoint marker, location ("File.php:42") and an optional condition.
type BreakpointRow struct {
	// Enabled is the checkbox; its State is the enabled flag.
	Enabled atoms.Checkbox
	File    string
	Line    int
	// Condition is a PHP expression; empty for unconditional breakpoints.
	Condition string
	Selected  bool

	row widget.Clickable
}

// NewBreakpointRow returns an enabled breakpoint row.
func NewBreakpointRow(file string, line int, condition string) *BreakpointRow {
	b := &BreakpointRow{File: file, Line: line, Condition: condition}
	b.Enabled.State = atoms.Checked
	return b
}

// IsEnabled reports whether the breakpoint is enabled.
func (b *BreakpointRow) IsEnabled() bool { return b.Enabled.State == atoms.Checked }

// Location returns "file:line".
func (b *BreakpointRow) Location() string { return fmt.Sprintf("%s:%d", b.File, b.Line) }

// Update processes input: clicked reports a click on the row, toggled a
// change of the enabled checkbox.
func (b *BreakpointRow) Update(gtx layout.Context) (clicked, toggled bool) {
	toggled = b.Enabled.Update(gtx)
	for b.row.Clicked(gtx) {
		clicked = true
	}
	return clicked, toggled
}

// Layout draws the row across the available width.
func (b *BreakpointRow) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.Update(gtx)
	return b.row.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(th.Size.RowH)
		w := draw.Width(gtx, gtx.Dp(320))
		size := image.Pt(w, h)
		switch {
		case b.Selected:
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Selection)
		case b.row.Hovered():
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceHover)
		}
		padX := gtx.Dp(th.Space.S)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*padX), h)}
		enabled := b.IsEnabled()
		m := op.Record(gtx.Ops)
		d := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return b.Enabled.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return breakpointMarker(gtx, th, enabled, b.Condition != "")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := atoms.Text(b.Location()).SingleLine()
				if !enabled {
					l.Role = atoms.RoleMuted
				}
				return l.Layout(gtx, th)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0
				if b.Condition == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.M}.Layout(gtx,
					atoms.Label{Text: b.Condition, Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Fn(th))
			}),
		)
		call := m.Stop()
		t := op.Offset(image.Pt(padX, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return layout.Dimensions{Size: size}
	})
}

// breakpointMarker draws the gutter-style marker: a filled circle when
// enabled, a ring when disabled, with a small bar for conditional ones.
func breakpointMarker(gtx layout.Context, th *tokens.Theme, enabled, conditional bool) layout.Dimensions {
	px := gtx.Dp(th.Size.IconSmall)
	c := px / 2
	rad := px * 5 / 12
	col := th.Editor.Breakpoint
	if enabled {
		draw.Circle(gtx.Ops, c, c, rad, col)
	} else {
		draw.Ring(gtx.Ops, c, c, rad-1, max(1, gtx.Dp(1.5)), col)
	}
	if conditional {
		fg := col
		if enabled {
			fg = th.Color.Background
		}
		w, hh := rad, max(1, gtx.Dp(1.5))
		draw.Rect(gtx.Ops, image.Rect(c-w/2, c-hh/2, c-w/2+w, c-hh/2+hh), fg)
	}
	return layout.Dimensions{Size: image.Pt(px, px)}
}
