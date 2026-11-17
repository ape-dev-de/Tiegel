package editorview

import (
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

func TestJoinToggleAndClipboardHistory(t *testing.T) {
	r := newRig(t, "foo(\n    bar\n)")
	var copied []string
	r.e.OnCopy = func(s string) { copied = append(copied, s) }
	r.focus()
	r.press("Ctrl+Shift+J")
	r.expectText("foo(bar\n)")
	r.press("Cmd+Shift+U")
	r.expectText("foo(BAR\n)")
	r.press("Shift+Right")
	r.press("Cmd+C")
	if len(copied) != 1 || copied[0] != "B" {
		t.Fatalf("OnCopy %q", copied)
	}
	r.takeEvents()
	r.press("Cmd+Shift+V")
	found := false
	for _, ev := range r.takeEvents() {
		if a, ok := ev.(ActionEvent); ok && a.Action == keymap.PasteFromHistory {
			found = true
		}
	}
	if !found {
		t.Fatal("paste from history not reported")
	}
	r.e.ReadOnly = true
	if r.e.Do(layout.Context{}, keymap.JoinLines) {
		t.Fatal("join in read-only editor")
	}
}
