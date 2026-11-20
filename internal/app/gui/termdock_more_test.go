package gui

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/findbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/termview"
)

func TestTabTitle(t *testing.T) {
	cases := []struct {
		info terminal.Info
		want string
	}{
		{terminal.Info{Name: "Local"}, "Local"},
		{terminal.Info{Name: "Local", Title: "zsh ~/app"}, "zsh ~/app"},
		{terminal.Info{Name: "Local", Title: "zsh", Busy: true, Process: "vim"}, "vim"},
		{terminal.Info{Name: "Build", Renamed: true, Busy: true, Process: "vim"}, "Build"},
	}
	for _, c := range cases {
		if got := tabTitle(c.info); got != c.want {
			t.Errorf("%+v: %q want %q", c.info, got, c.want)
		}
	}
}

func TestFindPattern(t *testing.T) {
	if f := findPattern("a.b", findbar.Options{}); f.Query != "a.b" || f.Regex {
		t.Fatalf("plain %+v", f)
	}
	if f := findPattern("a.b", findbar.Options{WholeWord: true, CaseSensitive: true}); f.Query != `\b(?:a\.b)\b` || !f.Regex || !f.CaseSensitive {
		t.Fatalf("whole word %+v", f)
	}
	if f := findPattern("a+", findbar.Options{WholeWord: true, Regex: true}); f.Query != `\b(?:a+)\b` {
		t.Fatalf("whole word regex %+v", f)
	}
}

func TestKeepShortcutTerminalKeys(t *testing.T) {
	mac := keepShortcut(true)
	for _, c := range []string{"Cmd+C", "Cmd+V", "Cmd+A", "Cmd+F"} {
		ch, _ := keymap.ParseChord(c)
		if mac(ch) {
			t.Errorf("%s kept by the IDE", c)
		}
	}
	ch, _ := keymap.ParseChord("Cmd+Shift+F")
	if !mac(ch) {
		t.Error("Cmd+Shift+F (find in files) stays with the IDE")
	}
}

// TestTerminalFindBusyClipboard drives a real shell: find in the
// scrollback, the busy state of a running job with the close
// confirmation, and a clipboard write by the program.
func TestTerminalFindBusyClipboard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	e := newEnv(t, true)
	for k, v := range map[string]any{terminal.KeyShell: "/bin/sh", terminal.KeyClipboard: true} {
		if err := settings.Set(e.app.Settings(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	e.v.RunAction(ActTerminalView)
	e.waitFor("session", func() bool { return len(e.ctl.Terminals.Tabs) == 1 && e.ctl.Terminals.Current().Screen != nil })
	cur := e.ctl.Terminals.Current()
	id := cur.Info.ID
	ts := e.ctl.Terminals
	// The theme colors reached the backend.
	if ts.Current() == nil || e.v.termd == nil {
		t.Fatal("no dock")
	}
	ts.SendText(id, "echo needle-1; echo needle-2; printf '\\033]52;c;Y2xpcA==\\007'\r")
	e.waitFor("output", func() bool { return strings.Contains(screenText(e), "needle-2") })
	e.waitFor("clipboard", func() bool { return e.v.clipboard != nil && *e.v.clipboard == "clip" })

	// Find: the action opens the bar, the query finds the lines.
	d := e.v.termd
	e.v.RunAction(ActTerminalFind)
	if !d.find.open {
		t.Fatal("find bar not open")
	}
	d.find.query = "needle-"
	d.search(id)
	e.waitFor("matches", func() bool { e.frame(); return len(d.find.matches) >= 4 })
	tv := d.views[id]
	e.waitFor("highlights", func() bool {
		e.frame()
		return len(tv.Matches) == len(d.find.matches) && tv.Current == len(d.find.matches)-1
	})
	// Next and previous wrap around.
	d.find.current = 0
	d.find.bar.Matches = len(d.find.matches)
	d.closeFind()
	e.frame()
	if d.find.open || len(tv.Matches) != 0 {
		t.Fatal("find bar still open")
	}
	// The Cmd+F of the view opens it again.
	_ = termview.FindEvent{}
	d.openFind()
	if !d.find.open {
		t.Fatal("reopen")
	}
	d.closeFind()

	// A running job makes the tab busy; closing asks first.
	ts.SendText(id, "sleep 30\r")
	e.waitFor("busy", func() bool { x := ts.Tab(id); return x != nil && x.Info.Busy && x.Info.Process == "sleep" })
	if got := tabTitle(ts.Tab(id).Info); got != "sleep" {
		t.Fatalf("title %q", got)
	}
	d.closeTab(id)
	if !e.v.PopupOpen() || len(ts.Tabs) != 1 {
		t.Fatal("no confirmation for a busy tab")
	}
	e.v.popup.Buttons[0].OnClick()
	e.waitFor("closed", func() bool { return len(ts.Tabs) == 0 })
}
