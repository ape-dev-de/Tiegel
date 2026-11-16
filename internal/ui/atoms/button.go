package atoms

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ButtonKind selects the visual weight of a button.
type ButtonKind int

// Button kinds.
const (
	// Primary is the single main action of a view (filled accent).
	Primary ButtonKind = iota
	// Secondary is an outlined button for other actions.
	Secondary
	// Ghost is a borderless button for toolbars and low-emphasis actions.
	Ghost
)

// String returns the kind name.
func (k ButtonKind) String() string {
	switch k {
	case Secondary:
		return "secondary"
	case Ghost:
		return "ghost"
	}
	return "primary"
}

// Button is a stateful text button with an optional leading glyph. Keep it
// in your widget state (not re-created per frame) and query Clicked before
// or after Layout.
type Button struct {
	Text     string
	Kind     ButtonKind
	Glyph    *Glyph // optional leading icon
	Disabled bool

	click widget.Clickable
}

// NewButton returns a button of kind with text.
func NewButton(kind ButtonKind, text string) *Button {
	return &Button{Kind: kind, Text: text}
}

// Clicked reports whether the button was clicked since the last call.
func (b *Button) Clicked(gtx layout.Context) bool {
	if b.Disabled {
		return false
	}
	return b.click.Clicked(gtx)
}

// Hovered reports whether the pointer is over the button.
func (b *Button) Hovered() bool { return b.click.Hovered() }

// Layout draws the button. Its width is the content width plus padding,
// at least the minimum constraint.
func (b *Button) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if b.Disabled {
		gtx = gtx.Disabled()
	}
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg, fg, border := b.colors(th)
		h := gtx.Dp(th.Size.ControlH)
		padX := gtx.Dp(th.Space.M)
		if b.Text == "" {
			padX = gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)
		}

		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		m := op.Record(gtx.Ops)
		content := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if b.Glyph == nil {
					return layout.Dimensions{}
				}
				return Icon{Glyph: b.Glyph, Color: fg}.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if b.Glyph == nil || b.Text == "" {
					return layout.Dimensions{}
				}
				return layout.Spacer{Width: th.Space.XS + th.Space.XXS}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if b.Text == "" {
					return layout.Dimensions{}
				}
				l := Label{Text: b.Text, Role: RoleBody, Color: fg, MaxLines: 1}
				if b.Kind == Primary {
					l.Role = RoleStrong
				}
				return l.Layout(gtx, th)
			}),
		)
		call := m.Stop()

		size := image.Pt(content.Size.X+2*padX, max(h, content.Size.Y))
		size.X = max(size.X, size.Y) // never narrower than tall
		size = gtx.Constraints.Constrain(size)
		r := image.Rectangle{Max: size}
		rad := gtx.Dp(th.Radius.M)
		draw.RRect(gtx.Ops, r, rad, bg)
		draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), border)
		if gtx.Focused(&b.click) {
			draw.Border(gtx.Ops, r, rad, gtx.Dp(2), th.Color.Focus)
		}
		draw.Centered(gtx.Ops, size, content.Size, call)
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		semantic.Button.Add(gtx.Ops)
		semantic.LabelOp(b.Text).Add(gtx.Ops)
		return layout.Dimensions{Size: size, Baseline: (size.Y-content.Size.Y)/2 + content.Baseline}
	})
}

func (b *Button) colors(th *tokens.Theme) (bg, fg, border color.NRGBA) {
	c := th.Color
	hover := b.click.Hovered() && !b.Disabled
	pressed := b.click.Pressed() && !b.Disabled
	switch b.Kind {
	case Primary:
		bg, fg = c.Accent, c.OnAccent
		if hover || pressed {
			bg = c.AccentHover
		}
		if b.Disabled {
			bg, fg = c.SurfaceAlt, c.TextDisabled
		}
	case Secondary:
		bg, fg, border = c.Surface, c.Text, c.BorderStrong
		if hover {
			bg = c.SurfaceHover
		}
		if pressed {
			border = c.Accent
		}
		if b.Disabled {
			bg, fg, border = c.Surface, c.TextDisabled, c.Border
		}
	default:
		fg = c.Text
		if hover {
			bg = c.SurfaceHover
		}
		if pressed {
			bg = c.Border
		}
		if b.Disabled {
			fg = c.TextDisabled
		}
	}
	return bg, fg, border
}

// IconButton is a square, borderless button showing only a glyph – for
// toolbars, tab close buttons and inline actions.
type IconButton struct {
	Glyph *Glyph
	// Size is the square's edge; defaults to th.Size.ControlH.
	Size unit.Dp
	// IconSize defaults to th.Size.Icon.
	IconSize unit.Dp
	// Color of the glyph; defaults to th.Color.TextMuted.
	Color    color.NRGBA
	Disabled bool
	// Active draws the button as a pressed toggle (accent tint and glyph),
	// e.g. "listening" or "breakpoints muted".
	Active bool
	// Description is the accessible name (and tooltip text for callers).
	Description string

	click widget.Clickable
}

// NewIconButton returns an icon button for g.
func NewIconButton(g *Glyph, description string) *IconButton {
	return &IconButton{Glyph: g, Description: description}
}

// Clicked reports whether the button was clicked since the last call.
func (b *IconButton) Clicked(gtx layout.Context) bool {
	if b.Disabled {
		return false
	}
	return b.click.Clicked(gtx)
}

// Hovered reports whether the pointer is over the button.
func (b *IconButton) Hovered() bool { return b.click.Hovered() }

// Layout draws the button.
func (b *IconButton) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if b.Disabled {
		gtx = gtx.Disabled()
	}
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		s := b.Size
		if s == 0 {
			s = th.Size.ControlH
		}
		px := gtx.Dp(s)
		size := gtx.Constraints.Constrain(image.Pt(px, px))
		r := image.Rectangle{Max: size}
		rad := gtx.Dp(th.Radius.S)
		col := b.Color
		if col.A == 0 {
			col = th.Color.TextMuted
		}
		if b.Active {
			draw.RRect(gtx.Ops, r, rad, tokens.Over(tokens.WithAlpha(th.Color.Accent, 0x38), th.Color.Surface))
			col = th.Color.Accent
		}
		if !b.Disabled && !b.Active && b.click.Hovered() {
			draw.RRect(gtx.Ops, r, rad, th.Color.SurfaceHover)
			col = th.Color.Text
		}
		if b.Disabled {
			col = th.Color.TextDisabled
		}
		if !b.Disabled && b.click.Pressed() {
			draw.RRect(gtx.Ops, r, rad, th.Color.Border)
		}
		if gtx.Focused(&b.click) {
			draw.Border(gtx.Ops, r, rad, gtx.Dp(2), th.Color.Focus)
		}
		m := op.Record(gtx.Ops)
		d := Icon{Glyph: b.Glyph, Size: b.IconSize, Color: col}.Layout(gtx, th)
		draw.Centered(gtx.Ops, size, d.Size, m.Stop())
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		if b.Description != "" {
			semantic.DescriptionOp(b.Description).Add(gtx.Ops)
		}
		semantic.SelectedOp(b.Active).Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}
