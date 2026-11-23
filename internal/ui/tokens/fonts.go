package tokens

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/fontscan"
)

// Bundled fallback families (the Go fonts shipped with Gio). They end every
// fallback chain so text always renders, even without system fonts.
const (
	bundledUI   = "Go"
	bundledMono = "Go Mono"
)

// Default sizes and limits of the font preferences.
const (
	defaultCodeSize   = 13
	defaultUISize     = 13
	defaultLineHeight = 1.4
)

// NewShaper returns a text shaper that resolves fonts from the installed
// system fonts and falls back to the bundled Go fonts. Runes missing from
// the requested family (CJK, emoji, …) are resolved per rune from any
// installed font that covers them.
//
// The first call in a process scans the system font directories (the
// result is cached on disk in the user cache directory), so it may take a
// moment; later calls are cheap.
//
// Variable system UI fonts (such as the macOS system font) are indexed as
// a single default instance, so bold text would render regular. NewShaper
// therefore registers fixed regular and bold instances of such a font
// under the alias family "Tiegel System UI", which heads every default UI
// chain (shapers without the alias skip it).
func NewShaper() *text.Shaper {
	coll := gofont.Collection()
	coll = append(coll, uiInstances()...)
	return text.NewShaper(text.WithCollection(coll))
}

// uiAlias names the instances of a variable system UI font registered by
// NewShaper.
const uiAlias = "Tiegel System UI"

// textOpticalSize is the optical size (opsz axis, in pt) of the alias
// instances: the UI body size, so fonts with optical sizes use their text
// design instead of the tighter display design.
const textOpticalSize = 13

// varFace is a font face with fixed variation coordinates. The parsed font
// is shared; every shaper gets its own face.
type varFace struct {
	ft   *gtfont.Font
	vars []gtfont.Variation
}

// Face implements font.Face.
func (v varFace) Face() *gtfont.Face {
	f := gtfont.NewFace(v.ft)
	f.SetVariations(v.vars)
	return f
}

var (
	uiOnce  sync.Once
	uiFaces []font.FontFace
)

// uiInstances returns the alias instances for the first installed family
// of the default UI chain (see variableInstances). It never panics.
func uiInstances() []font.FontFace {
	uiOnce.Do(func() {
		defer func() {
			if recover() != nil {
				uiFaces = nil
			}
		}()
		ui, _ := DefaultFamilies(goos)
		uiFaces = variableInstances(systemFootprints(), splitFamilies(ui))
	})
	return uiFaces
}

// systemFootprints returns the indexed system fonts (nil on error).
func systemFootprints() []fontscan.Footprint {
	cache, _ := os.UserCacheDir()
	fps, err := fontscan.SystemFonts(log.New(io.Discard, "", 0), cache)
	if err != nil {
		return nil
	}
	return fps
}

// splitFamilies splits a typeface chain into trimmed family names.
func splitFamilies(chain string) []string {
	var out []string
	for _, f := range strings.Split(chain, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// variableInstances finds the first of families that is installed. If it
// has no face of weight 600 or more but its regular face is variable along
// the weight axis, it returns a regular and a bold instance of it named
// uiAlias. Both instances parse the font separately, because the shaper
// identifies faces by their font.
func variableInstances(fps []fontscan.Footprint, families []string) []font.FontFace {
	for _, fam := range families {
		norm := gtfont.NormalizeFamily(fam)
		if norm == gtfont.NormalizeFamily(uiAlias) {
			continue
		}
		var regular, italic *fontscan.Footprint
		hasBold := false
		for i := range fps {
			fp := &fps[i]
			if fp.Family != norm {
				continue
			}
			if fp.Aspect.Weight >= 600 {
				hasBold = true
			}
			if fp.Aspect.Style == gtfont.StyleNormal && (regular == nil || regularity(fp.Aspect) < regularity(regular.Aspect)) {
				regular = fp
			}
			if fp.Aspect.Style == gtfont.StyleItalic && (italic == nil || regularity(fp.Aspect) < regularity(italic.Aspect)) {
				italic = fp
			}
		}
		if regular == nil {
			continue // not installed: try the next family
		}
		if hasBold {
			return nil
		}
		var out []font.FontFace
		for _, w := range []struct {
			gio  font.Weight
			wght float32
		}{{font.Normal, 400}, {font.Bold, 700}} {
			ft, err := loadFont(regular.Location)
			if err != nil {
				return nil
			}
			vf := varFace{ft: ft, vars: []gtfont.Variation{{Tag: ot.MustNewTag("wght"), Value: w.wght}, {Tag: ot.MustNewTag("opsz"), Value: textOpticalSize}}}
			if !hasNonZero(varFace{ft: ft, vars: []gtfont.Variation{{Tag: ot.MustNewTag("wght"), Value: 700}}}.Face().Coords()) {
				return nil // not a variable weight font
			}
			out = append(out, font.FontFace{Font: font.Font{Typeface: uiAlias, Weight: w.gio}, Face: vf})
		}
		// The italic file of the family (e.g. SFNSItalic) gives italic
		// instances; without them italic text would fall back to upright.
		if italic != nil {
			out = append(out, italicInstances(italic.Location)...)
		}
		return out
	}
	return nil
}

// italicInstances returns a regular and a bold italic instance of the
// variable italic font at loc (nil when it cannot be read or is not
// variable along the weight axis).
func italicInstances(loc fontscan.Location) []font.FontFace {
	var out []font.FontFace
	for _, w := range []struct {
		gio  font.Weight
		wght float32
	}{{font.Normal, 400}, {font.Bold, 700}} {
		ft, err := loadFont(loc)
		if err != nil {
			return nil
		}
		vf := varFace{ft: ft, vars: []gtfont.Variation{{Tag: ot.MustNewTag("wght"), Value: w.wght}, {Tag: ot.MustNewTag("opsz"), Value: textOpticalSize}}}
		if !hasNonZero(varFace{ft: ft, vars: []gtfont.Variation{{Tag: ot.MustNewTag("wght"), Value: 700}}}.Face().Coords()) {
			return nil
		}
		out = append(out, font.FontFace{Font: font.Font{Typeface: uiAlias, Weight: w.gio, Style: font.Italic}, Face: vf})
	}
	return out
}

func hasNonZero[T comparable](cs []T) bool {
	var zero T
	for _, c := range cs {
		if c != zero {
			return true
		}
	}
	return false
}

// loadFont parses the font at loc.
func loadFont(loc fontscan.Location) (*gtfont.Font, error) {
	data, err := os.ReadFile(loc.File)
	if err != nil {
		return nil, fmt.Errorf("read font: %w", err)
	}
	lds, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	if int(loc.Index) >= len(lds) {
		return nil, fmt.Errorf("font index %d out of range", loc.Index)
	}
	return gtfont.NewFont(lds[loc.Index])
}

// NewBundledShaper returns a text shaper loaded with the bundled Go fonts
// only. System fonts are not consulted, so rendering and metrics are
// identical on every machine; tests use it for deterministic output.
func NewBundledShaper() *text.Shaper {
	return text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
}

// DefaultFamilies returns the default UI and monospace font fallback chains
// for the operating system goos (as in runtime.GOOS). Both are
// comma-separated Gio typeface lists (see font.Typeface): the shaper uses
// the first installed family and ends with the bundled Go fonts and a
// generic family. The UI chain starts with the alias of a variable system
// UI font registered by NewShaper. CJK and emoji fonts are listed after
// the Go fonts; runes no listed family covers are resolved from any
// installed font.
func DefaultFamilies(goos string) (ui, mono string) {
	var extra string
	switch goos {
	case "darwin", "ios":
		ui = "System Font, .SF NS, Helvetica Neue"
		mono = "SF Mono, .SF NS Mono, Menlo, Monaco"
		extra = "Hiragino Sans, Apple SD Gothic Neo, Apple Color Emoji"
	case "windows":
		ui = "Segoe UI"
		mono = "Cascadia Mono, Consolas, Courier New"
		extra = "Microsoft YaHei, Malgun Gothic, Segoe UI Emoji, Segoe UI Symbol"
	default:
		ui = "Cantarell, Noto Sans, DejaVu Sans, Liberation Sans"
		mono = "DejaVu Sans Mono, Noto Sans Mono, Liberation Mono"
		extra = "Noto Sans CJK JP, Noto Color Emoji"
	}
	// CJK and emoji families come after the Go fonts: they only serve runes
	// the Go fonts lack, and emoji fonts also cover digits, which must keep
	// coming from the text font.
	return uiAlias + ", " + ui + ", " + bundledUI + ", " + extra + ", sans-serif",
		mono + ", " + bundledMono + ", " + extra + ", monospace"
}

// FontPrefs are the user's font preferences. Zero values select the
// defaults.
type FontPrefs struct {
	// EditorFamily is the editor font family. Empty selects the default
	// monospace chain; a single family gets the default chain appended as
	// fallback. A value containing commas is used as a complete chain.
	EditorFamily string
	// EditorSize is the editor font size in sp (0 = 13, clamped to 6…48).
	EditorSize float32
	// LineHeight is the editor row height as a multiple of the font height
	// (0 = 1.4, clamped to 1.0…2.5).
	LineHeight float32
	// UISize is the body text size of the UI in sp (0 = 13, clamped to
	// 9…24); the whole UI type scale is scaled proportionally.
	UISize float32
}

// Normalized returns p with defaults applied and values clamped to their
// valid ranges. EditorFamily is trimmed but otherwise unchanged.
func (p FontPrefs) Normalized() FontPrefs {
	p.EditorFamily = strings.TrimSpace(p.EditorFamily)
	p.EditorSize = clampOr(p.EditorSize, defaultCodeSize, 6, 48)
	p.LineHeight = clampOr(p.LineHeight, defaultLineHeight, 1, 2.5)
	p.UISize = clampOr(p.UISize, defaultUISize, 9, 24)
	return p
}

func clampOr(v, def, lo, hi float32) float32 {
	if v == 0 || v != v { // zero or NaN
		return def
	}
	return min(hi, max(lo, v))
}

// WithFonts returns a copy of th with the font preferences p applied: the
// editor font chain (Font.Mono), the code size and line height and the
// UI type scale. th itself is not modified.
func (th *Theme) WithFonts(p FontPrefs) *Theme {
	p = p.Normalized()
	out := *th
	_, mono := DefaultFamilies(goos)
	out.Font.Mono = font.Font{Typeface: font.Typeface(editorChain(p.EditorFamily, mono))}
	out.Text = scaledText(p.UISize)
	out.Text.Code = unit.Sp(p.EditorSize)
	out.Text.CodeLineHeight = p.LineHeight
	return &out
}

// editorChain builds the typeface chain for a user-selected family.
func editorChain(family, def string) string {
	switch {
	case family == "":
		return def
	case strings.Contains(family, ","):
		return family
	}
	// Drop the family from the default chain to avoid a duplicate entry.
	parts := []string{quoteFamily(family)}
	for _, f := range strings.Split(def, ",") {
		f = strings.TrimSpace(f)
		if gtfont.NormalizeFamily(f) != gtfont.NormalizeFamily(family) {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, ", ")
}

// quoteFamily quotes a family name if Gio's typeface parser would not read
// it as a single bare name.
func quoteFamily(f string) string {
	if !strings.ContainsAny(f, `"'\`) {
		return f
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(f) + `"`
}

// scaledText returns the UI type scale for body size body (13 = default),
// rounded to half sp.
func scaledText(body float32) TextSizes {
	s := body / defaultUISize
	r := func(v float32) unit.Sp { return unit.Sp(math.Round(float64(v*s)*2) / 2) }
	return TextSizes{
		Small: r(12), Body: unit.Sp(body), Large: r(15), Heading: r(17), Title: r(21),
		Code: defaultCodeSize, CodeLineHeight: defaultLineHeight,
	}
}

// monoAliases are normalized family names of well-known monospace fonts
// whose names do not contain "mono".
var monoAliases = map[string]bool{
	"menlo": true, "monaco": true, "courier": true, "couriernew": true, "consolas": true,
	"lucidaconsole": true, "andalemono": true, "inconsolata": true, "hack": true,
	"sourcecodepro": true, "firacode": true, "cascadiacode": true, "iosevka": true,
	"ibmplexmono": true, "ocraextended": true,
}

var (
	monoOnce     sync.Once
	monoFamilies []string
)

// MonospaceFamilies returns the sorted display names of the installed
// monospace font families, followed by the bundled "Go Mono". The list is
// computed once per process and cached; the first call may take a while
// (it reads font files), so call it from a background goroutine. It is
// safe for concurrent use and never panics; on errors the list contains
// just "Go Mono".
func MonospaceFamilies() []string {
	monoOnce.Do(func() {
		defer func() {
			if recover() != nil {
				monoFamilies = nil
			}
			monoFamilies = append(monoFamilies, bundledMono)
		}()
		monoFamilies = scanMonospace()
	})
	return append([]string(nil), monoFamilies...)
}

// scanMonospace lists installed monospace families (without "Go Mono").
func scanMonospace() []string {
	fps := systemFootprints()
	// Group candidate footprints by normalized family, preferring the
	// regular face of each family.
	best := map[string]fontscan.Footprint{}
	for _, fp := range fps {
		fam := fp.Family
		if strings.HasPrefix(fam, ".") || !isMonoCandidate(fam) {
			continue
		}
		cur, ok := best[fam]
		if !ok || regularity(fp.Aspect) < regularity(cur.Aspect) {
			best[fam] = fp
		}
	}
	seen := map[string]bool{gtfont.NormalizeFamily(bundledMono): true}
	var out []string
	for _, fp := range best {
		name, ok := monospaceName(fp.Location)
		if !ok || strings.HasPrefix(name, ".") || seen[gtfont.NormalizeFamily(name)] {
			continue
		}
		seen[gtfont.NormalizeFamily(name)] = true
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// isMonoCandidate reports whether a normalized family name looks like a
// monospace font; it is a cheap pre-filter before the files are read.
func isMonoCandidate(fam string) bool {
	return strings.Contains(fam, "mono") || strings.Contains(fam, "code") ||
		strings.Contains(fam, "console") || monoAliases[fam]
}

// regularity ranks an aspect by its distance from regular upright (lower
// is more regular).
func regularity(a gtfont.Aspect) int {
	d := int(math.Abs(float64(a.Weight - gtfont.WeightNormal)))
	if a.Style != gtfont.StyleNormal {
		d += 1000
	}
	if a.Stretch != gtfont.StretchNormal && a.Stretch != 0 {
		d += 500
	}
	return d
}

// monospaceName reads the font at loc and returns its display family name
// if its digits and the letters i and M share one advance.
func monospaceName(loc fontscan.Location) (name string, ok bool) {
	defer func() {
		if recover() != nil {
			name, ok = "", false
		}
	}()
	f, err := os.Open(loc.File)
	if err != nil {
		return "", false
	}
	defer f.Close()
	lds, err := ot.NewLoaders(f)
	if err != nil || int(loc.Index) >= len(lds) {
		return "", false
	}
	ld := lds[loc.Index]
	desc, _ := gtfont.Describe(ld, nil)
	ft, err := gtfont.NewFont(ld)
	if err != nil {
		return "", false
	}
	face := gtfont.NewFace(ft)
	var adv float32
	for i, r := range []rune{'0', 'i', 'M', 'W'} {
		gid, found := ft.NominalGlyph(r)
		if !found {
			return "", false
		}
		a := face.HorizontalAdvance(gid)
		if a <= 0 || (i > 0 && a != adv) {
			return "", false
		}
		adv = a
	}
	name = strings.TrimSpace(desc.Family)
	return name, name != ""
}
