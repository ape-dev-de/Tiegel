package editorview

import (
	"testing"
)

func TestInlayHintsShiftColumns(t *testing.T) {
	r := newRig(t, "f(a, b)\n\tx\n")
	base, _ := r.e.AnchorRect(0)
	// "x:" before a (with right padding: 3 columns), "y:" before b.
	r.e.SetInlayHints([]InlayHint{{Offset: 5, Label: "y:", PadRight: true}, {Offset: 2, Label: "x:", PadRight: true}, {Offset: 9, Label: "t"}})
	r.frame()
	col := func(off int) int {
		t.Helper()
		rect, ok := r.e.AnchorRect(off)
		if !ok {
			t.Fatalf("offset %d not visible", off)
		}
		return rect.Min.X - base.Min.X
	}
	for off, want := range map[int]int{0: 0, 2: 5, 3: 6, 5: 11, 6: 12, 9: 5} {
		if got := col(off); got != r.e.met.colX(want) {
			t.Errorf("offset %d at x %d, want col %d", off, got, want)
		}
	}
	// Clicks land on the text; a click into a hint snaps to its position.
	for _, off := range []int{0, 3, 6, 7} {
		r.click(r.pointAt(off), 0)
		if caret(r.e) != off {
			t.Errorf("click at %d → %d", off, caret(r.e))
		}
	}
	p := r.pointAt(0)
	p.X += float32(r.e.met.colX(3))
	r.click(p, 0)
	if caret(r.e) != 2 {
		t.Errorf("click into hint → %d", caret(r.e))
	}

	// Edits move the hints; hints in deleted text vanish.
	r.e.Buffer().Replace(0, 0, []byte("zz"))
	if h := r.e.InlayHints(); len(h) != 3 || h[0].Offset != 4 || h[1].Offset != 7 {
		t.Fatalf("after insert %+v", h)
	}
	r.e.Buffer().Replace(3, 3, nil)
	if h := r.e.InlayHints(); len(h) != 2 || h[0].Offset != 4 {
		t.Fatalf("after delete %+v", h)
	}
	if inlayCols([]lineInlay{{cols: 2}, {at: 3, cols: 1}}) != 3 || !inlayEqual(nil, nil) {
		t.Fatal("helpers")
	}

	// Soft wrap draws no hints.
	r.e.SoftWrap = true
	r.frame()
	if r.e.wrapped && r.e.visInlays(visLine{}) != nil {
		t.Fatal("hints while wrapping")
	}
	r.e.SetInlayHints(nil)
	r.frame()
}
