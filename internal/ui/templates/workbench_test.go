package templates

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func fill(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Max} }

func box(w, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(w, h)} }
}

func newBench() (*Workbench, Slots) {
	wb := &Workbench{}
	wb.Stripes[SideLeft] = []StripeButton{{ID: "project", Title: "Project", Glyph: atoms.GlyphFolder, Active: true}}
	wb.Stripes[SideBottom] = []StripeButton{{ID: "problems", Title: "Problems", Glyph: atoms.GlyphWarning, Badge: "2", Active: true}, {ID: "git", Title: "Git"}}
	wb.Stripes[SideRight] = []StripeButton{{ID: "debug", Title: "Debug"}}
	slots := Slots{Editor: fill, Status: box(100, 24), Menu: box(100, 20)}
	slots.Panels[SideLeft] = Panel{ID: "project", Title: "Project", Content: fill}
	slots.Panels[SideBottom] = Panel{ID: "problems", Title: "Problems", Content: fill}
	return wb, slots
}

func TestWorkbench(t *testing.T) {
	wb, slots := newBench()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, wb.Update(gtx)...)
		return wb.Layout(gtx, th, slots)
	}
	size := image.Pt(1200, 800)
	if d := uitest.Smoke(t, "workbench", size, w); d.Size != size {
		t.Fatalf("size %v", d.Size)
	}
	slots.Panels[SideRight] = Panel{ID: "debug", Title: "Debug", Content: fill}
	uitest.Smoke(t, "three panels", size, w)

	h := uitest.NewHarness(size)
	h.Frame(w)
	// Left stripe: the first button starts below the menu bar.
	h.Click(image.Pt(8, 20+30))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "project"}) {
		t.Fatalf("left stripe click %v", evs)
	}
	// Bottom stripe sits right above the 24px status bar.
	evs = nil
	h.Click(image.Pt(30, 800-24-10))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "problems"}) {
		t.Fatalf("bottom stripe click %v", evs)
	}
	// Right click opens the tool menu.
	evs = nil
	p := f32.Pt(30, 800-24-10)
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: p},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p},
	)
	h.Frame(w)
	h.Frame(w)
	if len(evs) == 0 || evs[0].(ToolMenuEvent).ID != "problems" {
		t.Fatalf("right click %v", evs)
	}
	// Dragging a bottom button to the right edge moves it.
	evs = nil
	h.Drag(image.Pt(30, 800-24-10), image.Pt(1150, 400))
	h.Frame(w)
	h.Frame(w)
	found := false
	for _, e := range evs {
		if m, ok := e.(MoveEvent); ok && m == (MoveEvent{ID: "problems", Side: SideRight}) {
			found = true
		}
		if _, ok := e.(ToolEvent); ok {
			t.Fatalf("drag also clicked: %v", evs)
		}
	}
	if !found {
		t.Fatalf("drag %v", evs)
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
	bare := &Workbench{HideStripes: true}
	uitest.Smoke(t, "no tools", size, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return bare.Layout(gtx, th, Slots{Editor: fill})
	})
}

// The panel header buttons report hide and options; dragging the left
// divider reports the new size once.
func TestWorkbenchPanelHeaderAndResize(t *testing.T) {
	wb, slots := newBench()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, wb.Update(gtx)...)
		return wb.Layout(gtx, th, slots)
	}
	size := image.Pt(1000, 700)
	h := uitest.NewHarness(size)
	h.Frame(w)
	th := h.Theme
	stripe := int(th.Size.RowH + th.Space.XS)
	leftW := int(float32(1000-2*stripe) * 0.22)
	headerY := 20 + int(th.Size.RowH+th.Space.XS)/2
	rowH := int(th.Size.RowH + th.Space.XS)
	h.Click(image.Pt(stripe+leftW-rowH/2, headerY))
	h.Frame(w)
	if len(evs) == 0 || evs[len(evs)-1] != (HideEvent{ID: "project"}) {
		t.Fatalf("hide %v", evs)
	}
	evs = nil
	h.Click(image.Pt(stripe+leftW-rowH-rowH/2, headerY))
	h.Frame(w)
	opts := false
	for _, e := range evs {
		if m, ok := e.(ToolMenuEvent); ok && m.ID == "project" {
			opts = true
		}
	}
	if !opts || evs[len(evs)-1] != (PanelPressEvent{ID: "project"}) {
		t.Fatalf("options %v", evs)
	}
	evs = nil
	h.Drag(image.Pt(stripe+leftW, 300), image.Pt(stripe+leftW+100, 300))
	h.Frame(w)
	h.Frame(w)
	h.Frame(w)
	n := 0
	for _, e := range evs {
		if r, ok := e.(ResizeEvent); ok {
			n++
			if r.Side != SideLeft || r.Size <= 0.22 {
				t.Fatalf("resize %v", r)
			}
		}
	}
	if n != 1 || wb.Sizes[SideLeft] <= 0.22 {
		t.Fatalf("resize events %d sizes %v", n, wb.Sizes)
	}
}
