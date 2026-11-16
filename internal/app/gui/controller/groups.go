package controller

// Editor groups (splits). The editor area is a binary tree of SplitNodes
// whose leaves are EditorGroups, each with its own tabs. Exactly one group
// is active; for compatibility the active group's tabs live in
// Controller.Tabs/Active (all tab methods act on the active group), the
// other groups keep theirs in EditorGroup.Tabs/Active. store() writes the
// active group's state back before groups are inspected or switched.
//
// The same file may be open in several groups: the tabs share one
// document (DocHandle, same doc.open generation); doc.close is only sent
// when the last tab of the file closes.

// EditorGroup is one editor pane with its own tab row.
type EditorGroup struct {
	// Tabs and Active are only current for inactive groups; use
	// Controller.GroupTabs.
	Tabs   []*Tab
	Active int
	node   *SplitNode
}

// SplitNode is a node of the editor split tree: a leaf holds a Group,
// an inner node two children side by side (Vertical false: "Split
// Right") or stacked (Vertical true: "Split Down").
type SplitNode struct {
	Group    *EditorGroup
	Vertical bool
	// Ratio is the share of First (0..1).
	Ratio         float32
	First, Second *SplitNode
	parent        *SplitNode
}

// Leaf reports whether n holds a group.
func (n *SplitNode) Leaf() bool { return n.Group != nil }

// resetGroups starts with one empty group.
func (c *Controller) resetGroups() {
	g := &EditorGroup{Active: -1}
	c.Root = &SplitNode{Group: g}
	g.node = c.Root
	c.groups = []*EditorGroup{g}
	c.activeGroup = 0
	c.Tabs, c.Active = nil, -1
	c.SplitVersion++
}

// store writes Tabs/Active back into the active group.
func (c *Controller) store() {
	if len(c.groups) == 0 {
		c.resetGroups()
	}
	g := c.groups[c.activeGroup]
	g.Tabs, g.Active = c.Tabs, c.Active
}

// Groups returns the editor groups in tree order (left/top first).
func (c *Controller) Groups() []*EditorGroup {
	c.store()
	return c.groups
}

// ActiveGroup returns the active editor group.
func (c *Controller) ActiveGroup() *EditorGroup {
	c.store()
	return c.groups[c.activeGroup]
}

// GroupTabs returns the tabs and the active index of g.
func (c *Controller) GroupTabs(g *EditorGroup) ([]*Tab, int) {
	c.store()
	return g.Tabs, g.Active
}

// GroupIndex returns the position of g (-1 if it is gone).
func (c *Controller) GroupIndex(g *EditorGroup) int {
	for i, x := range c.groups {
		if x == g {
			return i
		}
	}
	return -1
}

// FocusGroup makes group i active (no-op if it already is).
func (c *Controller) FocusGroup(i int) {
	if i < 0 || i >= len(c.groups) || i == c.activeGroup {
		return
	}
	c.store()
	c.activeGroup = i
	g := c.groups[i]
	c.Tabs, c.Active = g.Tabs, g.Active
	c.CloseCompletion()
	c.Signature = SignatureHelp{}
	c.Hover = HoverInfo{}
	c.SplitVersion++
}

// CycleGroup activates the next (delta 1) or previous (-1) group.
func (c *Controller) CycleGroup(delta int) {
	n := len(c.groups)
	if n > 1 {
		c.FocusGroup(((c.activeGroup+delta)%n + n) % n)
	}
}

// allTabs returns the tabs of all groups.
func (c *Controller) allTabs() []*Tab {
	c.store()
	var out []*Tab
	for _, g := range c.groups {
		out = append(out, g.Tabs...)
	}
	return out
}

// FileOpen reports whether file has a tab in any group.
func (c *Controller) FileOpen(file string) bool { return c.sharedTab(file, nil) != nil }

// sharedTab returns a tab of file with a loaded document in any group,
// other than except.
func (c *Controller) sharedTab(file string, except *Tab) *Tab {
	for _, t := range c.allTabs() {
		if t != except && t.File == file && (t.Doc != nil || t.Virtual != "") {
			return t
		}
	}
	return nil
}

// Split opens the active tab in a new group to the right (vertical
// false) or below (true) of the active group and focuses it. Without an
// active tab nothing happens.
func (c *Controller) Split(vertical bool) {
	t := c.ActiveTab()
	if t == nil {
		return
	}
	c.store()
	cur := c.groups[c.activeGroup]
	leaf := cur.node
	ng := &EditorGroup{Tabs: []*Tab{t.clone()}, Active: 0}
	// The leaf becomes an inner node; the current group moves into its
	// first child.
	first := &SplitNode{Group: cur, parent: leaf}
	second := &SplitNode{Group: ng, parent: leaf}
	cur.node, ng.node = first, second
	leaf.Group, leaf.Vertical, leaf.Ratio, leaf.First, leaf.Second = nil, vertical, 0.5, first, second
	c.rebuildGroups()
	c.FocusGroup(c.GroupIndex(ng))
	c.SplitVersion++
}

// Unsplit closes the active group: its tabs move to the neighbouring
// group (files already open there are not duplicated).
func (c *Controller) Unsplit() {
	if len(c.groups) < 2 {
		return
	}
	c.store()
	cur := c.groups[c.activeGroup]
	leaf := cur.node
	parent := leaf.parent
	sibling := parent.First
	if sibling == leaf {
		sibling = parent.Second
	}
	// The parent takes over the sibling subtree.
	*parent = SplitNode{Group: sibling.Group, Vertical: sibling.Vertical, Ratio: sibling.Ratio, First: sibling.First, Second: sibling.Second, parent: parent.parent}
	if parent.Group != nil {
		parent.Group.node = parent
	}
	for _, ch := range []*SplitNode{parent.First, parent.Second} {
		if ch != nil {
			ch.parent = parent
		}
	}
	target := firstLeaf(parent).Group
	merge(target, cur.Tabs, cur.Active)
	c.rebuildGroups()
	c.activeGroup = c.GroupIndex(target)
	c.Tabs, c.Active = target.Tabs, target.Active
	c.SplitVersion++
}

// UnsplitAll merges all groups into one.
func (c *Controller) UnsplitAll() {
	for len(c.groups) > 1 {
		c.Unsplit()
	}
}

// merge appends tabs to g, skipping files g already has, and activates
// the formerly active tab.
func merge(g *EditorGroup, tabs []*Tab, active int) {
	var activeFile string
	if active >= 0 && active < len(tabs) {
		activeFile = tabs[active].File
	}
	for _, t := range tabs {
		dup := false
		for _, x := range g.Tabs {
			if x.File == t.File {
				dup = true
			}
		}
		if !dup {
			g.Tabs = append(g.Tabs, t)
		}
	}
	for i, x := range g.Tabs {
		if x.File == activeFile {
			g.Active = i
		}
	}
	if g.Active < 0 && len(g.Tabs) > 0 {
		g.Active = 0
	}
}

func firstLeaf(n *SplitNode) *SplitNode {
	for !n.Leaf() {
		n = n.First
	}
	return n
}

// rebuildGroups lists the leaves in tree order.
func (c *Controller) rebuildGroups() {
	c.groups = c.groups[:0]
	var walk func(n *SplitNode)
	walk = func(n *SplitNode) {
		if n.Leaf() {
			c.groups = append(c.groups, n.Group)
			return
		}
		walk(n.First)
		walk(n.Second)
	}
	walk(c.Root)
}

// clone returns a tab for the same file sharing the document.
func (t *Tab) clone() *Tab {
	n := *t
	return &n
}

// MoveTabToGroup moves the active tab of the active group to group i
// (Split actions with an existing neighbour use this).
func (c *Controller) MoveTabToGroup(i int) {
	t := c.ActiveTab()
	if t == nil || i < 0 || i >= len(c.groups) || i == c.activeGroup {
		return
	}
	c.store()
	src := c.groups[c.activeGroup]
	dst := c.groups[i]
	src.Tabs = append([]*Tab(nil), src.Tabs...)
	src.Tabs = append(src.Tabs[:src.Active], src.Tabs[src.Active+1:]...)
	if src.Active >= len(src.Tabs) {
		src.Active = len(src.Tabs) - 1
	}
	c.Tabs, c.Active = src.Tabs, src.Active
	merge(dst, []*Tab{t}, 0)
	c.FocusGroup(i)
}
