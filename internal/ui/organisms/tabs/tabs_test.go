package tabs

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestBar(t *testing.T) {
	b := &Bar{Items: []Item{{ID: "a", Title: "A.php", Modified: true}, {ID: "b", Title: "B.php"}}, Active: "a"}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, b.Update(gtx)...)
		return b.Layout(gtx, th)
	}
	d := uitest.Smoke(t, "tabs", image.Pt(600, 100), w)
	if d.Size.Y != 32 {
		t.Fatalf("height %d", d.Size.Y)
	}
	h := uitest.NewHarness(image.Pt(600, 100))
	h.Frame(w)
	h.Click(image.Pt(20, 16))
	h.Frame(w)
	if len(evs) == 0 || evs[0] != (SelectEvent{ID: "a"}) {
		t.Fatalf("events %v", evs)
	}
	b.ScrollToActive()
	b.Items = b.Items[:1]
	for i := 0; i < 20; i++ {
		b.tab(string(rune('c' + i)))
	}
	h.Frame(w)
	if len(b.tabs) > 2 {
		t.Fatalf("stale tab state kept: %d", len(b.tabs))
	}
}
