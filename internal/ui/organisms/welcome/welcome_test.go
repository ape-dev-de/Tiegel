package welcome

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
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
	// The remove button appears on hover at the right end of the row.
	rowY := 0
	for y := 150; y < 500; y += 8 {
		evs = nil
		h.Click(image.Pt(500, y))
		h.Frame(w)
		if len(evs) > 0 {
			rowY = y
			break
		}
	}
	// D547: Shift+click opens in a new window, a right click asks for
	// the menu.
	evs = nil
	h.ClickWith(image.Pt(500, rowY), pointer.ButtonPrimary, key.ModShift)
	h.Frame(w)
	if len(evs) == 0 || evs[0] != (OpenEvent{Path: "/home/me/shop", NewWindow: true}) {
		t.Fatalf("shift click %v", evs)
	}
	evs = nil
	h.ClickWith(image.Pt(500, rowY), pointer.ButtonSecondary, 0)
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ContextEvent{Path: "/home/me/shop"}) {
		t.Fatalf("context click %v", evs)
	}
	v.GOOS = "darwin"
	evs = nil
	h.ClickWith(image.Pt(500, rowY), pointer.ButtonPrimary, key.ModCtrl)
	h.Frame(w)
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ContextEvent{Path: "/home/me/shop"}) {
		t.Fatalf("ctrl click %v", evs)
	}
	evs = nil
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(805, float32(rowY))})
	h.Frame(w)
	h.Click(image.Pt(805, rowY))
	h.Frame(w)
	if len(evs) == 0 || evs[0] != (RemoveEvent{Path: "/home/me/shop"}) {
		t.Fatalf("remove %v", evs)
	}
	v.Hints = []string{"Open…  Cmd+O"}
	uitest.Smoke(t, "hints", image.Pt(1000, 700), w)
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
