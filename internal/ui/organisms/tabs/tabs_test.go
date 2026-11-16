package tabs

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
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
	// Secondary click on the second tab: context event with its position.
	evs = nil
	x := b.ctxOf("a").w + 10
	p := f32.Pt(float32(x), 16)
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: p},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p},
	)
	h.Frame(w)
	h.Frame(w)
	if len(evs) == 0 || evs[0] != (ContextEvent{ID: "b", Pos: image.Pt(x, 16)}) {
		t.Fatalf("context %v", evs)
	}
	b.Dimmed = true
	uitest.Smoke(t, "dimmed", image.Pt(600, 100), w)
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
