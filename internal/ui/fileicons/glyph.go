package fileicons

import (
	"image"
	"image/color"
	"sync"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// glyphs caches the adapters per icon ID (and fixed tint).
var glyphs sync.Map // glyphKey → *atoms.Glyph

type glyphKey struct {
	id  string
	col color.NRGBA
}

// Glyph adapts the icon to an *atoms.Glyph, so organisms that take
// atoms glyphs (file rows, tabs, choosers, stripes, toolbar) draw it.
// Monochrome tool glyphs take the color the component asks for unless
// the icon was Tinted; file icons keep their own colors. Size is decided
// by the component. Unknown icons return nil. Adapters are cached per ID.
func (i Icon) Glyph() *atoms.Glyph {
	if !i.Valid() {
		return nil
	}
	k := glyphKey{id: i.ID, col: i.Color}
	if g, ok := glyphs.Load(k); ok {
		return g.(*atoms.Glyph)
	}
	ic := i
	g := atoms.NewPaintedGlyph("fileicons:"+i.ID, func(gtx layout.Context, th *tokens.Theme, px int, col color.NRGBA) {
		d := ic
		if d.Color.A == 0 {
			d.Color = col
		}
		im, ok := d.imageOp(px, th, gtx.Enabled())
		if !ok {
			return
		}
		defer clip.Rect{Max: image.Pt(px, px)}.Push(gtx.Ops).Pop()
		im.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	})
	actual, _ := glyphs.LoadOrStore(k, g)
	return actual.(*atoms.Glyph)
}

// GlyphFor is For(name, dir, h).Glyph().
func GlyphFor(name string, dir bool, h Hints) *atoms.Glyph { return For(name, dir, h).Glyph() }

// ToolGlyph is Tool(name).Glyph().
func ToolGlyph(name ToolName) *atoms.Glyph { return Tool(name).Glyph() }
