package findbar

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestFindBar(t *testing.T) {
	b := &Bar{}
	b.Open("needle", true)
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, b.Update(gtx)...)
		return b.Layout(gtx, th)
	}
	uitest.Smoke(t, "findbar", image.Pt(900, 200), w)
	if b.Query() != "needle" || b.Options() != (Options{}) {
		t.Fatalf("query %q", b.Query())
	}
	for _, m := range []struct {
		matches, cur int
		invalid      bool
		want         string
	}{
		{0, 0, false, "No matches"}, {3, 2, false, "2 of 3"}, {3, 0, false, "3 matches"}, {0, 0, true, "Invalid pattern"},
	} {
		b.Matches, b.Current, b.Invalid = m.matches, m.cur, m.invalid
		if got := b.counter(); got != m.want {
			t.Errorf("counter %q want %q", got, m.want)
		}
	}
	b.Matches, b.Invalid = 2, false
	b.Open("needle", true)
	h := uitest.NewHarness(image.Pt(900, 200))
	h.Frame(w)
	h.Frame(w)
	// Typing changes the query, Enter goes to the next match, Escape closes.
	h.Router.Queue(key.EditEvent{Range: key.Range{Start: 6, End: 6}, Text: "s"})
	h.Frame(w)
	h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.Frame(w)
	h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.Frame(w)
	var q, next, closed bool
	for _, e := range evs {
		switch e := e.(type) {
		case QueryEvent:
			q = e.Query == "needles"
		case NextEvent:
			next = true
		case CloseEvent:
			closed = true
		}
	}
	if !q || !next || !closed {
		t.Fatalf("events %v", evs)
	}
}
