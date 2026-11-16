package molecules

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// SectionHeader is the title bar of a panel section ("VARIABLES 12") with
// optional trailing actions. It is a value component and fills the width.
type SectionHeader struct {
	Title string
	// Badge is an optional count or state next to the title.
	Badge string
	// Trailing is drawn at the right end (e.g. a Toolbar).
	Trailing layout.Widget
}

// Layout draws the header.
func (s SectionHeader) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w := draw.Width(gtx, gtx.Dp(240))
	h := gtx.Dp(th.Size.ControlH)
	size := image.Pt(w, h)
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, h-max(1, gtx.Dp(th.Size.BorderWidth)), w, h), th.Color.Border)
	pad := gtx.Dp(th.Space.S)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Min: image.Pt(max(0, w-pad-gtx.Dp(th.Space.XXS)), 0), Max: image.Pt(max(0, w-pad-gtx.Dp(th.Space.XXS)), h)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
		layout.Rigid(atoms.Label{Text: strings.ToUpper(s.Title), Role: atoms.RoleSmall, MaxLines: 1}.Fn(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.Badge == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S - th.Space.XXS}.Layout(gtx, atoms.Badge{Text: s.Badge}.Fn(th))
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.Trailing == nil {
				return layout.Dimensions{}
			}
			return s.Trailing(gtx)
		}),
	)
	call := m.Stop()
	t := op.Offset(image.Pt(pad, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// Fn adapts the header to a layout.Widget.
func (s SectionHeader) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return s.Layout(gtx, th) }
}
