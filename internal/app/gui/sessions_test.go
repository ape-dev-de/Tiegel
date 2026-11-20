package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/modules"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// wsEnv is a window with the multi-project workspace: a host App, the
// Sessions and two temporary projects.
type wsEnv struct {
	t            *testing.T
	host         *api.App
	ws           *api.Workspace
	s            *Sessions
	h            *uitest.Harness
	rootA, rootB string
}

func wsProject(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	for n, c := range files {
		p := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newWsEnv(t *testing.T) *wsEnv {
	t.Helper()
	home := t.TempDir()
	opts := api.Options{
		ConfigDir: t.TempDir(),
		Interp:    interp.DiscoverOptions{Dirs: []string{}},
		LSP:       servers.DetectOptions{PathEnv: filepath.Join(home, "none"), HomeDir: home, ComposerHome: home, NoSystemDirs: true},
		IDEImport: api.IDEImportOptions{Home: home},
		TerminalPath: func(context.Context) string {
			return os.Getenv("PATH")
		},
	}
	opts.Extensions = append(opts.Extensions, modules.Install)
	host, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func() (*api.App, error) {
		o := opts
		o.GlobalSettings = host.Settings()
		return api.New(o)
	}
	ws, err := host.EnableWorkspace(api.WorkspaceOptions{NewApp: newApp})
	if err != nil {
		t.Fatal(err)
	}
	e := &wsEnv{t: t, host: host, ws: ws, h: uitest.NewHarness(windowSize),
		rootA: wsProject(t, "alpha", map[string]string{"a.php": "<?php\necho 'alpha';\n", "lib/util.php": "<?php\nfunction util() {}\n"}),
		rootB: wsProject(t, "beta", map[string]string{"b.php": "<?php\necho 'beta';\n// second line\n"}),
	}
	e.s = newSessions(host, ws, "darwin", nil, nil)
	t.Cleanup(func() {
		e.idle()
		e.s.Shutdown()
		_ = host.Close()
	})
	e.s.Start(e.rootA)
	e.waitFor("alpha shown", func() bool { return e.cur().Project.Root == e.rootA })
	return e
}

func (e *wsEnv) view() *View { return e.s.Current() }

func (e *wsEnv) cur() *controller.Controller { return e.view().ctl }

func (e *wsEnv) frame() {
	e.h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return e.s.Current().Frame(gtx) })
}

// idle waits for the lifecycle calls and every controller's work.
func (e *wsEnv) idle() {
	deadline := time.Now().Add(10 * time.Second)
	for e.s.ops.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for _, c := range e.s.Controllers() {
		c.Settle(time.Second)
	}
}

func (e *wsEnv) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		e.frame()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timeout waiting for %s (notice %q, projects %+v)", what, e.cur().Status.Notice, e.s.list)
		}
		for _, c := range e.s.Controllers() {
			c.Settle(20 * time.Millisecond)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *wsEnv) state(root string) workspace.State { return e.s.stateOf(root) }

func tabNames(c *controller.Controller) []string {
	var out []string
	for _, t := range c.Tabs {
		out = append(out, filepath.Base(t.File))
	}
	return out
}

// openBeta opens the second project next to the first.
func (e *wsEnv) openBeta() {
	e.view().requestProject(e.rootB)
	e.frame()
	if p := e.view().popup; p != nil { // "this window or new window": no host → this window
		e.t.Fatalf("unexpected popup %q", p.Title)
	}
	e.waitFor("beta shown", func() bool { return e.cur().Project.Root == e.rootB })
}

func TestSessionsSwitchKeepsViewState(t *testing.T) {
	e := newWsEnv(t)
	alpha := e.view()
	alpha.ctl.OpenFile("a.php", nil)
	e.waitFor("alpha tab", func() bool { return len(alpha.ctl.Tabs) == 1 })
	alpha.ShowTool(toolProblems)
	e.openBeta()
	beta := e.view()
	if beta == alpha || beta.ctl == alpha.ctl {
		t.Fatal("beta shares alpha's view")
	}
	if e.state(e.rootA) != workspace.StateBackground || e.state(e.rootB) != workspace.StateActive {
		t.Fatalf("states %+v", e.s.list)
	}
	beta.ctl.OpenFile("b.php", nil)
	e.waitFor("beta tab", func() bool { return len(beta.ctl.Tabs) == 1 })
	if got := tabNames(alpha.ctl); len(got) != 1 || got[0] != "a.php" {
		t.Fatalf("alpha tabs after opening beta's file: %v", got)
	}
	// The header lists both with their state; the title follows.
	open, current := e.s.Projects()
	if len(open) != 2 || current != e.rootB || open[0].State != toolbar.ProjectBackground || open[1].State != toolbar.ProjectActive {
		t.Fatalf("projects %+v %s", open, current)
	}
	if !strings.HasPrefix(windowTitle(e.cur()), "beta [") {
		t.Fatalf("title %q", windowTitle(e.cur()))
	}
	// Switching back is the same view with its tabs and tool windows.
	beta.hdr.handle(toolbar.SwitchProject{ID: e.rootA})
	e.waitFor("alpha again", func() bool { return e.view() == alpha })
	if got := tabNames(alpha.ctl); len(got) != 1 || got[0] != "a.php" || !alpha.ctl.Tools.Visible(toolProblems) {
		t.Fatalf("alpha state lost: %v", got)
	}
	if got := tabNames(beta.ctl); len(got) != 1 || got[0] != "b.php" {
		t.Fatalf("beta state lost: %v", got)
	}
	// Next/Previous Project cycle; the switcher chooser lists the others.
	alpha.RunAction(ActNextProject)
	e.waitFor("next", func() bool { return e.view() == beta })
	beta.RunAction(ActPrevProject)
	e.waitFor("previous", func() bool { return e.view() == alpha })
	alpha.RunAction(ActSwitchProject)
	e.frame()
	if alpha.chooseMode != chooseCustom || len(alpha.chooser.Items) == 0 || alpha.chooser.Items[0].Label != "beta" {
		t.Fatalf("switcher items %+v", alpha.chooser.Items)
	}
	alpha.custom.OnChoose(0, "")
	alpha.closeOverlay()
	e.waitFor("chosen", func() bool { return e.view() == beta })
	// Opening an open project switches to it.
	beta.requestProject(e.rootA)
	e.waitFor("requested", func() bool { return e.view() == alpha })
	// With other windows: "This Window" opens next to the others, "New
	// Window" stays available.
	host := &fakeHost{}
	alpha.Host = host
	gamma := wsProject(t, "gamma", map[string]string{"g.php": "<?php\n"})
	alpha.requestProject(gamma)
	e.frame()
	if alpha.popup == nil || !strings.Contains(alpha.popup.Detail, "keeps running") || alpha.popup.Buttons[1].Text != "New Window" {
		t.Fatalf("popup %+v", alpha.popup)
	}
	alpha.popup.Buttons[1].OnClick()
	alpha.ClosePopup()
	if len(host.opened) != 1 || host.opened[0] != gamma {
		t.Fatalf("new window %v", host.opened)
	}
	alpha.requestProject(gamma)
	e.frame()
	alpha.popup.Buttons[0].OnClick()
	alpha.ClosePopup()
	e.waitFor("gamma", func() bool { return e.cur().Project.Root == gamma })
	if len(e.s.list.Projects) != 3 || e.state(e.rootA) != workspace.StateBackground {
		t.Fatalf("projects %+v", e.s.list)
	}
}

func TestSessionsHibernateResumeRestoresTabs(t *testing.T) {
	e := newWsEnv(t)
	e.openBeta()
	beta := e.view()
	beta.ctl.OpenFile("b.php", nil)
	e.waitFor("beta tab", func() bool { return beta.activeSession() != nil })
	s := beta.activeSession()
	s.doc.Lock()
	s.ed.SetCaret(12)
	s.doc.Unlock()
	betaApp, _ := e.ws.App(e.rootB)

	// Hibernating the shown project shows alpha first, then releases
	// beta's App, controller and view.
	beta.hdr.handle(toolbar.HibernateProject{ID: e.rootB})
	e.waitFor("beta hibernated", func() bool { return e.state(e.rootB) == workspace.StateHibernated && e.s.slots[betaApp] == nil })
	if e.cur().Project.Root != e.rootA || len(e.s.slots) != 1 {
		t.Fatalf("shown %s, slots %d", e.cur().Project.Root, len(e.s.slots))
	}
	if beta.ctl.Context().Err() == nil {
		t.Fatal("beta's controller still open")
	}
	open, _ := e.s.Projects()
	if len(open) != 2 || open[1].State != toolbar.ProjectHibernated {
		t.Fatalf("projects %+v", open)
	}

	// Switching resumes it with its tabs and caret.
	e.view().hdr.handle(toolbar.SwitchProject{ID: e.rootB})
	e.waitFor("beta resumed", func() bool { return e.cur().Project.Root == e.rootB && len(e.cur().Tabs) == 1 })
	if e.view() == beta {
		t.Fatal("hibernated view reused")
	}
	e.waitFor("caret", func() bool {
		s := e.view().activeSession()
		if s == nil {
			return false
		}
		s.doc.Lock()
		defer s.doc.Unlock()
		return s.ed.Cursor().Primary().Head == 12
	})

	// Resume from the popup keeps it in the background.
	e.view().hdr.handle(toolbar.HibernateProject{ID: e.rootB})
	e.waitFor("hibernated again", func() bool { return e.state(e.rootB) == workspace.StateHibernated })
	e.view().hdr.handle(toolbar.ResumeProject{ID: e.rootB})
	e.waitFor("resumed in background", func() bool { return e.state(e.rootB) == workspace.StateBackground && len(e.s.slots) == 2 })
	if e.cur().Project.Root != e.rootA {
		t.Fatalf("resume switched to %s", e.cur().Project.Root)
	}
	// Auto-hibernation (the workspace hibernates without the GUI): the
	// GUI stores the tabs afterwards.
	var bv *View
	for _, sl := range e.s.slots {
		if sl.root == e.rootB {
			bv = sl.view
		}
	}
	e.waitFor("background tabs", func() bool { return len(bv.ctl.Tabs) == 1 })
	if _, err := e.ws.Hibernate(t.Context(), api.ProjectsHibernateParams{Name: e.rootB}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("auto state stored", func() bool { return len(e.s.slots) == 1 && e.s.ops.Load() == 0 })
	info, err := e.ws.Switch(t.Context(), e.rootB)
	if err != nil || !strings.Contains(string(info.ClientState), "b.php") {
		t.Fatalf("client state %s %v", info.ClientState, err)
	}
}

func TestSessionsBusyBlocksHibernateAndClose(t *testing.T) {
	e := newWsEnv(t)
	e.openBeta()
	beta := e.view()
	beta.ctl.Run.SaveConfig(run.Config{Name: "Sleep", Type: run.TypeCommand, Values: map[string]any{
		"command": helperCommand(t), "env": []string{guiRunHelper + "=sleep"}}}, func(c run.Config) { beta.ctl.Run.RunConfig(c.ID) })
	e.waitFor("sleeper", func() bool { t := beta.ctl.Run.Current(); return t != nil && t.Active() })

	beta.hdr.handle(toolbar.HibernateProject{ID: e.rootB})
	e.waitFor("busy popup", func() bool { return beta.popup != nil })
	if !strings.Contains(beta.popup.Detail, "run: Sleep") || beta.popup.Buttons[0].Text != "Stop and Hibernate" {
		t.Fatalf("popup %q %q", beta.popup.Title, beta.popup.Detail)
	}
	beta.ClosePopup()
	e.idle()
	if e.state(e.rootB) != workspace.StateActive {
		t.Fatalf("cancel hibernated: %+v", e.s.list)
	}
	// Close asks the same way; Force stops the run and closes.
	beta.RunAction(ActCloseProject)
	e.waitFor("close popup", func() bool { return beta.popup != nil })
	if !strings.Contains(beta.popup.Detail, "run: Sleep") || beta.popup.Buttons[0].Text != "Stop and Close" {
		t.Fatalf("popup %q", beta.popup.Detail)
	}
	beta.popup.Buttons[0].OnClick()
	beta.ClosePopup()
	e.waitFor("beta closed", func() bool { return e.state(e.rootB) == "" && e.cur().Project.Root == e.rootA })
	if len(e.s.slots) != 1 {
		t.Fatalf("slots %d", len(e.s.slots))
	}
	// The last project closed: the welcome screen of the host.
	e.view().RunAction(ActCloseProject)
	e.waitFor("welcome", func() bool { return !e.cur().Project.Open && e.view() == e.s.hostSlot.view })
	if windowTitle(e.cur()) != "Tiegel" {
		t.Fatalf("title %q", windowTitle(e.cur()))
	}
}

// The project shortcuts are free in the default keymaps.
func TestProjectShortcutsFree(t *testing.T) {
	for _, preset := range keymap.Presets {
		s := keymap.NewScheme(preset, nil)
		for _, a := range defaultActions() {
			if a.ID != ActNextProject && a.ID != ActPrevProject {
				continue
			}
			chord := a.PC
			if preset == keymap.PresetMac {
				chord = a.Mac
			}
			c, err := keymap.ParseChord(chord)
			if err != nil {
				t.Fatal(err)
			}
			for _, ctx := range keymap.Contexts {
				if other, ok := s.Lookup(ctx, c); ok {
					t.Errorf("%s %s: %s is bound to %s", preset, a.ID, chord, other)
				}
			}
		}
	}
}

// TestProjectPopupScreenshot renders the project popup with two open
// projects, one of them hibernated (TIEGEL_SHOTS=<dir> writes the PNGs).
func TestProjectPopupScreenshot(t *testing.T) {
	e := newWsEnv(t)
	e.openBeta()
	e.cur().OpenFile("b.php", nil)
	e.waitFor("tab", func() bool { return e.cur().ActiveTab() != nil })
	e.view().hdr.handle(toolbar.SwitchProject{ID: e.rootA})
	e.waitFor("alpha", func() bool { return e.cur().Project.Root == e.rootA })
	e.view().hdr.handle(toolbar.HibernateProject{ID: e.rootB})
	e.waitFor("beta hibernated", func() bool { return e.state(e.rootB) == workspace.StateHibernated && len(e.s.slots) == 1 })
	v := e.view()
	v.ctl.OpenFile("a.php", nil)
	e.waitFor("alpha tab", func() bool { return v.ctl.ActiveTab() != nil })
	v.hdr.bar.Show(toolbar.PopupProject)
	e.frame()
	if b := v.hdr.bar; b.Opened() != toolbar.PopupProject || len(b.Projects) != 2 || b.Projects[1].State != toolbar.ProjectHibernated || b.Current != e.rootA {
		t.Fatalf("bar %+v", b.Projects)
	}
	env := &env{t: t, ctl: v.ctl, v: v, h: e.h}
	env.shot("projects-popup")
}

// Review wave 9 U4: switching projects gives the keyboard to the editor
// of the shown project.
func TestSessionsSwitchFocusesEditor(t *testing.T) {
	e := newWsEnv(t)
	alpha := e.view()
	alpha.ctl.OpenFile("a.php", nil)
	e.waitFor("alpha editor", func() bool { return alpha.activeSession() != nil })
	e.openBeta()
	beta := e.view()
	beta.ctl.OpenFile("b.php", nil)
	e.waitFor("beta editor", func() bool { return beta.activeSession() != nil })
	alpha.focusEditor = false
	beta.hdr.handle(toolbar.SwitchProject{ID: e.rootA})
	e.waitFor("alpha shown", func() bool { return e.view() == alpha })
	e.frame()
	e.frame()
	if s := alpha.activeSession(); s == nil || !s.ed.Focused() {
		t.Fatal("alpha's editor has no keyboard focus after the switch")
	}
	beta.focusEditor = false
	alpha.RunAction(ActNextProject)
	e.waitFor("beta shown", func() bool { return e.view() == beta })
	e.frame()
	e.frame()
	if s := beta.activeSession(); s == nil || !s.ed.Focused() {
		t.Fatal("beta's editor has no keyboard focus after the switch")
	}
}

// Review wave 9 B7/U7: client state handed in after an auto-hibernation
// never hibernates the project again when a resume came first.
func TestSessionsLateClientStateKeepsResumedProject(t *testing.T) {
	e := newWsEnv(t)
	e.openBeta()
	beta := e.view()
	beta.ctl.OpenFile("b.php", nil)
	e.waitFor("beta tab", func() bool { return len(beta.ctl.Tabs) == 1 })
	beta.hdr.handle(toolbar.SwitchProject{ID: e.rootA})
	e.waitFor("alpha shown", func() bool { return e.cur().Project.Root == e.rootA })
	sl := e.s.slotOf(e.rootB)
	if sl == nil {
		t.Fatal("no slot for beta")
	}
	// The workspace hibernates beta, the GUI sees it hibernated, but a
	// resume wins before the client state is attached.
	if _, err := e.ws.Hibernate(t.Context(), api.ProjectsHibernateParams{Name: e.rootB}); err != nil {
		t.Fatal(err)
	}
	e.s.list = e.ws.List()
	if _, err := e.ws.Resume(t.Context(), e.rootB); err != nil {
		t.Fatal(err)
	}
	e.s.release(sl)
	e.idle()
	for _, p := range e.ws.List().Projects {
		if p.Root == e.rootB && p.State != workspace.StateBackground {
			t.Fatalf("beta %s after the late client state", p.State)
		}
	}
	e.waitFor("beta slot again", func() bool { return e.s.slotOf(e.rootB) != nil && e.state(e.rootB) == workspace.StateBackground })
}

// Review wave 9 U6: a native menu command reaches the project view shown
// when it runs, not the view of the window's last frame.
func TestRouteMenuToShownProject(t *testing.T) {
	e := newWsEnv(t)
	alpha := e.view()
	m := &windows{wins: map[*window]bool{}}
	win := &window{m: m, sess: e.s, view: alpha}
	m.wins[win] = true
	m.focused = win
	e.openBeta()
	beta := e.view()
	m.route("app.about")
	e.frame()
	if beta.popup == nil || alpha.popup != nil {
		t.Fatalf("menu command went to the wrong view (alpha %v, beta %v)", alpha.popup != nil, beta.popup != nil)
	}
	beta.ClosePopup()
	(&windows{}).route("app.about") // no focused window: nothing happens
}
