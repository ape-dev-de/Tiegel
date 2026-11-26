package gui

import (
	"encoding/json"
	"image"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// termText reads the text of terminal session id from the App of root.
func (e *wsEnv) termText(root, id string) string {
	app, err := e.ws.App(root)
	if err != nil || app == nil {
		return ""
	}
	res, err := app.Call(e.t.Context(), "terminal.text", json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil {
		return ""
	}
	if t, ok := res.(api.TerminalText); ok {
		return t.Text
	}
	return ""
}

// D547: switching hibernates the project left (language server, watcher
// and index go with its App), its terminals keep running with their
// shell state and are shown again, selected as before, on the way back.
func TestSessionsSwitchHibernatesKeepsTerminals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	e := newWsEnvOnSwitch(t, "hibernate")
	alpha := e.view()
	alpha.ctl.Terminals.Open(api.TerminalOpenParams{Shell: "/bin/sh", Env: []string{"PS1=$ "}})
	alpha.ctl.Terminals.Open(api.TerminalOpenParams{Shell: "/bin/sh", Env: []string{"PS1=$ "}})
	e.waitFor("two terminals", func() bool { return len(alpha.ctl.Terminals.Tabs) == 2 })
	first := alpha.ctl.Terminals.Tabs[0].Info.ID
	alpha.ctl.Terminals.Select(first)
	alpha.ctl.Terminals.SendText(first, "export FOO=bar; cd lib\r")
	e.waitFor("export", func() bool { return strings.Contains(e.termText(e.rootA, first), "cd lib") })

	e.openBeta()
	e.waitFor("alpha hibernated", func() bool { return e.state(e.rootA) == workspace.StateHibernated && e.s.slotOf(e.rootA) == nil })
	if app, _ := e.ws.App(e.rootA); app != nil {
		t.Fatal("hibernated alpha keeps its App (language server, watcher, index)")
	}
	if e.cur().Status.Notice != "" {
		t.Fatalf("notice %q", e.cur().Status.Notice)
	}

	// Back: the same sessions, the same selection, the shell state kept.
	e.view().hdr.handle(toolbar.SwitchProject{ID: e.rootA})
	e.waitFor("alpha resumed with terminals", func() bool {
		return e.cur().Project.Root == e.rootA && len(e.cur().Terminals.Tabs) == 2
	})
	e.waitFor("beta hibernated", func() bool { return e.state(e.rootB) == workspace.StateHibernated })
	term := e.cur().Terminals
	e.waitFor("selection restored", func() bool { return term.Selected == first })
	if x := term.Tab(first); x == nil || !x.Info.Running {
		t.Fatal("terminal not running")
	}
	term.SendText(first, "echo \"v=$FOO\"; pwd\r")
	e.waitFor("shell state", func() bool {
		txt := e.termText(e.rootA, first)
		return strings.Contains(txt, "v=bar") && strings.Contains(txt, "/lib\n")
	})
}

// A project with a running configuration stays in the background on a
// switch, with a notice.
func TestSessionsSwitchBusyStaysBackground(t *testing.T) {
	e := newWsEnvOnSwitch(t, "hibernate")
	alpha := e.view()
	alpha.ctl.Run.SaveConfig(run.Config{Name: "Sleep", Type: run.TypeCommand, Values: map[string]any{
		"command": helperCommand(t), "env": []string{guiRunHelper + "=sleep"}}}, func(c run.Config) { alpha.ctl.Run.RunConfig(c.ID) })
	e.waitFor("sleeper", func() bool { t := alpha.ctl.Run.Current(); return t != nil && t.Active() })
	e.openBeta()
	e.waitFor("notice", func() bool {
		return strings.Contains(e.cur().Status.Notice, "alpha keeps running in the background: run: Sleep")
	})
	if e.state(e.rootA) != workspace.StateBackground || e.s.slotOf(e.rootA) == nil {
		t.Fatalf("alpha %s", e.state(e.rootA))
	}
	// Closing beta shows alpha again; the run is stopped on close.
	alpha.ctl.Run.Stop(alpha.ctl.Run.Current().Info.ID)
	e.view().RunAction(ActCloseProject)
	e.waitFor("alpha back", func() bool { return e.cur().Project.Root == e.rootA })
}

// Opening a project from Recent Projects defaults to this window; "Open
// in New Window" (context menu, Shift+click) asks the window host.
func TestSessionsOpenInNewWindow(t *testing.T) {
	e := newWsEnvOnSwitch(t, "hibernate")
	alpha := e.view()
	host := &fakeHost{}
	alpha.Host = host
	alpha.hdr.sync()
	if !alpha.hdr.bar.NewWindows {
		t.Fatal("header offers no new window")
	}
	alpha.hdr.handle(toolbar.OpenRecentProject{Path: e.rootB, NewWindow: true})
	if len(host.opened) != 1 || host.opened[0] != e.rootB {
		t.Fatalf("opened %v", host.opened)
	}
	// The default opens in this window, without asking.
	alpha.hdr.handle(toolbar.OpenRecentProject{Path: e.rootB})
	e.frame()
	if alpha.popup != nil {
		t.Fatalf("popup %q", alpha.popup.Title)
	}
	e.waitFor("beta here", func() bool { return e.cur().Project.Root == e.rootB })
	beta := e.view()
	beta.Host = host
	// An open project of this window moves to a new window.
	beta.hdr.handle(toolbar.SwitchProject{ID: e.rootA, NewWindow: true})
	e.waitFor("alpha moved", func() bool { return e.state(e.rootA) == "" && len(host.opened) == 2 })
	if host.opened[1] != e.rootA {
		t.Fatalf("opened %v", host.opened)
	}
	// The welcome screen's context menu offers it as well, Recent
	// Projects… a submenu.
	var shown []MenuItem
	beta.MenuPresenter = func(_ *View, _ string, items []MenuItem, _ image.Point) { shown = items }
	beta.recentProjectMenu(e.rootA)
	if len(shown) != 4 || shown[1].Title != "Open in New Window" {
		t.Fatalf("menu %+v", shown)
	}
	shown[1].Run()
	e.waitFor("third window", func() bool { return len(host.opened) == 3 })
	beta.ctl.RecentProjects = []string{e.rootA}
	beta.openRecentProjects()
	if n := len(shown); n != 3 || shown[2].Title != "Open in New Window" || len(shown[2].Children) != 1 {
		t.Fatalf("recent items %+v", shown)
	}
}
