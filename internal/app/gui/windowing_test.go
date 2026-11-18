package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/modules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// chooseLabel picks the chooser item whose label contains s, or the
// entry of the open context menu labeled s.
func (e *env) chooseLabel(s string) {
	e.t.Helper()
	if e.v.navd.menus.Opened() {
		if !e.v.navd.menus.choose(e.v, s) {
			e.t.Fatalf("no menu entry %q", s)
		}
		e.frame()
		return
	}
	// An exact label wins: a substring may also occur in a random temp
	// directory name (e.g. "001" in "…2560200148").
	idx := slices.IndexFunc(e.v.chooser.Items, func(it palette.Item) bool { return it.Label == s })
	if idx < 0 {
		idx = slices.IndexFunc(e.v.chooser.Items, func(it palette.Item) bool { return strings.Contains(it.Label, s) })
	}
	if idx < 0 {
		e.t.Fatalf("no chooser item %q in %+v", s, e.v.chooser.Items)
	}
	e.v.choose(palette.ChooseEvent{Index: idx, Query: e.v.chooser.Query()})
	e.frame()
}

// Tool window shortcuts, stripes, move, maximize and Escape handling.
func TestToolWindowShortcuts(t *testing.T) {
	e := newEnv(t, true)
	e.waitFor("tool layout", func() bool { return e.ctl.ToolsReady() })
	e.frame()
	// Project and Structure on the left.
	if !e.v.ToolShown(ToolProject) || len(e.v.bench.Stripes[templates.SideLeft]) != 2 {
		t.Fatalf("project tree should start open on the left: %+v", e.v.bench.Stripes)
	}
	e.key("1", key.ModCommand)
	if e.v.ToolShown(ToolProject) {
		t.Fatal("Cmd+1 should hide the project tree")
	}
	e.key("1", key.ModCommand)
	e.key("6", key.ModCommand)
	if !e.v.ToolShown(ToolProject) || !e.v.ToolShown(toolProblems) {
		t.Fatal("Cmd+1/Cmd+6 should show project and problems")
	}
	// Move Problems to the right through its tool menu.
	e.v.openToolMenu(toolProblems)
	e.frame()
	e.chooseLabel("Move to Right")
	if e.ctl.Tools.SideOf(toolProblems) != controller.SideRight || !e.v.ToolShown(toolProblems) ||
		len(e.v.bench.Stripes[templates.SideRight]) != 1 {
		t.Fatalf("move: %+v", e.v.bench.Stripes[templates.SideRight])
	}
	// Maximize editor and back.
	e.key(key.NameF12, key.ModCommand|key.ModShift)
	if e.v.ToolShown(ToolProject) || e.v.ToolShown(toolProblems) {
		t.Fatal("maximize should hide all tool windows")
	}
	e.key(key.NameF12, key.ModCommand|key.ModShift)
	if !e.v.ToolShown(ToolProject) || !e.v.ToolShown(toolProblems) {
		t.Fatal("maximize again should restore")
	}
	// Shift+Escape hides the tool window used last.
	e.v.activeTool = toolProblems
	e.key(key.NameEscape, key.ModShift)
	if e.v.ToolShown(toolProblems) || !e.v.ToolShown(ToolProject) {
		t.Fatal("Shift+Escape should hide Problems only")
	}
	// The layout is stored per project and restored by a new window.
	e.settle()
	ctl2 := controller.New(controller.NewInProcess(e.app))
	t.Cleanup(ctl2.Close)
	v2 := NewView(ctl2, "darwin")
	t.Cleanup(v2.Close)
	ctl2.Start("")
	h := uitest.NewHarness(windowSize)
	for i := 0; i < 50 && !ctl2.ToolsReady(); i++ {
		ctl2.Settle(100e6)
		h.Frame(func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions { return v2.Frame(gtx) })
	}
	h.Frame(func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions { return v2.Frame(gtx) })
	if ctl2.Tools.SideOf(toolProblems) != controller.SideRight || v2.ToolShown(toolProblems) || !v2.ToolShown(ToolProject) {
		raw, _ := json.Marshal(ctl2.Tools)
		t.Fatalf("restored layout: %s ready %v", raw, ctl2.ToolsReady())
	}
}

// Split Right opens the active file in a second group sharing the
// buffer: typing in one group shows in the other.
func TestSplitEditorSharesBuffer(t *testing.T) {
	e := newEnv(t, true)
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	file := filepath.Join(e.root, "src", "Shop.php")
	e.v.runAction(ActSplitRight)
	e.settle()
	groups := e.ctl.Groups()
	if len(groups) != 2 {
		t.Fatalf("groups %d", len(groups))
	}
	s0 := e.v.editors[sessionKey{groups[0], file}]
	s1 := e.v.editors[sessionKey{groups[1], file}]
	if s0 == nil || s1 == nil || s0 == s1 || s0.doc != s1.doc || e.ctl.ActiveGroup() != groups[1] {
		t.Fatalf("sessions %p %p", s0, s1)
	}
	e.frame()
	if !s1.ed.Focused() {
		t.Fatal("new group's editor should have the focus")
	}
	st := e.h.Router.EditorState()
	e.h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: "X"})
	e.frame()
	s0.doc.Lock()
	text := string(s0.doc.Buffer().Slice(0, 1))
	s0.doc.Unlock()
	if text != "X" {
		t.Fatalf("group 0 sees %q", text)
	}
	// Group switching via action; closing the second group's tab keeps
	// the document (still open in group 0).
	e.v.runAction(ActPrevGroup)
	e.frame()
	if e.ctl.ActiveGroup() != groups[0] {
		t.Fatal("previous group")
	}
	e.ctl.FocusGroup(1)
	e.v.closeTab(0)
	e.settle()
	if len(e.ctl.Groups()) != 1 || !e.ctl.FileOpen(file) || len(e.v.editors) != 1 {
		t.Fatalf("after closing split: groups %d editors %d", len(e.ctl.Groups()), len(e.v.editors))
	}
	if _, err := e.app.Document(file); err != nil {
		t.Fatalf("document closed: %v", err)
	}
	// The tab menu offers splitting.
	e.v.openTabMenu(e.ctl.ActiveTab())
	e.frame()
	e.chooseLabel("Split Down")
	e.settle()
	if len(e.ctl.Groups()) != 2 || !e.ctl.Root.Vertical {
		t.Fatal("split down from tab menu")
	}
	e.v.runAction(ActUnsplitAll)
	e.settle()
	if len(e.ctl.Groups()) != 1 {
		t.Fatal("unsplit all")
	}
}

// Find Action lists the registered actions and runs the chosen one.
func TestFindActionAndMenus(t *testing.T) {
	e := newEnv(t, true)
	e.key("A", key.ModCommand|key.ModShift)
	if e.v.chooseMode != chooseCustom || len(e.v.chooser.Items) < 20 {
		t.Fatalf("find action: mode %d items %d", e.v.chooseMode, len(e.v.chooser.Items))
	}
	e.chooseLabel("Maximize Editor")
	if !e.ctl.Tools.Maximized() {
		t.Fatal("action not run")
	}
	menus := e.v.Registry.buildMenus()
	var titles []string
	for _, m := range menus {
		titles = append(titles, m.title)
	}
	if got := strings.Join(titles, ","); got != "File,Edit,View,Navigate,Code,Refactor,Run,Window,Help" {
		t.Fatalf("menus %s", got)
	}
	nm := nativeMenus(e.v.Registry, "darwin", e.v.actionContext(actions.CtxMain))
	var view []string
	for _, m := range nm {
		if m.Title == "View" {
			for _, it := range m.Items {
				view = append(view, it.Title+"="+it.Shortcut)
				for _, ch := range it.Children {
					view = append(view, ch.Title+"="+ch.Shortcut)
				}
			}
		}
	}
	got := strings.Join(view, "|")
	for _, want := range []string{"Project=Cmd+1", "Problems=Cmd+6", "Debug=Cmd+5", "Find Action…=Cmd+Shift+A", "Increase Editor Font Size=Cmd+="} {
		if !strings.Contains(got, want) {
			t.Errorf("View menu lacks %s: %s", want, got)
		}
	}
	if s := e.v.Registry.ChordText("edit.undo", "darwin"); s != "Cmd+Z" {
		t.Errorf("edit menu chord %q", s)
	}
	// The in-app menu bar opens a menu as a popup menu.
	e.v.ShowMenu("File", e.v.Registry.MainMenus(e.v.actionContext(actions.CtxMain), "linux")[0].Items)
	e.frame()
	e.chooseLabel("Close Project")
	e.waitFor("welcome", func() bool { return !e.ctl.Project.Open })
	e.settle()
}

// Zoom changes the editor font size of the window only.
func TestZoom(t *testing.T) {
	e := newEnv(t, true)
	base := e.v.th.Text.Code
	e.key("=", key.ModCommand)
	e.key("=", key.ModCommand)
	if e.v.th.Text.Code != base+2 {
		t.Fatalf("zoom in: %v → %v", base, e.v.th.Text.Code)
	}
	e.key("-", key.ModCommand)
	e.v.runAction(ActZoomReset)
	e.frame()
	if e.v.th.Text.Code != base {
		t.Fatalf("reset: %v", e.v.th.Text.Code)
	}
	e.ctl.SetEditorFontSize(16)
	e.ctl.SetLineHeight(1.8)
	e.frame()
	if e.v.th.Text.Code != 16 || e.v.th.Text.CodeLineHeight != 1.8 {
		t.Fatalf("prefs: %v %v", e.v.th.Text.Code, e.v.th.Text.CodeLineHeight)
	}
	e.settle() // the preference writes finish before the config dir goes
}

// Welcome: the folder browser (no native dialog) opens a project; with
// a project open, another one asks "this window / new window".
func TestOpenProjectFlow(t *testing.T) {
	e := newEnv(t, false)
	e.v.noNativeDialog = true
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.v.browseFolder(filepath.Dir(e.root))
	e.frame()
	e.chooseLabel(filepath.Base(e.root)) // descend
	e.chooseLabel("Open " + filepath.Base(e.root))
	e.waitFor("project", func() bool { return e.ctl.Project.Open })
	host := &fakeHost{}
	e.v.Host = host
	e.v.requestProject(other)
	e.frame()
	if e.v.popup == nil || len(e.v.popup.Buttons) != 3 {
		t.Fatal("no this/new window question")
	}
	e.v.popup.Buttons[1].OnClick()
	if len(host.opened) != 1 || !strings.HasSuffix(host.opened[0], filepath.Base(other)) {
		t.Fatalf("new window: %v", host.opened)
	}
	e.v.ClosePopup()
	// This window: the old project's documents are closed.
	e.ctl.OpenFile("index.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	e.v.requestProject(other)
	e.v.popup.Buttons[0].OnClick()
	e.v.ClosePopup()
	e.waitFor("switched", func() bool { return e.ctl.Project.Open && strings.HasSuffix(e.ctl.Project.Root, filepath.Base(other)) })
	var info api.ProjectInfo
	info, _ = e.app.OpenProject(t.Context(), other)
	if info.OpenDocs != 0 || len(e.ctl.Tabs) != 0 {
		t.Fatalf("old documents survive the switch: %+v", info)
	}
	// A project open in another window is focused instead.
	host.focus = true
	e.v.requestProject(e.root)
	if e.v.popup != nil || host.focused != 1 {
		t.Fatal("other window not focused")
	}
	if !strings.Contains(windowTitle(e.ctl), "[") {
		t.Fatalf("title %q", windowTitle(e.ctl))
	}
}

type fakeHost struct {
	opened  []string
	focus   bool
	focused int
}

func (h *fakeHost) OpenWindow(path string) { h.opened = append(h.opened, path) }
func (h *fakeHost) FocusProject(*View, string) bool {
	if h.focus {
		h.focused++
	}
	return h.focus
}

// Two windows of one process: each has its own App and project, the
// global settings are shared, and both run concurrently without races.
func TestTwoWindowsInParallel(t *testing.T) {
	first := newEnv(t, true)
	home := t.TempDir()
	opts := api.Options{
		ConfigDir: first.app.ConfigDir(), GlobalSettings: first.app.Settings(),
		Interp: interp.DiscoverOptions{Dirs: []string{}},
		LSP:    servers.DetectOptions{PathEnv: filepath.Join(home, "none"), HomeDir: home, ComposerHome: home, NoSystemDirs: true},
	}
	opts.Extensions = append(opts.Extensions, modules.Install)
	a2, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root2, "b.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctl2 := controller.New(controller.NewInProcess(a2))
	v2 := NewView(ctl2, "darwin")
	second := &env{t: t, app: a2, root: root2, ctl: ctl2, v: v2, h: uitest.NewHarness(windowSize)}
	t.Cleanup(func() { v2.Close(); ctl2.Close(); _ = a2.Close() })
	ctl2.Start(root2)
	second.settle()

	var wg sync.WaitGroup
	for i, e := range []*env{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "src/Shop.php"
			if i == 1 {
				name = "b.php"
			}
			e.ctl.OpenFile(name, nil)
			for j := 0; j < 30 && e.ctl.ActiveTab() == nil; j++ {
				e.ctl.Settle(50e6)
				e.frame()
			}
			e.ctl.SetTheme("light")
			e.v.runAction(ActSplitRight)
			for j := 0; j < 5; j++ {
				e.frame()
			}
		}()
	}
	wg.Wait()
	if first.ctl.ActiveTab() == nil || second.ctl.ActiveTab() == nil || first.app.ProjectRoot() == a2.ProjectRoot() {
		t.Fatal("windows did not work independently")
	}
	if !strings.HasSuffix(second.ctl.ActiveTab().File, "b.php") {
		t.Fatalf("second window tab %s", second.ctl.ActiveTab().File)
	}
	// Shared global settings: the recent projects of both windows.
	second.ctl.ReloadRecentProjects()
	second.settle()
	if len(second.ctl.RecentProjects) < 2 {
		t.Fatalf("recent projects not shared: %v", second.ctl.RecentProjects)
	}
}

// TestWindowingScreenshots renders the workbench with stripes, panels on
// three sides and a split editor, the welcome screen and the font
// settings (TIEGEL_SHOTS=<dir> writes the PNGs).
func TestWindowingScreenshots(t *testing.T) {
	e := newEnv(t, true)
	e.waitFor("tool layout", func() bool { return e.ctl.ToolsReady() })
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	e.ctl.OpenFile("index.php", nil)
	e.waitFor("tab 2", func() bool { return len(e.ctl.Tabs) == 2 })
	e.v.runAction(ActSplitRight)
	e.ctl.FocusGroup(0)
	e.ctl.Activate(0)
	e.v.ShowTool(toolProblems)
	e.v.MoveTool(ToolDebug, Right)
	e.v.ShowTool(ToolDebug)
	e.settle()
	e.shot("windowing-workbench")
	e.v.openSettings()
	e.v.settings.Selected = "core:Editor"
	e.waitFor("mono fonts", func() bool { return e.ctl.MonoFonts != nil && len(e.ctl.MonoFonts) > 0 })
	e.waitFor("settings schema", func() bool { return e.ctl.Schema != nil })
	e.settle()
	e.shot("windowing-settings-fonts")
	e.v.settings.Selected = "plugin:git"
	e.settle()
	e.shot("windowing-settings-plugin")
	e.v.settings.SetQuery("tab")
	e.settle()
	e.shot("windowing-settings-search")
	e.v.settings.SetQuery("")
	e.v.closeOverlay()
	e.v.closeProject()
	e.waitFor("welcome", func() bool { return !e.ctl.Project.Open })
	e.settle()
	e.shot("windowing-welcome")
}
