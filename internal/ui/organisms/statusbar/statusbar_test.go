package statusbar

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

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
	uitest.Smoke(t, "status", image.Pt(900, 24), w)
	h := uitest.NewHarness(image.Pt(900, 24))
	h.Frame(w)
	h.Click(image.Pt(10, 12))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ClickEvent{ID: "msg"}) {
		t.Fatalf("events %v", evs)
	}
	// Hovering a tooltip item shows the bubble after the delay.
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(10, 12), Time: 10 * time.Second})
	for i := 0; i < 40; i++ {
		h.Frame(w)
	}
}
