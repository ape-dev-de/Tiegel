package editorview

import (
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
)

func TestLongLineSegmentsAndHorizontalScrollbar(t *testing.T) {
	long := strings.Repeat("$a = 'x';\t", 2000) // 20000 bytes, one line
	r := newRig(t, long+"\nshort")
	r.e.Highlighter = highlight.PHPDemo
	r.focus()
	r.press("Cmd+Right")
	r.frame()
	if r.e.ScrollOffset().X <= 0 {
		t.Fatal("not scrolled to the end of the long line")
	}
	ent := r.e.cache.m[lineKey{}]
	if ent == nil || ent.from == 0 || len(ent.text) >= len(long) {
		t.Fatal("long line not shaped as a segment")
	}
	if _, ok := r.e.AnchorRect(caret(r.e)); !ok {
		t.Fatal("caret not visible")
	}
	// Clicking near the visible end lands on the right offset.
	want := len(long) - 2
	r.click(r.pointAt(want), 0)
	if caret(r.e) != want {
		t.Fatalf("click on scrolled long line: %d, want %d", caret(r.e), want)
	}
	// Drag the horizontal scrollbar back to the start.
	g := r.e.geo
	if g.hthumb.Empty() {
		t.Fatal("no horizontal thumb")
	}
	from := f32.Pt(float32(g.hthumb.Min.X+2), float32(g.hthumb.Min.Y+1))
	r.drag(from, f32.Pt(float32(g.hbar.Min.X-500), from.Y), 0)
	if r.e.ScrollOffset().X != 0 {
		t.Fatalf("hbar drag: %v", r.e.ScrollOffset())
	}
	// Clicking the track jumps.
	g = r.e.geo
	r.click(f32.Pt(float32(g.hbar.Max.X-3), float32(g.hbar.Min.Y+2)), 0)
	if r.e.ScrollOffset().X == 0 {
		t.Fatal("hbar track click did not scroll")
	}
}

func TestWordDrag(t *testing.T) {
	src := "alpha beta gamma\ndelta epsilon"
	r := newRig(t, src)
	// Double-click on "beta" and drag to "gamma": whole words.
	p0 := r.pointAt(strings.Index(src, "beta") + 1)
	p1 := r.pointAt(strings.Index(src, "gamma") + 2)
	r.clock += 1000
	r.h.Router.Queue(
		r.pointer(pointer.Move, p0, 0, 0),
		r.pointer(pointer.Press, p0, 0, pointer.ButtonPrimary),
		r.pointer(pointer.Release, p0, 0, 0),
	)
	r.clock += 50
	r.h.Router.Queue(r.pointer(pointer.Press, p0, 0, pointer.ButtonPrimary))
	r.frame()
	r.h.Router.Queue(r.pointer(pointer.Move, p1, 0, pointer.ButtonPrimary))
	r.frame()
	if got := r.e.Cursor().SelectedText()[0]; got != "beta gamma" {
		t.Fatalf("word drag forward: %q", got)
	}
	pa := r.pointAt(1)
	r.h.Router.Queue(r.pointer(pointer.Move, pa, 0, pointer.ButtonPrimary))
	r.frame()
	r.h.Router.Queue(r.pointer(pointer.Release, pa, 0, 0))
	r.frame()
	if got := r.e.Cursor().SelectedText()[0]; got != "alpha beta" {
		t.Fatalf("word drag backward: %q", got)
	}
}
