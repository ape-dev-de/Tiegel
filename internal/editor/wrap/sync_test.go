package wrap

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// TestModelSync: a model that missed an edit is detected (also when the
// line count did not change) and re-wrapped by Sync.
func TestModelSync(t *testing.T) {
	b := buffer.NewString("a\nb\nc")
	m := &Model{}
	if m.Sync(b.Snapshot()) {
		t.Fatal("Sync before Reset")
	}
	m.Reset(b.Snapshot(), 10, 4)
	if m.Sync(b.Snapshot()) {
		t.Fatal("Sync of an up-to-date model")
	}
	// A missed edit that keeps the line count, then a seen one.
	if _, err := b.Replace(0, 1, []byte(strings.Repeat("x", 25))); err != nil {
		t.Fatal(err)
	}
	e, err := b.Replace(b.Len(), 0, []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	m.ApplyEdit(b.Snapshot(), e)
	if m.Valid(10, 4) {
		t.Fatal("model valid after a missed edit")
	}
	if !m.Sync(b.Snapshot()) || m.Rows(0) != 3 || m.Total() != 5 || !m.Valid(10, 4) {
		t.Fatalf("after Sync: rows(0) %d total %d", m.Rows(0), m.Total())
	}
	// An edit Sync already caught up with is not applied twice.
	b.OnEdit(func(e buffer.Edit) { m.Sync(b.Snapshot()); m.ApplyEdit(b.Snapshot(), e) })
	if _, err := b.Replace(0, 0, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	if !m.Valid(10, 4) || m.LineCount() != 4 || m.Total() != 6 {
		t.Fatalf("after a synced edit: lines %d total %d", m.LineCount(), m.Total())
	}
}

// TestViewSyncsStaleModel: the View re-wraps a model that missed an edit
// before mapping offsets to rows.
func TestViewSyncsStaleModel(t *testing.T) {
	b := buffer.NewString("aaaa bbbb cccc dddd\nxy")
	v := &View{Wrap: &Model{}}
	v.Wrap.Reset(b.Snapshot(), 10, 4)
	if _, err := b.Replace(0, 0, []byte("zz\n")); err != nil {
		t.Fatal(err)
	}
	s := b.Snapshot()
	if r, c := v.RowOf(s, s.LineStart(2)); r != 3 || c != 0 || v.RowCount() != 4 {
		t.Fatalf("RowOf after a missed edit = %d,%d of %d rows", r, c, v.RowCount())
	}
}
