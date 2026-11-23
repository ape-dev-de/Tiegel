package gui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	debugui "github.com/ape-dev-de/tiegel/internal/ui/organisms/debug"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// End-to-end tests of the work dock: real api.App, real git, real file
// watcher, the window rendered headlessly.

const composerPSR4 = `{"require": {"php": "^8.0"}, "autoload": {"psr-4": {"App\\": "src/"}}}`

// startWatcher starts the project watcher (the GUI binary sets
// api.Options.Watch; the test env does not).
func (e *env) startWatcher() {
	e.t.Helper()
	if err := e.ctl.Backend().Call(context.Background(), "watch.start", map[string]any{"backend": "poll"}, nil); err != nil {
		e.t.Fatalf("watch.start: %v", err)
	}
}

// openTab opens a project file and waits for its editor.
func (e *env) openTab(rel string) *controller.Tab {
	e.t.Helper()
	abs := filepath.Join(e.root, filepath.FromSlash(rel))
	e.ctl.OpenFile(abs, nil)
	e.waitFor("tab "+rel, func() bool { t := e.ctl.ActiveTab(); return t != nil && t.File == abs && t.Doc != nil })
	e.frame()
	return e.ctl.ActiveTab()
}

// menuChoose picks an entry of the open menu (chooser presentation) by
// its flattened label, e.g. "New › PHP Class…".
func (e *env) menuChoose(label string) {
	e.t.Helper()
	e.frame()
	e.chooseLabel(label)
}

// TestTreeContextMenuNewPHPClass: right click on src → New › PHP Class…
// creates the file with the PSR-4 namespace and opens it.
func TestTreeContextMenuNewPHPClass(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{"composer.json": composerPSR4}, nil)
	e.waitFor("tree", func() bool { return e.ctl.Tree != nil && e.ctl.Tree.Root.Loaded })
	e.v.treeEvent(filetree.ContextEvent{Path: "src", Dir: true})
	e.frame()
	e.shot("tree-context-menu")
	e.menuChoose("New › PHP Class")
	e.waitFor("namespace chooser", func() bool {
		return e.v.chooseMode == chooseCustom && strings.HasPrefix(e.v.chooser.Items[0].Detail, "namespace")
	})
	if d := e.v.chooser.Items[0].Detail; d != `namespace App` {
		t.Fatalf("namespace suggestion %q", d)
	}
	e.v.choose(palette.ChooseEvent{Index: 2, Query: "InvoiceRepository"}) // interface
	file := filepath.Join(e.root, "src", "InvoiceRepository.php")
	e.waitFor("class tab", func() bool { t := e.ctl.ActiveTab(); return t != nil && t.File == file })
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, "namespace App;") || !strings.Contains(s, "interface InvoiceRepository") {
		t.Fatalf("class file:\n%s", s)
	}
	// The tree shows the new file (files.changed).
	e.waitFor("tree entry", func() bool { return e.ctl.Tree.Node("src/InvoiceRepository.php") != nil })

	// Rename via Shift+F6 in the tree follows the open tab.
	e.v.tree.Select("src/InvoiceRepository.php")
	e.v.treeEvent(filetree.KeyEvent{Path: "src/InvoiceRepository.php", Name: key.NameF6, Modifiers: key.ModShift})
	e.frame()
	if e.v.chooseMode != chooseCustom || e.v.chooser.Query() != "InvoiceRepository.php" {
		t.Fatalf("rename chooser %v %q", e.v.chooseMode, e.v.chooser.Query())
	}
	e.v.choose(palette.ChooseEvent{Index: -1, Query: "Invoices.php"})
	renamed := filepath.Join(e.root, "src", "Invoices.php")
	e.waitFor("tab follows rename", func() bool { t := e.ctl.ActiveTab(); return t != nil && t.File == renamed })
	e.frame()
	if s := e.v.activeSession(); s == nil || s.file != renamed {
		t.Fatal("editor did not follow the rename")
	}
}

// TestExternalChangeBannerTakeDisk: an unsaved document whose file is
// changed by another program shows the banner; Take disk reloads it.
func TestExternalChangeBannerTakeDisk(t *testing.T) {
	e := newEnv(t, true)
	e.startWatcher()
	tab := e.openTab("index.php")
	tab.Doc.Lock()
	if _, err := tab.Doc.Buffer().Insert(0, []byte("// mine\n")); err != nil {
		t.Fatal(err)
	}
	tab.Doc.Unlock()
	e.frame()
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(tab.File, []byte("<?php\necho 'disk';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitFor("conflict banner", func() bool { return e.ctl.Work.ExternalOf(tab.File) != nil })
	if e.v.work.externalBanner(e.v, tab) == nil {
		t.Fatal("no banner")
	}
	e.frame()
	e.shot("external-conflict-banner")

	// Show Diff: disk ↔ editor, editable right side.
	dt := e.ctl.Work.OpenDiff(controller.DiffSpec{Path: "index.php", External: true})
	e.waitFor("disk diff", func() bool { return dt.Version > 0 })
	e.frame()
	dv := e.v.work.diffs[dt]
	if dv == nil || !dv.win.Editable() || len(dv.win.Blocks()) == 0 {
		t.Fatalf("disk diff view %+v", dv)
	}
	e.closeActive()
	e.openTab("index.php")

	e.ctl.Work.ResolveExternal(tab.File, controller.ResolveTakeDisk)
	e.waitFor("resolved", func() bool { return e.ctl.Work.ExternalOf(tab.File) == nil })
	tab.Doc.Lock()
	text := tab.Doc.Buffer().String()
	tab.Doc.Unlock()
	if text != "<?php\necho 'disk';\n" {
		t.Fatalf("buffer after Take disk %q", text)
	}
}

func (e *env) closeActive() {
	e.v.closeTab(e.ctl.Active)
	e.settle()
}

// TestCompareWithHeadAndAnnotate: Compare with HEAD opens a diff tab with
// the right blocks, F7 moves through them (the debugger's F7 yields),
// Annotate shows the author, the file history and Local History open.
func TestCompareWithHeadAndAnnotate(t *testing.T) {
	old := controller.BlameDelay
	controller.BlameDelay = 20 * time.Millisecond
	t.Cleanup(func() { controller.BlameDelay = old })
	e := newGitEnv(t)
	e.waitFor("git", func() bool { return e.ctl.Vcs.Available })
	e.frame()
	shop := filepath.Join(e.root, "src", "Shop.php")
	data, _ := os.ReadFile(shop)
	changed := strings.Replace(string(data), "<?php\n", "<?php\n// added line\n", 1)
	if err := os.WriteFile(shop, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	e.v.treeEvent(filetree.ContextEvent{Path: "src/Shop.php"})
	e.menuChoose("Git › Compare with HEAD")
	var dt *controller.DiffTab
	e.waitFor("diff tab", func() bool {
		if t := e.ctl.ActiveTab(); t != nil {
			dt, _ = t.Data.(*controller.DiffTab)
		}
		return dt != nil && dt.Version > 0
	})
	e.frame()
	dv := e.v.work.diffs[dt]
	if dv == nil {
		t.Fatal("no diff view")
	}
	bs := dv.win.Blocks()
	if len(bs) != 1 || bs[0].Kind != diff.Added || bs[0].RightStart != 1 || bs[0].RightEnd != 2 {
		t.Fatalf("blocks %+v", bs)
	}
	if !dv.win.Editable() {
		t.Fatal("work tree side should be editable")
	}
	e.shot("diff-tab")
	if dv.win.Current() != -1 {
		t.Fatalf("current %d", dv.win.Current())
	}
	// The debugger binds F7 (Step Into) globally; without a paused
	// session it hands the key to the active diff.
	if _, ok := e.v.Registry.Action(debugui.ActionStepInto); !ok {
		t.Fatal("debugger actions not installed: F7 routing untested")
	}
	e.key(key.NameF7, 0)
	if dv.win.Current() != 0 {
		t.Fatalf("F7 did not reach the diff (current %d)", dv.win.Current())
	}
	if !diffKeyWins(true, false) || diffKeyWins(true, true) || diffKeyWins(false, false) {
		t.Fatal("F7 routing table")
	}

	// Annotate the (committed) file: the author shows in the gutter.
	e.closeActive()
	e.openTab("src/Shop.php")
	e.v.RunAction(ActVcsAnnotate)
	e.waitFor("annotation", func() bool {
		bv := e.v.work.blames[shop]
		return bv != nil && bv.ready
	})
	bv := e.v.work.blames[shop]
	if got := bv.gutter.Text(0); !strings.Contains(got, "Tiegel Test") {
		t.Fatalf("annotation of line 1 %q", got)
	}
	if got := bv.gutter.Text(1); got != "Not committed yet" {
		t.Fatalf("added line 2 %q", got)
	}
	e.frame()
	e.shot("annotate")

	// File history and Local History.
	e.v.RunAction(ActVcsHistory)
	e.waitFor("history", func() bool {
		ht, _ := e.ctl.ActiveTab().Data.(*controller.HistoryTab)
		return ht != nil && len(ht.Entries) == 1
	})
	e.frame()
	e.ctl.Save(shop)
	e.settle()
	e.ctl.Work.OpenLocalHistory("src/Shop.php", 0)
	e.waitFor("local history", func() bool {
		lt, _ := e.ctl.ActiveTab().Data.(*controller.LocalHistoryTab)
		return lt != nil && lt.DiffVersion > 0
	})
	e.frame()
	e.shot("local-history")
}

// TestPullDialogAndStashes: the pull dialog, a failing pull without a
// remote and the stash list.
func TestPullDialogAndStashes(t *testing.T) {
	e := newGitEnv(t)
	e.waitFor("git", func() bool { return e.ctl.Vcs.Available })
	e.frame()
	e.v.RunAction(ActUpdateProject)
	e.frame()
	if e.v.popup == nil || e.v.popup.Title != "Update Project" {
		t.Fatalf("update dialog %+v", e.v.popup)
	}
	e.shot("pull-dialog")
	e.v.popup.Buttons[0].OnClick()
	e.v.popup = nil
	e.waitFor("update failure", func() bool { return e.ctl.Work.Remote.Problem != nil })
	if p := e.ctl.Work.Remote.Problem; p.Kind != "noUpstream" && p.Kind != "noRemote" {
		t.Fatalf("problem %+v", p)
	}
	e.frame()
	if e.v.popup == nil || !strings.Contains(e.v.popup.Title, "failed") {
		t.Fatalf("problem popup %+v", e.v.popup)
	}
	e.v.closeOverlay()

	// Stash a change, list it, pop it.
	idx := filepath.Join(e.root, "index.php")
	if err := os.WriteFile(idx, []byte("<?php\necho 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.ctl.Work.Stash(controller.StashOptions{Message: "work in progress"})
	e.waitFor("stash list", func() bool { return len(e.ctl.Work.Remote.Stashes) == 1 })
	if data, _ := os.ReadFile(idx); string(data) != "<?php\necho 1;\n" {
		t.Fatalf("work tree not clean after stash: %q", data)
	}
	e.v.work.showStashes()
	e.frame()
	e.shot("stashes")
	e.ctl.Work.StashApply(e.ctl.Work.Remote.Stashes[0], true)
	e.waitFor("popped", func() bool { return len(e.ctl.Work.Remote.Stashes) == 0 })
	if data, _ := os.ReadFile(idx); string(data) != "<?php\necho 2;\n" {
		t.Fatalf("pop did not restore: %q", data)
	}
}

// TestTabMenuCloseVariants: Close Tabs to the Right / Others / All from
// the tab menu additions.
func TestTabMenuCloseVariants(t *testing.T) {
	e := newEnv(t, true)
	a := e.openTab("index.php")
	e.openTab("composer.json")
	e.openTab("src/Shop.php")
	if len(e.ctl.Tabs) != 3 {
		t.Fatalf("tabs %d", len(e.ctl.Tabs))
	}
	items := e.v.work.tabMenu(e.v, ContextTarget{Tab: a, File: a.File})
	if len(items) < 5 || items[0].Title != "Close Tabs to the Right" {
		t.Fatalf("tab menu %+v", items)
	}
	e.ctl.Activate(0)
	e.v.work.closeTabs(e.ctl.Tabs[0], closeRight)
	e.settle()
	if len(e.ctl.Tabs) != 1 || e.ctl.Tabs[0] != a {
		t.Fatalf("close right left %+v", e.ctl.Tabs)
	}
	e.openTab("composer.json")
	e.v.work.closeTabs(nil, closeAll)
	e.settle()
	if len(e.ctl.Tabs) != 0 {
		t.Fatalf("close all left %d", len(e.ctl.Tabs))
	}
	// Select in Project View reveals the active file.
	e.openTab("src/Shop.php")
	e.v.RunAction(ActSelectInTree)
	e.frame()
	if e.v.tree.Selected() != "src/Shop.php" || !e.v.ToolShown(ToolProject) {
		t.Fatalf("select in tree %q", e.v.tree.Selected())
	}
	// The project panel header offers the same as a button.
	if tw, ok := e.v.Registry.ToolWindow(ToolProject); !ok || tw.Actions == nil {
		t.Fatal("project tool window without header actions")
	}
	if e.v.locateBtn.Glyph != atoms.GlyphLocate || e.v.locateBtn.Disabled ||
		!strings.HasPrefix(e.v.locateTip.Text, "Select Opened File") {
		t.Fatalf("locate button %+v tip %q", e.v.locateBtn, e.v.locateTip.Text)
	}
	e.v.tree.Select("index.php")
	bh := uitest.NewHarness(image.Pt(60, 40))
	bw := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return e.v.layoutProjectActions(gtx, th) }
	bh.Frame(bw)
	bh.Click(image.Pt(10, 10))
	bh.Frame(bw)
	e.frame()
	if e.v.tree.Selected() != "src/Shop.php" {
		t.Fatalf("locate button selected %q", e.v.tree.Selected())
	}
}
