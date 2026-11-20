package fileicons

import (
	"image/color"
	"math"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// paintCtx is what a definition needs to draw itself.
type paintCtx struct {
	p    *tokens.FileIconPalette
	test bool        // test variant of a badge
	tint color.NRGBA // ink of monochrome tool glyphs
}

// def describes one icon.
type def struct {
	paint func(d *drawer, c *paintCtx)
	// mono glyphs are drawn in a single tint chosen by the caller.
	mono bool
	// testable badges get an automatic ".test" variant.
	testable bool
}

// Badge geometry in design units (13×13, aligned to whole units).
const (
	bx0, by0, bx1, by1 = 1, 1, 14, 14
	bRadius            = 2
	bandBottom         = 3 // bottom edge of the test band
)

type pick func(p *tokens.FileIconPalette) color.NRGBA

func inkLight(p *tokens.FileIconPalette) color.NRGBA { return p.InkLight }
func inkDark(p *tokens.FileIconPalette) color.NRGBA  { return p.InkDark }

// badgeBase fills the badge, with the test band when requested.
func badgeBase(d *drawer, c *paintCtx, fill color.NRGBA) {
	if !c.test {
		d.rrect(bx0, by0, bx1, by1, bRadius)
		d.fill(fill)
		return
	}
	d.rrect(bx0, by0, bx1, by1, bRadius)
	d.fill(c.p.Test)
	d.poly(d.rrectPts(bx0, bandBottom, bx1, by1, [4]float32{0, 0, bRadius, bRadius}), false)
	d.fill(fill)
}

// monogram places text centered on the badge: up to two characters in the
// 5×7 font, otherwise the 3×5 font.
func monogram(d *drawer, text string, ink color.NRGBA) {
	f := &font3x5
	y := float32(5)
	if len([]rune(text)) <= 2 && font5x7.has(text) {
		f, y = &font5x7, 4
	}
	x := float32(bx0 + (bx1-bx0-f.width(text))/2)
	d.text(f, text, x, y)
	d.fill(ink)
}

// solid is a filled badge with a monogram; it has a test variant.
func solid(fill, ink pick, text string) def {
	return def{testable: true, paint: func(d *drawer, c *paintCtx) {
		badgeBase(d, c, fill(c.p))
		monogram(d, text, ink(c.p))
	}}
}

// plain is a filled badge with a monogram and no test variant.
func plain(fill, ink pick, text string) def {
	df := solid(fill, ink, text)
	df.testable = false
	return df
}

// outlined is a ring badge with a small monogram in the same color; used
// for configuration files of a language or tool.
func outlined(col pick, text string) def {
	return def{paint: func(d *drawer, c *paintCtx) {
		d.rrectRing(bx0, by0, bx1, by1, bRadius, 1)
		d.fill(col(c.p))
		x := float32(bx0 + (bx1-bx0-font3x5.width(text))/2)
		d.text(&font3x5, text, x, 5)
		d.fill(col(c.p))
	}}
}

// picto is a filled badge with a pictogram drawn by draw.
func picto(fill pick, draw func(d *drawer, c *paintCtx, fill color.NRGBA)) def {
	return def{paint: func(d *drawer, c *paintCtx) {
		f := fill(c.p)
		badgeBase(d, c, f)
		draw(d, c, f)
	}}
}

func drawLock(d *drawer, c *paintCtx, fill color.NRGBA) {
	d.stroke(1, false, append(append([]pt{{5.5, 7.5}}, d.arcPts(7.5, 5.5, 2, 2, math.Pi, 2*math.Pi)...), pt{9.5, 7.5})...)
	d.rect(4, 7, 11, 12)
	d.fill(c.p.InkLight)
	d.rect(7, 8.5, 8, 10.5)
	d.fill(fill)
}

func drawGit(d *drawer, c *paintCtx, _ color.NRGBA) {
	d.line(1.2, 5, 4, 5, 11)
	d.stroke(1.2, false, quadPts(pt{10, 5.5}, pt{10, 9.5}, pt{5, 10}, 8)...)
	d.circle(5, 4, 1.6)
	d.circle(5, 11, 1.6)
	d.circle(10, 5.5, 1.6)
	d.fill(c.p.InkLight)
}

func drawContainers(d *drawer, c *paintCtx, _ color.NRGBA) {
	d.rect(3, 8.5, 7, 11.5)
	d.rect(8, 8.5, 12, 11.5)
	d.rect(5.5, 4.5, 9.5, 7.5)
	d.fill(c.p.InkLight)
}

func drawCompose(d *drawer, c *paintCtx, _ color.NRGBA) {
	d.rect(3, 3.5, 7, 7)
	d.rect(8, 3.5, 12, 7)
	d.rect(3, 8, 7, 11.5)
	d.rect(8, 8, 12, 11.5)
	d.fill(c.p.InkLight)
}

func drawImage(d *drawer, c *paintCtx, _ color.NRGBA) {
	d.poly([]pt{{2.5, 12}, {6, 7}, {8.5, 10}, {10, 8.5}, {12.5, 12}}, false)
	d.fill(c.p.InkLight)
	d.circle(10, 4.5, 1.75)
	d.fill(c.p.ImageSun)
}

func drawZip(d *drawer, c *paintCtx, _ color.NRGBA) {
	for y := float32(1); y < 7; y++ {
		if int(y)%2 == 1 {
			d.rect(6, y, 8, y+1)
		} else {
			d.rect(7, y, 9, y+1)
		}
	}
	d.rect(6, 7.5, 9, 12)
	d.poly([]pt{{7, 8.5}, {8, 8.5}, {8, 11}, {7, 11}}, true)
	d.fill(c.p.InkLight)
}

func drawShell(d *drawer, c *paintCtx, _ color.NRGBA) {
	d.text(&font5x7, ">", 2, 4)
	d.fill(c.p.ShellInk)
	d.text(&font5x7, "_", 8, 4)
	d.fill(c.p.InkLight)
}

// page draws the outline of a sheet with a folded corner.
func page(d *drawer, col color.NRGBA, lines bool) {
	d.stroke(1, true, pt{3.5, 1.5}, pt{9, 1.5}, pt{12.5, 5}, pt{12.5, 14.5}, pt{3.5, 14.5})
	d.stroke(1, false, pt{9, 1.5}, pt{9, 5}, pt{12.5, 5})
	if lines {
		for _, y := range []float32{7.5, 9.5, 11.5} {
			d.line(1, 5.5, y, 10.5, y)
		}
	}
	d.fill(col)
}

// Folder geometry.
func folderClosed(d *drawer, col, mark color.NRGBA, library bool) {
	d.rrect(1, 2, 7, 5, 1)
	d.rrect(1, 4, 15, 14, 1.5)
	d.fill(col)
	if library {
		for _, x := range []float32{4, 6.5, 9} {
			d.rect(x, 7, x+1.5, 12)
		}
		d.fill(mark)
	}
}

func folderOpen(d *drawer, col, back, mark color.NRGBA, library bool) {
	d.rrect(1, 2, 7, 5, 1)
	d.rrect(1, 4, 14, 14, 1.5)
	d.fill(back)
	d.poly([]pt{{3.5, 7}, {15.5, 7}, {13.5, 14}, {1, 14}}, false)
	d.fill(col)
	if library {
		for _, x := range []float32{5, 7.5, 10} {
			d.poly([]pt{{x + 0.5, 9}, {x + 2, 9}, {x + 1.5, 12.5}, {x, 12.5}}, false)
		}
		d.fill(mark)
	}
}

func folder(col pick, library, open bool) def {
	return def{paint: func(d *drawer, c *paintCtx) {
		f := col(c.p)
		mark := tokens.Mix(f, c.p.InkDark, 0.45)
		if open {
			folderOpen(d, f, tokens.Mix(f, c.p.InkDark, 0.3), mark, library)
			return
		}
		folderClosed(d, f, mark, library)
	}}
}

// File kinds (icon IDs).
const (
	KindGeneric      = "file"
	KindText         = "text"
	KindPHP          = "php"
	KindTypeScript   = "ts"
	KindTSX          = "tsx"
	KindJavaScript   = "js"
	KindJSX          = "jsx"
	KindMJS          = "mjs"
	KindJSON         = "json"
	KindYAML         = "yaml"
	KindMarkdown     = "md"
	KindHTML         = "html"
	KindCSS          = "css"
	KindSCSS         = "scss"
	KindXML          = "xml"
	KindSQL          = "sql"
	KindShell        = "shell"
	KindDocker       = "docker"
	KindDockerIgnore = "dockerignore"
	KindCompose      = "compose"
	KindEnv          = "env"
	KindGitIgnore    = "gitignore"
	KindGit          = "git"
	KindEditorConfig = "editorconfig"
	KindComposer     = "composer"
	KindComposerLock = "composer-lock"
	KindPackage      = "package"
	KindPackageLock  = "package-lock"
	KindTSConfig     = "tsconfig"
	KindJSConfig     = "jsconfig"
	KindPHPUnit      = "phpunit"
	KindMake         = "make"
	KindGo           = "go"
	KindPython       = "py"
	KindTwig         = "twig"
	KindImage        = "image"
	KindSVG          = "svg"
	KindArchive      = "archive"
	KindLock         = "lock"
	KindCSV          = "csv"
	KindTOML         = "toml"
	KindINI          = "ini"
	KindLog          = "log"
	KindHtaccess     = "htaccess"

	KindFolder         = "folder"
	KindFolderSource   = "folder-source"
	KindFolderTest     = "folder-test"
	KindFolderResource = "folder-resource"
	KindFolderExcluded = "folder-excluded"
	KindFolderLibrary  = "folder-library"
)

// testSuffix and openSuffix form variant IDs ("ts.test", "folder.open").
const (
	testSuffix = ".test"
	openSuffix = ".open"
)

// pal abbreviates the palette type in the definition tables.
type pal = tokens.FileIconPalette

var fileDefs = map[string]def{
	KindGeneric: {paint: func(d *drawer, c *paintCtx) { page(d, c.p.Generic, false) }},
	KindText:    {paint: func(d *drawer, c *paintCtx) { page(d, c.p.Generic, true) }},
	KindLog:     {paint: func(d *drawer, c *paintCtx) { page(d, c.p.Markdown, true) }},

	KindPHP:        solid(func(p *pal) color.NRGBA { return p.PHP }, inkLight, "PHP"),
	KindTypeScript: solid(func(p *pal) color.NRGBA { return p.TypeScript }, inkLight, "TS"),
	KindTSX:        solid(func(p *pal) color.NRGBA { return p.TypeScript }, inkLight, "TSX"),
	KindJavaScript: solid(func(p *pal) color.NRGBA { return p.JavaScript }, inkDark, "JS"),
	KindJSX:        solid(func(p *pal) color.NRGBA { return p.JavaScript }, inkDark, "JSX"),
	KindMJS:        solid(func(p *pal) color.NRGBA { return p.JavaScript }, inkDark, "MJS"),
	KindGo:         solid(func(p *pal) color.NRGBA { return p.Go }, inkLight, "GO"),
	KindPython:     solid(func(p *pal) color.NRGBA { return p.Python }, func(p *pal) color.NRGBA { return p.PythonInk }, "PY"),

	KindJSON:     plain(func(p *pal) color.NRGBA { return p.JSON }, inkDark, "{}"),
	KindYAML:     plain(func(p *pal) color.NRGBA { return p.YAML }, inkLight, "YML"),
	KindMarkdown: plain(func(p *pal) color.NRGBA { return p.Markdown }, inkLight, "MD"),
	KindHTML:     plain(func(p *pal) color.NRGBA { return p.HTML }, inkLight, "<>"),
	KindCSS:      plain(func(p *pal) color.NRGBA { return p.CSS }, inkLight, "CSS"),
	KindSCSS:     plain(func(p *pal) color.NRGBA { return p.SCSS }, inkLight, "SC"),
	KindXML:      plain(func(p *pal) color.NRGBA { return p.XML }, inkLight, "XML"),
	KindSQL:      plain(func(p *pal) color.NRGBA { return p.SQL }, inkLight, "SQL"),
	KindEnv:      plain(func(p *pal) color.NRGBA { return p.Env }, inkLight, "ENV"),
	KindComposer: plain(func(p *pal) color.NRGBA { return p.Composer }, inkLight, "CMP"),
	KindPackage:  plain(func(p *pal) color.NRGBA { return p.Package }, inkLight, "PKG"),
	KindMake:     plain(func(p *pal) color.NRGBA { return p.Make }, inkLight, "MK"),
	KindTwig:     plain(func(p *pal) color.NRGBA { return p.Twig }, inkLight, "TW"),
	KindCSV:      plain(func(p *pal) color.NRGBA { return p.Data }, inkLight, "CSV"),
	KindTOML:     plain(func(p *pal) color.NRGBA { return p.Data }, inkLight, "TML"),
	KindINI:      plain(func(p *pal) color.NRGBA { return p.Config }, inkLight, "INI"),
	KindSVG:      plain(func(p *pal) color.NRGBA { return p.Image }, inkLight, "SVG"),

	KindTSConfig:     outlined(func(p *pal) color.NRGBA { return p.TypeScript }, "TS"),
	KindJSConfig:     outlined(func(p *pal) color.NRGBA { return p.JavaScriptLine }, "JS"),
	KindPHPUnit:      outlined(func(p *pal) color.NRGBA { return p.PHP }, "PU"),
	KindEditorConfig: outlined(func(p *pal) color.NRGBA { return p.Config }, "EC"),
	KindHtaccess:     outlined(func(p *pal) color.NRGBA { return p.Config }, "HT"),

	KindShell:        picto(func(p *pal) color.NRGBA { return p.Shell }, drawShell),
	KindDocker:       picto(func(p *pal) color.NRGBA { return p.Docker }, drawContainers),
	KindDockerIgnore: picto(func(p *pal) color.NRGBA { return p.Config }, drawContainers),
	KindCompose:      picto(func(p *pal) color.NRGBA { return p.Compose }, drawCompose),
	KindGit:          picto(func(p *pal) color.NRGBA { return p.Git }, drawGit),
	KindGitIgnore:    picto(func(p *pal) color.NRGBA { return p.Config }, drawGit),
	KindComposerLock: picto(func(p *pal) color.NRGBA { return p.Composer }, drawLock),
	KindPackageLock:  picto(func(p *pal) color.NRGBA { return p.Package }, drawLock),
	KindLock:         picto(func(p *pal) color.NRGBA { return p.Config }, drawLock),
	KindImage:        picto(func(p *pal) color.NRGBA { return p.Image }, drawImage),
	KindArchive:      picto(func(p *pal) color.NRGBA { return p.Archive }, drawZip),

	KindFolder:         folder(func(p *pal) color.NRGBA { return p.Folder }, false, false),
	KindFolderSource:   folder(func(p *pal) color.NRGBA { return p.FolderSource }, false, false),
	KindFolderTest:     folder(func(p *pal) color.NRGBA { return p.FolderTest }, false, false),
	KindFolderResource: folder(func(p *pal) color.NRGBA { return p.FolderResource }, false, false),
	KindFolderExcluded: folder(func(p *pal) color.NRGBA { return p.FolderExcluded }, false, false),
	KindFolderLibrary:  folder(func(p *pal) color.NRGBA { return p.FolderLibrary }, true, false),
}

// folderKinds lists the folder kinds; each has an ".open" variant.
var folderKinds = []string{KindFolder, KindFolderSource, KindFolderTest, KindFolderResource, KindFolderExcluded, KindFolderLibrary}

func init() {
	openPicks := map[string]pick{
		KindFolder:         func(p *pal) color.NRGBA { return p.Folder },
		KindFolderSource:   func(p *pal) color.NRGBA { return p.FolderSource },
		KindFolderTest:     func(p *pal) color.NRGBA { return p.FolderTest },
		KindFolderResource: func(p *pal) color.NRGBA { return p.FolderResource },
		KindFolderExcluded: func(p *pal) color.NRGBA { return p.FolderExcluded },
		KindFolderLibrary:  func(p *pal) color.NRGBA { return p.FolderLibrary },
	}
	for k, pk := range openPicks {
		fileDefs[k+openSuffix] = folder(pk, k == KindFolderLibrary, true)
	}
	var testable []string
	for k, df := range fileDefs {
		if df.testable {
			testable = append(testable, k)
		}
	}
	for _, k := range testable {
		inner := fileDefs[k].paint
		fileDefs[k+testSuffix] = def{paint: func(d *drawer, c *paintCtx) {
			cc := *c
			cc.test = true
			inner(d, &cc)
		}}
	}
	buildRegistry()
}
