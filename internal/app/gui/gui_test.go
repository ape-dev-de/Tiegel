package gui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/modules"
	"github.com/ape-dev-de/tiegel/internal/syntax"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var windowSize = image.Pt(1280, 800)

const samplePHP = `<?php

enum Suit
{
    case Hearts;
}

function greet(string $name): string
{
    return "Hello $name";
}
`

// env is a real App with all modules (no PATH lookups) and a project in a
// temporary directory, driven through the view.
type env struct {
	t    *testing.T
	app  *api.App
	root string
	ctl  *controller.Controller
	v    *View
	h    *uitest.Harness
}

// ideImportRoots are the configuration roots of the settings import in
// newEnvWith (a test sets them to the fixture).
var ideImportRoots []string

func newEnv(t *testing.T, withProject bool) *env {
	t.Helper()
	return newEnvWith(t, withProject, nil, nil)
}

// newEnvWith is newEnv with extra project files and a preparation step
// (e.g. git init) before the backend starts.
func newEnvWith(t *testing.T, withProject bool, extra map[string]string, prep func(root string)) *env {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"src/Shop.php":  samplePHP,
		"index.php":     "<?php\necho 1;\n",
		"composer.json": `{"require": {"php": "^8.0"}}`,
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if prep != nil {
		prep(root)
	}
	home := t.TempDir()
	opts := api.Options{
		ConfigDir: t.TempDir(),
		Interp:    interp.DiscoverOptions{Dirs: []string{}},
		LSP:       servers.DetectOptions{PathEnv: filepath.Join(home, "none"), HomeDir: home, ComposerHome: home, NoSystemDirs: true},
		// The settings import never looks at the real home.
		IDEImport: api.IDEImportOptions{Home: home, Roots: ideImportRoots},
		// Terminal sessions never ask the login shell.
		TerminalPath: func(context.Context) string { return os.Getenv("PATH") },
	}
	opts.Extensions = append(opts.Extensions, modules.Install)
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctl := controller.New(controller.NewInProcess(a))
	v := NewView(ctl, "darwin")
	e := &env{t: t, app: a, root: root, ctl: ctl, v: v, h: uitest.NewHarness(windowSize)}
	t.Cleanup(func() {
		v.Close()
		ctl.Close()
		_ = a.Close()
	})
	project := ""
	if withProject {
		project = root
	}
	ctl.Start(project)
	e.settle()
	if withProject {
		e.root = ctl.Project.Root // symlinks resolved (macOS /private/var)
	}
	return e
}

func (e *env) widget(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return e.v.Frame(gtx)
}

func (e *env) frame() { e.h.Frame(e.widget) }

// settle waits for background work and renders a few frames.
func (e *env) settle() {
	for i := 0; i < 3; i++ {
		e.ctl.Settle(2 * time.Second)
		e.frame()
	}
}

func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	// Generous: the deadline only matters when the condition never
	// holds; under load (go test -race ./... on CI) events can take
	// seconds.
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timeout waiting for %s (notice %q)", what, e.ctl.Status.Notice)
		}
		e.ctl.Settle(200 * time.Millisecond)
		e.frame()
	}
}

func TestWelcomeScreen(t *testing.T) {
	e := newEnv(t, false)
	if e.ctl.Project.Open {
		t.Fatal("no project expected")
	}
	uitest.Smoke(t, "welcome", windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		e.ctl.Prefs.Theme = th.Mode.String()
		return e.v.Frame(gtx)
	})
	// Opening from the path field.
	e.v.welcome.SetPath(e.root)
	e.v.welcome.Recent = nil
	e.ctl.OpenProject(e.root)
	e.waitFor("project", func() bool { return e.ctl.Project.Open })
	if len(e.ctl.RecentProjects) == 0 {
		t.Fatal("recent projects not recorded")
	}
}

func TestWorkbenchSmokeBothThemes(t *testing.T) {
	e := newEnv(t, true)
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	e.v.toggleTool(toolProblems)
	for _, mode := range []string{"dark", "light"} {
		e.ctl.Prefs.Theme = mode
		e.frame()
		if e.v.th.Mode.String() != mode {
			t.Fatalf("theme %s not applied", mode)
		}
	}
	uitest.Smoke(t, "workbench", windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		e.ctl.Prefs.Theme = th.Mode.String()
		return e.v.Frame(gtx)
	})
	// Overlays render as well.
	e.v.openSettings()
	e.waitFor("settings schema", func() bool { return e.ctl.Schema != nil })
	e.settle()
	pages := settingsPages(e.ctl)
	if len(pages) < 6 {
		t.Fatalf("settings pages %d", len(pages))
	}
	for _, pg := range pages {
		e.v.settings.Selected = pg.ID
		e.settle()
	}
	e.v.closeOverlay()
	e.v.openQuickOpen()
	e.waitFor("file index", func() bool { return len(e.v.chooser.Items) == 3 })
	e.v.openGotoLine()
	e.frame()
	e.v.closeOverlay()
	e.frame()
}

// TestEditTypeAutosaveDiagnostics is the end-to-end flow: open a file from
// the project, type into the editor, lose the focus → the file is saved by
// the kernel's autosave; diagnostics events show up in the editor and the
// Problems list.
func TestEditTypeAutosaveDiagnostics(t *testing.T) {
	e := newEnv(t, true)
	if e.ctl.Status.LSPRunning || !strings.Contains(e.ctl.Status.LSPHint, "never downloads") {
		t.Fatalf("LSP status %q (%q)", e.ctl.Status.LSP, e.ctl.Status.LSPHint)
	}
	if e.ctl.Tree == nil || !e.ctl.Tree.Root.Loaded {
		t.Fatal("tree not loaded")
	}
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	file := filepath.Join(e.root, "src", "Shop.php")
	s := e.v.editors[sessionKey{e.ctl.ActiveGroup(), file}]
	if s == nil {
		t.Fatal("no editor session")
	}

	// Focus the editor and type at the start of line 2.
	e.v.focusEditor = true
	e.frame()
	e.frame()
	if !s.ed.Focused() {
		t.Fatal("editor not focused")
	}
	s.doc.Lock()
	s.ed.SetCaret(len("<?php\n"))
	s.doc.Unlock()
	e.frame()
	for _, ch := range "// typed\n" {
		st := e.h.Router.EditorState()
		e.h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: string(ch)})
		e.frame()
	}
	if !e.ctl.ActiveTab().Modified {
		t.Fatal("tab should be modified after typing")
	}
	want := strings.Replace(samplePHP, "<?php\n", "<?php\n// typed\n", 1)

	// Focus loss → autosave through the kernel.
	e.h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: nil})
		return e.widget(gtx, th)
	})
	e.waitFor("autosave", func() bool {
		data, _ := os.ReadFile(file)
		return string(data) == want
	})
	e.waitFor("modified flag cleared", func() bool { return !e.ctl.ActiveTab().Modified })

	// php-features reports the enum (PHP 8.1) against composer's ^8.0.
	// Its checks need Tree-sitter (cgo); without it (Windows build, D80)
	// only the language server diagnostic below is shown.
	wantProblems := 1
	if syntax.TreeSitter {
		wantProblems = 2
		e.waitFor("php diagnostics", func() bool { return len(s.ed.Diagnostics()) > 0 })
	}
	// A language server diagnostic arrives as event.
	e.app.Bus().Publish(api.TopicLSPDiagnostics, map[string]any{
		"path": "src/Shop.php", "file": file, "version": 1,
		"diagnostics": []map[string]any{{"severity": "error", "message": "Undefined function grete", "start": 10, "end": 14, "line": 2, "col": 5}},
	})
	e.waitFor("lsp diagnostics", func() bool {
		for _, d := range s.ed.Diagnostics() {
			if d.Message == "Undefined function grete" && d.Severity == editorview.SeverityError {
				return true
			}
		}
		return false
	})
	if len(e.v.probs.Items) < wantProblems {
		t.Fatalf("problems %+v", e.v.probs.Items)
	}

	// Problems click jumps to the location.
	e.v.toggleTool(toolProblems)
	e.frame()

	// Find bar.
	s.doc.Lock()
	v := e.v
	v.findOpen = true
	v.findQueryChanged(s, "name", v.find.Options())
	s.doc.Unlock()
	e.frame()
	if v.find.Matches != 2 || v.find.Current != 1 {
		t.Fatalf("find %d/%d", v.find.Current, v.find.Matches)
	}

	// Go to line.
	v.gotoLine(8, 1)
	e.frame()
	if !strings.HasPrefix(s.caretText, "8:1") {
		t.Fatalf("caret %q", s.caretText)
	}

	// Closing the tab saves and closes the document.
	e.ctl.CloseTab(e.ctl.Active)
	e.settle()
	if len(e.v.editors) != 0 {
		t.Fatal("editor not released")
	}
}

func TestShortcutsAndRegistry(t *testing.T) {
	e := newEnv(t, true)
	r := e.v.Registry
	if _, ok := r.Action(ActQuickOpen); !ok || r.ChordText(ActQuickOpen, "darwin") != "Cmd+Shift+O" || r.ChordText(ActQuickOpen, "linux") != "Ctrl+Shift+N" {
		t.Fatal("quick open action")
	}
	// Cmd+Shift+O opens the chooser, Cmd+, the settings.
	e.h.Router.Queue(key.Event{Name: "O", Modifiers: key.ModCommand | key.ModShift, State: key.Press})
	e.frame()
	e.frame()
	if e.v.chooseMode != chooseFiles {
		t.Fatalf("quick open not triggered (mode %d)", e.v.chooseMode)
	}
	e.h.Router.Queue(key.Event{Name: ",", Modifiers: key.ModCommand, State: key.Press})
	e.frame()
	e.frame()
	if !e.v.settingsOpen {
		t.Fatal("settings not opened")
	}
	e.h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	e.frame()
	if e.v.settingsOpen {
		t.Fatal("escape should close settings")
	}

	// Extension points.
	opened := false
	r.AddToolWindow(ToolWindow{ID: "git", Title: "Git", Order: 20, OnOpen: func() { opened = true },
		Layout: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}})
	r.AddGutterProvider(func(string) editorview.MarkerProvider {
		return editorview.LineMarkers{0: {{Kind: editorview.MarkerGitAdded}}}
	})
	r.AddGutterProvider(func(string) editorview.MarkerProvider {
		return editorview.LineMarkers{0: {{Kind: editorview.MarkerBreakpoint}}}
	})
	if m := r.markers("x").Markers(0); len(m) != 2 {
		t.Fatalf("combined markers %v", m)
	}
	ran := false
	r.AddAction(Action{ID: "debug.resume", Title: "Resume", Mac: "F9", PC: "F9", Run: func(*View) { ran = true }})
	e.v.toggleTool("git")
	e.frame()
	e.h.Router.Queue(key.Event{Name: "F9", State: key.Press})
	e.frame()
	e.frame()
	// Problems, Run, Terminal, Tests, the test window, Debug, Services and
	// Language Server (the project is no repository, so the git windows
	// are not installed).
	if !opened || !ran || len(e.v.bench.Stripes[templates.SideBottom]) != 8 {
		t.Fatalf("registry: opened %v ran %v tabs %+v", opened, ran, e.v.bench.Stripes)
	}
	r.AddStatusProvider(func() []statusbar.Item { return []statusbar.Item{{ID: "debug", Text: "Listening"}} })
	e.frame()
}

// TestScreenshots renders the welcome screen and the workbench headlessly.
// With TIEGEL_SHOTS=<dir> the PNGs are written there for visual review.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	e := newEnv(t, true)
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	if syntax.TreeSitter { // php-features checks need Tree-sitter (D80)
		e.waitFor("php diagnostics", func() bool { return e.ctl.DiagVersion > 1 && len(e.ctl.Problems()) > 0 })
	}
	e.v.toggleTool(toolProblems)
	e.settle()
	// The opened file is revealed: its folder is expanded and loaded.
	if n := e.ctl.Tree.Node("src"); n == nil || !n.Loaded || !e.v.tree.Expanded("src") {
		t.Fatalf("src not revealed: %+v", n)
	}
	for _, mode := range []string{"dark", "light"} {
		e.ctl.Prefs.Theme = mode
		e.frame()
		img, err := uitest.Render(e.v.th, windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return e.v.Frame(gtx)
		})
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if n := uitest.CountDiffering(img, e.v.th.Color.Background); n < 20000 {
			t.Errorf("%s: only %d pixels drawn", mode, n)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "workbench-"+mode+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}

// A panicking action reports itself instead of taking the window down.
func TestRunActionRecoversPanic(t *testing.T) {
	e := newEnv(t, false)
	e.v.Registry.AddAction(Action{ID: "test.boom", Title: "Boom…", Run: func(*View) { panic("kaputt") }})
	e.v.RunAction("test.boom")
	if n := e.ctl.Status.Notice; n != "Boom failed: kaputt" {
		t.Fatalf("notice %q", n)
	}
	e.frame()
}
