package cursor

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/wrap"
)

// TestRowMovement moves by visual rows of soft-wrapped lines.
func TestRowMovement(t *testing.T) {
	// Line 0 wraps at 10 cells into "aaaa bbbb " | "cccc dddd".
	b := buffer.NewString("aaaa bbbb cccc dddd\nxy")
	w := &wrap.Model{}
	w.Reset(b.Snapshot(), 10, 4)
	m := New(b)
	defer m.Close()
	m.Lines = fold.New(b.LineCount())
	m.Rows = &wrap.View{Wrap: w}
	m.SetCaret(2)
	m.Move(Down, false)
	if got := m.Primary().Head; got != 12 {
		t.Fatalf("Down = %d, want 12", got)
	}
	m.Move(Down, false)
	if got := m.Primary().Head; got != 22 { // "xy" end (goal 2)
		t.Fatalf("Down to next line = %d, want 22", got)
	}
	m.Move(Up, false)
	m.Move(Up, false)
	if got := m.Primary().Head; got != 2 {
		t.Fatalf("Up twice = %d, want 2", got)
	}
	m.Move(Up, false)
	if got := m.Primary().Head; got != 0 {
		t.Fatalf("Up past the top = %d", got)
	}
	m.SetCaret(21)
	m.Move(Down, false)
	if got := m.Primary().Head; got != b.Len() {
		t.Fatalf("Down past the end = %d", got)
	}
	// End: row end first, then line end; Home: row start, then smart home.
	m.SetCaret(2)
	m.Move(End, false)
	if got := m.Primary().Head; got != 9 {
		t.Fatalf("End = %d, want 9", got)
	}
	m.Move(End, false)
	if got := m.Primary().Head; got != 19 {
		t.Fatalf("End twice = %d, want 19", got)
	}
	m.Move(Home, false)
	if got := m.Primary().Head; got != 10 {
		t.Fatalf("Home = %d, want 10", got)
	}
	m.Move(Home, false)
	if got := m.Primary().Head; got != 0 {
		t.Fatalf("Home twice = %d, want 0", got)
	}
	// Clone caret below by row; nothing below the last row.
	m.SetCaret(3)
	m.AddCursorVertical(1)
	if len(m.Selections()) != 2 || m.Primary().Head != 13 {
		t.Fatalf("clone below: %v", m.Selections())
	}
	m.SetCaret(b.Len())
	m.AddCursorVertical(1)
	if len(m.Selections()) != 1 {
		t.Fatalf("clone below the last row: %v", m.Selections())
	}
}
