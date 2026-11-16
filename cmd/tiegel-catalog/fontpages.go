package main

import (
	"fmt"
	"sync"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// fallbackSample exercises the per-rune fallback of the font chains.
const fallbackSample = "Fallback: café Ångström · 漢字かな 한글 · 😀 ✓ → ≠"

// monoList loads tokens.MonospaceFamilies in the background, as the app
// does, and redraws once it is known.
var monoList struct {
	once  sync.Once
	mu    sync.Mutex
	names []string
}

func monospaceFamilies() []string {
	monoList.once.Do(func() {
		go func() {
			names := tokens.MonospaceFamilies()
			monoList.mu.Lock()
			monoList.names = names
			monoList.mu.Unlock()
			requestRedraw()
		}()
	})
	monoList.mu.Lock()
	defer monoList.mu.Unlock()
	return monoList.names
}

// fontVariants shows the font chains of th and how they render.
func fontVariants(th *tokens.Theme) []variant {
	return []variant{
		{"UI font chain (bold: Title, Heading, Strong)", column(th.Space.XS,
			atoms.Small(string(th.Font.UI.Typeface)).Fn(th),
			atoms.Text("Regular – The quick brown fox jumps over the lazy dog").Fn(th),
			atoms.Strong("Bold – The quick brown fox jumps over the lazy dog").Fn(th),
			atoms.Text(fallbackSample).Fn(th),
		)},
		{"Editor font chain", column(th.Space.XS,
			atoms.Small(string(th.Font.Mono.Typeface)).Fn(th),
			atoms.Code("0O 1lI| {}[]() <?php $x->y() ?? 'z'; // iiii MMMM").Fn(th),
			atoms.Code(fallbackSample).Fn(th),
		)},
	}
}

// settingsHost is a fake host for the settings panel: it applies the
// editor and font events to its preferences and renders the panel with
// th.WithFonts, like the app.
type settingsHost struct {
	p     settings.Panel
	prefs tokens.FontPrefs
	last  string
}

func settingsPage() *page {
	h := &settingsHost{p: settings.Panel{
		Section: 2, TabWidth: 4, Theme: "dark", IdleSeconds: 30,
		Modules: []settings.Module{{ID: "lang-php", Enabled: true, Running: true}},
	}}
	return &page{
		Level: "Organisms", Name: "Settings",
		Glyph:   atoms.GlyphSettings,
		Summary: "Settings dialog. The Editor section chooses the editor font (installed monospace families from tokens.MonospaceFamilies), its size and line height and the UI font size; the preview uses the theme with the chosen FontPrefs.",
		Variants: func(th *tokens.Theme) []variant {
			label := "Settings (Editor section)"
			if h.last != "" {
				label += " – " + h.last
			}
			return []variant{{label, sized(560, func(gtx layout.Context) layout.Dimensions {
				h.p.EditorFonts = monospaceFamilies()
				fth := th.WithFonts(h.prefs)
				for _, ev := range h.p.Update(gtx) {
					h.apply(ev)
				}
				return h.p.Layout(gtx, fth)
			})}}
		},
	}
}

func (h *settingsHost) apply(ev settings.Event) {
	switch e := ev.(type) {
	case settings.FontFamilyEvent:
		h.prefs.EditorFamily, h.p.EditorFont = e.Family, e.Family
	case settings.FontSizeEvent:
		h.prefs.EditorSize, h.p.EditorFontSize = e.Size, e.Size
	case settings.LineHeightEvent:
		h.prefs.LineHeight, h.p.LineHeight = e.Value, e.Value
	case settings.UIFontSizeEvent:
		h.prefs.UISize, h.p.UIFontSize = e.Size, e.Size
	case settings.TabWidthEvent:
		h.p.TabWidth = e.Width
	case settings.ThemeEvent:
		h.p.Theme = e.Theme
	default:
		return
	}
	h.last = fmt.Sprintf("%T %+v", ev, ev)
}
