package gui

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// screenText joins the text of the current terminal snapshot.
func screenText(e *env) string {
	t := e.ctl.Terminals.Current()
	if t == nil || t.Screen == nil {
		return ""
	}
	var b strings.Builder
	for _, l := range t.Screen.Lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestTerminalDock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	e := newEnv(t, true)
	if err := settings.Set(e.app.Settings(), terminal.KeyShell, "/bin/sh"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.v.Registry.ToolWindow(ToolTerminal); !ok {
		t.Fatal("no Terminal tool window")
	}
	if got := e.v.Registry.ChordText(ActTerminalView, "darwin"); got != "Alt+F12" && got != "⌥F12" {
		t.Logf("chord text %q", got)
	}
	// Alt+F12 opens the window with a shell in the project.
	e.v.RunAction(ActTerminalView)
	e.waitFor("session", func() bool { return len(e.ctl.Terminals.Tabs) == 1 && e.ctl.Terminals.Current().Screen != nil })
	cur := e.ctl.Terminals.Current()
	if !e.v.ToolShown(ToolTerminal) || cur.Info.Dir != e.ctl.Project.Root {
		t.Fatalf("session %+v", cur.Info)
	}
	// The view sized the session to the panel.
	// (Either dimension: with some fonts the panel is exactly the default
	// width.)
	e.waitFor("resize", func() bool {
		i := e.ctl.Terminals.Current().Info
		return i.Cols != terminal.DefaultCols || i.Rows != terminal.DefaultRows
	})
	e.waitFor("focus", func() bool { return e.v.termd.focused() })

	// Typing goes to the shell, also Ctrl+R (not Run).
	e.h.Router.Queue(key.EditEvent{Text: "echo hi-$((40+2))"})
	e.frame()
	e.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	e.frame()
	e.waitFor("output", func() bool { return strings.Contains(screenText(e), "hi-42") })
	e.h.Router.Queue(key.Event{Name: "R", Modifiers: key.ModCtrl, State: key.Press})
	e.frame()
	e.settle()
	if len(e.ctl.Run.Tabs) != 0 {
		t.Fatal("Ctrl+R in the terminal started a run")
	}
	e.h.Router.Queue(key.Event{Name: "C", Modifiers: key.ModCtrl, State: key.Press})
	e.frame()

	// Escape returns to the editor (default setting).
	e.h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	e.frame()
	e.frame()
	if e.v.termd.focused() {
		t.Fatal("Escape kept the focus in the terminal")
	}
	// Alt+F12 again focuses the open terminal, then hides it.
	e.v.RunAction(ActTerminalView)
	e.waitFor("refocus", func() bool { return e.v.termd.focused() })
	e.v.RunAction(ActTerminalView)
	e.frame()
	if e.v.ToolShown(ToolTerminal) {
		t.Fatal("toggle did not hide")
	}
	e.v.ShowTool(ToolTerminal)

	// A second tab, rename, close.
	e.v.RunAction(ActTerminalNew)
	e.waitFor("second tab", func() bool { return len(e.ctl.Terminals.Tabs) == 2 })
	second := e.ctl.Terminals.Current().Info.ID
	e.v.RunAction(ActTerminalRename)
	if !e.v.PopupOpen() {
		t.Fatal("rename popup")
	}
	e.frame()
	e.v.termd.rename.SetText("Build")
	e.v.popup.Buttons[0].OnClick()
	e.waitFor("renamed", func() bool { x := e.ctl.Terminals.Tab(second); return x != nil && x.Info.Name == "Build" })
	e.v.RunAction(ActTerminalClose)
	e.waitFor("closed", func() bool { return len(e.ctl.Terminals.Tabs) == 1 })

	// The shell exits with 0: the tab closes (terminal.closeOnExit).
	e.ctl.Terminals.SendText(e.ctl.Terminals.Current().Info.ID, "exit 0\r")
	e.waitFor("exit closes", func() bool { return len(e.ctl.Terminals.Tabs) == 0 })
	e.frame()

	// A shell in a compose service needs the docker plugin and a compose
	// file: here it fails with a notice, no tab.
	e.v.termd.openServiceShell("app", "")
	e.waitFor("service notice", func() bool { return strings.HasPrefix(e.ctl.Status.Notice, "Terminal:") })
	if len(e.ctl.Terminals.Tabs) != 0 {
		t.Fatal("tab for a failed service shell")
	}
}

func TestKeepShortcut(t *testing.T) {
	mac, pc := keepShortcut(true), keepShortcut(false)
	cases := []struct {
		chord   string
		mac, pc bool
	}{
		{"Cmd+Shift+A", true, false},
		{"Ctrl+R", false, false},
		{"Alt+F12", true, true},
		{"Cmd+4", true, false},
		{"Alt+4", false, true},
		{"F5", false, false},
		{"Ctrl+Shift+F12", true, true},
	}
	for _, c := range cases {
		ch, err := keymap.ParseChord(c.chord)
		if err != nil {
			t.Fatal(err)
		}
		if mac(ch) != c.mac || pc(ch) != c.pc {
			t.Errorf("%s: mac %v pc %v", c.chord, mac(ch), pc(ch))
		}
	}
}
