package gui

import (
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Tool windows: the registry lists them, controller.ToolLayout keeps
// their side, the open panel per side and the panel sizes (persisted per
// project), and the workbench template draws stripes and panels.

// ToolProject is the ID of the project tree tool window.
const ToolProject = controller.DefaultOpenTool

// sideOf converts between controller and template sides.
func benchSide(s controller.ToolSide) templates.Side {
	switch s {
	case controller.SideLeft:
		return templates.SideLeft
	case controller.SideRight:
		return templates.SideRight
	}
	return templates.SideBottom
}

func toolSide(s templates.Side) controller.ToolSide {
	switch s {
	case templates.SideLeft:
		return controller.SideLeft
	case templates.SideRight:
		return controller.SideRight
	}
	return controller.SideBottom
}

// installProjectTool registers the project tree as a tool window and the
// layout actions.
func (v *View) installProjectTool() {
	v.installLayoutActions()
	v.Registry.AddAction(Action{ID: ActReloadFromDisk, Title: "Reload All from Disk", Mac: "Cmd+Alt+Y", PC: "Ctrl+Alt+Y",
		Menu: "File", Section: "save", Run: func(v *View) { v.ctl.Work.Sync("") }})
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolProject, Title: "Project", Glyph: atoms.GlyphFolder, Side: Left, Order: 0,
		Mac: "Cmd+1", PC: "Alt+1", ActionID: ActProjectView,
		Layout:  func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return v.tree.Layout(gtx, th) },
		Actions: v.layoutProjectActions,
	})
}

// ActReloadFromDisk rescans the project and reloads the tree and open
// documents from disk (also in the project tree's header and context
// menu).
const ActReloadFromDisk keymap.Action = "file.reloadFromDisk"

// layoutProjectActions draws the project tree's header buttons "Reload
// All from Disk" and "Select Opened File" (the active editor's file in
// the tree).
func (v *View) layoutProjectActions(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(v.layoutSyncButton(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutLocateButton(gtx, th) }),
	)
}

func (v *View) layoutSyncButton(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := &v.syncBtn
		b.Glyph, b.Size, b.IconSize = atoms.GlyphRefresh, th.Size.ControlH-th.Space.XS, th.Size.IconSmall
		b.Description = "Reload All from Disk"
		if c := v.chordLabel(ActReloadFromDisk); c != "" {
			b.Description += " (" + c + ")"
		}
		if b.Clicked(gtx) {
			v.runAction(ActReloadFromDisk)
		}
		v.syncTip.Text = b.Description
		return v.syncTip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
	}
}

func (v *View) layoutLocateButton(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b := &v.locateBtn
	b.Glyph, b.Size, b.IconSize = atoms.GlyphLocate, th.Size.ControlH-th.Space.XS, th.Size.IconSmall
	t := v.ctl.ActiveTab()
	b.Disabled = t == nil || t.Virtual != ""
	b.Description = "Select Opened File"
	if c := v.chordLabel(ActSelectInTree); c != "" {
		b.Description += " (" + c + ")"
	}
	if b.Clicked(gtx) {
		v.runAction(ActSelectInTree)
	}
	v.locateTip.Text = b.Description
	return v.locateTip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
}

// syncTools registers the registry's tool windows in the layout (on
// registry or layout changes).
func (v *View) syncTools() {
	l := v.ctl.Tools
	ver := v.Registry.Version()
	if v.toolsFor == l && v.toolsSeen == ver {
		return
	}
	if v.toolsFor != l {
		v.toolIDs = map[string]bool{}
	}
	v.toolsFor, v.toolsSeen = l, ver
	known := map[string]bool{}
	for _, t := range v.Registry.AllToolWindows() {
		known[t.ID] = true
		g := controller.GroupPrimary
		if t.Split {
			g = controller.GroupSplit
		}
		l.RegisterIn(t.ID, t.Side.toolSide(), g)
	}
	// Windows that went away (module disabled) close; stored placements
	// of windows not registered yet stay untouched.
	for id := range v.toolIDs {
		if !known[id] {
			l.Unregister(id)
		}
	}
	v.toolIDs = known
}

// ShowTool opens the tool window id and brings it to the front.
func (v *View) ShowTool(id string) {
	l := v.ctl.Tools
	v.activeTool = id
	if l.Visible(id) {
		return
	}
	l.Show(id)
	t, ok := v.Registry.ToolWindow(id)
	if ok && t.Share > 0 && l.SideOf(id) == controller.SideBottom && l.Size(controller.SideBottom) < t.Share {
		l.SetSize(controller.SideBottom, t.Share)
	}
	if ok && t.OnOpen != nil {
		t.OnOpen()
	}
}

// ToolShown reports whether tool window id is open.
func (v *View) ToolShown(id string) bool { return v.ctl.Tools.Visible(id) }

// HideTool closes tool window id if it is open.
func (v *View) HideTool(id string) {
	if v.ctl.Tools.Visible(id) {
		v.ctl.Tools.Hide(id)
		v.focusEditor = true
	}
}

// ToggleTool shows or hides tool window id.
func (v *View) ToggleTool(id string) {
	if v.ctl.Tools.Visible(id) {
		v.HideTool(id)
		return
	}
	v.ShowTool(id)
}

// toggleTool is ToggleTool (kept for the tests of the first shell).
func (v *View) toggleTool(id string) { v.ToggleTool(id) }

// MoveTool docks tool window id at side (primary slot).
func (v *View) MoveTool(id string, side Side) { v.ctl.Tools.Move(id, side.toolSide()) }

// benchGroup and toolGroup convert between controller and template groups.
func benchGroup(g controller.ToolGroup) templates.StripeGroup {
	if g == controller.GroupSplit {
		return templates.GroupSplit
	}
	return templates.GroupPrimary
}

func toolGroup(g templates.StripeGroup) controller.ToolGroup {
	if g == templates.GroupSplit {
		return controller.GroupSplit
	}
	return controller.GroupPrimary
}

// hideActiveTool closes the tool window the user worked with last
// (Shift+Escape); without one, the last opened panel closes.
func (v *View) hideActiveTool() {
	if v.activeTool != "" && v.ToolShown(v.activeTool) {
		v.HideTool(v.activeTool)
		return
	}
	for _, side := range controller.ToolSides {
		for _, g := range controller.ToolGroups {
			if id := v.ctl.Tools.Shown(side, g); id != "" {
				v.HideTool(id)
				return
			}
		}
	}
}

// toolSlots fills the stripes (icons, groups, shortcuts) and the open
// panels of both slots per side.
func (v *View) toolSlots(slots *templates.Slots) {
	l := v.ctl.Tools
	th := v.th
	synced := v.toolsVersion == l.Version
	for _, side := range controller.ToolSides {
		bs := benchSide(side)
		ids := l.ToolsAt(side, v.Registry.toolOrder)
		btns := v.bench.Stripes[bs][:0]
		for _, id := range ids {
			t, ok := v.Registry.ToolWindow(id)
			if !ok {
				continue
			}
			b := templates.StripeButton{ID: id, Title: t.Title, Glyph: toolGlyph(t), Active: l.Visible(id),
				Group: benchGroup(l.GroupOf(id)), Shortcut: v.chordLabel(t.toggleAction())}
			if t.Badge != nil {
				b.Badge, b.BadgeKind = t.Badge()
			}
			btns = append(btns, b)
		}
		v.bench.Stripes[bs] = btns
		if !synced {
			v.bench.Sizes[bs] = l.Size(side)
			v.bench.SplitSizes[bs] = l.SplitSize(side)
		}
		for _, g := range controller.ToolGroups {
			id := l.Shown(side, g)
			if id == "" {
				continue
			}
			t, ok := v.Registry.ToolWindow(id)
			if !ok || t.Layout == nil {
				continue
			}
			p := templates.Panel{ID: t.ID, Title: t.Title, Content: func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) }}
			if t.Actions != nil {
				p.Actions = func(gtx layout.Context) layout.Dimensions { return t.Actions(gtx, th) }
			}
			for _, tid := range l.TabsOf(side, g) {
				if tt, ok := v.Registry.ToolWindow(tid); ok {
					p.Tabs = append(p.Tabs, templates.PanelTab{ID: tid, Title: tt.Title})
				}
			}
			if len(p.Tabs) < 2 {
				p.Tabs = nil
			}
			if g == controller.GroupSplit {
				slots.Splits[bs] = p
			} else {
				slots.Panels[bs] = p
			}
		}
	}
	if !synced {
		v.bench.Maximized = l.MaxPanel
	}
	v.maxDrawable = false
	if l.MaxPanel != "" {
		t, ok := v.Registry.ToolWindow(l.MaxPanel)
		v.maxDrawable = ok && t.Layout != nil
	}
	v.bench.BottomFull[templates.CornerLeft] = l.IsBottomFull(controller.SideLeft)
	v.bench.BottomFull[templates.CornerRight] = l.IsBottomFull(controller.SideRight)
	v.toolsVersion = l.Version
}

// handleBench processes the workbench events.
func (v *View) handleBench(gtx layout.Context) {
	l := v.ctl.Tools
	for _, e := range v.bench.Update(gtx) {
		v.handleBenchEvent(e)
	}
	// The template drops a maximized panel that is no longer shown. A
	// window that is not registered yet (module still starting) keeps its
	// stored maximize for when it registers.
	if v.bench.Maximized != l.MaxPanel && v.toolsVersion == l.Version {
		if v.maxDrawable {
			l.SetMaxPanel(v.bench.Maximized)
			v.toolsVersion = l.Version
		} else {
			v.bench.Maximized = l.MaxPanel
		}
	}
}

// handleBenchEvent applies one workbench event.
func (v *View) handleBenchEvent(ev templates.Event) {
	l := v.ctl.Tools
	switch e := ev.(type) {
	case templates.ToolEvent:
		v.ToggleTool(e.ID)
	case templates.ToolMenuEvent:
		v.openToolMenu(e.ID)
	case templates.HideEvent:
		v.HideTool(e.ID)
	case templates.MoveEvent:
		// A dropped window is shown where it landed.
		side, group := toolSide(e.Side), toolGroup(e.Group)
		if e.Tab {
			l.JoinTab(e.ID, side, group)
		} else {
			l.MoveTo(e.ID, side, group)
		}
		if e.FullWidth {
			l.SetBottomFull(controller.SideLeft, true)
			l.SetBottomFull(controller.SideRight, true)
		}
		v.ShowTool(e.ID)
	case templates.SelectEvent:
		v.ShowTool(e.ID)
	case templates.ResizeEvent:
		if e.Split {
			l.SetSplitSize(toolSide(e.Side), e.Size)
		} else {
			l.SetSize(toolSide(e.Side), e.Size)
		}
		v.toolsVersion = l.Version
	case templates.MaximizeEvent:
		id := ""
		if e.Maximized {
			id = e.ID
		}
		l.SetMaxPanel(id)
		v.toolsVersion = l.Version
	case templates.PanelPressEvent:
		v.activeTool = e.ID
		if e.ID == ToolTerminal {
			v.termd.pressed()
		}
	case templates.DismissEvent:
		v.closeOverlay()
	}
}

// toolSlotNames are the "Move to" entries of the tool window menu.
var toolSlotNames = []struct {
	side  controller.ToolSide
	group controller.ToolGroup
	title string
}{
	{controller.SideLeft, controller.GroupPrimary, "Move to Left"},
	{controller.SideLeft, controller.GroupSplit, "Move to Left Bottom"},
	{controller.SideRight, controller.GroupPrimary, "Move to Right"},
	{controller.SideRight, controller.GroupSplit, "Move to Right Bottom"},
	{controller.SideBottom, controller.GroupPrimary, "Move to Bottom"},
	{controller.SideBottom, controller.GroupSplit, "Move to Bottom Right"},
}

// openToolMenu shows the menu of a tool window: move to another slot,
// maximize, hide, plus entries registered for ContextToolWindow.
func (v *View) openToolMenu(id string) {
	t, ok := v.Registry.ToolWindow(id)
	if !ok {
		return
	}
	l := v.ctl.Tools
	side, group := l.SideOf(id), l.GroupOf(id)
	var moves []MenuItem
	for _, s := range toolSlotNames {
		cur := s.side == side && s.group == group
		moves = append(moves, MenuItem{Title: s.title, Checked: cur, Disabled: cur, Run: func() { l.MoveTo(id, s.side, s.group) }})
	}
	items := append(moves, MenuItem{Separator: true})
	if v.ToolShown(id) {
		title := "Maximize"
		if l.MaxPanel == id {
			title = "Restore"
		}
		items = append(items,
			MenuItem{Title: title, Run: func() {
				if l.MaxPanel == id {
					l.SetMaxPanel("")
				} else {
					l.SetMaxPanel(id)
				}
			}},
			MenuItem{Title: "Hide", Shortcut: "Shift+Escape", Run: func() { v.HideTool(id) }})
	} else {
		items = append(items, MenuItem{Title: "Show", Run: func() { v.ShowTool(id) }})
	}
	if len(l.TabsOf(side, group)) > 0 && contains(l.TabsOf(side, group), id) {
		items = append(items, MenuItem{Title: "Remove from Tab Group", Run: func() { l.LeaveTabs(id) }})
	}
	items = append(append(items, MenuItem{Separator: true}), v.layoutMenuItems()...)
	if more := v.Registry.ContextMenu(v, ContextToolWindow, ContextTarget{ToolID: id}); len(more) > 0 {
		items = append(append(items, MenuItem{Separator: true}), more...)
	}
	v.ShowMenu(t.Title, items)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Layout actions (View › Tool Window Layout, D475/D476).
const (
	ActBottomFullWidth  keymap.Action = "view.layout.bottomFullWidth"
	ActBottomUnderLeft  keymap.Action = "view.layout.bottomUnderLeft"
	ActBottomUnderRight keymap.Action = "view.layout.bottomUnderRight"
	ActResetLayout      keymap.Action = "view.layout.reset"
	ActSaveLayout       keymap.Action = "view.layout.save"
	ActApplyLayout      keymap.Action = "view.layout.apply"
	ActDeleteLayout     keymap.Action = "view.layout.delete"
)

// layoutMenu is the submenu of the layout actions.
const layoutMenu = "View/Tool Window Layout"

// installLayoutActions registers the layout actions.
func (v *View) installLayoutActions() {
	for _, a := range []Action{
		{ID: ActBottomFullWidth, Title: "Bottom Panel: Full Width", Section: "corners", Run: func(v *View) {
			on := !(v.ctl.Tools.IsBottomFull(controller.SideLeft) && v.ctl.Tools.IsBottomFull(controller.SideRight))
			v.ctl.Tools.SetBottomFull(controller.SideLeft, on)
			v.ctl.Tools.SetBottomFull(controller.SideRight, on)
		}},
		{ID: ActBottomUnderLeft, Title: "Bottom Panel: Below Left Panels", Section: "corners", Run: func(v *View) { v.toggleCorner(controller.SideLeft) }},
		{ID: ActBottomUnderRight, Title: "Bottom Panel: Below Right Panels", Section: "corners", Run: func(v *View) { v.toggleCorner(controller.SideRight) }},
		{ID: ActResetLayout, Title: "Reset Layout", Section: "layouts", Run: func(v *View) { v.ctl.ResetToolLayout() }},
		{ID: ActSaveLayout, Title: "Save Layout As…", Section: "layouts", Run: (*View).saveLayoutAs},
		{ID: ActApplyLayout, Title: "Apply Layout…", Section: "layouts", Run: func(v *View) { v.chooseLayout("Apply Layout", v.ctl.ApplyToolLayout) }},
		{ID: ActDeleteLayout, Title: "Delete Layout…", Section: "layouts", Run: func(v *View) { v.chooseLayout("Delete Layout", v.ctl.DeleteToolLayout) }},
	} {
		a.Menu = layoutMenu
		v.Registry.AddAction(a)
	}
}

// toggleCorner gives the bottom corner of side to the bottom panel or
// back to the side panels.
func (v *View) toggleCorner(side controller.ToolSide) {
	l := v.ctl.Tools
	l.SetBottomFull(side, !l.IsBottomFull(side))
}

// layoutMenuItems are the layout entries of the tool window menu.
func (v *View) layoutMenuItems() []MenuItem {
	l := v.ctl.Tools
	left, right := l.IsBottomFull(controller.SideLeft), l.IsBottomFull(controller.SideRight)
	return []MenuItem{
		{Title: "Bottom Panel: Full Width", Action: ActBottomFullWidth, Checked: left && right},
		{Title: "Bottom Panel: Below Left Panels", Action: ActBottomUnderLeft, Checked: left},
		{Title: "Bottom Panel: Below Right Panels", Action: ActBottomUnderRight, Checked: right},
		{Title: "Reset Layout", Action: ActResetLayout},
	}
}

// saveLayoutAs asks for a name and saves the current layout under it.
func (v *View) saveLayoutAs() {
	v.OpenChooser(ChooserSpec{Title: "Save Layout As", Hint: "Name of the layout (an existing one is replaced)", NoList: true,
		OnChoose: func(_ int, q string) {
			if q = strings.TrimSpace(q); q != "" {
				v.ctl.SaveToolLayout(q)
			}
		}})
}

// chooseLayout lists the saved layouts and runs fn with the chosen name.
func (v *View) chooseLayout(title string, fn func(name string)) {
	v.ctl.ToolLayoutNames(func(names []string) {
		if len(names) == 0 {
			v.ctl.Status.Notice = "No saved layouts (View › Tool Window Layout › Save Layout As…)"
			return
		}
		filter := func(q string) ([]palette.Item, []string) {
			var items []palette.Item
			var shown []string
			for _, n := range names {
				if strings.Contains(strings.ToLower(n), strings.ToLower(strings.TrimSpace(q))) {
					items = append(items, palette.Item{Label: n})
					shown = append(shown, n)
				}
			}
			return items, shown
		}
		items, shown := filter("")
		v.OpenChooser(ChooserSpec{Title: title, Hint: "Saved layout", Items: items,
			OnQuery: func(q string) []palette.Item {
				var out []palette.Item
				out, shown = filter(q)
				return out
			},
			OnChoose: func(i int, _ string) {
				if i >= 0 && i < len(shown) {
					fn(shown[i])
				}
			}})
	})
}
