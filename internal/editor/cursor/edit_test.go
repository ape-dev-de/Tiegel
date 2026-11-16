package cursor

import (
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

func TestInsertAtAllCursorsIsOneUndoStep(t *testing.T) {
	m := setup(t, "a|\nb|\nc|")
	for _, r := range "xyz" {
		m.Type(r)
	}
	expect(t, m, "axyz|\nbxyz|\ncxyz|")
	if !m.Undo() {
		t.Fatal("undo failed")
	}
	expect(t, m, "a|\nb|\nc|")
	if !m.Redo() {
		t.Fatal("redo failed")
	}
	expect(t, m, "axyz|\nbxyz|\ncxyz|")
	if m.Redo() {
		t.Fatal("extra redo")
	}
}

func TestTypingBreaksOnMove(t *testing.T) {
	m := setup(t, "|")
	m.Type('a')
	m.Type('b')
	m.Move(Left, false)
	m.Type('c')
	expect(t, m, "ac|b")
	m.Undo()
	expect(t, m, "a|b")
	m.Undo()
	expect(t, m, "|")
	if m.Undo() {
		t.Fatal("nothing left to undo")
	}
}

func TestInsertReplacesSelections(t *testing.T) {
	m := setup(t, "~foo| ~bar|")
	m.Insert("x")
	expect(t, m, "x| x|")
	m.Insert("multi")
	expect(t, m, "xmulti| xmulti|")
	m.Undo()
	expect(t, m, "x| x|")
	m.Undo()
	expect(t, m, "~foo| ~bar|")
}

func TestPaste(t *testing.T) {
	m := setup(t, "a|\nb|")
	m.Paste("1\n2\n")
	expect(t, m, "a1|\nb2|")
	m = setup(t, "a|\r\nb")
	m.Paste("x\ny")
	expect(t, m, "ax\r\ny|\r\nb")
}

func TestNewlineKeepsIndent(t *testing.T) {
	m := setup(t, "\tif (x) {|\n")
	m.Type('\n')
	expect(t, m, "\tif (x) {\n\t|\n")
	m = setup(t, "  ~ab|c")
	m.Newline()
	expect(t, m, "  \n  |c")
}

func TestDeleteBackwardForward(t *testing.T) {
	m := setup(t, "ab|c😀|d")
	m.DeleteBackward()
	expect(t, m, "a|c|d")
	m.DeleteForward()
	expect(t, m, "a|")
	if len(m.Selections()) != 1 {
		t.Fatal("carets not merged")
	}
	m = setup(t, "a\r\n|b")
	m.DeleteBackward()
	expect(t, m, "a|b")
	m = setup(t, "~abc|d")
	m.DeleteForward()
	expect(t, m, "|d")
	m = setup(t, "|x")
	m.DeleteBackward() // no-op at doc start
	expect(t, m, "|x")
	if m.buf.CanUndo() {
		t.Fatal("no-op delete recorded")
	}
}

func TestDeleteMergesOverlappingRanges(t *testing.T) {
	m := setup(t, "foo b|a|r")
	m.DeleteWordBackward()
	expect(t, m, "foo |r")
	m = setup(t, "|ab|c d")
	m.DeleteWordForward()
	expect(t, m, "| d")
	m = setup(t, "x ~yy|")
	m.DeleteWordBackward()
	expect(t, m, "x |")
	m = setup(t, "~x|y")
	m.DeleteWordForward()
	expect(t, m, "|y")
}

func TestBackspaceGroupsAndUndoRestoresCursors(t *testing.T) {
	m := setup(t, "abc|")
	m.DeleteBackward()
	m.DeleteBackward()
	expect(t, m, "a|")
	m.Undo()
	expect(t, m, "abc|")
}

func TestDeleteLines(t *testing.T) {
	m := setup(t, "one\nt|wo\nthree")
	m.DeleteLines()
	expect(t, m, "one\n|three")
	m = setup(t, "one\ntwo\nthr|ee")
	m.DeleteLines()
	expect(t, m, "one\ntwo|")
	m = setup(t, "o~ne\ntwo\n|three")
	m.DeleteLines()
	expect(t, m, "|three")
	m = setup(t, "o|nly")
	m.DeleteLines()
	expect(t, m, "|")
}

func TestAutoCloseTyping(t *testing.T) {
	m := setup(t, "foo|")
	m.Type('(')
	expect(t, m, "foo(|)")
	m.Type('$')
	m.Type('a')
	m.Type(')')
	expect(t, m, "foo($a)|")
	m.Type(';')
	expect(t, m, "foo($a);|")

	m = setup(t, "x = |;")
	m.Type('\'')
	expect(t, m, "x = '|';")
	m.Type('\'')
	expect(t, m, "x = ''|;")

	m = setup(t, "it|")
	m.Type('\'') // apostrophe after word char: no pair
	expect(t, m, "it'|")

	m = setup(t, "|abc")
	m.Type('[') // next is a word char: no pair
	expect(t, m, "[|abc")

	m = setup(t, "~abc|")
	m.Type('(')
	expect(t, m, "(~abc|)")
	m = setup(t, "|abc~")
	m.Type('"')
	expect(t, m, "\"|abc~\"")
	m = setup(t, "~abc|")
	m.Type('x')
	expect(t, m, "x|")

	m = setup(t, "|")
	m.AutoClose = false
	m.Type('(')
	expect(t, m, "(|")
}

func TestAutoDeletePairOnBackspace(t *testing.T) {
	m := setup(t, "f(|)")
	m.DeleteBackward()
	expect(t, m, "f|")
	m = setup(t, "\"|\"")
	m.DeleteBackward()
	expect(t, m, "|")
	m = setup(t, "(|)")
	m.AutoClose = false
	m.DeleteBackward()
	expect(t, m, "|)")
}

func TestUndoWithoutCursorStateUsesEditEnd(t *testing.T) {
	m := setup(t, "|abc")
	if _, err := m.buf.Insert(1, []byte("XY")); err != nil {
		t.Fatal(err)
	}
	m.Undo()
	expect(t, m, "a|bc")
}

func TestSelectHelpers(t *testing.T) {
	m := setup(t, "$fo|o bar\nbaz")
	m.SelectWord()
	expect(t, m, "~$foo| bar\nbaz")
	m.SelectLines()
	expect(t, m, "~$foo bar\n|baz")
	m.SelectAll()
	expect(t, m, "~$foo bar\nbaz|")
	if got := m.SelectedText(); !slices.Equal(got, []string{"$foo bar\nbaz"}) {
		t.Fatalf("SelectedText %q", got)
	}
}

func TestAddNextOccurrence(t *testing.T) {
	m := setup(t, "$a = $a|b + $ab; $ab")
	if !m.AddNextOccurrence() {
		t.Fatal("select word")
	}
	expect(t, m, "$a = ~$ab| + $ab; $ab")
	m.AddNextOccurrence()
	m.AddNextOccurrence()
	expect(t, m, "$a = ~$ab| + ~$ab|; ~$ab|")
	if m.AddNextOccurrence() {
		t.Fatal("all occurrences already selected")
	}
	m = setup(t, "x + |")
	if m.AddNextOccurrence() {
		t.Fatal("no word at caret")
	}
	m = setup(t, "ab ~ab| ab")
	m.AddNextOccurrence()
	m.AddNextOccurrence() // wraps to the first
	expect(t, m, "~ab| ~ab| ~ab|")
}

func TestMatchBracket(t *testing.T) {
	s := buffer.NewString("f(a[1], {b: (c)}) ]").Snapshot()
	cases := []struct {
		off        int
		open, clos int
		ok         bool
	}{
		{1, 1, 16, true},   // before '('
		{2, 1, 16, true},   // after '('
		{17, 1, 16, true},  // after ')'
		{3, 3, 5, true},    // '['
		{8, 8, 15, true},   // '{'
		{12, 12, 14, true}, // inner '('
		{0, 0, 0, false},
		{19, 0, 0, false}, // unmatched ']'
	}
	for _, c := range cases {
		o, cl, ok := MatchBracket(s, c.off)
		if ok != c.ok || (ok && (o != c.open || cl != c.clos)) {
			t.Errorf("MatchBracket(%d) = %d,%d,%v want %d,%d,%v", c.off, o, cl, ok, c.open, c.clos, c.ok)
		}
	}
	if _, _, ok := MatchBracket(buffer.NewString("((").Snapshot(), 0); ok {
		t.Error("unmatched opener")
	}
}

func TestAutoCloseFunction(t *testing.T) {
	cases := []struct {
		typed, prev, next rune
		want              AutoCloseResult
	}{
		{'(', 'f', 0, AutoCloseResult{Insert: "()", Caret: 1}},
		{'{', ' ', ' ', AutoCloseResult{Insert: "{}", Caret: 1}},
		{'[', 0, ';', AutoCloseResult{Insert: "[]", Caret: 1}},
		{'(', 0, 'x', AutoCloseResult{Insert: "(", Caret: 1}},
		{')', 0, ')', AutoCloseResult{Skip: true}},
		{']', 0, 'x', AutoCloseResult{Insert: "]", Caret: 1}},
		{'"', ' ', 0, AutoCloseResult{Insert: `""`, Caret: 1}},
		{'"', 0, '"', AutoCloseResult{Skip: true}},
		{'\'', '\\', 0, AutoCloseResult{Insert: "'", Caret: 1}},
		{'`', 'a', 0, AutoCloseResult{Insert: "`", Caret: 1}},
		{'ä', 0, 0, AutoCloseResult{Insert: "ä", Caret: 2}},
	}
	for _, c := range cases {
		if got := AutoClose(c.typed, c.prev, c.next); got != c.want {
			t.Errorf("AutoClose(%q,%q,%q) = %+v want %+v", c.typed, c.prev, c.next, got, c.want)
		}
	}
	if !AutoDeletePair('(', ')') || AutoDeletePair('(', ']') || AutoDeletePair('a', 'a') || !AutoDeletePair('\'', '\'') {
		t.Error("AutoDeletePair")
	}
}
