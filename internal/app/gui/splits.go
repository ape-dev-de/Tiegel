package gui

import (
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/tabs"
)

// Editor groups (splits): the editor area draws controller.Root, a tree
// of splits whose leaves are editor groups, each with its own tab row.
// The active group gets the find bar, the keyboard focus and the popups;
// a press anywhere in another group activates it.

// groupView is the view state of one editor group.
type groupView struct {
	bar        tabs.Bar
	lastActive string
	press      int
}

func (v *View) groupView(g *controller.EditorGroup) *groupView {
	gv := v.tabBars[g]
	if gv == nil {
		gv = &groupView{}
		v.tabBars[g] = gv
	}
	return gv
}

// focusGroup activates group g (if it still exists).
func (v *View) focusGroup(g *controller.EditorGroup) {
	if i := v.ctl.GroupIndex(g); i >= 0 {
		v.ctl.FocusGroup(i)
	}
}

// layoutGroups draws the split tree.
func (v *View) layoutGroups(gtx layout.Context) layout.Dimensions {
	root := v.ctl.Root
	if root == nil {
		return v.layoutNoEditor(gtx)
	}
	return v.layoutNode(gtx, root, len(v.ctl.Groups()) > 1)
}

func (v *View) layoutNode(gtx layout.Context, n *controller.SplitNode, split bool) layout.Dimensions {
	if n.Leaf() {
		return v.layoutGroup(gtx, n.Group, split)
	}
	sp := v.splitViews[n]
	if sp == nil {
		sp = &molecules.Split{Ratio: n.Ratio, MinPane: 120}
		v.splitViews[n] = sp
	}
	sp.Vertical = n.Vertical
	d := sp.Layout(gtx, v.th,
		func(gtx layout.Context) layout.Dimensions { return v.layoutNode(gtx, n.First, split) },
		func(gtx layout.Context) layout.Dimensions { return v.layoutNode(gtx, n.Second, split) },
	)
	n.Ratio = sp.Ratio
	return d
}

// layoutGroup draws one group: tab row, find bar (active group) and the
// editor of its active tab.
func (v *View) layoutGroup(gtx layout.Context, g *controller.EditorGroup, split bool) layout.Dimensions {
	th := v.th
	gv := v.groupView(g)
	active := g == v.ctl.ActiveGroup()
	tabsOf, act := v.ctl.GroupTabs(g)
	var cur *controller.Tab
	if act >= 0 && act < len(tabsOf) {
		cur = tabsOf[act]
	}
	v.groupTabs(gtx, g, gv, tabsOf, cur, active, split)
	size := gtx.Constraints.Max
	d := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(gv.bar.Items) == 0 {
				return layout.Dimensions{}
			}
			return gv.bar.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !active || !v.findOpen || v.activeSession() == nil {
				return layout.Dimensions{}
			}
			return v.layoutFind(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			atoms.Surface{Kind: atoms.SurfaceEditor}.Paint(gtx, th)
			switch {
			case cur == nil:
				return v.layoutNoEditor(gtx)
			case cur.Virtual != "":
				if tt, ok := v.Registry.TabType(cur.Virtual); ok && tt.Layout != nil {
					return tt.Layout(gtx, th, cur)
				}
				return layout.Center.Layout(gtx, atoms.Muted("No view for "+cur.Virtual).Fn(th))
			}
			s := v.session(g, cur)
			if s == nil {
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			return v.layoutEditor(gtx, s, active)
		}),
	)
	// A press anywhere in an inactive group activates it.
	if !active {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &gv.press, Kinds: pointer.Press})
			if !ok {
				break
			}
			if _, ok := ev.(pointer.Event); ok {
				v.Post(func() { v.focusGroup(g) })
			}
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &gv.press)
	area.Pop()
	pass.Pop()
	return d
}

// groupTabs fills and handles the tab row of group g.
func (v *View) groupTabs(gtx layout.Context, g *controller.EditorGroup, gv *groupView, list []*controller.Tab, cur *controller.Tab, active, split bool) {
	b := &gv.bar
	b.Items = b.Items[:0]
	for _, t := range list {
		it := tabs.Item{ID: t.File, Title: t.Title(), Tooltip: t.Path, Modified: t.Modified, Glyph: filetree.Glyph(&filetree.Node{Name: t.Title()})}
		if t.Virtual != "" {
			it.Glyph = atoms.GlyphSwap
			if tt, ok := v.Registry.TabType(t.Virtual); ok && tt.Glyph != nil {
				it.Glyph = tt.Glyph
			}
		}
		b.Items = append(b.Items, it)
	}
	b.Active = ""
	if cur != nil {
		b.Active = cur.File
	}
	b.Dimmed = split && !active
	if b.Active != gv.lastActive {
		gv.lastActive = b.Active
		b.ScrollToActive()
		if active && cur != nil && cur.Virtual == "" {
			v.tree.Select(cur.Path)
			v.ctl.LoadAncestors(cur.Path)
		}
	}
	for _, e := range b.Update(gtx) {
		switch e := e.(type) {
		case tabs.SelectEvent:
			v.Post(func() {
				v.focusGroup(g)
				if i := v.tabIndex(e.ID); i >= 0 {
					v.ctl.Activate(i)
				}
				v.focusEditor = true
			})
		case tabs.CloseEvent:
			v.Post(func() {
				v.focusGroup(g)
				if i := v.tabIndex(e.ID); i >= 0 {
					v.closeTab(i)
				}
			})
		case tabs.ContextEvent:
			v.Post(func() {
				v.focusGroup(g)
				if i := v.tabIndex(e.ID); i >= 0 {
					v.ctl.Activate(i)
					v.openTabMenu(v.ctl.Tabs[i])
				}
			})
		}
	}
}

// tabIndex returns the index of file in the active group.
func (v *View) tabIndex(file string) int {
	for i, t := range v.ctl.Tabs {
		if t.File == file {
			return i
		}
	}
	return -1
}

// closeTab closes tab i of the active group; a virtual tab's type is
// told.
func (v *View) closeTab(i int) {
	if i < 0 || i >= len(v.ctl.Tabs) {
		return
	}
	t := v.ctl.Tabs[i]
	v.ctl.CloseTab(i)
	if t.Virtual != "" {
		if tt, ok := v.Registry.TabType(t.Virtual); ok && tt.OnClose != nil {
			tt.OnClose(t)
		}
	}
}

// openTabMenu shows the context menu of the active tab.
func (v *View) openTabMenu(t *controller.Tab) {
	c := v.ctl
	n := len(c.Groups())
	items := []MenuItem{
		{Title: "Close", Action: ActCloseTab},
		{Title: "Close Other Tabs", Run: func() {
			for i := len(c.Tabs) - 1; i >= 0; i-- {
				if c.Tabs[i] != t {
					v.closeTab(i)
				}
			}
		}},
		{Separator: true},
		{Title: "Split Right", Action: ActSplitRight},
		{Title: "Split Down", Action: ActSplitDown},
		{Title: "Move to Next Group", Action: ActMoveToOtherSide, Disabled: n < 2},
		{Title: "Unsplit", Action: ActUnsplit, Disabled: n < 2},
	}
	if t.Virtual == "" {
		items = append(items, MenuItem{Separator: true}, MenuItem{Title: "Copy Path", Run: func() { v.CopyText(t.File) }})
	}
	v.ShowContextMenu(ContextTab, ContextTarget{File: t.File, Tab: t, Group: c.ActiveGroup()}, items)
}

// OpenVirtualTab opens (or activates) a virtual tab of a registered tab
// type in the active group, e.g. a diff (AP10.6).
func (v *View) OpenVirtualTab(kind, id, title string, data any) *controller.Tab {
	return v.ctl.OpenVirtual(kind, id, title, data)
}
