package completion

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestList(t *testing.T) {
	l := &List{}
	for _, k := range []string{"method", "class", "property", "constant", "keyword", "file", "other"} {
		l.Items = append(l.Items, Item{Label: k + "Name", Detail: "detail", Kind: k, Deprecated: k == "other"})
		if KindGlyph(k) == nil {
			t.Fatalf("glyph %s", k)
		}
	}
	for i := 0; i < 10; i++ {
		l.Items = append(l.Items, Item{Label: "x"})
	}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, l.Update(gtx)...)
		return l.Layout(gtx, th)
	}
	d := uitest.Smoke(t, "completion", image.Pt(800, 600), w)
	if d.Size.Y != MaxRows*24+2 {
		t.Fatalf("height %d", d.Size.Y)
	}
	h := uitest.NewHarness(image.Pt(800, 600))
	h.Frame(w)
	h.Click(image.Pt(50, 24+12))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ChooseEvent{Index: 1}) {
		t.Fatalf("events %v", evs)
	}
	l.Selected = 15
	h.Frame(w)
	l.Selected = 0
	h.Frame(w)
	uitest.Smoke(t, "loading", image.Pt(800, 600), (&List{Loading: true}).Layout)
	uitest.Smoke(t, "empty", image.Pt(800, 600), (&List{}).Layout)
}

func TestInfoAndPlace(t *testing.T) {
	sig := "send(string $to, string $body)"
	uitest.Smoke(t, "signature", image.Pt(800, 600), Info{Text: sig, HighlightStart: 5, HighlightEnd: 15}.Layout)
	uitest.Smoke(t, "hover", image.Pt(800, 600), Info{Text: "Undefined variable $x\n\nfunction send()", Mono: true}.Layout)
	uitest.Smoke(t, "plain", image.Pt(800, 600), Info{Text: "x"}.Layout)
	anchor := image.Rect(100, 100, 108, 116)
	if p := Place(anchor, image.Pt(200, 100), image.Pt(800, 600), false); p != image.Pt(100, 116) {
		t.Fatalf("below %v", p)
	}
	if p := Place(anchor, image.Pt(200, 50), image.Pt(800, 600), true); p != image.Pt(100, 50) {
		t.Fatalf("above %v", p)
	}
	if p := Place(image.Rect(700, 550, 708, 566), image.Pt(200, 100), image.Pt(800, 600), false); p != image.Pt(600, 450) {
		t.Fatalf("flip/shift %v", p)
	}
}
