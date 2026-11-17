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

// settingsHost is a fake host for the settings panel: it applies the set
// and reset events to its sample pages (font settings to its preferences)
// and renders the panel with th.WithFonts, like the app.
type settingsHost struct {
	p     settings.Panel
	prefs tokens.FontPrefs
	last  string
}

func ip(n int) *int { return &n }

// samplePages is a schema-like tree: core pages and plugins.
func samplePages() []settings.Page {
	return []settings.Page{
		{ID: "core:Appearance", Title: "Appearance", Fields: []settings.Field{
			{Key: "ui.theme", Kind: settings.KindEnum, Label: "Theme", Help: "Color theme of the whole application.", Options: []string{"dark", "light", "system"}, Value: "dark", Source: "global", HasGlobal: true},
			{Key: "ui.fontSize", Kind: settings.KindFloat, Label: "UI font size", Value: 13.0, Source: "default", Min: ip(9), Max: ip(24)},
		}},
		{ID: "core:Editor", Title: "Editor", Extra: settings.ExtraFontPreview, Fields: []settings.Field{
			{Key: "editor.fontFamily", Kind: settings.KindString, Label: "Editor font", Value: "", Source: "default", ChoiceDefault: "System default"},
			{Key: "editor.fontSize", Kind: settings.KindFloat, Label: "Editor font size", Value: 13.0, Source: "default", Min: ip(6), Max: ip(48)},
			{Key: "editor.lineHeight", Kind: settings.KindFloat, Label: "Line height", Value: 1.4, Source: "default", Min: ip(1), Max: ip(3)},
			{Key: "editor.tabWidth", Kind: settings.KindInt, Label: "Tab width", Help: "Display width of a tab and the indent size; .editorconfig overrides it per file.", Value: 4.0, Source: "global", HasGlobal: true, Min: ip(1), Max: ip(16)},
			{Key: "editor.insertSpaces", Kind: settings.KindBool, Label: "Indent with spaces", Value: true, Source: "default"},
		}},
		{ID: "core:Files", Title: "Files", Fields: []settings.Field{
			{Key: "files.editorconfig", Kind: settings.KindBool, Label: "Use .editorconfig", Value: true, Source: "default", ProjectScope: true},
			{Key: "files.associations", Kind: settings.KindList, Label: "File type associations", Help: "Pattern=language pairs such as *.tpl=php.", Value: []any{"*.tpl=php"}, Source: "project", HasProject: true, ProjectScope: true},
		}},
		{ID: "core:Interpreter", Title: "Interpreter", Extra: settings.ExtraInterpreter},
		{ID: "core:Updates", Title: "Updates", Extra: settings.ExtraUpdates, Fields: []settings.Field{
			{Key: "update.checkOnStart", Kind: settings.KindBool, Label: "Check for updates on start", Value: false, Source: "default"},
		}},
		{ID: "plugin:git", Title: "git", Module: &settings.Module{ID: "git", Enabled: true, Running: true}, Fields: []settings.Field{
			{Key: "git.updateMethod", Kind: settings.KindEnum, Label: "Update method", Help: "How Pull and Update Project integrate remote changes.", Options: []string{"merge", "rebase"}, Value: "rebase", Source: "project", HasProject: true, ProjectScope: true},
		}},
		{ID: "plugin:php-index", Title: "php-index", Module: &settings.Module{ID: "php-index", Enabled: true, Running: true}, Fields: []settings.Field{
			{Key: "php-index.libraries", Kind: settings.KindBool, Label: "Index libraries (vendor/)", Value: true, Source: "default", ProjectScope: true},
			{Key: "php-index.exclude", Kind: settings.KindList, Label: "Excluded patterns", Value: []any{}, Source: "default", ProjectScope: true},
		}},
		{ID: "plugin:debug-xdebug", Title: "debug-xdebug", Module: &settings.Module{ID: "debug-xdebug", DependsOn: []string{"php-runtime"}}},
	}
}

func settingsPage() *page {
	h := &settingsHost{p: settings.Panel{Pages: samplePages(), Selected: "core:Editor"}}
	return &page{
		Level: "Organisms", Name: "Settings",
		Glyph:   atoms.GlyphSettings,
		Summary: "Settings dialog generated from declared settings (D190): tree with Core pages and Plugins (with on/off switch), search across all settings, value source (default/global/project) and reset. The Editor page offers the installed monospace families (tokens.MonospaceFamilies) and previews the chosen FontPrefs.",
		Variants: func(th *tokens.Theme) []variant {
			label := "Settings"
			if h.last != "" {
				label += " – " + h.last
			}
			return []variant{{label, sized(600, func(gtx layout.Context) layout.Dimensions {
				for i := range h.p.Pages {
					for j := range h.p.Pages[i].Fields {
						if f := &h.p.Pages[i].Fields[j]; f.Key == "editor.fontFamily" {
							f.Choices = monospaceFamilies()
						}
					}
				}
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
	case settings.SetEvent:
		h.set(e.Key, e.Scope, e.Value)
	case settings.ResetEvent:
		h.set(e.Key, e.Scope, nil)
	case settings.ModuleEvent:
		for i := range h.p.Pages {
			if m := h.p.Pages[i].Module; m != nil && m.ID == e.ID {
				m.Enabled, m.Running = e.Enable, e.Enable
			}
		}
	default:
		return
	}
	h.last = fmt.Sprintf("%T %+v", ev, ev)
}

// set stores v (nil: reset) in the sample field and applies font settings.
func (h *settingsHost) set(key, scope string, v any) {
	for i := range h.p.Pages {
		for j := range h.p.Pages[i].Fields {
			f := &h.p.Pages[i].Fields[j]
			if f.Key != key {
				continue
			}
			f.Value, f.Source = v, scope
			if scope == settings.ScopeProject {
				f.HasProject = v != nil
			} else {
				f.HasGlobal = v != nil
			}
			if v == nil {
				f.Source = settings.SourceDefault
			}
		}
	}
	num := func() float32 {
		switch n := v.(type) {
		case float64:
			return float32(n)
		case int:
			return float32(n)
		}
		return 0
	}
	switch key {
	case "editor.fontFamily":
		h.prefs.EditorFamily, _ = v.(string)
	case "editor.fontSize":
		h.prefs.EditorSize = num()
	case "editor.lineHeight":
		h.prefs.LineHeight = num()
	case "ui.fontSize":
		h.prefs.UISize = num()
	}
}
