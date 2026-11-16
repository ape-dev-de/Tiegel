package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var window = image.Pt(1180, 780)

func TestAllPagesLayout(t *testing.T) {
	for _, light := range []bool{false, true} {
		c := newCatalog(light)
		if len(c.pages) < 16 {
			t.Fatalf("only %d pages", len(c.pages))
		}
		for i, p := range c.pages {
			c.selected = i
			d, err := uitest.Layout(c.th, window, func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions {
				return c.Layout(gtx)
			})
			if err != nil {
				t.Fatalf("page %s: %v", p.Name, err)
			}
			if d.Size != window {
				t.Errorf("page %s: size %v, want %v", p.Name, d.Size, window)
			}
			if len(p.Variants(c.th)) == 0 {
				t.Errorf("page %s has no variants", p.Name)
			}
		}
	}
}

func TestSelectPageAndFilter(t *testing.T) {
	c := newCatalog(false)
	if !c.selectPage("checkbox") || c.pages[c.selected].Name != "Checkbox" {
		t.Fatal("selectPage")
	}
	if c.selectPage("nope") {
		t.Fatal("unknown page selected")
	}
	if n := len(c.visible()); n != len(c.pages) {
		t.Fatalf("unfiltered visible = %d", n)
	}
	c.search.SetText("molecules")
	for _, i := range c.visible() {
		if c.pages[i].Level != "Molecules" {
			t.Errorf("filter leaked %s", c.pages[i].Name)
		}
	}
}

func TestThemeToggle(t *testing.T) {
	c := newCatalog(false)
	if c.th.Mode != tokens.ModeDark {
		t.Fatal("default must be dark")
	}
	c.toggle.Text = "x"
	h := uitest.NewHarness(window)
	w := func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions { return c.Layout(gtx) }
	h.Frame(w)
	// The toggle button sits at the right end of the 48dp top bar.
	h.Click(image.Pt(window.X-40, 24))
	h.Frame(w)
	if c.th.Mode != tokens.ModeLight {
		t.Fatal("toggle did not switch to light mode")
	}
}

// TestScreenshots renders every page headlessly. With TIEGEL_SHOTS=<dir>
// the PNGs are written there for visual review.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	for _, light := range []bool{false, true} {
		c := newCatalog(light)
		for i, p := range c.pages {
			c.selected = i
			img, err := uitest.Render(c.th, window, func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions {
				return c.Layout(gtx)
			})
			if err != nil {
				t.Skipf("headless rendering unavailable: %v", err)
			}
			if n := uitest.CountDiffering(img, c.th.Color.Background); n < 10000 {
				t.Errorf("page %s (%s): only %d pixels drawn", p.Name, c.th.Mode, n)
			}
			if dir == "" {
				continue
			}
			name := strings.ReplaceAll(strings.ToLower(p.Name), " ", "-")
			f, err := os.Create(filepath.Join(dir, c.th.Mode.String()+"-"+name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
