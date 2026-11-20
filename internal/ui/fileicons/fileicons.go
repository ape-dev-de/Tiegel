// Package fileicons is Tiegel's own icon set for files, folders and tool
// windows.
//
// Every icon is drawn in code on a 16×16 unit grid from simple geometric
// shapes (rounded badges with a pixel-font monogram, folders, pictograms)
// and rasterized once per pixel size and theme into an image that is cached
// and reused across frames, so a file tree with hundreds of rows costs one
// image paint per row. Colors come from tokens.FileIcons; tool glyphs are
// monochrome and take the caller's color. Nothing here is taken from
// another IDE.
//
// Usage for integrators:
//
//	// File tree row, editor tab, search result:
//	ic := fileicons.For(node.Path, node.Dir, fileicons.Hints{
//		Open:       node.Expanded,
//		SourceRoot: isSourceRoot, TestRoot: isTestRoot,
//		Excluded:   isExcluded, LibraryRoot: isLibrary,
//		InTests:    underTestRoot, // file below a test root
//	})
//	ic.Layout(gtx, th) // th.Size.Icon (16dp) square
//
//	// Tool-window stripe button (monochrome, tinted):
//	fileicons.Tool(fileicons.ToolTerminal).Tinted(th.Color.TextMuted).Layout(gtx, th)
//
// For accepts a base name or a slash-separated path; directory parts of a
// path only matter for test detection (tests/, e2e-tests/ …). Icon is a
// small value type; keep it in row models or compute it per frame (For is
// a few string operations).
package fileicons

import (
	"image"
	"image/color"
	"path"
	"slices"
	"strings"
	"sync"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Hints carries project knowledge the name alone cannot tell.
type Hints struct {
	// Open selects the open-folder variant (expanded tree node).
	Open bool
	// Folder roles from the project model (marked directories).
	SourceRoot   bool
	TestRoot     bool
	ResourceRoot bool
	Excluded     bool
	// LibraryRoot marks dependency directories; node_modules and vendor
	// are recognised by name as well.
	LibraryRoot bool
	// Test forces the test variant of a file icon; InTests marks a file
	// below a test root. Test file names (*.spec.ts, FooTest.php …) and
	// paths below tests/, test/, e2e-tests/ … are detected anyway.
	Test    bool
	InTests bool
}

// Icon is a renderable icon. The zero value draws nothing but reserves
// its space.
type Icon struct {
	// ID identifies the drawing, e.g. "php", "ts.test", "folder-source.open"
	// or "tool:terminal".
	ID string
	// Size defaults to th.Size.Icon.
	Size unit.Dp
	// Color tints monochrome tool glyphs; it defaults to th.Color.Text
	// (th.Color.TextDisabled in a disabled context). File icons ignore it.
	Color color.NRGBA
}

// toolPrefix starts the IDs of tool glyphs.
const toolPrefix = "tool:"

// Tool returns the tool-window glyph name (see the Tool* constants and
// Tools). Unknown names yield an icon that draws nothing.
func Tool(name ToolName) Icon {
	if a, ok := toolAliases[name]; ok {
		name = a
	}
	return Icon{ID: toolPrefix + string(name)}
}

// Tinted returns a copy of the icon drawn in col (tool glyphs only).
func (i Icon) Tinted(col color.NRGBA) Icon { i.Color = col; return i }

// Sized returns a copy of the icon with the given size.
func (i Icon) Sized(s unit.Dp) Icon { i.Size = s; return i }

// Valid reports whether the icon has a drawing.
func (i Icon) Valid() bool { return lookup(i.ID) != nil }

// Mono reports whether the icon is a monochrome glyph tinted by Color.
func (i Icon) Mono() bool {
	d := lookup(i.ID)
	return d != nil && d.mono
}

// registry holds every definition by ID.
var registry = map[string]*def{}

// buildRegistry fills registry from fileDefs (after the variants were
// added) and toolDefs.
func buildRegistry() {
	for id, d := range fileDefs {
		registry[id] = &d
	}
	for name, f := range toolDefs {
		registry[toolPrefix+string(name)] = &def{mono: true, paint: func(d *drawer, _ *paintCtx) {
			f(d)
			d.fill(colorOpaque)
		}}
	}
}

// lookup returns the definition of id, or nil.
func lookup(id string) *def { return registry[id] }

// colorOpaque paints the coverage of monochrome glyphs before tinting.
var colorOpaque = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

// All returns every file and folder icon (including test and open
// variants) followed by every tool glyph, sorted by ID within each group.
// It feeds catalogs and tests.
func All() []Icon {
	ids := make([]string, 0, len(fileDefs))
	for id := range fileDefs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]Icon, 0, len(ids)+len(toolOrder))
	for _, id := range ids {
		out = append(out, Icon{ID: id})
	}
	for _, t := range toolOrder {
		out = append(out, Tool(t))
	}
	return out
}

// For returns the icon for a file or directory. name may be a base name or
// a slash-separated path (backslashes are accepted too).
func For(name string, dir bool, h Hints) Icon {
	name = strings.ReplaceAll(name, "\\", "/")
	base := path.Base(strings.TrimRight(name, "/"))
	if dir {
		return Icon{ID: folderKind(base, h)}
	}
	k := fileKind(base)
	if registry[k+testSuffix] != nil && (h.Test || h.InTests || IsTest(name)) {
		k += testSuffix
	}
	return Icon{ID: k}
}

func folderKind(base string, h Hints) string {
	lb := strings.ToLower(base)
	k := KindFolder
	switch {
	case h.LibraryRoot || lb == "node_modules" || lb == "vendor":
		k = KindFolderLibrary
	case h.Excluded:
		k = KindFolderExcluded
	case h.TestRoot:
		k = KindFolderTest
	case h.SourceRoot:
		k = KindFolderSource
	case h.ResourceRoot:
		k = KindFolderResource
	}
	if h.Open {
		k += openSuffix
	}
	return k
}

// testDirs are directory names whose files count as tests.
var testDirs = map[string]bool{
	"tests": true, "test": true, "e2e-tests": true, "e2e": true,
	"__tests__": true, "spec": true, "specs": true,
}

// IsTest reports whether a file path looks like a test by its name
// (*.spec.*, *.test.*, *Test.php, *_test.go, test_*.py …) or by a
// directory part (tests/, test/, e2e-tests/, e2e/, __tests__/, spec/).
func IsTest(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	base := parts[len(parts)-1]
	for _, dir := range parts[:len(parts)-1] {
		if testDirs[strings.ToLower(dir)] {
			return true
		}
	}
	lb := strings.ToLower(base)
	switch {
	case strings.Contains(lb, ".spec.") || strings.Contains(lb, ".test.") || strings.Contains(lb, ".e2e."):
		return true
	case strings.HasSuffix(base, "Test.php") || strings.HasSuffix(base, "Cest.php"):
		return true
	case strings.HasSuffix(lb, "_test.go"), strings.HasSuffix(lb, "_test.py"):
		return true
	case strings.HasPrefix(lb, "test_") && strings.HasSuffix(lb, ".py"):
		return true
	}
	return false
}

// exactNames maps lower-case file names to kinds.
var exactNames = map[string]string{
	"dockerfile":          KindDocker,
	"containerfile":       KindDocker,
	".dockerignore":       KindDockerIgnore,
	"docker-compose.yml":  KindCompose,
	"docker-compose.yaml": KindCompose,
	"compose.yml":         KindCompose,
	"compose.yaml":        KindCompose,
	".gitignore":          KindGitIgnore,
	".gitattributes":      KindGit,
	".gitmodules":         KindGit,
	".gitkeep":            KindGitIgnore,
	".editorconfig":       KindEditorConfig,
	"composer.json":       KindComposer,
	"composer.lock":       KindComposerLock,
	"package.json":        KindPackage,
	"package-lock.json":   KindPackageLock,
	"yarn.lock":           KindPackageLock,
	"pnpm-lock.yaml":      KindPackageLock,
	"phpunit.xml":         KindPHPUnit,
	"phpunit.xml.dist":    KindPHPUnit,
	"phpunit.dist.xml":    KindPHPUnit,
	"makefile":            KindMake,
	"gnumakefile":         KindMake,
	".htaccess":           KindHtaccess,
	"license":             KindText,
	"readme":              KindText,
}

// extKinds maps lower-case extensions (without dot) to kinds.
var extKinds = map[string]string{
	"php": KindPHP, "phtml": KindPHP, "php8": KindPHP, "inc": KindPHP,
	"ts": KindTypeScript, "mts": KindTypeScript, "cts": KindTypeScript,
	"tsx": KindTSX,
	"js":  KindJavaScript, "cjs": KindJavaScript,
	"jsx":  KindJSX,
	"mjs":  KindMJS,
	"json": KindJSON, "jsonc": KindJSON, "json5": KindJSON,
	"yaml": KindYAML, "yml": KindYAML,
	"md": KindMarkdown, "markdown": KindMarkdown,
	"html": KindHTML, "htm": KindHTML, "xhtml": KindHTML,
	"css":  KindCSS,
	"scss": KindSCSS, "sass": KindSCSS, "less": KindSCSS,
	"xml": KindXML, "xsd": KindXML, "xsl": KindXML, "xslt": KindXML,
	"sql": KindSQL,
	"sh":  KindShell, "bash": KindShell, "zsh": KindShell, "fish": KindShell, "ksh": KindShell,
	"go": KindGo,
	"py": KindPython, "pyi": KindPython,
	"twig": KindTwig,
	"txt":  KindText, "text": KindText, "rst": KindText,
	"log": KindLog,
	"png": KindImage, "jpg": KindImage, "jpeg": KindImage, "gif": KindImage,
	"webp": KindImage, "bmp": KindImage, "ico": KindImage, "avif": KindImage, "tif": KindImage, "tiff": KindImage,
	"svg": KindSVG,
	"zip": KindArchive, "tar": KindArchive, "gz": KindArchive, "tgz": KindArchive,
	"bz2": KindArchive, "xz": KindArchive, "7z": KindArchive, "rar": KindArchive,
	"phar": KindArchive, "jar": KindArchive,
	"lock": KindLock,
	"csv":  KindCSV, "tsv": KindCSV,
	"toml": KindTOML,
	"ini":  KindINI, "cfg": KindINI, "conf": KindINI, "neon": KindINI,
	"mk": KindMake,
}

// fileKind classifies a base name.
func fileKind(base string) string {
	lb := strings.ToLower(base)
	if k, ok := exactNames[lb]; ok {
		return k
	}
	switch {
	case lb == ".env" || strings.HasPrefix(lb, ".env.") || strings.HasSuffix(lb, ".env"):
		return KindEnv
	case strings.HasPrefix(lb, "dockerfile.") || strings.HasSuffix(lb, ".dockerfile"):
		return KindDocker
	case (strings.HasPrefix(lb, "docker-compose.") || strings.HasPrefix(lb, "compose.")) &&
		(strings.HasSuffix(lb, ".yml") || strings.HasSuffix(lb, ".yaml")):
		return KindCompose
	case strings.HasPrefix(lb, "tsconfig") && strings.HasSuffix(lb, ".json"):
		return KindTSConfig
	case strings.HasPrefix(lb, "jsconfig") && strings.HasSuffix(lb, ".json"):
		return KindJSConfig
	case strings.HasPrefix(lb, "phpunit") && (strings.HasSuffix(lb, ".xml") || strings.HasSuffix(lb, ".xml.dist")):
		return KindPHPUnit
	}
	if i := strings.LastIndexByte(lb, '.'); i >= 0 && i < len(lb)-1 {
		if k, ok := extKinds[lb[i+1:]]; ok {
			return k
		}
	}
	return KindGeneric
}

// Layout draws the icon in a Size×Size square (th.Size.Icon by default).
// An unknown icon reserves the space only.
func (i Icon) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := i.Size
	if size == 0 {
		size = th.Size.Icon
	}
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	if px <= 0 {
		return layout.Dimensions{Size: sz}
	}
	im, ok := i.imageOp(px, th, gtx.Enabled())
	if !ok {
		return layout.Dimensions{Size: sz}
	}
	defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
	im.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: sz}
}

// Fn adapts the icon to a layout.Widget.
func (i Icon) Fn(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return i.Layout(gtx, th) }
}

// Image renders the icon at px×px pixels for th into a new premultiplied
// image (for contact sheets, window icons and tests). It returns nil for
// an unknown icon or a non-positive size.
func (i Icon) Image(px int, th *tokens.Theme) *image.RGBA {
	df := lookup(i.ID)
	if df == nil || px <= 0 || px > maxPx {
		return nil
	}
	return render(df, px, th.Mode, i.tint(th, true))
}

// maxPx bounds rasterization sizes.
const maxPx = 512

func (i Icon) tint(th *tokens.Theme, enabled bool) color.NRGBA {
	if i.Color.A != 0 {
		return i.Color
	}
	if !enabled {
		return th.Color.TextDisabled
	}
	return th.Color.Text
}

// render draws df at px pixels.
func render(df *def, px int, mode tokens.Mode, tint color.NRGBA) *image.RGBA {
	p := tokens.FileIcons(mode)
	d := newDrawer(px)
	df.paint(d, &paintCtx{p: &p, tint: tint})
	if df.mono {
		tintImage(d.img, tint)
	}
	return d.img
}

// tintImage recolors a white coverage image to col.
func tintImage(img *image.RGBA, col color.NRGBA) {
	ca := uint32(col.A)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		a := uint32(img.Pix[i+3]) * ca / 255
		img.Pix[i] = uint8(uint32(col.R) * a / 255)
		img.Pix[i+1] = uint8(uint32(col.G) * a / 255)
		img.Pix[i+2] = uint8(uint32(col.B) * a / 255)
		img.Pix[i+3] = uint8(a)
	}
}

// cacheKey identifies one rasterization.
type cacheKey struct {
	id    string
	px    int
	mode  tokens.Mode
	tint  color.NRGBA // mono glyphs only
	faded bool        // file icons in a disabled context
}

// maxCache bounds the number of cached rasterizations; the cache is
// cleared when it is full (icons are re-rasterized on demand).
const maxCache = 2048

var cache struct {
	sync.Mutex
	m map[cacheKey]paint.ImageOp
}

// imageOp returns the cached image operation for the icon at px.
func (i Icon) imageOp(px int, th *tokens.Theme, enabled bool) (paint.ImageOp, bool) {
	df := lookup(i.ID)
	if df == nil || px > maxPx {
		return paint.ImageOp{}, false
	}
	k := cacheKey{id: i.ID, px: px, mode: th.Mode}
	if df.mono {
		k.tint = i.tint(th, enabled)
	} else {
		k.faded = !enabled
	}
	cache.Lock()
	defer cache.Unlock()
	if op, ok := cache.m[k]; ok {
		return op, true
	}
	img := render(df, px, th.Mode, k.tint)
	if k.faded {
		fade(img, 0x70)
	}
	if cache.m == nil || len(cache.m) >= maxCache {
		cache.m = make(map[cacheKey]paint.ImageOp)
	}
	op := paint.NewImageOp(img)
	cache.m[k] = op
	return op, true
}

// cached returns the number of cached rasterizations (for tests).
func cached() int {
	cache.Lock()
	defer cache.Unlock()
	return len(cache.m)
}
