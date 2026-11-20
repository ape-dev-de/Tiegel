package templates

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// fullBench is newBench with both left slots, the right and the bottom
// panel open.
func fullBench() (*Workbench, Slots) {
	wb, slots := newBench()
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	slots.Panels[SideRight] = Panel{ID: "debug", Title: "Debug", Content: fill}
	return wb, slots
}

func fpt(p image.Point) f32.Point { return f32.Pt(float32(p.X), float32(p.Y)) }

// The bottom corners belong to the side panels (default) or to the
// bottom panel, independently left and right.
func TestWorkbenchCorners(t *testing.T) {
	for _, c := range []struct {
		name        string
		full        [numCorners]bool
		bottomLeft  bool // the bottom panel reaches the left edge
		bottomRight bool // ... and the right edge
	}{
		{"sides full height", [numCorners]bool{}, false, false},
		{"bottom full width", [numCorners]bool{true, true}, true, true},
		{"bottom left", [numCorners]bool{true, false}, true, false},
		{"bottom right", [numCorners]bool{false, true}, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			wb, slots := fullBench()
			wb.BottomFull = c.full
			var evs []Event
			w := collect(wb, &slots, &evs)
			uitest.Smoke(t, c.name, image.Pt(winW, winH), w)
			h := uitest.NewHarness(image.Pt(winW, winH))
			h.Frame(w)
			m := wb.main
			g := wb.geom
			left, structure := g.panels[SideLeft][GroupPrimary], g.panels[SideLeft][GroupSplit]
			right, bottom := g.panels[SideRight][GroupPrimary], g.panels[SideBottom][GroupPrimary]
			if left.Empty() || structure.Empty() || right.Empty() || bottom.Empty() || g.editor.Empty() {
				t.Fatalf("missing rects %+v", g)
			}
			if (bottom.Min.X == m.Min.X) != c.bottomLeft || (bottom.Max.X == m.Max.X) != c.bottomRight {
				t.Fatalf("bottom %v in main %v", bottom, m)
			}
			// The side owning a corner reaches the bottom; otherwise it
			// ends above the bottom panel.
			if (structure.Max.Y == m.Max.Y) == c.bottomLeft || (right.Max.Y == m.Max.Y) == c.bottomRight {
				t.Fatalf("structure %v right %v main %v", structure, right, m)
			}
			if !c.bottomLeft && bottom.Min.X <= structure.Max.X || !c.bottomRight && bottom.Max.X >= right.Min.X {
				t.Fatalf("bottom %v overlaps the sides %v %v", bottom, structure, right)
			}
			if bottom.Max.Y != m.Max.Y || g.editor.Max.Y >= bottom.Min.Y {
				t.Fatalf("bottom %v editor %v", bottom, g.editor)
			}
			// The recorded geometry is where the panels really are.
			for _, probe := range []struct {
				at   image.Point
				full bool
				side string
			}{
				{image.Pt(m.Min.X+20, m.Max.Y-20), c.bottomLeft, "structure"},
				{image.Pt(m.Max.X-20, m.Max.Y-20), c.bottomRight, "debug"},
			} {
				evs = nil
				want := probe.side
				if probe.full {
					want = "problems"
				}
				h.Click(probe.at)
				h.Frame(w)
				if !has(evs, PanelPressEvent{ID: want}) {
					t.Fatalf("press at %v: %v, want %s", probe.at, evs, want)
				}
			}
		})
	}
}

// The drag state machine: a threshold separates clicks from drags; the
// target follows the pointer; release reports the move.
func TestWorkbenchDragStateMachine(t *testing.T) {
	wb, slots := fullBench()
	h := uitest.NewHarness(image.Pt(winW, winH))
	var evs []Event
	h.Frame(collect(wb, &slots, &evs))
	from := slotRef{SideLeft, GroupPrimary}
	origin := wb.geom.panels[SideLeft][GroupPrimary].Min
	local := func(p image.Point) f32.Point { return fpt(p.Sub(origin)) }
	wb.pressDrag("project", "Project", from, origin, f32.Pt(30, 10))
	wb.moveDrag(f32.Pt(33, 12), 6)
	if wb.Dragging() {
		t.Fatal("below the threshold")
	}
	if m, moved := wb.releaseDrag(f32.Pt(33, 12), 6); m != nil || moved {
		t.Fatalf("click became a move: %v %v", m, moved)
	}
	// Over the bottom edge: full width; the preview spans the main area.
	wb.pressDrag("project", "Project", from, origin, f32.Pt(30, 10))
	wb.moveDrag(local(image.Pt(500, wb.main.Max.Y-4)), 6)
	if !wb.Dragging() || !wb.drag.hasTarget || wb.drag.target != (dropTarget{at: slotRef{SideBottom, GroupPrimary}, full: true}) {
		t.Fatalf("target %+v", wb.drag)
	}
	if r := wb.previewRect(wb.drag, wb.drag.target); r.Min.X != wb.main.Min.X || r.Max.X != wb.main.Max.X || r.Max.Y != wb.main.Max.Y {
		t.Fatalf("full width preview %v main %v", r, wb.main)
	}
	// Over the header of the bottom panel: as tab, the preview is that
	// panel.
	ph := wb.geom.panels[SideBottom][GroupPrimary]
	wb.moveDrag(local(ph.Min.Add(image.Pt(ph.Dx()/2, 8))), 6)
	if wb.drag.target != (dropTarget{at: slotRef{SideBottom, GroupPrimary}, tab: true}) || wb.previewRect(wb.drag, wb.drag.target) != ph {
		t.Fatalf("tab target %+v", wb.drag.target)
	}
	// Over its own header: no tab target (the left zone applies, which
	// is where it is).
	wb.moveDrag(f32.Pt(40, 10), 6)
	if wb.drag.target.tab {
		t.Fatalf("own header as tab target %+v", wb.drag.target)
	}
	if m, moved := wb.releaseDrag(f32.Pt(40, 10), 6); m != nil || !moved {
		t.Fatalf("drop on own slot: %v %v", m, moved)
	}
	// Into the right split zone: the preview is the lower part of the
	// right side.
	wb.pressDrag("project", "Project", from, origin, f32.Pt(30, 10))
	to := local(image.Pt(winW-60, 700))
	wb.moveDrag(to, 6)
	r := wb.previewRect(wb.drag, wb.drag.target)
	right := wb.geom.panels[SideRight][GroupPrimary]
	if r.Min.Y <= right.Min.Y || r.Max.X != right.Max.X || r.Max.Y != right.Max.Y {
		t.Fatalf("right split preview %v (right %v)", r, right)
	}
	m, _ := wb.releaseDrag(to, 6)
	if m == nil || *m != (MoveEvent{ID: "project", Side: SideRight, Group: GroupSplit}) {
		t.Fatalf("move %v", m)
	}
	// Escape cancels; the rest of the gesture is ignored.
	wb.pressDrag("project", "Project", from, origin, f32.Pt(30, 10))
	wb.moveDrag(to, 6)
	wb.cancelDrag()
	wb.moveDrag(to.Add(f32.Pt(0, 5)), 6)
	if wb.Dragging() {
		t.Fatal("cancelled drag still active")
	}
	if m, moved := wb.releaseDrag(to, 6); m != nil || !moved {
		t.Fatalf("cancelled drag: %v %v", m, moved)
	}
}

// Panel headers are drag sources: to the bottom edge (full width), to
// another header (as tab); Escape cancels; a click is no drag.
func TestWorkbenchHeaderDrag(t *testing.T) {
	wb, slots := fullBench()
	var evs []Event
	w := collect(wb, &slots, &evs)
	h := uitest.NewHarness(image.Pt(winW, winH))
	h.Frame(w)
	g := newGeom(h.Theme)
	head := image.Pt(g.stripe+40, g.mainTop+g.header/2) // project header title
	moves := func() []MoveEvent {
		var out []MoveEvent
		for _, e := range evs {
			if m, ok := e.(MoveEvent); ok {
				out = append(out, m)
			}
		}
		return out
	}
	h.Drag(head, image.Pt(500, winH-statusH-4))
	h.Frame(w)
	if m := moves(); len(m) != 1 || m[0] != (MoveEvent{ID: "project", Side: SideBottom, FullWidth: true}) {
		t.Fatalf("drag to bottom edge: %v", evs)
	}
	evs = nil
	bottom := wb.geom.panels[SideBottom][GroupPrimary]
	h.Drag(head, bottom.Min.Add(image.Pt(bottom.Dx()/2, g.header/2)))
	h.Frame(w)
	if m := moves(); len(m) != 1 || m[0] != (MoveEvent{ID: "project", Side: SideBottom, Tab: true}) {
		t.Fatalf("drag to bottom header: %v", evs)
	}
	// A click and a tiny movement are no drags.
	evs = nil
	h.Click(head)
	h.Frame(w)
	h.Drag(head, head.Add(image.Pt(3, 2)))
	h.Frame(w)
	if m := moves(); len(m) != 0 {
		t.Fatalf("click moved: %v", evs)
	}
	// Escape while dragging cancels.
	evs = nil
	target := image.Pt(winW-60, 300)
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: fpt(head)},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: fpt(head)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: fpt(target)},
	)
	h.Frame(w)
	if !wb.Dragging() {
		t.Fatal("drag not active")
	}
	h.Frame(w) // the Escape filter is declared
	h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.Frame(w)
	if wb.Dragging() {
		t.Fatal("Escape did not cancel")
	}
	h.Router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: fpt(target)})
	h.Frame(w)
	h.Frame(w)
	if m := moves(); len(m) != 0 {
		t.Fatalf("cancelled drag moved: %v", evs)
	}
	// Right click on a header opens the tool menu.
	evs = nil
	h.Router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: fpt(head)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: fpt(head)},
	)
	h.Frame(w)
	menu := false
	for _, e := range evs {
		if m, ok := e.(ToolMenuEvent); ok && m.ID == "project" && m.Pos == head {
			menu = true
		}
	}
	if !menu {
		t.Fatalf("header right click %v", evs)
	}
}

// A drag in progress renders its preview in both themes.
func TestWorkbenchDragPreviewSmoke(t *testing.T) {
	wb, slots := fullBench()
	h := uitest.NewHarness(image.Pt(winW, winH))
	var evs []Event
	h.Frame(collect(wb, &slots, &evs))
	origin := wb.geom.panels[SideLeft][GroupPrimary].Min
	for _, to := range []image.Point{{600, winH - statusH - 4}, {winW - 60, 300}, {winW / 2, wb.geom.panels[SideBottom][GroupPrimary].Min.Y + 8}} {
		wb.pressDrag("project", "Project", slotRef{SideLeft, GroupPrimary}, origin, f32.Pt(30, 10))
		wb.moveDrag(fpt(to.Sub(origin)), 6)
		uitest.Smoke(t, "preview", image.Pt(winW, winH), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return wb.Layout(gtx, th, slots)
		})
		wb.drag = nil
	}
}

// A panel with several tool windows shows them as tabs: a click selects
// one, dragging a tab moves that window.
func TestWorkbenchPanelTabs(t *testing.T) {
	wb, slots := newBench()
	slots.Panels[SideBottom].Tabs = []PanelTab{{ID: "problems", Title: "Problems"}, {ID: "terminal", Title: "Terminal"}}
	var evs []Event
	w := collect(wb, &slots, &evs)
	uitest.Smoke(t, "tabs", image.Pt(winW, winH), w)
	h := uitest.NewHarness(image.Pt(winW, winH))
	h.Frame(w)
	g := newGeom(h.Theme)
	spans := wb.headers[SideBottom][GroupPrimary].spans
	if len(spans) != 2 || spans[1].id != "terminal" || spans[1].x0 <= spans[0].x0 {
		t.Fatalf("tab spans %+v", spans)
	}
	ph := wb.geom.panels[SideBottom][GroupPrimary]
	second := ph.Min.Add(image.Pt((spans[1].x0+spans[1].x1)/2, g.header/2))
	h.Click(second)
	h.Frame(w)
	if !has(evs, SelectEvent{ID: "terminal"}) {
		t.Fatalf("tab click %v", evs)
	}
	evs = nil
	first := ph.Min.Add(image.Pt((spans[0].x0+spans[0].x1)/2, g.header/2))
	h.Click(first)
	h.Frame(w)
	for _, e := range evs {
		if _, ok := e.(SelectEvent); ok {
			t.Fatalf("click on the active tab selected: %v", evs)
		}
	}
	evs = nil
	h.Drag(second, image.Pt(winW-40, 200))
	h.Frame(w)
	if !has(evs, MoveEvent{ID: "terminal", Side: SideRight}) {
		t.Fatalf("tab drag %v", evs)
	}
}

// TestWorkbenchDnDScreenshots renders the "terminal full width, project
// left above" layout and a drag in progress (TIEGEL_SHOTS=<dir> writes
// the PNGs).
func TestWorkbenchDnDScreenshots(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	wb, slots := fullBench()
	slots.Menu = nil
	slots.Panels[SideRight] = Panel{}
	slots.Panels[SideBottom] = Panel{ID: "terminal", Title: "Terminal", Content: fill,
		Tabs: []PanelTab{{ID: "terminal", Title: "Terminal"}, {ID: "problems", Title: "Problems"}}}
	wb.BottomFull = [numCorners]bool{true, true}
	bar := &statusbar.Bar{Path: []statusbar.Crumb{{Text: "shop"}, {Text: "src"}}}
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		slots.Status = func(gtx layout.Context) layout.Dimensions { return bar.Layout(gtx, th) }
		slots.Header = atoms.Strong("Toolbar").Fn(th)
		for _, shot := range []string{"full-width", "drag-preview"} {
			h := uitest.NewHarness(image.Pt(winW, winH))
			h.Theme = th
			w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
				wb.Update(gtx)
				return wb.Layout(gtx, th, slots)
			}
			h.Frame(w)
			if shot == "drag-preview" {
				// From the project header over the right edge (lower
				// part: right split).
				from := wb.geom.panels[SideLeft][GroupPrimary].Min.Add(image.Pt(40, 10))
				to := image.Pt(winW-80, 520)
				h.Router.Queue(
					pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: fpt(from)},
					pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: fpt(from)},
					pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: fpt(to)},
				)
				h.Frame(w)
				if !wb.Dragging() {
					t.Fatal("no drag")
				}
			}
			img, err := uitest.Render(th, image.Pt(winW, winH), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
				return wb.Layout(gtx, th, slots)
			})
			wb.drag, wb.dragTag = nil, nil
			if err != nil {
				t.Skipf("headless rendering unavailable: %v", err)
			}
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, "template-"+shot+"-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
