package editorview

import (
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
)

// TestSoftWrapEndKeepsLastCluster: End on a continued row goes after the
// row's last cluster: Shift+End selects it, typing appends to the row.
func TestSoftWrapEndKeepsLastCluster(t *testing.T) {
	// Word wrap breaks after a space; a single long word breaks inside it.
	for _, text := range []string{longLine, strings.Repeat("abcdefghij", 20)} {
		r := wrapRig(t, text)
		s := r.e.Buffer().Snapshot()
		_, brk, last := r.e.view.RowBounds(s, 0)
		if last {
			t.Fatal("line not wrapped")
		}
		r.e.SetCaret(2)
		r.frame()
		r.press("Cmd+Shift+Right")
		if p := r.e.Cursor().Primary(); p.Start() != 2 || p.End() != brk {
			t.Fatalf("Shift+End selected [%d,%d), want [2,%d)", p.Start(), p.End(), brk)
		}
		r.e.SetCaret(2)
		r.frame()
		r.press("Cmd+Right")
		// The caret (and the IME caret) is drawn at the end of row 0.
		cr, ok := r.e.caretRect(r.e.Cursor().Primary())
		if lr, _ := r.e.AnchorRect(brk - 1); !ok || cr.Min.Y != lr.Min.Y || cr.Min.X <= lr.Min.X {
			t.Fatalf("caret rect %v, last cluster of row 0 at %v", cr, lr)
		}
		r.typ("Z")
		r.expectText(text[:brk] + "Z" + text[brk:])
	}
}

// TestSoftWrapBlockSelectContinuationRows: Alt+drag on continuation rows
// selects the columns under the pointer, not the same columns counted
// from the line start.
func TestSoftWrapBlockSelectContinuationRows(t *testing.T) {
	r := wrapRig(t, longLine+"\n"+longLine)
	s := r.e.Buffer().Snapshot()
	start0, _, _ := r.e.view.RowBounds(s, r.e.wrap.Width()+5)
	if start0 == 0 {
		t.Fatal("second row starts at 0")
	}
	ls1 := s.LineStart(1)
	start1 := ls1 + start0
	r.drag(r.pointAt(start0+2), r.pointAt(start1+6), key.ModAlt)
	got := r.e.Cursor().Selections()
	want := []cursor.Selection{cursor.Range(start0+2, start0+6), cursor.Range(start1+2, start1+6)}
	if len(got) != len(want) {
		t.Fatalf("block selection %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Anchor != want[i].Anchor || got[i].Head != want[i].Head {
			t.Fatalf("block selection %v, want %v", got, want)
		}
	}
}
