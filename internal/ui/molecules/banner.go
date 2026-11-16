package molecules

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BannerKind selects the color and glyph of a Banner.
type BannerKind int

// Banner kinds.
const (
	BannerInfo BannerKind = iota
	BannerSuccess
	BannerWarning
	BannerError
)

// BannerKinds lists all kinds (for the catalog and tests).
var BannerKinds = []BannerKind{BannerInfo, BannerSuccess, BannerWarning, BannerError}

// String returns the kind name.
func (k BannerKind) String() string {
	return [...]string{"info", "success", "warning", "error"}[k]
}

func (k BannerKind) style(th *tokens.Theme) (color.NRGBA, *atoms.Glyph) {
	switch k {
	case BannerSuccess:
		return th.Color.Success, atoms.GlyphDone
	case BannerWarning:
		return th.Color.Warning, atoms.GlyphWarning
	case BannerError:
		return th.Color.Danger, atoms.GlyphError
	}
	return th.Color.Info, atoms.GlyphInfo
}

// Banner is an inline message box: a tinted strip with a glyph, a bold
// title and an optional muted detail text (wrapped). Use it for errors
// and notices inside panels. It is a value component and fills the
// available width.
type Banner struct {
	Kind   BannerKind
	Title  string
	Detail string
}

// Layout draws the banner.
func (b Banner) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	base, glyph := b.Kind.style(th)
	w := draw.Width(gtx, gtx.Dp(360))
	pad := gtx.Dp(th.Space.S)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad), draw.Unbounded)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{}.Layout(cgtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.S}.Layout(gtx, atoms.Icon{Glyph: glyph, Color: base}.Fn(th))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(atoms.Strong(b.Title).Fn(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if b.Detail == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: th.Space.XXS}.Layout(gtx, atoms.Label{Text: b.Detail, Role: atoms.RoleMuted, MaxLines: 6}.Fn(th))
				}),
			)
		}),
	)
	call := m.Stop()
	size := image.Pt(w, d.Size.Y+2*pad)
	r := image.Rectangle{Max: size}
	rad := gtx.Dp(th.Radius.M)
	draw.RRect(gtx.Ops, r, rad, tokens.Over(tokens.WithAlpha(base, 0x22), th.Color.Surface))
	draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), tokens.WithAlpha(base, 0x90))
	t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// Fn adapts the banner to a layout.Widget.
func (b Banner) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
}
