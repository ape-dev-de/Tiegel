package atoms_test

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// Painted glyphs draw through Icon with the icon's size and color.
func TestPaintedGlyph(t *testing.T) {
	var gotPx int
	var gotCol color.NRGBA
	var gotMax image.Point
	g := atoms.NewPaintedGlyph("p", func(gtx layout.Context, th *tokens.Theme, px int, col color.NRGBA) {
		gotPx, gotCol, gotMax = px, col, gtx.Constraints.Max
	})
	if !g.Valid() || g.Name() != "p" {
		t.Fatal("painted glyph should be valid")
	}
	if atoms.NewPaintedGlyph("nil", nil).Valid() {
		t.Fatal("nil painter is not valid")
	}
	red := color.NRGBA{R: 255, A: 255}
	var d layout.Dimensions
	uitest.Smoke(t, "painted", area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		d = atoms.Icon{Glyph: g, Size: 20, Color: red}.Layout(gtx, th)
		return d
	})
	if gotPx != 20 || gotCol != red || gotMax != image.Pt(20, 20) || d.Size != image.Pt(20, 20) {
		t.Fatalf("painter got px %d col %v max %v, dims %v", gotPx, gotCol, gotMax, d.Size)
	}
}
