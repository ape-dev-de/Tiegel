package fileicons

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestForFiles(t *testing.T) {
	cases := map[string]string{
		"index.php":                    KindPHP,
		"src/Foo.php":                  KindPHP,
		"tests/Unit/FooTest.php":       KindPHP + testSuffix,
		"FooTest.php":                  KindPHP + testSuffix,
		"shared.ts":                    KindTypeScript,
		"antrag.spec.ts":               KindTypeScript + testSuffix,
		"e2e-tests/login.ts":           KindTypeScript + testSuffix,
		"App.tsx":                      KindTSX,
		"App.test.tsx":                 KindTSX + testSuffix,
		"main.js":                      KindJavaScript,
		"x.jsx":                        KindJSX,
		"vite.config.mjs":              KindMJS,
		"data.json":                    KindJSON,
		"tests/fixture.json":           KindJSON,
		"a.yaml":                       KindYAML,
		"b.YML":                        KindYAML,
		"README.md":                    KindMarkdown,
		"index.html":                   KindHTML,
		"app.css":                      KindCSS,
		"app.scss":                     KindSCSS,
		"layout.xml":                   KindXML,
		"schema.sql":                   KindSQL,
		"run.sh":                       KindShell,
		"x.bash":                       KindShell,
		".zsh":                         KindShell,
		"Dockerfile":                   KindDocker,
		"Dockerfile.prod":              KindDocker,
		"app.dockerfile":               KindDocker,
		"docker-compose.yml":           KindCompose,
		"docker-compose.override.yaml": KindCompose,
		"compose.dev.yml":              KindCompose,
		".env":                         KindEnv,
		".env.local":                   KindEnv,
		"prod.env":                     KindEnv,
		".gitignore":                   KindGitIgnore,
		".gitattributes":               KindGit,
		".dockerignore":                KindDockerIgnore,
		".editorconfig":                KindEditorConfig,
		"composer.json":                KindComposer,
		"composer.lock":                KindComposerLock,
		"package.json":                 KindPackage,
		"package-lock.json":            KindPackageLock,
		"tsconfig.json":                KindTSConfig,
		"tsconfig.app.json":            KindTSConfig,
		"jsconfig.json":                KindJSConfig,
		"phpunit.xml":                  KindPHPUnit,
		"phpunit.xml.dist":             KindPHPUnit,
		"phpunit.ci.xml":               KindPHPUnit,
		"Makefile":                     KindMake,
		"main.go":                      KindGo,
		"main_test.go":                 KindGo + testSuffix,
		"app.py":                       KindPython,
		"test_app.py":                  KindPython + testSuffix,
		"notes.txt":                    KindText,
		"logo.png":                     KindImage,
		"logo.svg":                     KindSVG,
		"dist.tar.gz":                  KindArchive,
		"Cargo.lock":                   KindLock,
		"unknown.xyz":                  KindGeneric,
		"noext":                        KindGeneric,
		"trailingdot.":                 KindGeneric,
		`C:\proj\tests\a.php`:          KindPHP + testSuffix,
	}
	for name, want := range cases {
		if got := For(name, false, Hints{}).ID; got != want {
			t.Errorf("For(%q) = %q, want %q", name, got, want)
		}
	}
	if got := For("a.ts", false, Hints{Test: true}).ID; got != KindTypeScript+testSuffix {
		t.Errorf("Test hint: %q", got)
	}
	if got := For("a.ts", false, Hints{InTests: true}).ID; got != KindTypeScript+testSuffix {
		t.Errorf("InTests hint: %q", got)
	}
	if got := For("a.md", false, Hints{Test: true}).ID; got != KindMarkdown {
		t.Errorf("non-testable kind with Test hint: %q", got)
	}
}

func TestForFolders(t *testing.T) {
	cases := []struct {
		name string
		h    Hints
		want string
	}{
		{"src", Hints{}, KindFolder},
		{"src", Hints{Open: true}, KindFolder + openSuffix},
		{"src", Hints{SourceRoot: true}, KindFolderSource},
		{"tests", Hints{TestRoot: true, Open: true}, KindFolderTest + openSuffix},
		{"res", Hints{ResourceRoot: true}, KindFolderResource},
		{"var", Hints{Excluded: true, SourceRoot: true}, KindFolderExcluded},
		{"lib", Hints{LibraryRoot: true}, KindFolderLibrary},
		{"node_modules", Hints{}, KindFolderLibrary},
		{"/p/vendor/", Hints{Open: true}, KindFolderLibrary + openSuffix},
	}
	for _, c := range cases {
		if got := For(c.name, true, c.h).ID; got != c.want {
			t.Errorf("For(%q, dir, %+v) = %q, want %q", c.name, c.h, got, c.want)
		}
	}
}

func TestIsTest(t *testing.T) {
	yes := []string{"a.spec.ts", "a.test.js", "x.e2e.ts", "FooTest.php", "FooCest.php", "x_test.go", "x_test.py", "test_x.py", "tests/a.php", "test/b.js", "__tests__/c.ts", "spec/d.rb", "Tests/A.php"}
	no := []string{"a.ts", "Testing.php", "latest.php", "contest.go", "testdata.json", "src/a.ts"}
	for _, p := range yes {
		if !IsTest(p) {
			t.Errorf("IsTest(%q) = false", p)
		}
	}
	for _, p := range no {
		if IsTest(p) {
			t.Errorf("IsTest(%q) = true", p)
		}
	}
}

func TestToolsAndAliases(t *testing.T) {
	if len(Tools()) != len(toolDefs) {
		t.Fatalf("Tools() has %d entries, toolDefs %d", len(Tools()), len(toolDefs))
	}
	for _, n := range Tools() {
		ic := Tool(n)
		if !ic.Valid() || !ic.Mono() {
			t.Errorf("tool %q: valid=%v mono=%v", n, ic.Valid(), ic.Mono())
		}
	}
	for a, n := range toolAliases {
		if Tool(a).ID != Tool(n).ID {
			t.Errorf("alias %q", a)
		}
	}
	if Tool("nope").Valid() || (Icon{}).Valid() {
		t.Error("unknown icon is valid")
	}
	if For("a.php", false, Hints{}).Mono() {
		t.Error("file icon is mono")
	}
	ic := Tool(ToolRun).Tinted(tokens.Hex(0x112233)).Sized(20)
	if ic.Color != tokens.Hex(0x112233) || ic.Size != 20 {
		t.Error("Tinted/Sized")
	}
}

// TestMonogramsFit checks that every monogram uses known glyphs and stays
// inside the badge, so no text is ever clipped.
func TestMonogramsFit(t *testing.T) {
	for _, s := range []string{"PHP", "TS", "TSX", "JS", "JSX", "MJS", "GO", "PY", "{}", "YML", "MD", "<>", "CSS", "SC", "XML", "SQL", "ENV", "CMP", "PKG", "MK", "TW", "CSV", "TML", "INI", "SVG"} {
		f := &font3x5
		if len([]rune(s)) <= 2 && font5x7.has(s) {
			f = &font5x7
		}
		if !f.has(s) {
			t.Errorf("monogram %q: missing glyph", s)
		}
		if w := f.width(s); w > bx1-bx0-2 {
			t.Errorf("monogram %q: width %d too wide", s, w)
		}
	}
	for _, f := range []*pixelFont{&font5x7, &font3x5} {
		for r, rows := range f.glyph {
			if len(rows) != f.h {
				t.Errorf("glyph %q: %d rows", r, len(rows))
			}
			for _, row := range rows {
				if len(row) != f.w {
					t.Errorf("glyph %q: row %q", r, row)
				}
			}
		}
	}
	if font3x5.width("") != 0 {
		t.Error("empty width")
	}
}

func themes() []*tokens.Theme {
	sh := tokens.NewBundledShaper()
	return []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)}
}

func TestRenderEveryIcon(t *testing.T) {
	for _, th := range themes() {
		for _, ic := range All() {
			for _, px := range []int{16, 24, 32} {
				img := ic.Image(px, th)
				if img == nil {
					t.Fatalf("%s: nil image", ic.ID)
				}
				if n := 255 * px * px / 8; inked(img) < n/20 {
					t.Errorf("%s at %d px (%s): almost empty (%d)", ic.ID, px, th.Mode, inked(img))
				}
			}
		}
	}
	if (Icon{ID: "x"}).Image(16, themes()[0]) != nil || For("a", false, Hints{}).Image(0, themes()[0]) != nil {
		t.Error("Image of unknown icon or zero size")
	}
}

// inked sums the alpha of img divided by 8.
func inked(img *image.RGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		n += int(img.Pix[i])
	}
	return n / 8
}

func TestVariantsDiffer(t *testing.T) {
	th := themes()[0]
	pairs := [][2]Icon{
		{For("shared.ts", false, Hints{}), For("antrag.spec.ts", false, Hints{})},
		{For("src", true, Hints{}), For("src", true, Hints{Open: true})},
		{For("src", true, Hints{}), For("src", true, Hints{SourceRoot: true})},
		{For("composer.json", false, Hints{}), For("composer.lock", false, Hints{})},
		{Tool(ToolFind), Tool(ToolSearch)},
	}
	for _, p := range pairs {
		a, b := p[0].Image(16, th), p[1].Image(16, th)
		if string(a.Pix) == string(b.Pix) {
			t.Errorf("%s and %s render identically", p[0].ID, p[1].ID)
		}
	}
}

func TestToolTint(t *testing.T) {
	th := themes()[0]
	red := color.NRGBA{R: 0xFF, A: 0xFF}
	img := Tool(ToolStop).Tinted(red).Image(16, th)
	c := img.RGBAAt(8, 8)
	if c.R != 0xFF || c.G != 0 || c.B != 0 || c.A != 0xFF {
		t.Errorf("center of stop glyph = %v", c)
	}
	if img.RGBAAt(0, 0).A != 0 {
		t.Error("corner not transparent")
	}
}

func TestSmoke(t *testing.T) {
	for _, ic := range All() {
		ic := ic
		d := uitest.Smoke(t, ic.ID, image.Pt(64, 64), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return ic.Layout(gtx, th)
		})
		if d.Size != image.Pt(16, 16) {
			t.Errorf("%s: size %v", ic.ID, d.Size)
		}
	}
	// Explicit size, Fn, disabled context, unknown icon and zero size.
	uitest.Smoke(t, "sized", image.Pt(64, 64), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return For("a.php", false, Hints{}).Sized(24).Fn(th)(gtx)
	})
	uitest.Smoke(t, "disabled", image.Pt(64, 64), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx = gtx.Disabled()
		Tool(ToolRun).Layout(gtx, th)
		return For("a.php", false, Hints{}).Layout(gtx, th)
	})
	uitest.Smoke(t, "unknown", image.Pt(64, 64), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return Icon{ID: "nope"}.Layout(gtx, th)
	})
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Constraints: layout.Exact(image.Pt(8, 8))}
	if d := (Icon{ID: KindPHP, Size: unit.Dp(-1)}).Layout(gtx, themes()[0]); d.Size.X > 0 {
		t.Errorf("negative size: %v", d.Size)
	}
	if d := (Icon{ID: KindPHP, Size: 1000}).Layout(gtx, themes()[0]); d.Size.X != 1000 {
		t.Errorf("oversized icon: %v", d.Size)
	}
}

func TestCacheReuseAndBound(t *testing.T) {
	th := themes()[0]
	ic := For("x.php", false, Hints{})
	a, ok1 := ic.imageOp(16, th, true)
	b, ok2 := ic.imageOp(16, th, true)
	if !ok1 || !ok2 || a != b {
		t.Fatal("image op not reused")
	}
	for px := 1; px <= maxCache+10 && px <= maxPx; px++ {
		ic.imageOp(px, th, true)
	}
	for i := 0; cached() < maxCache && i < maxCache; i++ {
		Tool(ToolRun).Tinted(color.NRGBA{R: uint8(i), G: uint8(i >> 8), A: 0xFF}).imageOp(16, th, true)
	}
	Tool(ToolRun).Tinted(color.NRGBA{B: 1, A: 0xFF}).imageOp(16, th, true)
	if n := cached(); n > maxCache {
		t.Errorf("cache holds %d > %d", n, maxCache)
	}
}

// TestContactSheet renders every icon in both themes at 16 and 32 px into
// a PNG. It is written to the test's temp dir, or to the path in
// TIEGEL_ICON_SHEET when set.
func TestContactSheet(t *testing.T) {
	icons := All()
	const cols, cell = 12, 56
	rows := (len(icons) + cols - 1) / cols
	ths := themes()
	sheet := image.NewRGBA(image.Rect(0, 0, cols*cell*len(ths), rows*cell))
	for ti, th := range ths {
		bg := image.Rect(ti*cols*cell, 0, (ti+1)*cols*cell, rows*cell)
		draw.Draw(sheet, bg, image.NewUniform(th.Color.Surface), image.Point{}, draw.Src)
		for i, ic := range icons {
			x0 := ti*cols*cell + (i%cols)*cell + 4
			y0 := (i / cols) * cell
			small := ic.Image(16, th)
			big := ic.Image(32, th)
			draw.Draw(sheet, image.Rect(x0, y0+20, x0+16, y0+36), small, image.Point{}, draw.Over)
			draw.Draw(sheet, image.Rect(x0+20, y0+12, x0+52, y0+44), big, image.Point{}, draw.Over)
		}
	}
	out := os.Getenv("TIEGEL_ICON_SHEET")
	if out == "" {
		out = filepath.Join(t.TempDir(), "fileicons.png")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
	t.Logf("contact sheet: %s", out)
}

func TestPalettesComplete(t *testing.T) {
	for _, m := range []tokens.Mode{tokens.ModeDark, tokens.ModeLight} {
		p := tokens.FileIcons(m)
		for _, c := range []color.NRGBA{p.PHP, p.TypeScript, p.Test, p.Folder, p.FolderLibrary, p.InkLight, p.InkDark} {
			if c.A == 0 {
				t.Errorf("%v: transparent palette entry", m)
			}
		}
	}
}
