package agentchat

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestPlanModeCheckboxAndBody(t *testing.T) {
	size := image.Pt(420, 600)
	c := &Chat{PlanAvailable: true}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, c.Update(gtx)...)
		return c.Layout(gtx, th)
	}
	// The checkbox sits at the left of the bottom row.
	h := uitest.NewHarness(size)
	h.Frame(w)
	for y := size.Y - 4; y > size.Y-60 && len(evs) == 0; y -= 4 {
		h.Click(image.Pt(20, y))
		h.Frame(w)
	}
	if len(evs) != 1 || evs[0] != (PlanModeEvent{On: true}) {
		t.Fatalf("%v", evs)
	}
	c.PlanMode = true
	c.submit("Plan the change")
	evs = append(evs[:0], c.pending...)
	if len(evs) != 1 || evs[0] != (SendEvent{Text: "Plan the change", Plan: true}) {
		t.Fatalf("%v", evs)
	}
	called := false
	c.Body = func(gtx layout.Context) layout.Dimensions {
		called = true
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	c.Changes = []Change{{Path: "a.php", Added: 1}}
	uitest.Smoke(t, "body", size, c.Layout)
	if !called {
		t.Fatal("body not drawn")
	}
	t2 := &Chat{Entries: []Entry{{Key: 0, Kind: User, Text: "hi"}}}
	uitest.Smoke(t, "transcript", size, t2.LayoutTranscript)
}
