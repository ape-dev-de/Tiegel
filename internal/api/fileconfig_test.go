package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
)

const trimConfig = "root = true\n[*]\ntrim_trailing_whitespace = true\ninsert_final_newline = true\n"

// TestEditorConfigSaveKeepsCarets checks that a save filter that trims
// whitespace changes only the affected bytes: carets elsewhere stay where
// they were and the filter is one undo step of its own (D245).
func TestEditorConfigSaveKeepsCarets(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{
		".editorconfig": trimConfig,
		"a.php":         "<?php\n$a = 1;   \n$b = 2;\n$c = 3;  \necho $b;",
	})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	d, err := a.doc("a.php")
	if err != nil {
		t.Fatal(err)
	}
	d.Lock()
	m := cursor.New(d.Buffer())
	// Caret on "$b = 2;" between "$b" and " = 2" (line 2, column 2).
	m.SetSelections([]cursor.Selection{cursor.Caret(d.Buffer().LineStart(2) + 2)}, 0)
	d.Unlock()
	defer m.Close()

	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "// x\n"}, nil)
	call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, nil)
	want := "// x\n<?php\n$a = 1;\n$b = 2;\n$c = 3;\necho $b;\n"
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); string(data) != want {
		t.Fatalf("saved %q", data)
	}
	d.Lock()
	defer d.Unlock()
	b := d.Buffer()
	if b.String() != want {
		t.Fatalf("buffer %q", b.String())
	}
	if got, exp := m.Primary().Head, b.LineStart(3)+2; got != exp {
		t.Fatalf("caret %d (%v), want %d", got, b.OffsetToPos(got), exp)
	}
	if b.Modified() {
		t.Fatal("modified after save")
	}
	// One undo restores the whitespace (all filter edits together), the
	// next one the typed comment.
	if _, _, ok := b.Undo(); !ok || b.String() != "// x\n<?php\n$a = 1;   \n$b = 2;\n$c = 3;  \necho $b;" {
		t.Fatalf("undo filter %q", b.String())
	}
	if _, _, ok := b.Undo(); !ok || b.String() != "<?php\n$a = 1;   \n$b = 2;\n$c = 3;  \necho $b;" {
		t.Fatalf("undo typing %q", b.String())
	}
}

// TestEditorConfigAutosaveKeepsTrailingBlank checks that automatic saves
// do not trim trailing whitespace (the blank just typed before the caret
// survives), while an explicit save does.
func TestEditorConfigAutosaveKeepsTrailingBlank(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{
		".editorconfig": trimConfig,
		"a.php":         "<?php\necho\n",
	})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	d, err := a.doc("a.php")
	if err != nil {
		t.Fatal(err)
	}
	d.Lock()
	m := cursor.New(d.Buffer())
	m.SetSelections([]cursor.Selection{cursor.Caret(len("<?php\necho"))}, 0)
	d.Unlock()
	defer m.Close()
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: len("<?php\necho")}, Text: " "}, nil)
	d.Lock()
	m.SetSelections([]cursor.Selection{cursor.Caret(len("<?php\necho "))}, 0)
	d.Unlock()
	for _, tr := range []autosave.Trigger{autosave.TriggerIdle, autosave.TriggerFocusLost} {
		if err := a.saver.SaveAll(context.Background(), tr); err != nil {
			t.Fatal(err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); string(data) != "<?php\necho \n" {
		t.Fatalf("autosaved %q", data)
	}
	d.Lock()
	head := m.Primary().Head
	d.Unlock()
	if head != len("<?php\necho ") {
		t.Fatalf("caret %d", head)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "#"}, nil)
	call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, nil)
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); string(data) != "#<?php\necho\n" {
		t.Fatalf("saved %q", data)
	}
	if !trimOnSave(autosave.TriggerManual) || !trimOnSave(autosave.TriggerBeforeCommit) || trimOnSave(autosave.TriggerBeforeRun) {
		t.Fatal("trimOnSave")
	}
}

func TestApplyFilteredMinimal(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"a  \r\nb\r\n", "a\r\nb\r\n"},
		{"x\r\n", "y\n"},
		{"x\n", "y\r\n"},
		{"ä \nö", "ä\nö\n"},
		{"", "new\n"},
		{"gone\n", ""},
		{"a\nb\nc\n", "c\nb\na\n"},
	} {
		b := buffer.NewString(tc.from)
		if err := applyFiltered(b, []byte(tc.to)); err != nil || b.String() != tc.to {
			t.Fatalf("%q -> %q: got %q, %v", tc.from, tc.to, b.String(), err)
		}
	}
}
