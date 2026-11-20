package controller

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func registered() *ToolLayout {
	l := NewToolLayout()
	l.Register("project", SideLeft)
	l.RegisterIn("structure", SideLeft, GroupSplit)
	l.Register("problems", SideBottom)
	l.Register("terminal", SideBottom)
	l.Register("debug", SideRight)
	return l
}

// Corner owners: per bottom corner, persisted, old layouts load with the
// side panels owning both corners.
func TestToolLayoutCorners(t *testing.T) {
	l := registered()
	if l.IsBottomFull(SideLeft) || l.IsBottomFull(SideRight) {
		t.Fatal("default: side panels own the corners")
	}
	v := l.Version
	l.SetBottomFull(SideLeft, true)
	l.SetBottomFull(SideBottom, true) // not a corner: ignored
	if !l.IsBottomFull(SideLeft) || l.IsBottomFull(SideRight) || l.Version == v {
		t.Fatalf("set left: %v", l.BottomFull)
	}
	v = l.Version
	l.SetBottomFull(SideLeft, true)
	if l.Version != v {
		t.Fatal("unchanged corner bumped the version")
	}
	raw, _ := json.Marshal(l)
	if !strings.Contains(string(raw), `"corners":{"left":"bottom"}`) {
		t.Fatalf("stored %s", raw)
	}
	n := registered()
	if err := json.Unmarshal(raw, n); err != nil {
		t.Fatal(err)
	}
	if !n.IsBottomFull(SideLeft) || n.IsBottomFull(SideRight) {
		t.Fatalf("restored %v", n.BottomFull)
	}
	n.SetBottomFull(SideLeft, false)
	raw, _ = json.Marshal(n)
	if strings.Contains(string(raw), "corners") {
		t.Fatalf("default corners stored: %s", raw)
	}
	// A layout stored before AP18.8 (no corners, no tabs) loads as before.
	old := `{"tools":{"problems":"left"},"sides":{"left":{"active":"problems","open":true,"size":0.3},"bottom":{"open":false}},"maximized":""}`
	o := registered()
	if err := json.Unmarshal([]byte(old), o); err != nil {
		t.Fatal(err)
	}
	if o.IsBottomFull(SideLeft) || o.IsBottomFull(SideRight) || !o.Visible("problems") || o.Size(SideLeft) != 0.3 ||
		o.TabsOf(SideLeft, GroupPrimary) != nil {
		t.Fatalf("old layout %+v", o.Sides[SideLeft])
	}
	// Unknown corner values are ignored.
	u := registered()
	if err := json.Unmarshal([]byte(`{"corners":{"left":"side","middle":"bottom"}}`), u); err != nil || u.IsBottomFull(SideLeft) {
		t.Fatalf("unknown corners: %v %v", err, u.BottomFull)
	}
}

// Tool windows dropped onto a panel form a tab group; it follows moves
// and is persisted.
func TestToolLayoutTabs(t *testing.T) {
	l := registered()
	l.Show("terminal")
	l.JoinTab("problems", SideBottom, GroupPrimary)
	if !l.Visible("problems") || l.Visible("terminal") {
		t.Fatal("joined tab should be in front")
	}
	if tabs := l.TabsOf(SideBottom, GroupPrimary); len(tabs) != 2 || tabs[0] != "terminal" || tabs[1] != "problems" {
		t.Fatalf("tabs %v", tabs)
	}
	// Selecting another tab keeps the group.
	l.Show("terminal")
	if len(l.TabsOf(SideBottom, GroupPrimary)) != 2 {
		t.Fatal("selecting a tab dropped the group")
	}
	// A window from another side joins too.
	l.JoinTab("project", SideBottom, GroupPrimary)
	if tabs := l.TabsOf(SideBottom, GroupPrimary); len(tabs) != 3 || l.SideOf("project") != SideBottom || l.Shown(SideLeft, GroupPrimary) != "" {
		t.Fatalf("project as tab: %v", tabs)
	}
	l.JoinTab("project", SideBottom, GroupPrimary) // already there: no-op
	raw, _ := json.Marshal(l)
	n := registered()
	if err := json.Unmarshal(raw, n); err != nil {
		t.Fatal(err)
	}
	if tabs := n.TabsOf(SideBottom, GroupPrimary); len(tabs) != 3 || !n.Visible("project") {
		t.Fatalf("restored tabs %v from %s", tabs, raw)
	}
	// Moving the window in front away shows the next tab.
	l.MoveTo("project", SideLeft, GroupPrimary)
	if !l.Visible("project") || l.Shown(SideBottom, GroupPrimary) != "terminal" {
		t.Fatalf("after move: %q", l.Shown(SideBottom, GroupPrimary))
	}
	if tabs := l.TabsOf(SideBottom, GroupPrimary); len(tabs) != 2 {
		t.Fatalf("tabs after move %v", tabs)
	}
	// Opening a window of the slot that is not in the group hides the
	// tab strip; leaving the group dissolves a group of two.
	l.Register("git", SideBottom)
	l.Show("git")
	if l.TabsOf(SideBottom, GroupPrimary) != nil {
		t.Fatal("tab strip for a window outside the group")
	}
	l.LeaveTabs("problems")
	l.LeaveTabs("problems") // not in a group: no-op
	l.Show("terminal")
	if l.TabsOf(SideBottom, GroupPrimary) != nil {
		t.Fatal("a single tab is no group")
	}
	l.JoinTab("x", "middle", GroupPrimary) // ignored
}

// Reset restores the defaults; Apply replaces the layout by a stored one
// and keeps the registrations.
func TestToolLayoutResetApply(t *testing.T) {
	l := registered()
	l.MoveTo("terminal", SideLeft, GroupSplit)
	l.Show("terminal")
	l.SetBottomFull(SideRight, true)
	l.SetSize(SideLeft, 0.4)
	l.SetMaxPanel("terminal")
	saved, _ := json.Marshal(l)
	l.Reset()
	if l.SideOf("terminal") != SideBottom || l.GroupOf("terminal") != GroupPrimary || l.IsBottomFull(SideRight) ||
		l.Size(SideLeft) != DefaultSideSize || l.MaxPanel != "" || !l.Visible(DefaultOpenTool) || l.Visible("terminal") {
		t.Fatalf("reset: %+v", l)
	}
	if err := l.Apply(saved); err != nil {
		t.Fatal(err)
	}
	if l.SideOf("terminal") != SideLeft || !l.Visible("terminal") || !l.IsBottomFull(SideRight) || l.Size(SideLeft) != 0.4 ||
		l.MaxPanel != "terminal" {
		t.Fatalf("applied %s: %+v", saved, l)
	}
	// An empty layout shows the project tree; an invalid one changes
	// nothing.
	if err := l.Apply([]byte(`{}`)); err != nil || !l.Visible(DefaultOpenTool) || l.SideOf("terminal") != SideBottom {
		t.Fatalf("empty layout: %v", err)
	}
	v := l.Version
	if err := l.Apply([]byte(`[1]`)); err == nil || l.Version != v {
		t.Fatal("invalid layout applied")
	}
	// A window registered later still finds its stored placement.
	l2 := NewToolLayout()
	if err := l2.Apply([]byte(`{"tools":{"later":"right"}}`)); err != nil {
		t.Fatal(err)
	}
	l2.Register("later", SideBottom)
	if l2.SideOf("later") != SideRight {
		t.Fatal("placement of a later window lost")
	}
}

// Named layouts go through the kernel: save stores the current layout,
// apply replaces it, reset restores the defaults.
func TestNamedToolLayouts(t *testing.T) {
	f := projectFake()
	var mu sync.Mutex
	named := map[string]json.RawMessage{}
	f.on("layout.save", func(raw json.RawMessage) (any, error) {
		var p struct {
			Name   string
			Layout json.RawMessage
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		named[p.Name] = p.Layout
		mu.Unlock()
		return map[string]any{"name": p.Name, "found": true}, nil
	})
	f.on("layout.get", func(raw json.RawMessage) (any, error) {
		var p struct{ Name string }
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		defer mu.Unlock()
		names := []string{}
		for n := range named {
			names = append(names, n)
		}
		return map[string]any{"name": p.Name, "found": p.Name != "", "layout": named[p.Name], "names": names}, nil
	})
	f.on("layout.delete", func(raw json.RawMessage) (any, error) {
		var p struct{ Name string }
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		delete(named, p.Name)
		mu.Unlock()
		return map[string]any{}, nil
	})
	c := started(t, f)
	c.Tools.Register(DefaultOpenTool, SideLeft)
	c.Tools.Register("terminal", SideBottom)
	c.Tools.Show("terminal")
	c.Tools.SetBottomFull(SideLeft, true)
	c.Tools.SetBottomFull(SideRight, true)
	c.SaveToolLayout("  Wide ")
	c.SaveToolLayout(" ") // ignored
	c.Settle(time.Second)
	var names []string
	c.ToolLayoutNames(func(n []string) { names = n })
	c.Settle(time.Second)
	if len(names) != 1 || names[0] != "Wide" || !strings.Contains(c.Status.Notice, "saved") {
		t.Fatalf("names %v notice %q", names, c.Status.Notice)
	}
	c.ResetToolLayout()
	if c.Tools.IsBottomFull(SideLeft) || c.Tools.Visible("terminal") {
		t.Fatal("reset")
	}
	c.ApplyToolLayout("Wide")
	c.Settle(time.Second)
	if !c.Tools.IsBottomFull(SideLeft) || !c.Tools.IsBottomFull(SideRight) || !c.Tools.Visible("terminal") {
		t.Fatalf("applied: %q", c.Status.Notice)
	}
	c.DeleteToolLayout("Wide")
	c.Settle(time.Second)
	c.Status.Notice = ""
	c.ApplyToolLayout("Wide") // gone: the fake returns no layout
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "tool layout") || !c.Tools.IsBottomFull(SideLeft) {
		t.Fatal("applying a missing layout should report")
	}
}
