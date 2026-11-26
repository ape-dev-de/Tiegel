package cursor

import (
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// blockTexts returns the selected texts and head offsets relative to their
// line starts.
func blockTexts(s buffer.Snapshot, sels []Selection) (texts []string, heads []int) {
	for _, sel := range sels {
		texts = append(texts, string(s.Slice(sel.Start(), sel.End())))
		heads = append(heads, s.OffsetToPos(sel.Head).Col)
	}
	return texts, heads
}

func TestBlockSelections(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		b     Block
		texts []string
		heads []int // byte column of each head
		prim  int
	}{
		{"plain", "abcdef\nabcdef\nab\nabcdef", Block{0, 1, 3, 3},
			[]string{"bc", "bc", "b", "bc"}, []int{3, 3, 2, 3}, 3},
		{"upwards and leftwards", "abcdef\nabcdef\nabcdef", Block{2, 4, 0, 1},
			[]string{"bcd", "bcd", "bcd"}, []int{1, 1, 1}, 0},
		{"short line caret at end", "abcdef\nx\n\nabcdef", Block{0, 3, 3, 5},
			[]string{"de", "", "", "de"}, []int{5, 1, 0, 5}, 3},
		{"tabs expanded", "\tab\n    cd\nefghij", Block{0, 4, 2, 6},
			[]string{"ab", "cd", "ij"}, []int{3, 6, 6}, 2},
		{"tab partly covered", "a\tb\nabcdef", Block{0, 2, 1, 5},
			[]string{"\tb", "cde"}, []int{3, 5}, 1},
		{"wide chars", "漢字x\nabcde", Block{0, 2, 1, 4},
			[]string{"字", "cd"}, []int{6, 4}, 1},
		{"zero width", "abc\nabc", Block{0, 1, 1, 1},
			[]string{"", ""}, []int{1, 1}, 1},
		{"clamped lines and columns", "abc\nabc", Block{-3, -1, 9, 2},
			[]string{"ab", "ab"}, []int{2, 2}, 1},
	}
	for _, c := range cases {
		s := buffer.NewString(c.doc).Snapshot()
		sels, prim := c.b.Selections(s, 4, nil)
		texts, heads := blockTexts(s, sels)
		if !slices.Equal(texts, c.texts) || !slices.Equal(heads, c.heads) || prim != c.prim {
			t.Errorf("%s: texts %q heads %v primary %d; want %q %v %d", c.name, texts, heads, prim, c.texts, c.heads, c.prim)
		}
		for _, sel := range sels {
			if !sel.HasGoal || sel.Goal != max(c.b.HeadCol, 0) {
				t.Errorf("%s: goal %v", c.name, sel)
			}
		}
	}
}

func TestBlockSelectionsHiddenAndLimit(t *testing.T) {
	s := buffer.NewString("a1\nb2\nc3\nd4").Snapshot()
	sels, prim := Block{0, 0, 3, 1}.Selections(s, 4, func(l int) bool { return l == 1 || l == 2 })
	if texts, _ := blockTexts(s, sels); !slices.Equal(texts, []string{"a", "d"}) || prim != 1 {
		t.Fatalf("hidden: %q %d", texts, prim)
	}
	if sels, prim := (Block{1, 0, 2, 1}).Selections(s, 4, func(int) bool { return true }); len(sels) != 0 || prim != 0 {
		t.Fatalf("all hidden: %v %d", sels, prim)
	}
	var big []byte
	for i := 0; i < MaxBlockLines+10; i++ {
		big = append(big, "x\n"...)
	}
	bs := buffer.New(big).Snapshot()
	if sels, _ := (Block{0, 0, MaxBlockLines + 5, 1}).Selections(bs, 4, nil); len(sels) != MaxBlockLines {
		t.Fatalf("limit: %d", len(sels))
	}
}

func TestBlockAtAndSetBlock(t *testing.T) {
	m := setup(t, "\tx~yz|\nabc")
	s := m.Buffer().Snapshot()
	b, ok := BlockAt(s, m.Primary(), 4)
	if !ok || b != (Block{0, 5, 0, 7}) {
		t.Fatalf("BlockAt = %v %v", b, ok)
	}
	if _, ok := BlockAt(s, Range(1, 7), 4); ok {
		t.Fatal("multi-line selection gave a block")
	}
	b.HeadLine = 1
	m.SetBlock(b, 4, nil)
	if got := m.SelectedText(); !slices.Equal(got, []string{"yz", ""}) || m.PrimaryIndex() != 1 {
		t.Fatalf("SetBlock: %q primary %d", got, m.PrimaryIndex())
	}
	if p := m.Primary(); p.Head != s.LineStart(1)+3 {
		t.Fatalf("primary head %d", p.Head)
	}
}

func TestCurrentBlock(t *testing.T) {
	m := setup(t, "ab|cd\nab|cd\nab|cd")
	if b := m.CurrentBlock(4); b != (Block{0, 2, 2, 2}) {
		t.Fatalf("carets downwards: %v", b)
	}
	m.SetSelections(m.Selections(), 0)
	if b := m.CurrentBlock(4); b != (Block{2, 2, 0, 2}) {
		t.Fatalf("carets upwards: %v", b)
	}
	m.SetSelections([]Selection{Range(1, 8)}, 0)
	if b := m.CurrentBlock(4); b != (Block{1, 3, 1, 3}) {
		t.Fatalf("multi-line selection: %v", b)
	}
	m.SetSelections([]Selection{Range(3, 1)}, 0)
	if b := m.CurrentBlock(4); b != (Block{0, 3, 0, 1}) {
		t.Fatalf("selection: %v", b)
	}
}

// Cloning carets past a short line keeps the column of the first caret.
func TestAddCursorVerticalKeepsGoal(t *testing.T) {
	m := setup(t, "abcde|f\nab\nabcdef")
	m.AddCursorVertical(1)
	m.AddCursorVertical(1)
	s := m.Buffer().Snapshot()
	var cols []int
	for _, sel := range m.Selections() {
		cols = append(cols, s.OffsetToPos(sel.Head).Col)
	}
	if !slices.Equal(cols, []int{5, 2, 5}) {
		t.Fatalf("columns %v", cols)
	}
}
