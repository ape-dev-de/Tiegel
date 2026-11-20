package vt

import (
	"runtime"
	"strings"
	"testing"
)

// TestReflowScrollbackTrimmed: after a width change the scrollback
// keeps only the content of its lines, not the padding to the full
// width (review wave 9 B1).
func TestReflowScrollbackTrimmed(t *testing.T) {
	term := New(300, 10, 10000)
	for range 10000 {
		feed(term, "ab\r\n")
	}
	before := heapInUse()
	term.Resize(299, 10)
	grown := heapInUse() - before
	runtime.KeepAlive(term)
	// 10 000 lines × 299 padded cells would be far above 20 MB.
	if grown > 20<<20 {
		t.Fatalf("heap grew by %d MB after a resize", grown>>20)
	}
	for i := range term.History() {
		if l := term.Line(i); len(l.Cells) > 2 {
			t.Fatalf("scrollback line %d holds %d cells, want the 2 of its content", i, len(l.Cells))
		}
	}
	// The screen is still full width.
	if l := term.Line(term.History()); len(l.Cells) != 299 {
		t.Fatalf("screen line has %d cells", len(l.Cells))
	}
}

func heapInUse() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// TestIntermediatesBounded: endless intermediate bytes do not grow the
// parser and the sequence is ignored (review wave 9 B5).
func TestIntermediatesBounded(t *testing.T) {
	for _, intro := range []string{"\x1b[", "\x1b"} {
		term := New(10, 2, 0)
		feed(term, intro+strings.Repeat(" ", 100000))
		if n := len(term.p.inter); n > maxInter {
			t.Fatalf("%q: %d intermediates kept", intro, n)
		}
		feed(term, "Ax")
		if got := term.ScreenText(); got != "x" {
			t.Fatalf("%q: screen %q, want the over-long sequence ignored", intro, got)
		}
	}
	// Short sequences still work: DECSCUSR (CSI Ps SP q).
	term := New(10, 2, 0)
	feed(term, "\x1b[4 q")
	if term.Cursor().Shape == 0 {
		t.Fatal("DECSCUSR with one intermediate ignored")
	}
}

// TestReflowCursorPastContent: a cursor behind the content of a line
// with further lines below does not insert blank rows (review wave 9
// B6).
func TestReflowCursorPastContent(t *testing.T) {
	term := New(20, 4, 100)
	feed(term, "ab\r\ncd\r\nef")
	// The cursor stands far behind "ab" in the first row.
	feed(term, "\x1b[1;16H")
	term.Resize(10, 4)
	if got := term.ScreenText(); got != "ab\ncd\nef" {
		t.Fatalf("screen %q, want no blank rows between the lines", got)
	}
	if c := term.Cursor(); c.Row != 0 || c.Col != 9 {
		t.Fatalf("cursor %d,%d, want 0,9 (end of the line's last row)", c.Row, c.Col)
	}
}
