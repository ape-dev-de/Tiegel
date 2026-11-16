package settings

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// choose opens the select d by scanning for its field at x and clicks
// option i. It returns the events reported for the click.
func choose(t *testing.T, h *uitest.Harness, p *Panel, d *molecules.Dropdown, x, i int) []Event {
	t.Helper()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	top := -1
	for y := 40; y < 700; y += 4 {
		h.Click(image.Pt(x, y))
		h.Frame(w)
		if d.Open {
			top = y
			break
		}
		for _, o := range []*molecules.Dropdown{p.fontSel, p.sizeSel, p.lineSel, p.uiSizeSel, p.tabSel, p.themeSel} {
			o.Open = false
		}
		h.Frame(w)
	}
	if top < 0 {
		t.Fatal("dropdown field not found")
	}
	// The click hit the field somewhere within its height (28px): scan
	// for the row of option i, reopening after a miss.
	for start := top - 28; start <= top+4; start += 4 {
		if !d.Open {
			h.Click(image.Pt(x, top))
			h.Frame(w)
		}
		evs = nil
		h.Click(image.Pt(x, start+28+8+24*i+12))
		h.Frame(w)
		h.Frame(w)
		if len(evs) > 0 && d.Selected == i {
			return evs
		}
	}
	return evs
}

func TestFontSettings(t *testing.T) {
	p := &Panel{Section: indexOf(Sections, "Editor"), TabWidth: 4, Theme: "dark",
		EditorFont: "Fancy Mono", EditorFonts: []string{"Menlo", "Monaco", "Go Mono"},
		EditorFontSize: 15, LineHeight: 1.6, UIFontSize: 0}
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		p.Update(gtx)
		return p.Layout(gtx, th)
	}
	uitest.Smoke(t, "editor fonts", image.Pt(1000, 700), w)
	// Default first, installed families, then the missing current one.
	if got := p.fontValues; len(got) != 5 || got[0] != "" || got[1] != "Menlo" || got[4] != "Fancy Mono" {
		t.Fatalf("font values %q", got)
	}
	if p.fontSel.Selected != 4 || p.sizeSel.Selected != indexOf(FontSizes, 15) ||
		p.lineSel.Selected != indexOf(LineHeights, 1.6) || p.uiSizeSel.Selected != indexOf(UIFontSizes, 13) {
		t.Fatalf("selection font %d size %d line %d ui %d", p.fontSel.Selected, p.sizeSel.Selected, p.lineSel.Selected, p.uiSizeSel.Selected)
	}
	// A value outside the choices is shown as placeholder.
	p.EditorFontSize = 17
	uitest.Smoke(t, "odd size", image.Pt(1000, 700), w)
	if p.sizeSel.Selected != -1 || p.sizeSel.Placeholder != "17" {
		t.Fatalf("odd size: %d %q", p.sizeSel.Selected, p.sizeSel.Placeholder)
	}
	p.EditorFontSize = 15
	// An installed current family is not duplicated; the list follows the host.
	p.EditorFont, p.EditorFonts = "menlo", []string{"Menlo"}
	uitest.Smoke(t, "installed", image.Pt(1000, 700), w)
	if len(p.fontValues) != 2 || p.fontIndex() != 1 {
		t.Fatalf("font values %q index %d", p.fontValues, p.fontIndex())
	}
	p.EditorFonts, p.EditorFont = nil, ""
	uitest.Smoke(t, "no list", image.Pt(1000, 700), w)
	if len(p.fontValues) != 1 || p.fontIndex() != 0 {
		t.Fatalf("empty list: %q", p.fontValues)
	}

	// Choosing entries reports events.
	p.EditorFonts = []string{"Menlo", "Monaco"}
	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	x := 180 + 1 + 16 + 140 + 60
	for _, c := range []struct {
		d    *molecules.Dropdown
		i    int
		want Event
	}{
		{p.fontSel, 2, FontFamilyEvent{Family: "Monaco"}},
		{p.fontSel, 0, FontFamilyEvent{Family: ""}},
		{p.sizeSel, 4, FontSizeEvent{Size: 14}},
		{p.lineSel, 0, LineHeightEvent{Value: 1}},
		{p.uiSizeSel, 5, UIFontSizeEvent{Size: 16}},
	} {
		evs := choose(t, h, p, c.d, x, c.i)
		if len(evs) != 1 || evs[0] != c.want {
			t.Errorf("choose %d: events %+v, want %+v", c.i, evs, c.want)
		}
		c.d.Open = false
		h.Frame(w)
	}
}
