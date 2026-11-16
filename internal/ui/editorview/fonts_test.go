package editorview

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/text"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// placed is a glyph painted by paintGlyphs with its cluster's byte offset.
type placed struct {
	g   text.Glyph
	off int
}

// layoutLine renders a one-line document with th and returns the editor
// and the glyphs placed for the line.
func layoutLine(t *testing.T, th *tokens.Theme, line string) (*Editor, []placed) {
	t.Helper()
	e := New(buffer.NewString(line))
	t.Cleanup(e.Close)
	var got []placed
	e.shp.trace = func(g text.Glyph, off int) { got = append(got, placed{g, off}) }
	h := uitest.NewHarness(image.Pt(900, 200))
	h.Theme = th
	h.Frame(e.Layout)
	if len(got) == 0 {
		t.Fatalf("no glyphs painted for %q", line)
	}
	return e, got
}

func systemTheme(t *testing.T) *tokens.Theme {
	t.Helper()
	uitest.SkipWithoutSystemMono(t)
	return tokens.Dark(tokens.NewShaper()).WithFonts(tokens.FontPrefs{EditorSize: 14})
}

// TestSystemFontGridASCII checks that with the system editor font every
// ASCII glyph starts exactly at its column and advances by one cell.
func TestSystemFontGridASCII(t *testing.T) {
	th := systemTheme(t)
	line := "abc 0123 MWil {$x->y()}"
	e, glyphs := layoutLine(t, th, line)
	t.Logf("font %q: cell %v, line height %d", th.Font.Mono.Typeface, e.met.cellW, e.met.lineH)
	for _, p := range glyphs {
		col := buffer.VisualCol([]byte(line), p.off, e.tabWidth())
		if want := fixed.Int26_6(col) * e.met.cellW; p.g.X != want {
			t.Errorf("%q at x %v, want col %d = %v", line[p.off], p.g.X, col, want)
		}
		if p.g.Advance != e.met.cellW {
			t.Errorf("%q advance %v != cell %v: editor font is not monospace", line[p.off], p.g.Advance, e.met.cellW)
		}
	}
}

// TestSystemFontGridFallback checks that wide CJK and emoji clusters
// (shaped from fallback fonts) do not shift the ASCII columns after them.
func TestSystemFontGridFallback(t *testing.T) {
	th := systemTheme(t)
	line := "漢字x😀y\tz é!"
	e, glyphs := layoutLine(t, th, line)
	seen := map[byte]bool{}
	for _, p := range glyphs {
		c := line[p.off]
		col := buffer.VisualCol([]byte(line), p.off, e.tabWidth())
		if want := fixed.Int26_6(col) * e.met.cellW; c < 0x80 && p.g.X != want {
			t.Errorf("%q at x %v, want col %d = %v", c, p.g.X, col, want)
		}
		if c < 0x80 {
			seen[c] = true
		}
	}
	for _, c := range []byte("xyz!") {
		if !seen[c] {
			t.Errorf("glyph %q not painted", c)
		}
	}
	if got, want := buffer.VisualCol([]byte(line), len("漢字x😀"), e.tabWidth()), 7; got != want {
		t.Fatalf("visual column of y = %d, want %d (wide clusters count 2)", got, want)
	}
}

// TestCodeLineHeight checks that the row height follows the theme's
// CodeLineHeight and that changing it invalidates the metrics.
func TestCodeLineHeight(t *testing.T) {
	base := tokens.Dark(nil)
	var prev int
	for _, lh := range []float32{1.0, 1.4, 2.0} {
		th := base.WithFonts(tokens.FontPrefs{LineHeight: lh})
		e, _ := layoutLine(t, th, "x")
		h := (e.met.ascent + e.met.descent).Ceil()
		if want := int(math.Ceil(float64(h) * float64(lh))); e.met.lineH != want || e.LineHeight() != want {
			t.Errorf("line height %.1f: %d (geo %d), want %d", lh, e.met.lineH, e.LineHeight(), want)
		}
		if e.met.lineH <= prev {
			t.Errorf("line height %.1f not taller than the previous one", lh)
		}
		prev = e.met.lineH
	}
	// Zero falls back to 1.4.
	th := *base
	th.Text.CodeLineHeight = 0
	if lineHeightOf(&th) != defaultLineHeight {
		t.Fatal("zero CodeLineHeight must use the default")
	}
	// Changing the line height on the same editor re-measures.
	e := New(buffer.NewString("x"))
	defer e.Close()
	h := uitest.NewHarness(image.Pt(300, 100))
	h.Theme = base.WithFonts(tokens.FontPrefs{LineHeight: 1})
	h.Frame(e.Layout)
	small := e.LineHeight()
	h.Theme = base.WithFonts(tokens.FontPrefs{LineHeight: 2})
	h.Frame(e.Layout)
	if e.LineHeight() <= small {
		t.Fatalf("line height did not grow: %d → %d", small, e.LineHeight())
	}
}

// TestRenderSystemFonts draws a grid-alignment sample with the system
// fonts on the GPU. With TIEGEL_SHOTS=<dir> it writes
// editor-system-fonts.png for visual review.
func TestRenderSystemFonts(t *testing.T) {
	th := systemTheme(t).WithFonts(tokens.FontPrefs{EditorSize: 16, LineHeight: 1.6})
	src := "0123456789|0123456789|\n" +
		"漢字かな한글|ascii after|\n" +
		"😀😀✓✓→→≠≠|ascii after|\n" +
		"café\tÅngström|\n" +
		"<?php $x = fn(int $y) => $y * 2; // ok\n"
	e := New(buffer.NewString(src))
	defer e.Close()
	img, err := uitest.Render(th, image.Pt(520, 200), e.Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if n := uitest.CountDiffering(img, th.Editor.Background); n < 2000 {
		t.Errorf("only %d pixels drawn", n)
	}
	if dir := os.Getenv("TIEGEL_SHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "editor-system-fonts.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
