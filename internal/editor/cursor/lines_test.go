package cursor

import "testing"

func TestIndentOutdent(t *testing.T) {
	m := setup(t, "a|b\nc")
	if !m.IndentLines("\t") {
		t.Fatal("indent")
	}
	expect(t, m, "\ta|b\nc")
	m.Undo()
	expect(t, m, "a|b\nc")

	m = setup(t, "~one\n\ntwo|\nthree")
	m.IndentLines("    ")
	expect(t, m, "~    one\n\n    two|\nthree")
	m.OutdentLines()
	expect(t, m, "~one\n\ntwo|\nthree")
	if m.OutdentLines() {
		t.Fatal("outdent of unindented lines reported change")
	}

	m = setup(t, "\t\tx|\n  y\n      z")
	m.SelectAll()
	m.OutdentLines()
	if got := m.Buffer().String(); got != "\tx\ny\n  z" {
		t.Fatalf("outdent mixed: %q", got)
	}
	if m.IndentLines("") {
		t.Fatal("empty unit")
	}
}

func TestIndentSelectionEndingAtColumnZero(t *testing.T) {
	m := setup(t, "~a\nb\n|c")
	m.IndentLines("\t")
	expect(t, m, "~\ta\n\tb\n|c")
}

func TestToggleComment(t *testing.T) {
	m := setup(t, "  ~if (x) {\n\n    y();|\n  }")
	if !m.ToggleComment("//") {
		t.Fatal("comment")
	}
	expect(t, m, "  ~// if (x) {\n\n  //   y();|\n  }")
	m.ToggleComment("//")
	expect(t, m, "  ~if (x) {\n\n    y();|\n  }")

	m = setup(t, "//a|\n# b")
	m.ToggleComment("//")
	expect(t, m, "a|\n# b")
	m.ToggleComment("#")
	expect(t, m, "# a|\n# b")
	if m.ToggleComment("") {
		t.Fatal("empty prefix")
	}
	m = setup(t, "x\n|\ny")
	m.ToggleComment("#")
	expect(t, m, "x\n# |\ny")
	m.Undo()
	expect(t, m, "x\n|\ny")
}

func TestDuplicateLines(t *testing.T) {
	m := setup(t, "ab|c\nd")
	m.DuplicateLines()
	expect(t, m, "abc\nab|c\nd")
	m.Undo()
	expect(t, m, "ab|c\nd")

	m = setup(t, "x ~foo| y")
	m.DuplicateLines()
	expect(t, m, "x foo~foo| y")

	// Two carets on one line duplicate it once.
	m = setup(t, "a|b|\nc|")
	m.DuplicateLines()
	expect(t, m, "ab\na|b|\nc\nc|")
}

func TestMoveLines(t *testing.T) {
	m := setup(t, "1\n2|\n3")
	if !m.MoveLines(-1) {
		t.Fatal("move up")
	}
	expect(t, m, "2|\n1\n3")
	if m.MoveLines(-1) {
		t.Fatal("moved past top")
	}
	m.MoveLines(1)
	m.MoveLines(1)
	expect(t, m, "1\n3\n2|")
	if m.MoveLines(1) {
		t.Fatal("moved past bottom")
	}
	if m.MoveLines(0) {
		t.Fatal("zero delta")
	}
	m.Undo()
	expect(t, m, "1\n2|\n3")

	m = setup(t, "a\n~b\nc|\nd\ne|")
	m.MoveLines(-1)
	expect(t, m, "~b\nc|\na\ne|\nd")
}

func TestMoveLinesCRLF(t *testing.T) {
	m := setup(t, "a\r\nb|")
	m.MoveLines(-1)
	if got := m.Buffer().String(); got != "b\r\na" {
		t.Fatalf("got %q", got)
	}
	if m.Primary().Head != 1 {
		t.Fatalf("head %d", m.Primary().Head)
	}
}

func TestPasteLines(t *testing.T) {
	m := setup(t, "ab|c\nd|")
	m.PasteLines("x")
	expect(t, m, "x\nab|c\nx\nd|")
	m.Undo()
	expect(t, m, "ab|c\nd|")
}

func TestSelectAllOccurrences(t *testing.T) {
	m := setup(t, "$a = $a + |$a;")
	if n := m.SelectAllOccurrences(); n != 3 {
		t.Fatalf("got %d selections: %q", n, render(m))
	}
	m = setup(t, "  |  ")
	if n := m.SelectAllOccurrences(); n != 1 {
		t.Fatalf("whitespace: %d", n)
	}
}

func TestMapThrough(t *testing.T) {
	ps := []prim{{at: 2, ins: "xx"}, {at: 5, del: 2}}
	cases := []struct {
		off   int
		stick bool
		want  int
	}{{1, false, 1}, {2, true, 2}, {2, false, 4}, {3, false, 5}, {6, false, 7}, {7, false, 7}, {9, false, 9}}
	for _, c := range cases {
		if got := mapThrough(c.off, ps, c.stick); got != c.want {
			t.Errorf("mapThrough(%d, %v) = %d, want %d", c.off, c.stick, got, c.want)
		}
	}
}
