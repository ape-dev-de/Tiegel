package palette

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestChooser(t *testing.T) {
	c := &Chooser{}
	c.Open("Go to File", "File name", "")
	for i := 0; i < 20; i++ {
		c.Items = append(c.Items, Item{Label: "File.php", Detail: "src", Glyph: atoms.GlyphFileCode})
	}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, c.Update(gtx)...)
		return c.Layout(gtx, th)
	}
	uitest.Smoke(t, "chooser", image.Pt(800, 600), w)
	c.Open("Go to File", "File name", "")
	h := uitest.NewHarness(image.Pt(800, 600))
	h.Frame(w)
	h.Frame(w)
	for _, n := range []key.Name{key.NameDownArrow, key.NameDownArrow, key.NameUpArrow, key.NamePageDown, key.NamePageUp, key.NameDownArrow} {
		h.Router.Queue(key.Event{Name: n, State: key.Press})
		h.Frame(w)
	}
	if c.Selected != 2 {
		t.Fatalf("selected %d", c.Selected)
	}
	h.Router.Queue(key.EditEvent{Text: "fi"})
	h.Frame(w)
	h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.Frame(w)
	h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.Frame(w)
	var q, choose, cancel bool
	for _, e := range evs {
		switch e := e.(type) {
		case QueryEvent:
			q = e.Query == "fi"
		case ChooseEvent:
			choose = e.Index == 0 && e.Query == "fi"
		case CancelEvent:
			cancel = true
		}
	}
	if !q || !choose || !cancel {
		t.Fatalf("events %+v", evs)
	}
	// Plain input (go to line) reports index -1.
	c.Open("Go to Line", "line", "12")
	c.NoList = true
	if ev := c.choose(); ev.Index != -1 || ev.Query != "12" {
		t.Fatalf("no list choose %+v", ev)
	}
	uitest.Smoke(t, "nolist", image.Pt(800, 600), c.Layout)
	c.NoList, c.Items, c.Message = false, nil, "Indexing…"
	uitest.Smoke(t, "message", image.Pt(800, 600), c.Layout)
}
