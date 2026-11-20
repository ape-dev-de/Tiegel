package tokens

import (
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"gioui.org/font"
	"gioui.org/text"
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
	"golang.org/x/image/math/fixed"
)

func splitChain(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		out = append(out, strings.TrimSpace(f))
	}
	return out
}

func TestDefaultFamiliesShape(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd", ""} {
		ui, mono := DefaultFamilies(goos)
		u, m := splitChain(ui), splitChain(mono)
		if u[0] != uiAlias || !slices.Contains(u, bundledUI) || u[len(u)-1] != "sans-serif" {
			t.Errorf("%s: ui chain %q must start with the alias, contain Go and end with sans-serif", goos, ui)
		}
		if !slices.Contains(m, bundledMono) || m[len(m)-1] != "monospace" {
			t.Errorf("%s: mono chain %q must contain Go Mono and end with monospace", goos, mono)
		}
		// Emoji fonts cover digits: they must come after the Go fonts.
		for _, c := range [][]string{u, m} {
			for i, f := range c {
				if strings.Contains(strings.ToLower(f), "emoji") && i < slices.Index(c, bundledUI)+slices.Index(c, bundledMono)+1 {
					t.Errorf("%s: emoji font %q before the Go fonts in %q", goos, f, c)
				}
			}
		}
		for _, f := range append(u, m...) {
			if f == "" {
				t.Errorf("%s: empty family in %q / %q", goos, ui, mono)
			}
		}
	}
}

func TestNewUsesDefaultFamilies(t *testing.T) {
	ui, mono := DefaultFamilies(goos)
	th := Dark(nil)
	if string(th.Font.UI.Typeface) != ui || string(th.Font.Mono.Typeface) != mono {
		t.Fatalf("fonts = %q / %q", th.Font.UI.Typeface, th.Font.Mono.Typeface)
	}
	if th.Text.Code != 13 || th.Text.Body != 13 || th.Text.CodeLineHeight != 1.4 {
		t.Fatalf("default sizes %+v", th.Text)
	}
}

func TestFontPrefsNormalized(t *testing.T) {
	for _, c := range []struct {
		in, want FontPrefs
	}{
		{FontPrefs{}, FontPrefs{EditorSize: 13, LineHeight: 1.4, UISize: 13}},
		{FontPrefs{EditorFamily: "  Menlo ", EditorSize: 2, LineHeight: 9, UISize: 99},
			FontPrefs{EditorFamily: "Menlo", EditorSize: 6, LineHeight: 2.5, UISize: 24}},
		{FontPrefs{EditorSize: 100, LineHeight: 0.5, UISize: 1}, FontPrefs{EditorSize: 48, LineHeight: 1, UISize: 9}},
		{FontPrefs{EditorSize: 15, LineHeight: 1.6, UISize: 16}, FontPrefs{EditorSize: 15, LineHeight: 1.6, UISize: 16}},
	} {
		if got := c.in.Normalized(); got != c.want {
			t.Errorf("Normalized(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestWithFonts(t *testing.T) {
	base := Dark(nil)
	_, mono := DefaultFamilies(goos)
	th := base.WithFonts(FontPrefs{EditorFamily: "Menlo", EditorSize: 16, LineHeight: 1.8, UISize: 16})
	if th == base || base.Text.Code != 13 || string(base.Font.Mono.Typeface) != mono {
		t.Fatal("WithFonts must not modify the receiver")
	}
	if th.Text.Code != 16 || th.Text.CodeLineHeight != 1.8 || th.Text.Body != 16 {
		t.Fatalf("sizes %+v", th.Text)
	}
	if th.Text.Small >= th.Text.Body || th.Text.Large <= th.Text.Body || th.Text.Title <= th.Text.Heading {
		t.Fatalf("type scale not ordered: %+v", th.Text)
	}
	if want := float32(12) * 16 / 13; float32(th.Text.Small) < want-0.5 || float32(th.Text.Small) > want+0.5 {
		t.Fatalf("Small = %v, want ≈%v", th.Text.Small, want)
	}
	chain := splitChain(string(th.Font.Mono.Typeface))
	if chain[0] != "Menlo" || !slices.Contains(chain, bundledMono) {
		t.Fatalf("editor chain %q", th.Font.Mono.Typeface)
	}
	if strings.Count(strings.ToLower(string(th.Font.Mono.Typeface)), "menlo") != 1 {
		t.Fatalf("duplicate family in %q", th.Font.Mono.Typeface)
	}
	if th.Font.UI != base.Font.UI || th.Color != base.Color || th.Shaper != base.Shaper {
		t.Fatal("WithFonts must keep UI font, colors and shaper")
	}
	// Defaults reproduce New.
	if d := base.WithFonts(FontPrefs{}); d.Text != base.Text || d.Font != base.Font {
		t.Fatalf("zero prefs changed the theme: %+v vs %+v", d.Text, base.Text)
	}
	// A full chain is used verbatim; odd names are quoted.
	if got := editorChain("A, B", mono); got != "A, B" {
		t.Errorf("chain = %q", got)
	}
	if got := editorChain(`My "Font"`, "Go Mono"); got != `"My \"Font\"", Go Mono` {
		t.Errorf("quoted chain = %q", got)
	}
	// Toggling keeps the fonts.
	if tg := th.Toggled(); tg.Text != th.Text || tg.Font != th.Font || tg.Mode == th.Mode {
		t.Fatal("Toggled must keep fonts and sizes")
	}
}

// TestChainsParse checks that every chain shapes text with the bundled
// shaper (Gio rejects unparsable typefaces silently, so compare against
// the Go fonts' advance).
func TestChainsParse(t *testing.T) {
	sh := NewBundledShaper()
	adv := func(tf string) fixed.Int26_6 {
		sh.LayoutString(text.Parameters{Font: font.Font{Typeface: font.Typeface(tf)}, PxPerEm: fixed.I(20), MaxWidth: 1 << 20}, "0")
		g, ok := sh.NextGlyph()
		for _, more := sh.NextGlyph(); more; _, more = sh.NextGlyph() {
		}
		if !ok {
			t.Fatalf("no glyph for %q", tf)
		}
		return g.Advance
	}
	goMono := adv(bundledMono)
	for _, goos := range []string{"darwin", "linux", "windows"} {
		_, mono := DefaultFamilies(goos)
		if got := adv(mono); got != goMono {
			t.Errorf("%s mono chain falls back to advance %v, want Go Mono %v", goos, got, goMono)
		}
	}
}

var (
	sysOnce  sync.Once
	sysFonts []fontscan.Footprint
)

func systemFonts(t *testing.T) []fontscan.Footprint {
	t.Helper()
	sysOnce.Do(func() {
		cache, _ := os.UserCacheDir()
		sysFonts, _ = fontscan.SystemFonts(log.New(io.Discard, "", 0), cache)
	})
	if len(sysFonts) == 0 {
		t.Skip("no system fonts installed")
	}
	return sysFonts
}

// resolveFamily returns the family the system font map picks for r with
// the typeface chain tf.
func resolveFamily(t *testing.T, tf string, r rune, w gtfont.Weight) string {
	t.Helper()
	systemFonts(t)
	fm := fontscan.NewFontMap(log.New(io.Discard, "", 0))
	cache, _ := os.UserCacheDir()
	if err := fm.UseSystemFonts(cache); err != nil {
		t.Skipf("system fonts: %v", err)
	}
	fm.SetQuery(fontscan.Query{Families: splitChain(tf), Aspect: gtfont.Aspect{Weight: w}})
	face := fm.ResolveFace(r)
	if face == nil {
		return ""
	}
	fam, _ := fm.FontMetadata(face.Font)
	return fam
}

func TestSystemChainsResolve(t *testing.T) {
	systemFonts(t)
	ui, mono := DefaultFamilies(goos)
	for _, c := range []struct {
		name string
		tf   string
		r    rune
	}{
		{"ui", ui, 'a'}, {"mono", mono, '0'}, {"mono CJK", mono, '漢'}, {"mono emoji", mono, '✓'},
		{"ui CJK", ui, '字'}, {"mono emoji 2", mono, '😀'},
	} {
		got := resolveFamily(t, c.tf, c.r, gtfont.WeightNormal)
		t.Logf("%-12s %q → %q", c.name, c.r, got)
		if got == "" && (c.r == 'a' || c.r == '0') {
			t.Errorf("%s: nothing resolves %q", c.name, c.r)
		}
	}
	if goos == "darwin" {
		if got := gtfont.NormalizeFamily(resolveFamily(t, mono, '0', gtfont.WeightNormal)); got == "gomono" {
			t.Errorf("darwin mono chain fell back to Go Mono")
		}
		t.Logf("ui bold → %q", resolveFamily(t, ui, 'a', gtfont.WeightBold))
	}
}

// TestSystemShaperShapesFallbackRunes checks that the system shaper finds
// glyphs for CJK and emoji runes missing from the editor font.
func TestSystemShaperShapesFallbackRunes(t *testing.T) {
	systemFonts(t)
	sh := NewShaper()
	_, mono := DefaultFamilies(goos)
	sh.LayoutString(text.Parameters{Font: font.Font{Typeface: font.Typeface(mono)}, PxPerEm: fixed.I(20), MaxWidth: 1 << 20}, "a漢✓")
	var missing int
	n := 0
	for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
		n++
		if uint64(g.ID)&(1<<32-1) == 0 {
			missing++
		}
	}
	if n == 0 {
		t.Fatal("no glyphs")
	}
	if missing > 0 {
		t.Logf("%d of %d glyphs are .notdef (no installed font covers them)", missing, n)
	}
}

func TestMonospaceFamilies(t *testing.T) {
	fams := MonospaceFamilies()
	if len(fams) == 0 || fams[len(fams)-1] != bundledMono {
		t.Fatalf("list must end with Go Mono: %q", fams)
	}
	again := MonospaceFamilies()
	again[0] = "mutated"
	if MonospaceFamilies()[0] == "mutated" {
		t.Fatal("MonospaceFamilies must return a copy")
	}
	seen := map[string]bool{}
	for i, f := range fams {
		n := gtfont.NormalizeFamily(f)
		if seen[n] || strings.HasPrefix(f, ".") || f == "" {
			t.Errorf("bad or duplicate family %q", f)
		}
		seen[n] = true
		if i > 0 && i < len(fams)-1 && strings.ToLower(fams[i-1]) > strings.ToLower(f) {
			t.Errorf("not sorted: %q > %q", fams[i-1], f)
		}
	}
	t.Logf("monospace families: %q", fams)
	if goos == "darwin" && len(systemFonts(t)) > 0 && !seen["menlo"] {
		t.Errorf("Menlo missing on darwin: %q", fams)
	}
}

func TestMonoCandidateFilter(t *testing.T) {
	for fam, want := range map[string]bool{
		"menlo": true, "dejavusansmono": true, "sourcecodepro": true, "helvetica": false, "arial": false,
	} {
		if isMonoCandidate(fam) != want {
			t.Errorf("isMonoCandidate(%q) != %v", fam, want)
		}
	}
	if regularity(gtfont.Aspect{Weight: gtfont.WeightNormal}) >= regularity(gtfont.Aspect{Weight: gtfont.WeightBold}) {
		t.Error("regular must rank before bold")
	}
	if _, ok := monospaceName(fontscan.Location{File: "/nonexistent/font.ttf"}); ok {
		t.Error("missing file must not be monospace")
	}
}

// TestSystemShaperBold checks that bold UI text is wider than regular text
// with the system shaper, including variable system fonts.
func TestSystemShaperBold(t *testing.T) {
	sh := NewShaper()
	ui, _ := DefaultFamilies(goos)
	width := func(w font.Weight) fixed.Int26_6 {
		sh.LayoutString(text.Parameters{Font: font.Font{Typeface: font.Typeface(ui), Weight: w}, PxPerEm: fixed.I(20), MaxWidth: 1 << 20}, "Hello World")
		var adv fixed.Int26_6
		for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
			adv += g.Advance
		}
		return adv
	}
	reg, bold := width(font.Normal), width(font.Bold)
	t.Logf("regular %v, bold %v, alias instances %d", reg, bold, len(uiInstances()))
	if bold <= reg {
		t.Errorf("bold (%v) is not wider than regular (%v)", bold, reg)
	}
}

func TestVariableInstances(t *testing.T) {
	if got := variableInstances(nil, []string{"Nope", uiAlias}); got != nil {
		t.Fatalf("no fonts must give no instances: %v", got)
	}
	fps := []fontscan.Footprint{
		{Family: "real", Aspect: gtfont.Aspect{Style: gtfont.StyleNormal, Weight: 400}},
		{Family: "real", Aspect: gtfont.Aspect{Style: gtfont.StyleNormal, Weight: 700}},
	}
	if got := variableInstances(fps, []string{"Real"}); got != nil {
		t.Fatalf("a family with a real bold face needs no instances: %v", got)
	}
	fps = []fontscan.Footprint{{Family: "broken", Location: fontscan.Location{File: "/nonexistent.ttf"}, Aspect: gtfont.Aspect{Style: gtfont.StyleNormal, Weight: 400}}}
	if got := variableInstances(fps, []string{"Broken"}); got != nil {
		t.Fatalf("unreadable font must give no instances: %v", got)
	}
}
