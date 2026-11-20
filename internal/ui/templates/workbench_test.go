package templates

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func fill(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Max} }

func box(w, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(w, h)} }
}

// Geometry of newBench in a 1200×800 window (1px = 1dp in uitest).
const (
	winW, winH = 1200, 800
	menuH      = 20
	statusH    = 24
)

func newBench() (*Workbench, Slots) {
	wb := &Workbench{}
	wb.Stripes[SideLeft] = []StripeButton{
		{ID: "project", Title: "Project", Glyph: atoms.GlyphFolder, Active: true, Shortcut: "Cmd+1"},
		{ID: "structure", Title: "Structure", Glyph: atoms.GlyphList, Group: GroupSplit},
	}
	wb.Stripes[SideBottom] = []StripeButton{
		{ID: "problems", Title: "Problems", Glyph: atoms.GlyphWarning, Badge: "2", BadgeKind: atoms.BadgeDanger, Active: true},
		{ID: "git", Title: "Git"},
		{ID: "services", Title: "Services", Glyph: atoms.GlyphContainer, Group: GroupSplit},
	}
	wb.Stripes[SideRight] = []StripeButton{{ID: "debug", Title: "Debug", Glyph: atoms.GlyphDebug}}
	slots := Slots{Editor: fill, Status: box(100, statusH), Menu: box(100, menuH)}
	slots.Panels[SideLeft] = Panel{ID: "project", Title: "Project", Content: fill}
	slots.Panels[SideBottom] = Panel{ID: "problems", Title: "Problems", Content: fill, Actions: box(40, 20)}
	return wb, slots
}

type geom struct {
	th                    *tokens.Theme
	stripe, btn, margin   int
	gap, header, action   int
	mainTop, mainBottom   int
	leftStripeBottomFirst int // y of the first button of the left bottom group (2 buttons)
}

func newGeom(th *tokens.Theme) geom {
	g := geom{th: th, stripe: int(th.Size.StripeW), btn: int(th.Size.StripeButton), gap: int(th.Space.XS),
		header: int(th.Size.PanelHeaderH), action: int(th.Size.ActionButton), mainTop: menuH, mainBottom: winH - statusH}
	g.margin = (g.stripe - g.btn) / 2
	g.leftStripeBottomFirst = g.mainBottom - g.margin - 2*g.btn - g.gap
	return g
}

// stripeBtn returns the center of the i-th top button of a stripe.
func (g geom) stripeBtn(side Side, i int) image.Point {
	x := g.stripe / 2
	if side == SideRight {
		x = winW - g.stripe/2
	}
	return image.Pt(x, g.mainTop+g.margin+i*(g.btn+g.gap)+g.btn/2)
}

func collect(wb *Workbench, slots *Slots, evs *[]Event) uitest.Widget {
	return func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		*evs = append(*evs, wb.Update(gtx)...)
		return wb.Layout(gtx, th, *slots)
	}
}

func has(evs []Event, want Event) bool {
	for _, e := range evs {
		if e == want {
			return true
		}
	}
	return false
}

func TestWorkbench(t *testing.T) {
	wb, slots := newBench()
	var evs []Event
	w := collect(wb, &slots, &evs)
	size := image.Pt(winW, winH)
	if d := uitest.Smoke(t, "workbench", size, w); d.Size != size {
		t.Fatalf("size %v", d.Size)
	}
	slots.Panels[SideRight] = Panel{ID: "debug", Title: "Debug", Content: fill}
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	slots.Splits[SideBottom] = Panel{ID: "services", Title: "Services", Content: fill}
	uitest.Smoke(t, "split panels", size, w)

	h := uitest.NewHarness(size)
	g := newGeom(h.Theme)
	h.Frame(w)
	// Left stripe: the first button starts below the menu bar.
	h.Click(g.stripeBtn(SideLeft, 0))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "project"}) {
		t.Fatalf("left stripe click %v", evs)
	}
	// The split group follows after a separator.
	evs = nil
	p := g.stripeBtn(SideLeft, 1)
	p.Y += int(h.Theme.Space.S) + 1
	h.Click(p)
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "structure"}) {
		t.Fatalf("split group click %v", evs)
	}
	// Bottom tool windows sit at the bottom of the left stripe ...
	evs = nil
	bottom := image.Pt(g.stripe/2, g.leftStripeBottomFirst+g.btn/2)
	h.Click(bottom)
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "problems"}) {
		t.Fatalf("bottom group click %v", evs)
	}
	evs = nil
	h.Click(bottom.Add(image.Pt(0, g.btn+g.gap)))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "git"}) {
		t.Fatalf("bottom group second click %v", evs)
	}
	// ... and bottom split windows at the bottom of the right stripe.
	evs = nil
	h.Click(image.Pt(winW-g.stripe/2, g.mainBottom-g.margin-g.btn/2))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (ToolEvent{ID: "services"}) {
		t.Fatalf("right bottom group click %v", evs)
	}
	// There is no bottom stripe: right above the status bar is the
	// bottom panel.
	evs = nil
	h.Click(image.Pt(winW/2, g.mainBottom-4))
	h.Frame(w)
	for _, e := range evs {
		if _, ok := e.(ToolEvent); ok {
			t.Fatalf("bottom stripe still present: %v", evs)
		}
	}
	// Right click opens the tool menu.
	evs = nil
	fp := f32.Pt(float32(bottom.X), float32(bottom.Y))
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: fp},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: fp},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: fp},
	)
	h.Frame(w)
	h.Frame(w)
	if len(evs) == 0 || evs[0].(ToolMenuEvent).ID != "problems" {
		t.Fatalf("right click %v", evs)
	}
	// Dragging a bottom button to the right edge moves it: the upper
	// part is the primary slot, the lower part the split slot.
	for _, c := range []struct {
		to   image.Point
		want MoveEvent
	}{
		{image.Pt(1150, 300), MoveEvent{ID: "problems", Side: SideRight}},
		{image.Pt(1150, 700), MoveEvent{ID: "problems", Side: SideRight, Group: GroupSplit}},
		{image.Pt(10, 650), MoveEvent{ID: "problems", Side: SideLeft, Group: GroupSplit}},
		{image.Pt(800, 760), MoveEvent{ID: "problems", Side: SideBottom, Group: GroupSplit}},
	} {
		evs = nil
		h.Drag(bottom, c.to)
		h.Frame(w)
		h.Frame(w)
		if !has(evs, c.want) {
			t.Fatalf("drag to %v: %v", c.to, evs)
		}
		for _, e := range evs {
			if _, ok := e.(ToolEvent); ok {
				t.Fatalf("drag also clicked: %v", evs)
			}
		}
	}
	// Dropping into its own slot or the editor center does nothing.
	for _, to := range []image.Point{image.Pt(400, 760), image.Pt(600, 300)} {
		evs = nil
		h.Drag(bottom, to)
		h.Frame(w)
		h.Frame(w)
		for _, e := range evs {
			if _, ok := e.(MoveEvent); ok {
				t.Fatalf("drop to %v moved: %v", to, evs)
			}
		}
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

// The header slot sits below the menu bar with the toolbar height and a
// horizontal padding; the stripes start below it.
func TestWorkbenchHeaderSlot(t *testing.T) {
	wb, slots := newBench()
	var got layout.Constraints
	slots.Header = func(gtx layout.Context) layout.Dimensions {
		got = gtx.Constraints
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 20)}
	}
	var evs []Event
	w := collect(wb, &slots, &evs)
	uitest.Smoke(t, "header", image.Pt(winW, winH), w)
	h := uitest.NewHarness(image.Pt(winW, winH))
	th := h.Theme
	h.Frame(w)
	inner := winW - 2*int(th.Space.S)
	if got.Max != image.Pt(inner, int(th.Size.ToolbarH)) || got.Min.X != inner || got.Min.Y != 0 {
		t.Fatalf("header constraints %+v", got)
	}
	g := newGeom(th)
	p := g.stripeBtn(SideLeft, 0).Add(image.Pt(0, int(th.Size.ToolbarH)))
	h.Click(p)
	h.Frame(w)
	if !has(evs, ToolEvent{ID: "project"}) {
		t.Fatalf("stripe below header: %v", evs)
	}
}

// The panel header buttons report hide, options and maximize; dragging
// the dividers reports the new sizes once.
func TestWorkbenchPanelHeaderAndResize(t *testing.T) {
	wb, slots := newBench()
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	var evs []Event
	w := collect(wb, &slots, &evs)
	h := uitest.NewHarness(image.Pt(winW, winH))
	h.Frame(w)
	g := newGeom(h.Theme)
	mainW := winW - 2*g.stripe
	leftW := int(float32(mainW) * 0.22)
	right := g.stripe + leftW         // right edge of the left panels
	gap := int(h.Theme.Space.XXS)     // between header buttons
	hideX := right - 4 - g.action/2   // hide button center (right inset XS)
	optX := hideX - g.action - gap    // options
	maxX := optX - g.action - gap     // maximize
	headerY := g.mainTop + g.header/2 // primary header
	splitTop := g.mainTop + (g.mainBottom-g.mainTop-1)/2 + 1
	splitHeaderY := splitTop + g.header/2

	h.Click(image.Pt(hideX, headerY))
	h.Frame(w)
	if !has(evs, HideEvent{ID: "project"}) {
		t.Fatalf("hide %v", evs)
	}
	evs = nil
	h.Click(image.Pt(hideX, splitHeaderY))
	h.Frame(w)
	if !has(evs, HideEvent{ID: "structure"}) || !has(evs, PanelPressEvent{ID: "structure"}) {
		t.Fatalf("hide split %v", evs)
	}
	evs = nil
	h.Click(image.Pt(optX, headerY))
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

	// Maximize by button: the panel fills the main area.
	evs = nil
	h.Click(image.Pt(maxX, headerY))
	h.Frame(w)
	if !has(evs, MaximizeEvent{ID: "project", Maximized: true}) || wb.Maximized != "project" {
		t.Fatalf("maximize %v %q", evs, wb.Maximized)
	}
	uitest.Smoke(t, "maximized", image.Pt(winW, winH), w)
	// Its hide button is now at the right edge of the main area.
	evs = nil
	fullHide := winW - g.stripe - 4 - g.action/2
	h.Click(image.Pt(fullHide, headerY))
	h.Frame(w)
	if !has(evs, HideEvent{ID: "project"}) {
		t.Fatalf("hide maximized %v", evs)
	}
	// Double click on the header title restores.
	evs = nil
	h.DoubleClick(image.Pt(g.stripe+60, headerY))
	h.Frame(w)
	if !has(evs, MaximizeEvent{ID: "project"}) || wb.Maximized != "" {
		t.Fatalf("restore %v %q", evs, wb.Maximized)
	}
	// ... and maximizes the split panel.
	evs = nil
	h.DoubleClick(image.Pt(g.stripe+60, splitHeaderY))
	h.Frame(w)
	if !has(evs, MaximizeEvent{ID: "structure", Maximized: true}) {
		t.Fatalf("maximize split %v", evs)
	}
	// A maximized panel that is closed by the host is forgotten.
	slots.Splits[SideLeft] = Panel{}
	h.Frame(w)
	if wb.Maximized != "" {
		t.Fatalf("maximized after close: %q", wb.Maximized)
	}
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	h.Frame(w)

	// Resize the side.
	evs = nil
	h.Drag(image.Pt(right, 300), image.Pt(right+100, 300))
	h.Frame(w)
	h.Frame(w)
	h.Frame(w)
	n := 0
	for _, e := range evs {
		if r, ok := e.(ResizeEvent); ok {
			n++
			if r.Side != SideLeft || r.Split || r.Size <= 0.22 {
				t.Fatalf("resize %v", r)
			}
		}
	}
	if n != 1 || wb.Sizes[SideLeft] <= 0.22 {
		t.Fatalf("resize events %d sizes %v", n, wb.Sizes)
	}
	// Resize the split between project and structure.
	evs = nil
	h.Drag(image.Pt(g.stripe+50, splitTop-1), image.Pt(g.stripe+50, splitTop+120))
	h.Frame(w)
	h.Frame(w)
	h.Frame(w)
	n = 0
	for _, e := range evs {
		if r, ok := e.(ResizeEvent); ok {
			n++
			if r.Side != SideLeft || !r.Split || r.Size <= 0.5 {
				t.Fatalf("split resize %v", r)
			}
		}
	}
	if n != 1 || wb.SplitSize(SideLeft) <= 0.5 {
		t.Fatalf("split resize events %d sizes %v", n, wb.SplitSizes)
	}
}

// Only a split panel open on a side fills the side; split panels at the
// bottom sit side by side.
func TestWorkbenchSplitOnly(t *testing.T) {
	wb, slots := newBench()
	slots.Panels[SideLeft] = Panel{}
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	slots.Splits[SideBottom] = Panel{ID: "services", Title: "Services", Content: fill}
	var evs []Event
	w := collect(wb, &slots, &evs)
	h := uitest.NewHarness(image.Pt(winW, winH))
	h.Frame(w)
	g := newGeom(h.Theme)
	h.Click(image.Pt(g.stripe+60, g.mainTop+g.header/2))
	h.Frame(w)
	if !has(evs, PanelPressEvent{ID: "structure"}) {
		t.Fatalf("split-only panel %v", evs)
	}
	if wb.SplitSize(SideBottom) != 0.5 || wb.Size(SideBottom) != 0.32 || wb.Size(SideRight) != 0.22 {
		t.Fatal("defaults")
	}
}

// Hovering a stripe button shows its tooltip after the delay (title,
// badge and shortcut); right-stripe tooltips open to the left.
func TestWorkbenchStripeTooltip(t *testing.T) {
	wb, slots := newBench()
	var evs []Event
	w := collect(wb, &slots, &evs)
	h := uitest.NewHarness(image.Pt(winW, winH))
	g := newGeom(h.Theme)
	h.Frame(w)
	for _, p := range []image.Point{g.stripeBtn(SideLeft, 0), g.stripeBtn(SideRight, 0), {g.stripe / 2, g.leftStripeBottomFirst + 4}} {
		h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(p.X), float32(p.Y)), Time: 10 * time.Second})
		for range 40 {
			h.Frame(w)
		}
	}
	if len(wb.btns) != 6 {
		t.Fatalf("buttons %d", len(wb.btns))
	}
	// Buttons that left the stripes are dropped.
	wb.Stripes[SideRight] = nil
	h.Frame(w)
	if _, ok := wb.btns["debug"]; ok {
		t.Fatal("stale button kept")
	}
}

// TestWorkbenchScreenshots renders the workbench with toolbar slot, split
// panels and footer headlessly. With TIEGEL_SHOTS=<dir> the PNGs are
// written there for visual review.
func TestWorkbenchScreenshots(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	wb, slots := newBench()
	slots.Menu = nil
	slots.Splits[SideLeft] = Panel{ID: "structure", Title: "Structure", Content: fill}
	slots.Splits[SideBottom] = Panel{ID: "services", Title: "Services", Content: fill}
	bar := &statusbar.Bar{
		Path: []statusbar.Crumb{{Text: "shop"}, {Text: "src"}, {Text: "Shop.php", Glyph: atoms.GlyphFileCode}},
		Right: []statusbar.Item{{Text: "PHP 8.3"}, {Text: "12:4"}, {Text: "LF"}, {Text: "UTF-8"}, {Text: "4 spaces"},
			{Text: "main", Glyph: atoms.GlyphBranch}, {Text: "Xdebug", Glyph: atoms.GlyphListen}},
	}
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		slots.Header = atoms.Strong("Toolbar slot").Fn(th)
		slots.Status = func(gtx layout.Context) layout.Dimensions { return bar.Layout(gtx, th) }
		img, err := uitest.Render(th, image.Pt(winW, winH), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return wb.Layout(gtx, th, slots)
		})
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if n := uitest.CountDiffering(img, th.Color.Background); n < 20000 {
			t.Errorf("%s: only %d pixels drawn", th.Mode, n)
		}
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, "template-workbench-"+th.Mode.String()+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
