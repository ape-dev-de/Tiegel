package fold

import "testing"

func TestVersionAndHiddenRuns(t *testing.T) {
	m := New(20)
	m.SetRanges([]Range{{StartLine: 1, EndLine: 4}, {StartLine: 2, EndLine: 3}, {StartLine: 10, EndLine: 12}})
	v0 := m.Version()
	if m.Version() != v0 {
		t.Fatal("version changed without a change")
	}
	m.Collapse(1)
	m.Collapse(10)
	if m.Version() == v0 {
		t.Fatal("version unchanged after collapse")
	}
	runs := m.HiddenRuns()
	if len(runs) != 2 || runs[0] != (Range{2, 4}) || runs[1] != (Range{11, 12}) {
		t.Fatalf("runs %v", runs)
	}
}
