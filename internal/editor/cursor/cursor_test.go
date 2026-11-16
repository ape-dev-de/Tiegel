package cursor

import (
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// setup parses a document in which '|' marks a caret/head and '~' marks the
// anchor of the following or preceding head. The last head is primary.
func setup(t *testing.T, doc string) *Model {
	t.Helper()
	var text strings.Builder
	var heads, anchors []int
	for _, r := range doc {
		switch r {
		case '|':
			heads = append(heads, text.Len())
		case '~':
			anchors = append(anchors, text.Len())
		default:
			text.WriteRune(r)
		}
	}
	m := New(buffer.NewString(text.String()))
	t.Cleanup(m.Close)
	sels := make([]Selection, len(heads))
	for i, h := range heads {
		sels[i] = Caret(h)
		if i < len(anchors) {
			sels[i].Anchor = anchors[i]
		}
	}
	if len(sels) > 0 {
		m.SetSelections(sels, len(sels)-1)
	}
	return m
}

// render prints the document with '|' at heads and '~' at anchors.
func render(m *Model) string {
	text := m.buf.String()
	type mark struct {
		off int
		ch  byte
	}
	var marks []mark
	for _, s := range m.sels {
		if !s.Empty() {
			marks = append(marks, mark{s.Anchor, '~'})
		}
		marks = append(marks, mark{s.Head, '|'})
	}
	slices.SortStableFunc(marks, func(a, b mark) int { return b.off - a.off })
	for _, mk := range marks {
		text = text[:mk.off] + string(mk.ch) + text[mk.off:]
	}
	return text
}

func expect(t *testing.T, m *Model, want string) {
	t.Helper()
	if got := render(m); got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

func TestSelectionBasics(t *testing.T) {
	s := Range(5, 2)
	if s.Start() != 2 || s.End() != 5 || s.Empty() || s.Len() != 3 {
		t.Fatal("selection accessors")
	}
	if !Caret(3).Empty() {
		t.Fatal("caret not empty")
	}
}

func TestNormalizeMerges(t *testing.T) {
	m := setup(t, "abcdef")
	m.SetSelections([]Selection{Range(0, 3), Range(2, 5), Caret(5), Caret(5), Range(6, 6)}, 3)
	if len(m.sels) != 2 {
		t.Fatalf("got %v", m.sels)
	}
	if m.sels[0] != Range(0, 5) || m.PrimaryIndex() != 0 {
		t.Fatalf("merged %v primary %d", m.sels, m.primary)
	}
	// Reversed selection keeps direction.
	m.SetSelections([]Selection{Range(4, 1), Range(3, 0)}, 0)
	if m.sels[0] != Range(4, 0) {
		t.Fatalf("reversed merge %v", m.sels)
	}
	m.SetSelections(nil, 0)
	if len(m.sels) != 1 || m.sels[0] != Caret(0) {
		t.Fatal("empty selections")
	}
	m.SetSelections([]Selection{Caret(-5), Caret(99)}, 9)
	if m.sels[0].Head != 0 || m.sels[1].Head != 6 || m.primary != 1 {
		t.Fatalf("clamp %v %d", m.sels, m.primary)
	}
}

func TestCharMovement(t *testing.T) {
	m := setup(t, "a|😀b\r\nc")
	m.Move(Right, false)
	expect(t, m, "a😀|b\r\nc")
	m.Move(Right, false)
	m.Move(Right, false)
	expect(t, m, "a😀b\r\n|c")
	m.Move(Left, false)
	expect(t, m, "a😀b|\r\nc")
	m.Move(Left, true)
	m.Move(Left, true)
	expect(t, m, "a|😀b~\r\nc")
	m.Move(Right, false) // collapse to end
	expect(t, m, "a😀b|\r\nc")
	m.Move(Left, true)
	m.Move(Left, false) // collapse to start
	expect(t, m, "a😀|b\r\nc")
	m.Move(DocEnd, false)
	expect(t, m, "a😀b\r\nc|")
	m.Move(DocStart, true)
	expect(t, m, "|a😀b\r\nc~")
}

func TestWordMovement(t *testing.T) {
	cases := []struct {
		mv       Movement
		in, want string
	}{
		{WordRight, "|$foo = $bar->baz();", "$foo| = $bar->baz();"},
		{WordRight, "$foo| = $bar->baz();", "$foo =| $bar->baz();"},
		{WordRight, "$foo =| $bar->baz();", "$foo = $bar|->baz();"},
		{WordRight, "$foo = $bar|->baz();", "$foo = $bar->|baz();"},
		{WordRight, "a|\nb", "a\n|b"},
		{WordRight, "x=|$y", "x=$y|"},
		{WordRight, "ab  |", "ab  |"},
		{WordRight, "a|   \nb", "a   |\nb"},
		{WordLeft, "$foo = $bar|->baz();", "$foo = |$bar->baz();"},
		{WordLeft, "$foo = |$bar", "$foo |= $bar"},
		{WordLeft, "$foo |= $bar", "|$foo = $bar"},
		{WordLeft, "a\n|b", "a|\nb"},
		{WordLeft, "  |x", "|  x"},
		{WordLeft, "über|", "|über"},
		{WordLeft, "a|\r\nb", "|a\r\nb"},
		{WordRight, "Foo\\|Bar", "Foo\\Bar|"},
	}
	for _, c := range cases {
		m := setup(t, c.in)
		m.Move(c.mv, false)
		if got := render(m); got != c.want {
			t.Errorf("%d on %q: got %q want %q", c.mv, c.in, got, c.want)
		}
	}
}

func TestSmartHomeAndEnd(t *testing.T) {
	m := setup(t, "    echo| 1;\nx")
	m.Move(Home, false)
	expect(t, m, "    |echo 1;\nx")
	m.Move(Home, false)
	expect(t, m, "|    echo 1;\nx")
	m.Move(Home, false)
	expect(t, m, "    |echo 1;\nx")
	m.Move(End, true)
	expect(t, m, "    ~echo 1;|\nx")
}

func TestVerticalGoalColumn(t *testing.T) {
	m := setup(t, "long line|\nab\n\tlonger line\n")
	m.Move(Down, false)
	expect(t, m, "long line\nab|\n\tlonger line\n")
	m.Move(Down, false) // goal col 9 visual, tab = 4 cells
	expect(t, m, "long line\nab\n\tlonge|r line\n")
	m.Move(Up, false)
	m.Move(Up, false)
	expect(t, m, "long line|\nab\n\tlonger line\n")
	m.Move(Up, false)
	expect(t, m, "|long line\nab\n\tlonger line\n")
	m.Move(Right, false) // horizontal move resets goal
	m.Move(Down, false)
	expect(t, m, "long line\na|b\n\tlonger line\n")
	m.Move(DocEnd, false)
	m.Move(Down, false)
	expect(t, m, "long line\nab\n\tlonger line\n|")
}

func TestPageMovement(t *testing.T) {
	doc := strings.Repeat("line\n", 100)
	m := setup(t, "|"+doc)
	m.PageLines = 10
	m.Move(PageDown, false)
	if p := m.buf.OffsetToPos(m.Primary().Head); p.Line != 10 {
		t.Fatalf("PageDown to %v", p)
	}
	m.Move(PageUp, true)
	if m.Primary().Head != 0 || m.Primary().Anchor != m.buf.LineStart(10) {
		t.Fatalf("PageUp %v", m.Primary())
	}
	m.PageLines = 0
	m.TabWidth = 0
	m.SetCaret(0)
	m.Move(PageDown, false)
	if p := m.buf.OffsetToPos(m.Primary().Head); p.Line != 30 {
		t.Fatalf("default PageDown to %v", p)
	}
}

// hideMapper hides document line 1..2.
type hideMapper struct{}

func (hideMapper) DocToVisible(l int) int {
	if l >= 3 {
		return l - 2
	}
	return min(l, 1)
}
func (hideMapper) VisibleToDoc(r int) int {
	if r >= 1 {
		return r + 2
	}
	return r
}
func (hideMapper) VisibleCount() int { return 3 }

func TestVerticalWithLineMapper(t *testing.T) {
	m := setup(t, "a|a\nbb\ncc\ndd\nee")
	m.Lines = hideMapper{}
	m.Move(Down, false)
	expect(t, m, "aa\nbb\ncc\nd|d\nee")
	m.Move(Up, false)
	expect(t, m, "a|a\nbb\ncc\ndd\nee")
}

func TestMultiCursorMovementMerges(t *testing.T) {
	m := setup(t, "ab|c|d")
	m.Move(Right, false)
	expect(t, m, "abc|d|")
	m.Move(Right, false)
	expect(t, m, "abcd|")
	if len(m.Selections()) != 1 {
		t.Fatal("cursors not merged")
	}
}

func TestAddCursorVertical(t *testing.T) {
	m := setup(t, "abc\nab|c\nabc")
	m.AddCursorVertical(1)
	m.AddCursorVertical(1) // no further line: no-op
	expect(t, m, "abc\nab|c\nab|c")
	m.SetCaret(5)
	m.AddCursorVertical(-1)
	m.AddCursorVertical(-1)
	expect(t, m, "a|bc\na|bc\nabc")
	m.CollapseToPrimary()
	if len(m.sels) != 1 || m.Primary().Head != 1 {
		t.Fatalf("collapse %v", m.sels)
	}
	m.SetSelections([]Selection{Range(0, 2)}, 0)
	m.CollapseToPrimary()
	expect(t, m, "ab|c\nabc\nabc")
	m.AddCursor(0)
	if m.PrimaryIndex() != 0 || len(m.Selections()) != 2 {
		t.Fatal("AddCursor")
	}
}

func TestStateRoundTrip(t *testing.T) {
	m := setup(t, "a|b|c")
	st := m.State()
	m.SetCaret(0)
	m.SetState(st)
	expect(t, m, "a|b|c")
	if m.Buffer() == nil {
		t.Fatal("Buffer")
	}
}

func TestExternalEditsShiftCursors(t *testing.T) {
	m := setup(t, "ab|cd|ef")
	if _, err := m.buf.Insert(0, []byte("XX")); err != nil {
		t.Fatal(err)
	}
	expect(t, m, "XXab|cd|ef")
	if _, err := m.buf.Delete(3, 3); err != nil { // deletes "bcd"
		t.Fatal(err)
	}
	expect(t, m, "XXa|ef")
	m.Close()
	m.Close()
	if _, err := m.buf.Insert(0, []byte("Y")); err != nil {
		t.Fatal(err)
	}
	expect(t, m, "YXX|aef")
}

func TestWordAt(t *testing.T) {
	cases := []struct{ doc, want string }{
		{"$fo|o = 1", "$foo"},
		{"|$foo", "$foo"},
		{"$foo| ", "$foo"},
		{"a + |", ""},
		{"|", ""},
		{"x->na|me()", "name"},
		{"über|", "über"},
	}
	for _, c := range cases {
		m := setup(t, c.doc)
		s := m.buf.Snapshot()
		start, end := WordAt(s, m.Primary().Head)
		if got := string(s.Slice(start, end)); got != c.want {
			t.Errorf("WordAt(%q) = %q want %q", c.doc, got, c.want)
		}
	}
}
