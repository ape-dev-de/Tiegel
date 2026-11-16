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
		l.Register(t.ID, t.Side.toolSide())
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

// MoveTool docks tool window id at side.
func (v *View) MoveTool(id string, side Side) { v.ctl.Tools.Move(id, side.toolSide()) }

// hideActiveTool closes the tool window the user worked with last
// (Shift+Escape); without one, the last opened panel closes.
func (v *View) hideActiveTool() {
	if v.activeTool != "" && v.ToolShown(v.activeTool) {
		v.HideTool(v.activeTool)
		return
	}
	for _, side := range controller.ToolSides {
		if s := v.ctl.Tools.Sides[side]; s.Open && s.Active != "" {
			v.HideTool(s.Active)
			return
		}
	}
}

// toolSlots fills the stripes and the open panels.
func (v *View) toolSlots(slots *templates.Slots) {
	l := v.ctl.Tools
	th := v.th
	for _, side := range controller.ToolSides {
		bs := benchSide(side)
		ids := l.ToolsAt(side, v.Registry.toolOrder)
		btns := v.bench.Stripes[bs][:0]
		for _, id := range ids {
			t, ok := v.Registry.ToolWindow(id)
			if !ok {
				continue
			}
			b := templates.StripeButton{ID: id, Title: t.Title, Glyph: t.Glyph, Active: l.Visible(id)}
			if t.Badge != nil {
				b.Badge, b.BadgeKind = t.Badge()
			}
			btns = append(btns, b)
		}
		v.bench.Stripes[bs] = btns
		if v.toolsVersion != l.Version {
			v.bench.Sizes[bs] = l.Size(side)
		}
		s := l.Sides[side]
		if !s.Open || s.Active == "" {
			continue
		}
		t, ok := v.Registry.ToolWindow(s.Active)
		if !ok || t.Layout == nil {
			continue
		}
		slots.Panels[bs] = templates.Panel{ID: t.ID, Title: t.Title, Content: func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) }}
	}
	v.toolsVersion = l.Version
}

// handleBench processes the workbench events.
func (v *View) handleBench(gtx layout.Context) {
	for _, e := range v.bench.Update(gtx) {
		switch e := e.(type) {
		case templates.ToolEvent:
			v.ToggleTool(e.ID)
		case templates.ToolMenuEvent:
			v.openToolMenu(e.ID)
		case templates.HideEvent:
			v.HideTool(e.ID)
		case templates.MoveEvent:
			v.ctl.Tools.Move(e.ID, toolSide(e.Side))
		case templates.ResizeEvent:
			v.ctl.Tools.SetSize(toolSide(e.Side), e.Size)
			v.toolsVersion = v.ctl.Tools.Version
		case templates.PanelPressEvent:
			v.activeTool = e.ID
		case templates.DismissEvent:
			v.closeOverlay()
		}
	}
}

// openToolMenu shows the menu of a tool window: move to another side,
// hide, plus entries registered for ContextToolWindow.
func (v *View) openToolMenu(id string) {
	t, ok := v.Registry.ToolWindow(id)
	if !ok {
		return
	}
	cur := v.ctl.Tools.SideOf(id)
	move := func(s controller.ToolSide, title string) MenuItem {
		return MenuItem{Title: title, Checked: cur == s, Disabled: cur == s, Run: func() { v.ctl.Tools.Move(id, s) }}
	}
	items := []MenuItem{
		move(controller.SideLeft, "Move to Left"),
		move(controller.SideRight, "Move to Right"),
		move(controller.SideBottom, "Move to Bottom"),
		{Separator: true},
	}
	if v.ToolShown(id) {
		items = append(items, MenuItem{Title: "Hide", Shortcut: "Shift+Escape", Run: func() { v.HideTool(id) }})
	} else {
		items = append(items, MenuItem{Title: "Show", Run: func() { v.ShowTool(id) }})
	}
	if more := v.Registry.ContextMenu(v, ContextToolWindow, ContextTarget{ToolID: id}); len(more) > 0 {
		items = append(append(items, MenuItem{Separator: true}), more...)
	}
	v.ShowMenu(t.Title, items)
}
