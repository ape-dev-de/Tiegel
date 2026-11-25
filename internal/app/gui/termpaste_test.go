package gui

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/transfer"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
)

// TestTerminalPaste: Cmd+V and Edit > Paste paste into the focused
// terminal, never into the editor, with and without
// terminal.overrideIdeShortcuts.
func TestTerminalPaste(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	for _, override := range []bool{true, false} {
		e := newEnv(t, true)
		for k, v := range map[string]any{terminal.KeyShell: "/bin/sh", terminal.KeyOverrideKeys: override} {
			if err := settings.Set(e.app.Settings(), k, v); err != nil {
				t.Fatal(err)
			}
		}
		s := e.openEditor("index.php")
		text := func() string {
			s.doc.Lock()
			defer s.doc.Unlock()
			return string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len()))
		}
		before := text()
		e.v.RunAction(ActTerminalView)
		e.waitFor("session", func() bool { return len(e.ctl.Terminals.Tabs) == 1 && e.ctl.Terminals.Current().Screen != nil })
		e.waitFor("focus", func() bool { return e.v.termd.focused() })

		n := 0
		paste := func(how string, do func()) {
			t.Helper()
			n++
			do()
			e.frame()
			if !e.h.Router.ClipboardRequested() {
				t.Fatalf("override=%v %s: the terminal did not read the clipboard (pending %q)", override, how, e.v.pendingEdit)
			}
			want := "pasted-" + strings.Repeat("x", n)
			e.h.Router.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader("echo " + want + "\r")) }})
			e.frame()
			e.waitFor(how, func() bool { return strings.Count(screenText(e), want) >= 2 })
			if e.v.pendingEdit != "" || text() != before || !e.v.termd.focused() {
				t.Fatalf("override=%v %s: pasted into the editor", override, how)
			}
		}
		paste("Cmd+V", func() { e.h.Router.Queue(key.Event{Name: "V", Modifiers: key.ModShortcut, State: key.Press}) })
		paste("Edit > Paste", func() { e.v.RunAction(keymap.Paste) })
	}
}

// TestTerminalFocusOnPanelPress: a click into the terminal window that
// misses the view (tab row) still focuses the terminal.
func TestTerminalFocusOnPanelPress(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil || runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	e := newEnv(t, true)
	if err := settings.Set(e.app.Settings(), terminal.KeyShell, "/bin/sh"); err != nil {
		t.Fatal(err)
	}
	e.openEditor("index.php")
	e.v.RunAction(ActTerminalView)
	e.waitFor("focus", func() bool { return e.v.termd.focused() })
	e.v.focusEditor = true
	e.frame()
	e.frame()
	if e.v.termd.focused() {
		t.Fatal("terminal kept the focus")
	}
	e.v.handleBenchEvent(templates.PanelPressEvent{ID: ToolTerminal})
	e.frame()
	e.frame()
	if !e.v.termd.focused() {
		t.Fatal("panel press did not focus the terminal")
	}
}
