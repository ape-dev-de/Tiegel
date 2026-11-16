package runtime

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Severity of a hint.
type Severity int

// Hint severities.
const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

// String returns the severity name.
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	}
	return "info"
}

// Hint is a diagnostic about the Xdebug setup.
type Hint struct {
	Severity Severity
	// Code identifies the hint (stable, e.g. "no-host-gateway").
	Code    string
	Message string
}

// XdebugHintsList shows setup hints with a severity icon; messages wrap.
// An empty list shows a success message.
type XdebugHintsList struct {
	Items []Hint
	// Empty is shown when there are no hints.
	Empty string

	rows []widget.Clickable
	list molecules.ScrollList
}

// Update returns a HintSelectEvent for a clicked hint.
func (l *XdebugHintsList) Update(gtx layout.Context) []Event {
	if len(l.rows) != len(l.Items) {
		l.rows = make([]widget.Clickable, len(l.Items))
	}
	var out []Event
	for i := range l.rows {
		for l.rows[i].Clicked(gtx) {
			out = append(out, HintSelectEvent{Code: l.Items[i].Code})
		}
	}
	return out
}

func severityStyle(th *tokens.Theme, s Severity) (*atoms.Glyph, color.NRGBA) {
	switch s {
	case SeverityWarning:
		return atoms.GlyphWarning, th.Color.Warning
	case SeverityError:
		return atoms.GlyphError, th.Color.Danger
	}
	return atoms.GlyphInfo, th.Color.Info
}

// Layout draws the list; it fills the available space.
func (l *XdebugHintsList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	l.Update(gtx)
	if len(l.Items) == 0 {
		text := l.Empty
		if text == "" {
			text = "No problems found in the Xdebug setup."
		}
		gtx.Constraints.Min.Y = 0
		return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Icon{Glyph: atoms.GlyphDone, Color: th.Color.Success}.Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, atoms.Muted(text).Fn(th)),
			)
		})
	}
	return l.list.Layout(gtx, th, len(l.Items), func(gtx layout.Context, i int) layout.Dimensions {
		it := l.Items[i]
		return l.rows[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			pad := gtx.Dp(th.Space.S)
			g, col := severityStyle(th, it.Severity)
			cgtx := gtx
			cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad), unbounded)}
			m := op.Record(gtx.Ops)
			d := layout.Flex{Alignment: layout.Start}.Layout(cgtx,
				layout.Rigid(atoms.Icon{Glyph: g, Color: col}.Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = 0
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(atoms.Text(it.Message).Fn(th)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if it.Code == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Top: th.Space.XXS}.Layout(gtx, atoms.Small(it.Code).Fn(th))
						}),
					)
				}),
			)
			call := m.Stop()
			size := image.Pt(w, d.Size.Y+2*pad)
			if l.rows[i].Hovered() {
				atoms.Surface{Color: th.Color.SurfaceHover}.Layout(withExact(gtx, size), th)
			}
			t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			t.Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: size}
		})
	})
}

func withExact(gtx layout.Context, size image.Point) layout.Context {
	gtx.Constraints = layout.Exact(size)
	return gtx
}
