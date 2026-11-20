package statusbar

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

type fixed struct{}

func (fixed) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(40, 10)}
}

func TestBar(t *testing.T) {
	b := &Bar{
		Path: []Crumb{{Text: "shop"}, {Text: "src"}, {Text: "Shop.php", Glyph: atoms.GlyphFileCode, Tooltip: "/p/shop/src/Shop.php"}},
		Left: []Item{{ID: "msg", Text: "Saved", Tone: ToneMuted, Clickable: true}},
		Right: []Item{
			{ID: "caret", Text: "12:4", Clickable: true, Tooltip: "Go to line"},
			{ID: "lsp", Text: "No language server", Glyph: atoms.GlyphWarning, Tone: ToneWarning, Tooltip: "install one"},
			{ID: "w", Widget: fixed{}},
			{ID: "e", Text: "err", Tone: ToneError}, {ID: "a", Text: "acc", Tone: ToneAccent}, {Text: "plain"},
		},
	}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, b.Update(gtx)...)
		return b.Layout(gtx, th)
	}
	th := tokens.Dark(nil)
	var ops op.Ops
	var r input.Router
	gtx := uitest.Context(&ops, &r, image.Pt(900, 100))
	bh := Height(gtx, th)
	if bh != int(th.Size.StatusH) || bh < 24 {
		t.Fatalf("height %d", bh)
	}
	// The bar grows with the user's UI font size.
	if big := Height(gtx, th.WithFonts(tokens.FontPrefs{UISize: 22})); big <= bh {
		t.Fatalf("height with large font %d", big)
	}
	uitest.Smoke(t, "status", image.Pt(1200, bh), w)
	h := uitest.NewHarness(image.Pt(1200, bh))
	h.Frame(w)
	// Crumbs, separated by chevrons, then the left items.
	for _, c := range []struct {
		x    int
		want Event
	}{
		{12, CrumbEvent{Index: 0}},
		{120, CrumbEvent{Index: 2}},
		{250, ClickEvent{ID: "msg"}},
	} {
		evs = nil
		h.Click(image.Pt(c.x, bh/2))
		h.Frame(w)
		if len(evs) != 1 || evs[0] != c.want {
			t.Fatalf("click at %d: %v, want %v", c.x, evs, c.want)
		}
	}
	// Hovering a tooltip item shows the bubble after the delay.
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(120, float32(bh/2)), Time: 10 * time.Second})
	for i := 0; i < 40; i++ {
		h.Frame(w)
	}
}
