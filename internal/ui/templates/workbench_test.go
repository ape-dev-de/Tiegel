package templates

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func fill(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Max} }

func box(w, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(w, h)} }
}

func TestWorkbench(t *testing.T) {
	wb := &Workbench{SidebarVisible: true, RightVisible: true,
		ToolTabs:   []ToolTab{{ID: "problems", Title: "Problems", Glyph: atoms.GlyphWarning, Badge: "2"}, {ID: "git", Title: "Git"}},
		ActiveTool: "problems", ToolOpen: true}
	slots := Slots{Sidebar: fill, Tabs: box(100, 32), Find: box(100, 30), Editor: fill, Tool: fill, Right: fill, Status: box(100, 24)}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, wb.Update(gtx)...)
		return wb.Layout(gtx, th, slots)
	}
	size := image.Pt(1200, 800)
	if d := uitest.Smoke(t, "workbench", size, w); d.Size != size {
		t.Fatalf("size %v", d.Size)
	}
	wb.ToolOpen = false
	uitest.Smoke(t, "collapsed", size, w)

	// The collapsed tool header sits above the 24px status bar.
	h := uitest.NewHarness(size)
	h.Frame(w)
	h.Click(image.Pt(1200*22/100+40, 800-24-14))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "problems"}) {
		t.Fatalf("tool click %v", evs)
	}

	// Overlay: clicks outside dismiss, clicks inside do not.
	evs = nil
	slots.Overlay = box(400, 200)
	h.Frame(w)
	h.Click(image.Pt(600, 64+100))
	h.Frame(w)
	if len(evs) != 0 {
		t.Fatalf("inside click dismissed: %v", evs)
	}
	h.Click(image.Pt(20, 700))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (DismissEvent{}) {
		t.Fatalf("outside click %v", evs)
	}
	slots.OverlayCentered = true
	uitest.Smoke(t, "overlay", size, w)
	bare := &Workbench{}
	uitest.Smoke(t, "no tools", size, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return bare.Layout(gtx, th, Slots{Editor: fill})
	})
}
