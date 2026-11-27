package gui

import (
	"io"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/transfer"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// TestPopupPaste: with a text field of a popup focused (Search
// Everywhere), Cmd+V and Edit > Paste paste into the field, never into
// the code editor.
func TestPopupPaste(t *testing.T) {
	e := newEnv(t, true)
	s := e.openEditor("index.php")
	text := func() string {
		s.doc.Lock()
		defer s.doc.Unlock()
		return string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len()))
	}
	before := text()
	for i, how := range []string{"Edit > Paste", "Cmd+V"} {
		e.v.RunAction(ActSearchEverywhere)
		e.frame()
		e.frame()
		if how == "Cmd+V" {
			e.h.Router.Queue(key.Event{Name: "V", Modifiers: key.ModShortcut, State: key.Press})
		} else {
			e.v.RunAction(keymap.Paste)
		}
		e.frame()
		if !e.h.Router.ClipboardRequested() {
			t.Fatalf("%s: the field did not read the clipboard (pending %q)", how, e.v.pendingEdit)
		}
		want := strings.Repeat("Shop", i+1)
		e.h.Router.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader(want)) }})
		e.frame()
		e.frame()
		if got := e.v.chooser.Query(); got != want {
			t.Fatalf("%s: query %q, want %q", how, got, want)
		}
		if e.v.pendingEdit != "" || text() != before {
			t.Fatalf("%s: pasted into the editor", how)
		}
		e.key(key.NameEscape, 0)
	}
}

// TestEditorKeepsPaste: without a popup, Cmd+V still pastes into the
// focused code editor (the editor binds the key itself), and Edit > Select
// All / Copy act on a focused popup field.
func TestEditorKeepsPaste(t *testing.T) {
	e := newEnv(t, true)
	s := e.openEditor("index.php")
	e.frame()
	e.waitFor("editor focus", func() bool { e.frame(); return s.ed.Focused() })
	e.h.Router.Queue(key.Event{Name: "V", Modifiers: key.ModShortcut, State: key.Press})
	e.frame()
	if !e.h.Router.ClipboardRequested() {
		t.Fatal("the editor did not read the clipboard")
	}
	e.h.Router.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader("PASTED")) }})
	e.frame()
	s.doc.Lock()
	got := string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len()))
	s.doc.Unlock()
	if !strings.Contains(got, "PASTED") {
		t.Fatalf("editor text %q", got)
	}

	e.v.RunAction(ActSearchEverywhere)
	e.frame()
	e.frame()
	e.h.Router.Queue(key.EditEvent{Text: "Shop"})
	e.frame()
	if q := e.v.chooser.Query(); q != "Shop" {
		t.Fatalf("query %q", q)
	}
	e.v.RunAction(keymap.SelectAll)
	e.frame()
	e.v.RunAction(keymap.Copy)
	e.frame()
	if _, c, ok := e.h.Router.WriteClipboard(); !ok || string(c) != "Shop" {
		t.Fatalf("clipboard %q %v", c, ok)
	}
}
