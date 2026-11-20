package fileicons

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// The atoms adapter is cached per icon, draws file icons in their own
// colors and tints tool glyphs with the color the component asks for.
func TestGlyphAdapter(t *testing.T) {
	php := GlyphFor("src/Shop.php", false, Hints{})
	if php == nil || php != GlyphFor("Shop.php", false, Hints{}) || !php.Valid() {
		t.Fatal("file glyph should be valid and cached per ID")
	}
	if (Icon{ID: "nope"}).Glyph() != nil {
		t.Fatal("unknown icon should give nil")
	}
	tool := ToolGlyph(ToolTerminal)
	if tool == nil || tool == Tool(ToolTerminal).Tinted(color.NRGBA{R: 1, A: 255}).Glyph() {
		t.Fatal("tinted tool glyph is a separate adapter")
	}
	red := color.NRGBA{R: 255, A: 255}
	th := tokens.New(tokens.ModeDark, nil)
	img, err := uitest.Render(th, image.Pt(32, 32), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return atoms.Icon{Glyph: tool, Size: 32, Color: red}.Layout(gtx, th)
	})
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	reddish := 0
	for y := range 32 {
		for x := range 32 {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 0x8000 && r > 0xc000 && g < 0x4000 && b < 0x4000 {
				reddish++
			}
		}
	}
	if reddish < 20 {
		t.Fatalf("tool glyph not tinted (%d red pixels)", reddish)
	}
	if n := uitest.CountDiffering(mustRender(t, th, php), th.Color.Background); n < 50 {
		t.Fatalf("file glyph drew %d pixels", n)
	}
}

func mustRender(t *testing.T, th *tokens.Theme, g *atoms.Glyph) *image.RGBA {
	t.Helper()
	img, err := uitest.Render(th, image.Pt(32, 32), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return atoms.Icon{Glyph: g, Size: 32}.Layout(gtx, th)
	})
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	return img
}
