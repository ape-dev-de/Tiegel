package fold

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// naive computes the visible doc lines directly.
func naive(m *Model) []int {
	hide := make([]bool, m.lineCount)
	for _, r := range m.ranges {
		if r.collapsed {
			for l := r.StartLine + 1; l <= r.EndLine && l < m.lineCount; l++ {
				hide[l] = true
			}
		}
	}
	var vis []int
	for l, h := range hide {
		if !h {
			vis = append(vis, l)
		}
	}
	return vis
}

func checkMapping(t *testing.T, m *Model) {
	t.Helper()
	vis := naive(m)
	if m.VisibleCount() != len(vis) {
		t.Fatalf("VisibleCount = %d want %d", m.VisibleCount(), len(vis))
	}
	for row, line := range vis {
		if got := m.VisibleToDoc(row); got != line {
			t.Fatalf("VisibleToDoc(%d) = %d want %d", row, got, line)
		}
		if got := m.DocToVisible(line); got != row {
			t.Fatalf("DocToVisible(%d) = %d want %d", line, got, row)
		}
	}
	for l := 0; l < m.lineCount; l++ {
		hidden := !slices.Contains(vis, l)
		if m.IsHidden(l) != hidden {
			t.Fatalf("IsHidden(%d) = %v", l, !hidden)
		}
		if hidden {
			row := m.DocToVisible(l)
			if vis[row] >= l {
				t.Fatalf("hidden line %d maps to row %d (line %d)", l, row, vis[row])
			}
		}
	}
}

func TestBasicFolding(t *testing.T) {
	m := New(10)
	m.SetRanges([]Range{{1, 4}, {2, 3}, {6, 8}, {5, 5}, {7, 20}, {-1, 2}, {1, 4}})
	if got := m.Ranges(); !slices.Equal(got, []Range{{1, 4}, {2, 3}, {6, 8}}) {
		t.Fatalf("Ranges = %v", got)
	}
	checkMapping(t, m)
	if !m.Collapse(2) || m.Collapse(2) {
		t.Fatal("Collapse result")
	}
	checkMapping(t, m)
	m.Collapse(1)
	m.Collapse(6)
	checkMapping(t, m)
	if m.VisibleCount() != 5 || m.HiddenCount() != 5 {
		t.Fatalf("visible %d", m.VisibleCount())
	}
	if got := m.CollapsedRanges(); len(got) != 3 {
		t.Fatalf("collapsed %v", got)
	}
	if m.Collapse(3) || m.Expand(0) {
		t.Fatal("no range at that line")
	}
	m.Toggle(1)
	if m.IsCollapsed(1) || !m.IsCollapsed(2) {
		t.Fatal("Toggle")
	}
	checkMapping(t, m)
	if r, ok := m.RangeAt(6); !ok || r != (Range{6, 8}) {
		t.Fatal("RangeAt")
	}
	if _, ok := m.RangeAt(0); ok {
		t.Fatal("RangeAt(0)")
	}
	m.ExpandAll()
	if m.VisibleCount() != 10 {
		t.Fatal("ExpandAll")
	}
	m.CollapseAll()
	checkMapping(t, m)
	if !m.Reveal(3) || m.IsHidden(3) || m.Reveal(3) {
		t.Fatal("Reveal")
	}
	checkMapping(t, m)
	if m.VisibleToDoc(-5) != 0 || m.VisibleToDoc(999) != 9 || m.DocToVisible(999) != m.VisibleCount()-1 {
		t.Fatal("clamping")
	}
}

func TestStatePreservedOnSetRanges(t *testing.T) {
	m := New(20)
	m.SetRanges([]Range{{2, 5}, {8, 12}})
	m.Collapse(8)
	m.SetRanges([]Range{{2, 6}, {8, 14}})
	if !m.IsCollapsed(8) || m.IsCollapsed(2) {
		t.Fatal("collapsed state lost")
	}
	if m.LineCount() != 20 {
		t.Fatal("LineCount")
	}
	m.SetLineCount(10)
	if len(m.Ranges()) != 1 {
		t.Fatalf("SetLineCount kept %v", m.Ranges())
	}
}

func TestApplyEdit(t *testing.T) {
	b := buffer.NewString("a\nb\nc\nd\ne\nf\ng\nh\n")
	m := New(b.LineCount())
	m.SetRanges([]Range{{1, 3}, {5, 7}})
	m.Collapse(5)
	b.OnEdit(m.ApplyEdit)
	// Insert two lines at line 0: everything shifts down by 2.
	if _, err := b.Insert(0, []byte("x\ny\n")); err != nil {
		t.Fatal(err)
	}
	if got := m.Ranges(); !slices.Equal(got, []Range{{3, 5}, {7, 9}}) || !m.IsCollapsed(7) {
		t.Fatalf("after insert %v", got)
	}
	// Same-line edit: no shift.
	if _, err := b.Insert(0, []byte("z")); err != nil {
		t.Fatal(err)
	}
	// Delete lines 4..5 (inside first range, removing its end line).
	if _, err := b.Delete(b.LineStart(4), b.LineStart(6)-b.LineStart(4)); err != nil {
		t.Fatal(err)
	}
	if got := m.Ranges(); !slices.Equal(got, []Range{{3, 4}, {5, 7}}) {
		t.Fatalf("after delete %v", got)
	}
	// Delete lines 4..5, which contain the header of the second range.
	if _, err := b.Delete(b.LineStart(4), b.LineStart(6)-b.LineStart(4)); err != nil {
		t.Fatal(err)
	}
	if got := m.Ranges(); !slices.Equal(got, []Range{{3, 4}}) {
		t.Fatalf("after header delete %v", got)
	}
	if m.LineCount() != b.LineCount() {
		t.Fatalf("line count %d vs %d", m.LineCount(), b.LineCount())
	}
	checkMapping(t, m)
}

func TestRandomMapping(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.IntN(60)
		m := New(n)
		var rs []Range
		for k := rng.IntN(12); k > 0; k-- {
			s := rng.IntN(n)
			rs = append(rs, Range{s, s + rng.IntN(n-s)})
		}
		m.SetRanges(rs)
		for _, r := range m.Ranges() {
			if rng.IntN(2) == 0 {
				m.Collapse(r.StartLine)
			}
		}
		checkMapping(t, m)
	}
}

func BenchmarkMapping(b *testing.B) {
	m := New(100_000)
	var rs []Range
	for l := 0; l+5 < 100_000; l += 10 {
		rs = append(rs, Range{l, l + 5})
	}
	m.SetRanges(rs)
	m.CollapseAll()
	m.VisibleCount()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = m.VisibleToDoc(m.DocToVisible(i % 100_000))
	}
}
