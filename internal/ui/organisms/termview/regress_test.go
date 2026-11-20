package termview

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestDragBeyondEdge: dragging out of the view keeps selecting and
// scrolls; a held button of a program's mouse report is released
// there too (review wave 9 U1: Gio sends Leave to the pressed handler).
func TestDragBeyondEdge(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = screen(10)
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	m := r.v.met
	at := func(row, col int) f32.Point {
		return f32.Pt(float32(r.v.origin.X+m.colX(col)+2), float32(r.v.origin.Y+row*m.lineH+m.lineH/2))
	}
	above := f32.Pt(50, -5)
	h.Router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(0, 3), Time: time.Second},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: above, Time: time.Second},
	)
	h.Frame(r.frame)
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: above.Add(f32.Pt(1, 0)), Time: time.Second})
	h.Frame(r.frame)
	if !r.v.sel.dragging {
		t.Fatal("leaving the view ended the drag")
	}
	if r.v.Following() || r.v.Top() >= 10 {
		t.Fatalf("no edge scrolling: top %d", r.v.Top())
	}
	h.Router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: above, Time: time.Second})
	h.Frame(r.frame)
	if from, to := r.v.Selection(); !r.v.HasSelection() || from.Line >= 10 || to != (Pos{10, 3}) {
		t.Fatalf("selection %+v %+v", from, to)
	}

	// Mouse reporting: the release outside reaches the program.
	r = &rec{v: New()}
	sc := screen(0)
	sc.Modes.Mouse = vt.MouseModeButton
	r.v.Screen = sc
	h = uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	r.take()
	h.Router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(1, 1)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: above},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: above},
	)
	h.Frame(r.frame)
	h.Frame(r.frame) // the focus request defers the rest
	released := false
	for _, e := range r.take() {
		if me, ok := e.(MouseEvent); ok && me.Mouse.Action == vt.MouseRelease && me.Mouse.Button == vt.MouseLeft {
			released = true
		}
	}
	if !released {
		t.Fatal("release outside the view not reported")
	}
}

// TestLinkClickNewSnapshot: a link click works when the snapshot was
// replaced between press and release (review wave 9 U5).
func TestLinkClickNewSnapshot(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = screen(10)
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	m := r.v.met
	p := f32.Pt(float32(r.v.origin.X+m.colX(2)+2), float32(r.v.origin.Y+m.lineH+m.lineH/2))
	h.Router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p, Time: time.Second})
	h.Frame(r.frame)
	// New output arrives: the owner sets a fresh snapshot.
	r.v.Screen = screen(10)
	h.Frame(r.frame)
	h.Router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p, Time: time.Second})
	h.Frame(r.frame)
	h.Frame(r.frame)
	for _, e := range r.take() {
		if l, ok := e.(LinkEvent); ok && l.Link.File == "/p/a.php" {
			return
		}
	}
	t.Fatal("link click across snapshots did nothing")
}

// TestDroppedLinesShiftPositions: lines the scrollback drops move the
// scrolled view and the selection with their text (review wave 9 U2).
func TestDroppedLinesShiftPositions(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = screen(10)
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	r.v.follow, r.v.top = false, 5
	r.v.Select(Pos{6, 1}, Pos{7, 3})
	sc := screen(10)
	sc.Dropped = 2
	r.v.Screen = sc
	h.Frame(r.frame)
	if r.v.Top() != 3 {
		t.Fatalf("top %d, want 3", r.v.Top())
	}
	if from, to := r.v.Selection(); from != (Pos{4, 1}) || to != (Pos{5, 3}) {
		t.Fatalf("selection %+v %+v", from, to)
	}
	// Partly dropped: the selection starts at the oldest line.
	sc = screen(10)
	sc.Dropped = 7
	r.v.Screen = sc
	h.Frame(r.frame)
	if from, to := r.v.Selection(); !r.v.HasSelection() || from != (Pos{}) || to != (Pos{0, 3}) {
		t.Fatalf("partly dropped selection %+v %+v", from, to)
	}
	// Entirely dropped: no selection (it would copy other text).
	sc = screen(10)
	sc.Dropped = 9
	r.v.Screen = sc
	h.Frame(r.frame)
	if r.v.HasSelection() || r.v.Top() != 0 {
		t.Fatalf("dropped selection kept (top %d)", r.v.Top())
	}
}
