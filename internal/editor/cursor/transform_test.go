package cursor

import "testing"

func TestJoinLinesCaret(t *testing.T) {
	m := setup(t, "$a = foo(|  \n    $b,\n    $c\n);")
	if !m.JoinLines() {
		t.Fatal("join")
	}
	expect(t, m, "$a = foo(|$b,\n    $c\n);")
	m.Undo()
	expect(t, m, "$a = foo(|  \n    $b,\n    $c\n);")

	// Space between words, none before a closing bracket or at an empty
	// line.
	m = setup(t, "one|\n   two")
	m.JoinLines()
	expect(t, m, "one |two")
	m = setup(t, "x(1,|\n)")
	m.JoinLines()
	expect(t, m, "x(1,|)")
	m = setup(t, "|\n  y")
	m.JoinLines()
	expect(t, m, "|y")

	// The last line has nothing to join.
	m = setup(t, "a\nb|")
	if m.JoinLines() {
		t.Fatal("join on last line")
	}
}

func TestJoinLinesSelection(t *testing.T) {
	m := setup(t, "~a\n  b\n  c|\nd")
	m.JoinLines()
	expect(t, m, "~a b c|\nd")

	// Two carets on neighbouring lines join each line with the next.
	m = setup(t, "a|\nb\nc|\nd")
	m.JoinLines()
	expect(t, m, "a |b\nc |d")
}

func TestToggleCase(t *testing.T) {
	m := setup(t, "~Hello World|")
	if !m.ToggleCase() {
		t.Fatal("toggle")
	}
	expect(t, m, "~HELLO WORLD|")
	m.ToggleCase()
	expect(t, m, "~hello world|")

	// Backwards selection keeps its direction.
	m = setup(t, "|abc~ def")
	m.ToggleCase()
	expect(t, m, "|ABC~ def")

	// A caret toggles the word under it and stays.
	m = setup(t, "foo ba|r baz")
	m.ToggleCase()
	expect(t, m, "foo BA|R baz")

	// Two carets in one word toggle it once.
	m = setup(t, "a|bc|d")
	m.ToggleCase()
	if got := m.Buffer().String(); got != "ABCD" {
		t.Fatalf("two carets: %q", got)
	}

	// Nothing to change.
	m = setup(t, "1|2 ")
	if m.ToggleCase() {
		t.Fatal("digits have no case")
	}
	if ToggledCase("ÄB") != "äb" || ToggledCase("äb") != "ÄB" {
		t.Fatal("ToggledCase")
	}
}
