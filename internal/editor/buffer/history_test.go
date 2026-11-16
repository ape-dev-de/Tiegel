package buffer

import "testing"

func TestUndoRedoSingle(t *testing.T) {
	b := NewString("abc")
	mustReplace(t, b, 1, 1, "XYZ")
	if b.String() != "aXYZc" {
		t.Fatal(b.String())
	}
	edits, _, ok := b.Undo()
	if !ok || b.String() != "abc" || len(edits) != 1 {
		t.Fatalf("undo: %v %q %d", ok, b.String(), len(edits))
	}
	if _, _, ok := b.Undo(); ok {
		t.Fatal("undo on empty history")
	}
	if _, _, ok := b.Redo(); !ok || b.String() != "aXYZc" {
		t.Fatalf("redo: %q", b.String())
	}
	if _, _, ok := b.Redo(); ok {
		t.Fatal("redo on empty stack")
	}
}

func TestTypingMerges(t *testing.T) {
	b := NewString("")
	for i, c := range "hello" {
		mustReplace(t, b, i, 0, string(c))
	}
	b.BreakUndo()
	for i, c := range " world" {
		mustReplace(t, b, 5+i, 0, string(c))
	}
	// Non-contiguous insert starts a new group.
	mustReplace(t, b, 0, 0, ">")
	b.Undo()
	if b.String() != "hello world" {
		t.Fatalf("after 1 undo %q", b.String())
	}
	b.Undo()
	if b.String() != "hello" {
		t.Fatalf("after 2 undo %q", b.String())
	}
	b.Undo()
	if b.String() != "" || b.CanUndo() {
		t.Fatalf("after 3 undo %q", b.String())
	}
}

func TestDeletingMerges(t *testing.T) {
	b := NewString("abcdef")
	// Backspace from the end twice, then forward delete at 0 twice.
	mustReplace(t, b, 5, 1, "")
	mustReplace(t, b, 4, 1, "")
	b.BreakUndo()
	mustReplace(t, b, 0, 1, "")
	mustReplace(t, b, 0, 1, "")
	if b.String() != "cd" {
		t.Fatal(b.String())
	}
	b.Undo()
	if b.String() != "abcd" {
		t.Fatalf("%q", b.String())
	}
	b.Undo()
	if b.String() != "abcdef" {
		t.Fatalf("%q", b.String())
	}
	// Insert followed by delete never merges.
	mustReplace(t, b, 0, 0, "x")
	mustReplace(t, b, 0, 1, "")
	b.Undo()
	if b.String() != "xabcdef" {
		t.Fatalf("%q", b.String())
	}
	// Replacements never merge.
	mustReplace(t, b, 0, 1, "y")
	mustReplace(t, b, 0, 1, "z")
	b.Undo()
	if b.String() != "yabcdef" {
		t.Fatalf("%q", b.String())
	}
}

func TestExplicitGroupsAndState(t *testing.T) {
	b := NewString("a b c")
	b.BeginGroup(GroupTyping, "before1")
	b.BeginGroup(GroupOther, "ignored") // nested
	mustReplace(t, b, 5, 0, "!")
	mustReplace(t, b, 3, 0, "!")
	mustReplace(t, b, 1, 0, "!")
	b.EndGroup("ignored")
	b.EndGroup("after1")
	// Same kind, no break: merges, keeps first before-state.
	b.BeginGroup(GroupTyping, "before2")
	mustReplace(t, b, 0, 0, "#")
	b.EndGroup("after2")
	if b.String() != "#a! b! c!" {
		t.Fatal(b.String())
	}
	if b.CanUndo() && b.hist.depth != 0 {
		t.Fatal("depth")
	}
	_, st, ok := b.Undo()
	if !ok || st != "before1" || b.String() != "a b c" {
		t.Fatalf("undo state %v %q", st, b.String())
	}
	_, st, _ = b.Redo()
	if st != "after2" || b.String() != "#a! b! c!" {
		t.Fatalf("redo state %v %q", st, b.String())
	}
	// Empty groups are dropped; stray EndGroup is ignored.
	b.EndGroup(nil)
	b.BreakUndo()
	b.BeginGroup(GroupOther, nil)
	b.EndGroup(nil)
	b.Undo()
	if b.String() != "a b c" {
		t.Fatal(b.String())
	}
}

func TestUndoBlockedInsideGroup(t *testing.T) {
	b := NewString("x")
	mustReplace(t, b, 0, 0, "y")
	b.BeginGroup(GroupOther, nil)
	if b.CanUndo() {
		t.Fatal("undo allowed inside open group")
	}
	if _, _, ok := b.Undo(); ok {
		t.Fatal("undo inside group")
	}
	b.EndGroup(nil)
	if !b.CanUndo() {
		t.Fatal("cannot undo after group")
	}
}

func TestRedoClearedByEdit(t *testing.T) {
	b := NewString("x")
	mustReplace(t, b, 1, 0, "y")
	b.Undo()
	if !b.CanRedo() {
		t.Fatal("no redo")
	}
	mustReplace(t, b, 0, 0, "z")
	if b.CanRedo() {
		t.Fatal("redo survived edit")
	}
}

func TestModified(t *testing.T) {
	b := NewString("x")
	if b.Modified() {
		t.Fatal("fresh buffer modified")
	}
	mustReplace(t, b, 1, 0, "a")
	if !b.Modified() {
		t.Fatal("not modified after edit")
	}
	b.MarkSaved()
	mustReplace(t, b, 2, 0, "b") // merges into saved group
	if !b.Modified() {
		t.Fatal("merge into saved step not detected")
	}
	b.Undo()
	if !b.Modified() {
		t.Fatal("undo of merged group should differ from saved state")
	}
	b.Redo()
	b.Undo()
	b.Undo()
	if !b.Modified() {
		t.Fatal("initial state differs from saved state")
	}
	b.Redo()
	b.MarkSaved()
	mustReplace(t, b, 0, 0, "q")
	b.Undo()
	if b.Modified() {
		t.Fatal("undo back to saved state still modified")
	}
}

func TestUndoLimit(t *testing.T) {
	b := NewString("")
	b.SetUndoLimit(3)
	for i := 0; i < 10; i++ {
		b.BreakUndo()
		mustReplace(t, b, i, 0, "x")
	}
	n := 0
	for b.CanUndo() {
		b.Undo()
		n++
	}
	if n != 3 || b.String() != "xxxxxxx" {
		t.Fatalf("undid %d steps, content %q", n, b.String())
	}
}

func TestUndoEditsDescribeChanges(t *testing.T) {
	b := NewString("ab\ncd")
	var applied []Edit
	b.OnEdit(func(e Edit) { applied = append(applied, e) })
	b.BeginGroup(GroupOther, nil)
	mustReplace(t, b, 0, 1, "X\n")
	mustReplace(t, b, 4, 2, "")
	b.EndGroup(nil)
	edits, _, _ := b.Undo()
	if len(edits) != 2 || len(applied) != 4 {
		t.Fatalf("edits %d applied %d", len(edits), len(applied))
	}
	if b.String() != "ab\ncd" {
		t.Fatal(b.String())
	}
	// Edits must replay the same transformation on a reference model.
	ref := []byte("X\nb\n")
	for _, e := range edits {
		ref = append(ref[:e.StartByte:e.StartByte], append(append([]byte{}, e.Text...), ref[e.OldEndByte:]...)...)
	}
	if string(ref) != "ab\ncd" {
		t.Fatalf("replayed %q", ref)
	}
}
