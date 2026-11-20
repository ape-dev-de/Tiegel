package gui

import (
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
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

// installProjectTool registers the project tree as a tool window.
func (v *View) installProjectTool() {
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolProject, Title: "Project", Glyph: atoms.GlyphFolder, Side: Left, Order: 0,
		Mac: "Cmd+1", PC: "Alt+1", ActionID: ActProjectView,
		Layout: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return v.tree.Layout(gtx, th) },
	})
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
				Group: benchGroup(l.GroupOf(id)), Shortcut: v.Registry.ChordText(t.toggleAction(), v.goos)}
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
	v.toolsVersion = l.Version
}

// handleBench processes the workbench events.
func (v *View) handleBench(gtx layout.Context) {
	l := v.ctl.Tools
	for _, e := range v.bench.Update(gtx) {
		v.handleBenchEvent(e)
	}
	// The template drops a maximized panel that is no longer shown.
	if v.bench.Maximized != l.MaxPanel && v.toolsVersion == l.Version {
		l.SetMaxPanel(v.bench.Maximized)
		v.toolsVersion = l.Version
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
		l.MoveTo(e.ID, toolSide(e.Side), toolGroup(e.Group))
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
	if more := v.Registry.ContextMenu(v, ContextToolWindow, ContextTarget{ToolID: id}); len(more) > 0 {
		items = append(append(items, MenuItem{Separator: true}), more...)
	}
	v.ShowMenu(t.Title, items)
}
