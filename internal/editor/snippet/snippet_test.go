package snippet

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tp, err := Parse("foreach ($$$ARRAY$ as $$$VALUE$) {\n\t$END$\n} // $5 $ $x-$$")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tp.Vars, []string{"ARRAY", "VALUE"}) || !tp.HasEnd || tp.HasSelection {
		t.Fatalf("vars %v end %v sel %v", tp.Vars, tp.HasEnd, tp.HasSelection)
	}
	e := Expand(tp, ExpandOptions{Values: map[string]string{"ARRAY": "items"}})
	want := "foreach ($items as $VALUE) {\n\t\n} // $5 $ $x-$"
	if e.Text != want {
		t.Fatalf("text %q", e.Text)
	}
	if len(e.Fields) != 2 || e.Fields[0].Ranges[0] != (Range{10, 15}) || e.Fields[1].Name != "VALUE" || e.Text[e.Fields[1].Ranges[0].Start:e.Fields[1].Ranges[0].End] != "VALUE" {
		t.Fatalf("fields %+v", e.Fields)
	}
	if e.Text[e.End-2:e.End] != "{\n" && e.Text[e.End-1] != '\t' {
		t.Fatalf("end %d", e.End)
	}
	if _, err := Parse(""); err == nil {
		t.Fatal("empty body accepted")
	}
}

func TestExpandIndentSelectionSkip(t *testing.T) {
	tp, _ := Parse("try {\n\t$SELECTION$$END$\n} catch ($EX$ $$e) {\n\t$EX$\n}")
	e := Expand(tp, ExpandOptions{Selection: "a();\nb();", Indent: "  ", Tab: "    ", Values: map[string]string{"EX": "\\Throwable"}})
	want := "try {\n      a();\n      b();\n  } catch (\\Throwable $e) {\n      \\Throwable\n  }"
	if e.Text != want {
		t.Fatalf("text %q", e.Text)
	}
	if len(e.Fields) != 1 || len(e.Fields[0].Ranges) != 2 {
		t.Fatalf("fields %+v", e.Fields)
	}
	e = Expand(tp, ExpandOptions{Skip: map[string]bool{"EX": true}})
	if len(e.Fields) != 0 {
		t.Fatalf("skipped field %+v", e.Fields)
	}
	s := e.Shift(5)
	if s.End != e.End+5 {
		t.Fatal("shift")
	}
}

func TestSessionLinkedEditing(t *testing.T) {
	// "$A$ = $A$ + $B$;$END$" expanded as "A = A + B;"
	tp, _ := Parse("$A$ = $A$ + $B$;$END$")
	e := Expand(tp, ExpandOptions{}).Shift(10)
	s := NewSession(e.Fields, e.End)
	f, ok := s.Current()
	if !ok || f.Name != "A" || len(f.Ranges) != 2 {
		t.Fatalf("current %+v", f)
	}
	// Type "xy" over both occurrences of A (one caret each): replace the
	// later range first, like a multi-caret edit applied back to front.
	// Occurrence 2 at [14,15), occurrence 1 at [10,11).
	if !s.Edit(14, 15, 16) || !s.Edit(10, 11, 12) {
		t.Fatal("edit broke the session")
	}
	f, _ = s.Current()
	if f.Ranges[0] != (Range{10, 12}) || f.Ranges[1] != (Range{15, 17}) {
		t.Fatalf("ranges %+v", f.Ranges)
	}
	// Typing at the end of a current range grows it.
	s.Edit(12, 12, 13)
	f, _ = s.Current()
	if f.Ranges[0] != (Range{10, 13}) || f.Ranges[1] != (Range{16, 18}) {
		t.Fatalf("grown %+v", f.Ranges)
	}
	if !s.Next() {
		t.Fatal("next")
	}
	f, _ = s.Current()
	if f.Name != "B" || f.Ranges[0] != (Range{21, 22}) {
		t.Fatalf("B %+v", f)
	}
	if s.End() != 23 {
		t.Fatalf("end %d", s.End())
	}
	// Typing at the end of B moves the end position along.
	s.Edit(22, 22, 25)
	if s.End() != 26 {
		t.Fatalf("end after typing %d", s.End())
	}
	if !s.Prev() {
		t.Fatal("prev")
	}
	if f, _ = s.Current(); f.Name != "A" {
		t.Fatalf("prev %+v", f)
	}
	if s.Prev() {
		t.Fatal("prev before the first field")
	}
	s.Next()
	if s.Next() || !s.Done() {
		t.Fatal("should be done after the last field")
	}
	if _, ok := s.Current(); ok {
		t.Fatal("current after done")
	}
	if s.Edit(0, 0, 1) {
		t.Fatal("edit after done")
	}
}

func TestSessionBreaksAndEnd(t *testing.T) {
	s := NewSession([]Field{{Name: "A", Ranges: []Range{{5, 6}}}, {Name: "B", Ranges: []Range{{8, 9}}}}, 4)
	// End before the current field: typing at the field start does not
	// move it.
	s.Edit(5, 5, 6)
	if s.End() != 4 {
		t.Fatalf("end %d", s.End())
	}
	// An edit before everything shifts all.
	s.Edit(0, 0, 2)
	if f, _ := s.Current(); f.Ranges[0] != (Range{7, 9}) || s.End() != 6 {
		t.Fatalf("shift %+v %d", f, s.End())
	}
	// An edit over field B breaks the session.
	if s.Edit(9, 12, 9) || !s.Done() {
		t.Fatal("overlap should end the session")
	}
	if NewSession(nil, 3).Done() != true {
		t.Fatal("no fields: done")
	}
	c := NewSession([]Field{{Name: "A", Ranges: []Range{{0, 1}}}}, 1)
	if i, n := c.Index(); i != 0 || n != 1 {
		t.Fatal("index")
	}
	c.Cancel()
	if !c.Done() || c.Next() {
		t.Fatal("cancel")
	}
	// An edit that swallows the end position moves it to the edit start.
	d := NewSession([]Field{{Name: "A", Ranges: []Range{{0, 1}}}}, 5)
	d.Edit(3, 7, 3)
	if d.End() != 3 {
		t.Fatalf("end %d", d.End())
	}
}
