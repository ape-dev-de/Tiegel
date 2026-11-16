package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestScreenshotsSystemFonts renders the font-related pages with the
// system font shaper. With TIEGEL_SHOTS=<dir> the PNGs are written there
// (prefix "system-") for visual review.
func TestScreenshotsSystemFonts(t *testing.T) {
	uitest.SkipWithoutSystemMono(t)
	dir := os.Getenv("TIEGEL_SHOTS")
	c := newCatalog(false)
	c.th = tokens.New(tokens.ModeDark, tokens.NewShaper())
	for _, name := range []string{"Typography & Spacing", "Settings", "Editor"} {
		if !c.selectPage(name) {
			t.Fatalf("page %q not found", name)
		}
		img, err := uitest.Render(c.th, window, func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions {
			return c.Layout(gtx)
		})
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if n := uitest.CountDiffering(img, c.th.Color.Background); n < 10000 {
			t.Errorf("page %s: only %d pixels drawn", name, n)
		}
		if dir == "" {
			continue
		}
		file := "system-" + strings.NewReplacer(" ", "-", "&", "and").Replace(strings.ToLower(name)) + ".png"
		f, err := os.Create(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

func TestSettingsHostAppliesFontEvents(t *testing.T) {
	h := &settingsHost{}
	for _, ev := range []settings.Event{
		settings.FontFamilyEvent{Family: "Menlo"}, settings.FontSizeEvent{Size: 16},
		settings.LineHeightEvent{Value: 1.8}, settings.UIFontSizeEvent{Size: 15},
		settings.TabWidthEvent{Width: 2}, settings.ThemeEvent{Theme: "light"}, settings.CloseEvent{},
	} {
		h.apply(ev)
	}
	want := tokens.FontPrefs{EditorFamily: "Menlo", EditorSize: 16, LineHeight: 1.8, UISize: 15}
	if h.prefs != want || h.p.EditorFont != "Menlo" || h.p.TabWidth != 2 || h.p.Theme != "light" {
		t.Fatalf("prefs %+v panel %+v", h.prefs, h.p)
	}
	if !strings.Contains(h.last, "ThemeEvent") {
		t.Fatalf("last %q", h.last)
	}
}
