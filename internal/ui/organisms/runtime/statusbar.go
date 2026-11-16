package runtime

import (
	"image"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// StatusbarVersion is the status bar entry showing the effective PHP
// version and interpreter ("PHP 8.3 · compose:php"). Hovering shows where
// the version comes from; clicking asks the host to open the override.
type StatusbarVersion struct {
	// Version is the effective PHP version, e.g. "8.3".
	Version string
	// Interpreter is a short interpreter label, e.g. "compose:php".
	Interpreter string
	// Source explains the version (tooltip), e.g. "composer.json:
	// require php ^8.3".
	Source string
	// Override marks a version set manually by the user.
	Override bool
	// Hint explains why the version is only an estimate (e.g. the compose
	// container is stopped); it is shown in the tooltip and as a badge.
	Hint string
	// HintBadge is the short badge text of Hint (default "hint").
	HintBadge string

	click widget.Clickable
	tip   atoms.Tooltip
}

// Text returns the label.
func (s *StatusbarVersion) Text() string {
	v := "PHP ?"
	if s.Version != "" {
		v = "PHP " + s.Version
	}
	if s.Interpreter != "" {
		v += " · " + s.Interpreter
	}
	return v
}

// Tooltip returns the hover text.
func (s *StatusbarVersion) Tooltip() string {
	var b strings.Builder
	if s.Source != "" {
		b.WriteString(s.Source)
	} else {
		b.WriteString("PHP version unknown")
	}
	if s.Override {
		b.WriteString(" (manual override)")
	}
	b.WriteString(" – click to change")
	if s.Hint != "" {
		b.WriteString("\n" + s.Hint)
	}
	return b.String()
}

// Update returns an OverrideEvent when clicked.
func (s *StatusbarVersion) Update(gtx layout.Context) []Event {
	var out []Event
	for s.click.Clicked(gtx) {
		out = append(out, OverrideEvent{})
	}
	return out
}

// Layout draws the widget at its intrinsic size (one status bar row).
func (s *StatusbarVersion) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s.Update(gtx)
	s.tip.Text = s.Tooltip()
	s.tip.Above = true // the status bar sits at the window bottom
	gtx.Constraints.Min = image.Point{}
	return s.tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return s.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			h := gtx.Dp(th.Size.RowH)
			pad := gtx.Dp(th.Space.S) - gtx.Dp(th.Space.XXS)
			m := op.Record(gtx.Ops)
			d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Small(s.Text()).WithColor(th.Color.Text).Fn(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !s.Override {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: "override", Kind: atoms.BadgeAccent}.Fn(th))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if s.Hint == "" {
						return layout.Dimensions{}
					}
					text := s.HintBadge
					if text == "" {
						text = "hint"
					}
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: text, Kind: atoms.BadgeWarning}.Fn(th))
				}),
			)
			call := m.Stop()
			size := gtx.Constraints.Constrain(image.Pt(d.Size.X+2*pad, max(h, d.Size.Y)))
			if s.click.Hovered() {
				atoms.Surface{Kind: atoms.SurfaceHover}.Paint(withExact(gtx, size), th)
			}
			t := op.Offset(image.Pt(pad, (size.Y-d.Size.Y)/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			t.Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp(s.Text()).Add(gtx.Ops)
			return layout.Dimensions{Size: size}
		})
	})
}
