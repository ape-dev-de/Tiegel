package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
)

// processStart approximates the process start (package initialisation).
var processStart = time.Now()

// Run opens the first window for app and runs until all windows are
// closed or ctx is cancelled. With projectPath the project is shown (it
// may already be open in app); without it the welcome screen appears.
//
// Every window has its own api.App hosting a multi-project workspace
// (api.Workspace, D468/D477): one active project, others in the
// background or hibernated, each in its own App created by newApp. newApp
// also creates the App of further windows ("New Window", "open in new
// window"). Their global settings are shared with app (see
// api.Options.GlobalSettings). newApp may be nil: then projects always
// open in the current window (with the workspace's default Apps).
//
// Gio's app.Main must own the main goroutine and never returns, so the
// window loops run on goroutines; when the last window closed (modified
// documents are saved on close, each App is closed), Run exits the
// process (exit code 1 on error).
func Run(ctx context.Context, a *api.App, projectPath string, newApp func() (*api.App, error)) error {
	if os.Getenv("TIEGEL_STARTUP_TRACE") != "" {
		fmt.Fprintf(os.Stderr, "tiegel: backend ready after %v\n", time.Since(processStart).Round(time.Millisecond))
	}
	m := &windows{ctx: ctx, newApp: newApp, wins: map[*window]bool{}}
	// Mouse side buttons and swipes (macOS) go to the focused window.
	native.SetGestureHandler(func(g string) { m.route(gesturePrefix + g) })
	m.open(a, projectPath)
	app.Main()
	return nil
}

// windows manages the project windows of the process.
type windows struct {
	ctx    context.Context
	newApp func() (*api.App, error)

	mu      sync.Mutex
	wins    map[*window]bool
	focused *window
	lastErr error
}

// window is one project window.
type window struct {
	m    *windows
	w    *app.Window
	view *View // the view drawn (guarded by windows.mu)
	sess *Sessions
	// roots are the window's open projects (guarded by windows.mu).
	roots []string
}

// open starts a window for a showing project (may be "").
func (m *windows) open(a *api.App, project string) {
	win := &window{m: m, w: new(app.Window)}
	m.mu.Lock()
	m.wins[win] = true
	m.mu.Unlock()
	go func() {
		win.w.Option(app.Title("Tiegel"), app.Size(unit.Dp(1280), unit.Dp(820)), app.MinSize(unit.Dp(640), unit.Dp(400)))
		err := win.loop(a, project)
		if cerr := a.Close(); err == nil && cerr != nil {
			fmt.Fprintf(os.Stderr, "tiegel: %v\n", cerr)
		}
		m.closed(win, err)
	}()
}

// closed removes win; the process ends with the last window.
func (m *windows) closed(win *window, err error) {
	m.mu.Lock()
	delete(m.wins, win)
	if m.focused == win {
		m.focused = nil
	}
	if err != nil {
		m.lastErr = err
	}
	left := len(m.wins)
	last := m.lastErr
	m.mu.Unlock()
	if left == 0 {
		os.Exit(exitCode(last))
	}
}

// OpenWindow implements WindowHost.
func (m *windows) OpenWindow(path string) {
	if m.newApp == nil {
		return
	}
	go func() {
		a, err := m.newApp()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tiegel: new window: %v\n", err)
			return
		}
		m.open(a, path)
	}()
}

// FocusProject implements WindowHost: the other window that has root
// open is raised and switches to it.
func (m *windows) FocusProject(v *View, root string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for win := range m.wins {
		if win.view == v || root == "" || !slices.Contains(win.roots, root) {
			continue
		}
		win.w.Perform(system.ActionRaise)
		if sess := win.sess; sess != nil {
			sess.Post(func() { sess.Switch(root) })
		}
		return true
	}
	return false
}

// quit closes all windows (each saves its documents).
func (m *windows) quit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for win := range m.wins {
		win.w.Perform(system.ActionClose)
	}
}

// route delivers a native menu item to the window with the focus.
func (m *windows) route(id string) {
	m.mu.Lock()
	win := m.focused
	m.mu.Unlock()
	if win != nil && win.view != nil {
		win.view.Post(func() { win.view.runMenuID(id) })
	}
}

func exitCode(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: %v\n", err)
		return 1
	}
	return 0
}

// loop runs the window event loop.
func (win *window) loop(a *api.App, projectPath string) error {
	w, m, ctx := win.w, win.m, win.m.ctx
	// Background work may finish before the native window exists; Gio's
	// window must not be poked (Invalidate) before its first event – the
	// first frame draws everything anyway.
	var live atomic.Bool
	invalidate := func() {
		if live.Load() {
			w.Invalidate()
		}
	}
	ws, err := a.EnableWorkspace(api.WorkspaceOptions{NewApp: m.newApp})
	if err != nil {
		return err
	}
	sess := newSessions(a, ws, runtime.GOOS, invalidate, func(v *View) {
		v.ctl.Invalidate = invalidate
		v.Invalidate = invalidate
		v.Raise = func() {
			if live.Load() {
				w.Perform(system.ActionRaise)
			}
		}
		v.Host, v.Quit = m, m.quit
	})
	defer sess.Shutdown()
	m.mu.Lock()
	win.sess, win.view = sess, sess.Current()
	m.mu.Unlock()
	sess.Start(projectPath)
	go func() {
		<-ctx.Done()
		w.Perform(system.ActionClose)
	}()

	var ops op.Ops
	first := true
	focused := true
	title := ""
	for {
		ev := w.Event()
		live.Store(true)
		switch e := ev.(type) {
		case app.DestroyEvent:
			saveAllOnClose(sess)
			return e.Err
		case app.ConfigEvent:
			ctl := sess.Current().ctl
			if focused && !e.Config.Focused {
				ctl.Autosave("focusLost")
			}
			focused = e.Config.Focused
			if focused {
				m.mu.Lock()
				m.focused = win
				m.mu.Unlock()
				ctl.ReloadRecentProjects()
				ctl.Work.Refresh() // external changes while away (watch.refresh)
			}
		case app.FrameEvent:
			if ctx.Err() != nil {
				saveAllOnClose(sess)
				return nil
			}
			frameStart := time.Now()
			gtx := app.NewContext(&ops, e)
			v := sess.Current()
			v.Frame(gtx)
			layoutDone := time.Now()
			e.Frame(gtx.Ops)
			if first && os.Getenv("TIEGEL_STARTUP_TRACE") != "" {
				fmt.Fprintf(os.Stderr, "tiegel: first FrameEvent after %v (layout %v, GPU submit %v)\n",
					frameStart.Sub(processStart).Round(time.Millisecond), layoutDone.Sub(frameStart).Round(time.Millisecond),
					time.Since(layoutDone).Round(time.Millisecond))
			}
			v.syncNativeMenu(focused, m.route)
			m.mu.Lock()
			win.view, win.roots = v, sess.Roots()
			m.mu.Unlock()
			if t := windowTitle(v.ctl); t != title {
				title = t
				w.Option(app.Title(t))
			}
			if first {
				first = false
				d := time.Since(processStart)
				v.ctl.Status.Notice = ""
				if os.Getenv("TIEGEL_STARTUP_TRACE") != "" {
					fmt.Fprintf(os.Stderr, "tiegel: first frame after %v\n", d.Round(time.Millisecond))
				}
			}
		}
	}
}

// saveAllOnClose saves the modified documents of every project of the
// window.
func saveAllOnClose(sess *Sessions) {
	for _, ctl := range sess.Controllers() {
		saveOnClose(ctl)
	}
}

// saveOnClose saves modified documents synchronously before the window
// goes away (autosave on close).
func saveOnClose(ctl *controller.Controller) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ctl.AutosaveSync(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "tiegel: saving on close: %v\n", err)
	}
}

// windowTitle is "project [~/path] – file" (the project name and its
// path, then the active file).
func windowTitle(c *controller.Controller) string {
	if !c.Project.Open {
		return "Tiegel"
	}
	t := c.Project.Name + " [" + tildePath(c.Project.Root) + "]"
	if tab := c.ActiveTab(); tab != nil {
		t += " – " + tab.Path
	}
	return t
}

// tildePath abbreviates the home directory as ~.
func tildePath(p string) string {
	if h := homeDir(); h != "" {
		if r, err := filepath.Rel(h, p); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			if r == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + r
		}
	}
	return p
}

// systemDarkMode reports the OS appearance for the "system" theme. Only
// macOS is queried (local `defaults`, no network); elsewhere dark is
// assumed.
func systemDarkMode() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return err == nil && strings.Contains(string(out), "Dark")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
