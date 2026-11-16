package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestToolLayout(t *testing.T) {
	l := NewToolLayout()
	l.Show("project") // before registration: pending
	if l.Visible("project") {
		t.Fatal("unregistered window shown")
	}
	l.Register("project", SideLeft)
	l.Register("problems", SideBottom)
	l.Register("git", SideBottom)
	if !l.Visible("project") || l.SideOf("git") != SideBottom {
		t.Fatalf("pending show / sides: %+v", l.Sides[SideLeft])
	}
	// One window per side: showing git replaces problems.
	l.Show("problems")
	l.Show("git")
	if l.Visible("problems") || !l.Visible("git") {
		t.Fatal("bottom side shows two windows")
	}
	if l.Toggle("git") || l.Visible("git") {
		t.Fatal("toggle should hide")
	}
	// Moving a shown window keeps it shown on the new side.
	l.Show("problems")
	l.Move("problems", SideRight)
	if !l.Visible("problems") || l.SideOf("problems") != SideRight || l.Sides[SideBottom].Open {
		t.Fatalf("move: %+v %+v", l.Sides[SideRight], l.Sides[SideBottom])
	}
	ids := l.ToolsAt(SideBottom, func(string) int { return 0 })
	if len(ids) != 1 || ids[0] != "git" {
		t.Fatalf("bottom tools %v", ids)
	}
	l.SetSize(SideLeft, 2)
	if l.Size(SideLeft) != 0.9 || l.Size(SideBottom) != DefaultBottomSize {
		t.Fatalf("sizes %v %v", l.Size(SideLeft), l.Size(SideBottom))
	}
	// Maximize hides all and restores them.
	l.ToggleMaximize()
	if l.AnyOpen() || !l.Maximized() {
		t.Fatal("maximize")
	}
	raw, err := json.Marshal(l) // stored with the panels open
	if err != nil {
		t.Fatal(err)
	}
	l.ToggleMaximize()
	if !l.Visible("project") || !l.Visible("problems") || l.Maximized() {
		t.Fatal("restore")
	}
	// Round trip: the moved window and the open panels come back; the
	// default placement of git is not stored.
	if strings.Contains(string(raw), `"git"`) {
		t.Fatalf("default placement stored: %s", raw)
	}
	n := NewToolLayout()
	if err := json.Unmarshal(raw, n); err != nil {
		t.Fatal(err)
	}
	n.Register("project", SideLeft)
	n.Register("problems", SideBottom)
	if n.SideOf("problems") != SideRight || !n.Visible("problems") || !n.Visible("project") || n.Size(SideLeft) != 0.9 {
		t.Fatalf("restored %s: %+v", raw, n.Sides)
	}
	// A window that goes away closes.
	n.Unregister("problems")
	if n.Visible("problems") {
		t.Fatal("unregistered still visible")
	}
}

// The layout is loaded per project (default: project tree open) and
// stored when it changed.
func TestToolLayoutPersisted(t *testing.T) {
	f := projectFake()
	var mu sync.Mutex
	stored := map[string]json.RawMessage{}
	f.on("settings.get", func(raw json.RawMessage) (any, error) {
		var p struct{ Key, Scope string }
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		defer mu.Unlock()
		if v, ok := stored[p.Scope+":"+p.Key]; ok {
			return map[string]any{"found": true, "value": v}, nil
		}
		return map[string]any{"found": false}, nil
	})
	f.on("settings.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			Key, Scope string
			Value      json.RawMessage
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		stored[p.Scope+":"+p.Key] = p.Value
		mu.Unlock()
		return map[string]any{}, nil
	})
	c := started(t, f)
	c.Tools.Register(DefaultOpenTool, SideLeft)
	c.Tools.Register("problems", SideBottom)
	if !c.ToolsReady() || !c.Tools.Visible(DefaultOpenTool) {
		t.Fatalf("default layout: %+v", c.Tools.Sides[SideLeft])
	}
	c.Tools.Move("problems", SideLeft)
	c.Tools.Show("problems")
	c.PersistToolLayout()
	c.Settle(time.Second)
	mu.Lock()
	v := stored["project:"+KeyToolLayout]
	mu.Unlock()
	if !strings.Contains(string(v), `"problems":"left"`) {
		t.Fatalf("stored %s", v)
	}
	// Reopening the project restores it.
	c.OpenProject(root)
	c.Settle(time.Second)
	c.Tools.Register(DefaultOpenTool, SideLeft)
	c.Tools.Register("problems", SideBottom)
	if c.Tools.SideOf("problems") != SideLeft || !c.Tools.Visible("problems") {
		t.Fatalf("restored %+v %+v", c.Tools.Tools, c.Tools.Sides[SideLeft])
	}
}

// Splits share the document: the second group opens no second document,
// closing one of the tabs keeps it open, closing the last one closes it.
func TestSplitSharesDocument(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	file := filepath.Join(root, "index.php")
	c.OpenFile(file, nil)
	c.Settle(time.Second)
	first := c.ActiveTab()
	c.Split(false)
	if len(c.Groups()) != 2 || c.ActiveGroup() != c.Groups()[1] || c.Root.Leaf() || c.Root.Vertical {
		t.Fatalf("split: %d groups", len(c.Groups()))
	}
	second := c.ActiveTab()
	if second == nil || second == first || second.Doc != first.Doc || second.Generation != first.Generation {
		t.Fatalf("second tab %+v", second)
	}
	// Opening the file in the new group again only activates it; opening
	// another file there opens a document once.
	c.OpenFile(file, nil)
	c.OpenFile(filepath.Join(root, "src/Mailer.php"), nil)
	c.Settle(time.Second)
	if n := f.called("doc.open"); n != 2 {
		t.Fatalf("doc.open %d times", n)
	}
	// Focus switches tabs of the groups.
	c.FocusGroup(0)
	if c.ActiveTab() != first || len(c.Tabs) != 1 {
		t.Fatalf("group 0 tabs %v", c.Tabs)
	}
	c.CloseTab(0) // the emptied group closes, the file stays open in group 1
	c.Settle(time.Second)
	if f.called("doc.close") != 0 || len(c.Groups()) != 1 || !c.FileOpen(file) {
		t.Fatalf("closed shared document: calls %v groups %d", f.calls, len(c.Groups()))
	}
	if !c.Root.Leaf() || c.Root.Group != c.ActiveGroup() {
		t.Fatal("tree not collapsed")
	}
	c.CloseTab(c.tabIndex(file))
	c.Settle(time.Second)
	if f.called("doc.close") != 1 || c.FileOpen(file) {
		t.Fatalf("last tab must close the document: %v", f.calls)
	}
}

func TestSplitDownUnsplitAndMove(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	a, b := filepath.Join(root, "index.php"), filepath.Join(root, "src/Mailer.php")
	c.OpenFile(a, nil)
	c.Settle(time.Second)
	c.Split(true)
	c.OpenFile(b, nil)
	c.Settle(time.Second)
	c.Split(false) // nested: group 1 splits again
	if len(c.Groups()) != 3 || !c.Root.Vertical || c.Root.Second.Leaf() {
		t.Fatalf("nested split %d", len(c.Groups()))
	}
	c.CycleGroup(1) // wraps to group 0
	if c.GroupIndex(c.ActiveGroup()) != 0 {
		t.Fatalf("cycle: %d", c.GroupIndex(c.ActiveGroup()))
	}
	c.MoveTabToGroup(1)
	if c.GroupIndex(c.ActiveGroup()) != 1 || c.ActiveTab().File != a {
		t.Fatalf("move: %+v", c.ActiveTab())
	}
	tabs0, _ := c.GroupTabs(c.Groups()[0])
	if len(tabs0) != 0 {
		t.Fatalf("source group keeps %v", tabs0)
	}
	c.UnsplitAll()
	if len(c.Groups()) != 1 || len(c.Tabs) != 2 {
		t.Fatalf("unsplit all: %d groups, tabs %v", len(c.Groups()), c.Tabs)
	}
	c.Settle(time.Second)
	if f.called("doc.close") != 0 {
		t.Fatal("unsplit closed documents")
	}
}

// Virtual tabs (e.g. a diff) need no document calls.
func TestVirtualTab(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	tab := c.OpenVirtual("diff", "x.php", "Diff x.php", 42)
	if c.ActiveTab() != tab || tab.Title() != "Diff x.php" || tab.File != VirtualKey("diff", "x.php") {
		t.Fatalf("virtual %+v", tab)
	}
	if again := c.OpenVirtual("diff", "x.php", "Diff x.php (2)", 43); again != tab || tab.Data != 43 || len(c.Tabs) != 1 {
		t.Fatalf("reopen %+v", again)
	}
	c.Split(false)
	c.CloseTab(0)
	c.CloseTab(0)
	c.Settle(time.Second)
	if f.called("doc.open")+f.called("doc.close")+f.called("doc.save") != 0 || len(c.RecentFiles) != 0 {
		t.Fatalf("virtual tab called %v, recent %v", f.calls, c.RecentFiles)
	}
}

// Switching the project saves, stops the LSP, closes the old project in
// the backend and drops all tabs; CloseProject returns to the welcome
// state.
func TestProjectSwitchAndClose(t *testing.T) {
	f := projectFake()
	f.answer("lsp.stop", map[string]any{})
	f.answer("project.close", ProjectInfo{})
	c := started(t, f)
	c.OpenFile(filepath.Join(root, "index.php"), nil)
	c.Settle(time.Second)
	c.Split(false)
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	c.OpenProject("/other")
	c.Settle(2 * time.Second)
	f.mu.Lock()
	order := strings.Join(f.calls, " ")
	f.mu.Unlock()
	if !strings.Contains(order, "autosave.trigger lsp.stop project.close project.open") {
		t.Fatalf("switch order: %s", order)
	}
	if len(c.Groups()) != 1 || len(c.Tabs) != 0 || !c.Project.Open {
		t.Fatalf("state after switch: %d groups %d tabs", len(c.Groups()), len(c.Tabs))
	}
	c.CloseProject()
	c.Settle(2 * time.Second)
	if c.Project.Open || c.Tree != nil || len(c.Tabs) != 0 {
		t.Fatalf("after close %+v", c.Project)
	}
	// Recent projects: removal re-reads the shared list first.
	c.RecentProjects = []string{"/a", root}
	c.RemoveRecentProject(root)
	c.Settle(time.Second)
	for _, p := range c.RecentProjects {
		if p == root {
			t.Fatalf("not removed: %v", c.RecentProjects)
		}
	}
}

func TestFontPrefs(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	c.SetEditorFont("Menlo")
	c.SetEditorFontSize(15)
	c.SetEditorFontSize(100) // ignored
	c.SetLineHeight(1.6)
	c.SetUIFontSize(14)
	c.Settle(time.Second)
	if p := c.Prefs; p.EditorFont != "Menlo" || p.EditorFontSize != 15 || p.LineHeight != 1.6 || p.UIFontSize != 14 {
		t.Fatalf("prefs %+v", p)
	}
	if f.called("settings.set") < 4 {
		t.Fatalf("not stored: %v", f.calls)
	}
	c.LoadMonoFonts(func() []string { return []string{"Menlo", "Go Mono"} })
	c.Settle(time.Second)
	if len(c.MonoFonts) != 2 {
		t.Fatalf("mono fonts %v", c.MonoFonts)
	}
}
