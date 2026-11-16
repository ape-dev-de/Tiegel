package welcome

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestWelcome(t *testing.T) {
	v := &View{Recent: []string{"/home/me/shop", "/home/me/blog"}, Version: "dev", Error: "not a directory"}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, v.Update(gtx)...)
		return v.Layout(gtx, th)
	}
	uitest.Smoke(t, "welcome", image.Pt(1000, 700), w)
	uitest.Smoke(t, "empty", image.Pt(1000, 700), (&View{}).Layout)
	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	// Find the first recent row by scanning for a click that opens it.
	for y := 150; y < 500 && len(evs) == 0; y += 8 {
		h.Click(image.Pt(500, y))
		h.Frame(w)
	}
	if len(evs) == 0 || evs[0] != (OpenEvent{Path: "/home/me/shop"}) {
		t.Fatalf("recent click %v", evs)
	}
	evs = nil
	v.SetPath("/tmp/x")
	h.Frame(w)
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: &v.path.Editor})
		return w(gtx, th)
	})
	h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.Frame(w)
	if len(evs) == 0 || evs[0] != (OpenEvent{Path: "/tmp/x"}) {
		t.Fatalf("submit %v", evs)
	}
}
