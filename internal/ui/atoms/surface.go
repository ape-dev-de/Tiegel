package atoms

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// SurfaceKind selects the background color of a Surface.
type SurfaceKind int

// Surface kinds.
const (
	// SurfacePanel is th.Color.Surface: panels, cards, inputs.
	SurfacePanel SurfaceKind = iota
	// SurfaceAlt is th.Color.SurfaceAlt: headers, toolbars.
	SurfaceAlt
	// SurfaceWindow is th.Color.Background: the window background.
	SurfaceWindow
	// SurfaceEditor is th.Editor.Background: code and diff areas.
	SurfaceEditor
	// SurfaceHover is th.Color.SurfaceHover: hovered rows and entries.
	SurfaceHover
)

// SurfaceKinds lists all kinds (for the catalog and tests).
var SurfaceKinds = []SurfaceKind{SurfacePanel, SurfaceAlt, SurfaceWindow, SurfaceEditor, SurfaceHover}

// String returns the kind name.
func (k SurfaceKind) String() string {
	switch k {
	case SurfaceAlt:
		return "alt"
	case SurfaceWindow:
		return "window"
	case SurfaceEditor:
		return "editor"
	case SurfaceHover:
		return "hover"
	}
	return "panel"
}

// Color returns the background color of k.
func (k SurfaceKind) Color(th *tokens.Theme) color.NRGBA {
	switch k {
	case SurfaceAlt:
		return th.Color.SurfaceAlt
	case SurfaceWindow:
		return th.Color.Background
	case SurfaceEditor:
		return th.Editor.Background
	case SurfaceHover:
		return th.Color.SurfaceHover
	}
	return th.Color.Surface
}

// Surface fills the area of its content with a themed background, with an
// optional border and rounded corners. It is a value component and a
// wrapper: Layout takes the content as last parameter. The content is laid
// out with the incoming constraints, so pass tight constraints to fill an
// area.
type Surface struct {
	Kind SurfaceKind
	// Border draws a 1dp outline in th.Color.Border.
	Border bool
	// Rounded uses th.Radius.L corners.
	Rounded bool
}

// Layout draws the background and then content on top.
func (s Surface) Layout(gtx layout.Context, th *tokens.Theme, content layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	d := content(gtx)
	call := m.Stop()
	r := image.Rectangle{Max: d.Size}
	rad := 0
	if s.Rounded {
		rad = gtx.Dp(th.Radius.L)
	}
	draw.RRect(gtx.Ops, r, rad, s.Kind.Color(th))
	call.Add(gtx.Ops)
	if s.Border {
		draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), th.Color.Border)
	}
	return d
}

// Paint fills exactly gtx.Constraints.Min with the background (no
// content), e.g. as the backdrop of a fixed-size area.
func (s Surface) Paint(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return s.Fill(gtx, th, func(layout.Context) layout.Dimensions { return layout.Dimensions{} })
}

// Fill is a convenience for Layout with content stretched to the minimum
// constraints: the surface covers at least gtx.Constraints.Min.
func (s Surface) Fill(gtx layout.Context, th *tokens.Theme, content layout.Widget) layout.Dimensions {
	return s.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		d := content(gtx)
		d.Size = gtx.Constraints.Constrain(d.Size)
		return d
	})
}
