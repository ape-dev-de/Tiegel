package gui

import (
	"image"
	"sort"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/tabs"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The registries are the docking points for GUI features beyond the core
// shell (git tool window, commit dialog, gutter change markers, debug
// panel and breakpoints): they register tool windows, gutter marker
// providers, gutter click handlers, actions with shortcuts and menu
// places, status bar items, context menu entries and virtual tab types
// instead of editing the window code. See doc.go for an overview.

// Side selects where a tool window is docked by default. The user can
// move it (stripe button menu, "Move to"); the current side is part of
// the project's tool layout (controller.ToolLayout).
type Side uint8

// Tool window sides.
const (
	// Bottom tool windows share the panel below the editor.
	Bottom Side = iota
	// Right tool windows are shown in the panel right of the editor.
	Right
	// Left tool windows are shown in the panel left of the editor (the
	// project tree).
	Left
)

// toolSide converts a Side.
func (s Side) toolSide() controller.ToolSide {
	switch s {
	case Left:
		return controller.SideLeft
	case Right:
		return controller.SideRight
	}
	return controller.SideBottom
}

// ToolWindow is a panel docked at a side of the workbench, toggled by
// its stripe button or shortcut.
type ToolWindow struct {
	ID    string
	Title string
	// Glyph is the stripe button icon.
	Glyph *atoms.Glyph
	// Side is the default side (the user may move it).
	Side Side
	// Split opens the window in the split slot of its side by default
	// (left/right: below the primary window, bottom: right of it).
	Split bool
	// Order sorts the stripe buttons (lower first).
	Order int
	// Mac and PC are the chords toggling the window ("Cmd+9"); the
	// registry adds an action (ActionID, default "toolwindow.<ID>") in
	// the View → Tool Windows menu.
	Mac, PC string
	// ActionID overrides the ID of the toggle action.
	ActionID keymap.Action
	// Badge returns a short counter/state for the button (may be nil).
	Badge func() (string, atoms.BadgeKind)
	// Layout draws the content; it fills the given constraints.
	Layout func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	// OnOpen is called when the window is shown (may be nil).
	OnOpen func()
	// Share is the minimum share of the editor column a bottom panel
	// takes when this window opens (0: keep the current size).
	Share float32
	// BeforeToggle (optional) runs before the toggle action; returning
	// true handles it (e.g. focusing an open but unfocused window).
	BeforeToggle func() bool
}

// toggleAction returns the ID of the window's toggle action.
func (t ToolWindow) toggleAction() keymap.Action {
	if t.ActionID != "" {
		return t.ActionID
	}
	return keymap.Action("toolwindow." + t.ID)
}

// GutterProvider returns the gutter markers of an open file (absolute
// path), e.g. git changes or breakpoints. It is called once per frame for
// the visible editor; the returned provider is queried per visible line.
type GutterProvider func(file string) editorview.MarkerProvider

// GutterClickHandler handles a click into the gutter of file; it returns
// true if it consumed the click.
type GutterClickHandler func(file string, ev editorview.GutterClickEvent) bool

// ActionScope tells where an action's shortcut is active.
type ActionScope uint8

// Action scopes.
const (
	// ScopeGlobal shortcuts work regardless of the focus.
	ScopeGlobal ActionScope = iota
	// ScopeEditor shortcuts work while the editor has the focus. Run is
	// called with the active editor's document locked.
	ScopeEditor
)

// Action is a command with optional shortcuts (menu, Find Action, keys).
type Action struct {
	ID    keymap.Action
	Title string
	// Mac and PC are the default chords ("Cmd+Shift+O"); empty: none.
	Mac, PC string
	Scope   ActionScope
	// Menu places the action in the menu bar: a top-level menu name
	// ("File", "Edit", "View", "Navigate", "Git", "Run", "Window",
	// "Help"), optionally with a submenu ("View/Tool Windows"). Empty:
	// only in Find Action.
	Menu string
	// Section groups entries of a menu; a separator is drawn between
	// different sections (in registration order).
	Section string
	// Icon names the icon in menus and on a header button (atoms glyph or
	// fileicons tool glyph name, see RegisterToolbarAction).
	Icon string
	// KeyHint is a shortcut shown in menus that is handled elsewhere
	// (e.g. by the editor keymap); it is not bound by the window.
	KeyHint string
	// Run executes the action on the UI goroutine.
	Run func(v *View)
}

// StatusProvider returns extra status bar items (right side).
type StatusProvider func() []statusbar.Item

// StatusClickHandler handles a click on a (clickable) status item; it
// returns true if it consumed the click.
type StatusClickHandler func(v *View, id string) bool

// FrameHandler runs at the start of every workbench frame (after the
// controller results were applied, before layout). Features use it to
// follow controller state, e.g. to add or remove their tool windows when a
// module is enabled or disabled.
type FrameHandler func(v *View, gtx layout.Context)

// ContextID names a place with a context menu.
type ContextID string

// Context menu places (docking points for AP10.4).
const (
	// ContextEditor: right click into the editor text.
	ContextEditor ContextID = "editor"
	// ContextEditorGutter: right click into the gutter.
	ContextEditorGutter ContextID = "editorGutter"
	// ContextProjectTree: right click on project tree entries.
	ContextProjectTree ContextID = "projectTree"
	// ContextTab: right click on an editor tab.
	ContextTab ContextID = "tab"
	// ContextToolWindow: right click on a stripe button or the options
	// button of a tool window header.
	ContextToolWindow ContextID = "toolWindow"
)

// ContextTarget describes what a context menu was opened on. Only the
// fields of the place are set.
type ContextTarget struct {
	// File is the absolute file (editor, gutter, tab, tree).
	File string
	// Paths are the selected tree entries (absolute).
	Paths []string
	// Line is the 1-based line (editor, gutter); Offset the byte offset
	// in the document (editor).
	Line, Offset int
	// Tab is the tab (tab context).
	Tab *controller.Tab
	// Group is the editor group of the tab or editor.
	Group *controller.EditorGroup
	// ToolID is the tool window (toolWindow context).
	ToolID string
	// Pos is the pointer position in window coordinates.
	Pos image.Point
}

// MenuItem is an entry of a context menu (or of a menu built for the
// menu presenters).
type MenuItem struct {
	Title string
	// Action runs a registered action (its shortcut is shown); Run is
	// used when Action is empty.
	Action keymap.Action
	Run    func()
	// Shortcut is shown right-aligned (filled from Action if empty).
	Shortcut  string
	Disabled  bool
	Checked   bool
	Separator bool
	Children  []MenuItem
	// Icon names a glyph (atoms.GlyphNamed), e.g. from the action model.
	Icon string
}

// ContextMenuProvider returns the entries a feature adds to a context
// menu (nil: none). Entries of all providers are joined with separators
// in registration order.
type ContextMenuProvider func(v *View, t ContextTarget) []MenuItem

// TabType draws virtual editor tabs of one kind (tabs without a file,
// e.g. a diff): controller.Controller.OpenVirtual / View.OpenVirtualTab
// open them, the tab bar shows Title and Glyph, and the editor area of
// the group shows Layout.
type TabType struct {
	Kind  string
	Glyph *atoms.Glyph
	// Layout draws the tab content filling the constraints.
	Layout func(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions
	// OnClose runs after the tab was closed (may be nil).
	OnClose func(tab *controller.Tab)
}

// Registry collects the extension points of the window.
type Registry struct {
	tools        []ToolWindow
	gutters      []GutterProvider
	gutterClicks []GutterClickHandler
	actions      map[keymap.Action]Action
	order        []keymap.Action
	status       []StatusProvider
	statusClicks []StatusClickHandler
	frames       []FrameHandler
	contexts     map[ContextID][]ContextMenuProvider
	tabTypes     map[string]TabType
	banners      []EditorBannerProvider
	gutterCols   []GutterColumnProvider
	tabDecos     []TabDecorator
	version      uint64
	// model is the action model (menus, Find Action, labels, shortcuts
	// texts, enabled states; D160), see actionmodel.go.
	model     *actions.Registry
	modelGOOS string
	mainSeen  uint64
	// scheme resolves the chords of catalog actions (keymap.Defs) with
	// the user overrides; editorKeys is its editor widget keymap (keys.go).
	scheme     *keymap.Scheme
	editorKeys *keymap.Keymap
}

// EditorBannerProvider returns a banner shown above the editor of a file
// tab, e.g. "File changed on disk" (nil: none).
type EditorBannerProvider func(v *View, t *controller.Tab) layout.Widget

// GutterColumnProvider returns an extra gutter column for an open file,
// e.g. the git annotations (nil: none). The first non-nil column wins.
type GutterColumnProvider func(file string) editorview.GutterColumn

// TabDecorator adjusts the tab row entry of a tab (tooltip, glyph), e.g.
// to mark files deleted on disk.
type TabDecorator func(t *controller.Tab, it *tabs.Item)

// AddEditorBanner registers a banner provider for file editors.
func (r *Registry) AddEditorBanner(p EditorBannerProvider) { r.banners = append(r.banners, p) }

// AddGutterColumn registers an extra gutter column provider.
func (r *Registry) AddGutterColumn(p GutterColumnProvider) { r.gutterCols = append(r.gutterCols, p) }

// AddTabDecorator registers a tab decorator.
func (r *Registry) AddTabDecorator(d TabDecorator) { r.tabDecos = append(r.tabDecos, d) }

// gutterColumn returns the extra gutter column of file (nil: none).
func (r *Registry) gutterColumn(file string) editorview.GutterColumn {
	for _, p := range r.gutterCols {
		if c := p(file); c != nil {
			return c
		}
	}
	return nil
}

// decorateTab applies the tab decorators.
func (r *Registry) decorateTab(t *controller.Tab, it *tabs.Item) {
	for _, d := range r.tabDecos {
		d(t, it)
	}
}

// editorBanner draws the banners of tab t (nothing for virtual tabs).
func (v *View) editorBanner(gtx layout.Context, t *controller.Tab) layout.Dimensions {
	if t == nil || t.Virtual != "" || len(v.Registry.banners) == 0 {
		return layout.Dimensions{}
	}
	var ws []layout.FlexChild
	for _, p := range v.Registry.banners {
		if w := p(v, t); w != nil {
			ws = append(ws, layout.Rigid(w))
		}
	}
	if len(ws) == 0 {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ws...)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{actions: map[keymap.Action]Action{}, contexts: map[ContextID][]ContextMenuProvider{}, tabTypes: map[string]TabType{}}
}

// Version changes whenever tool windows or actions change.
func (r *Registry) Version() uint64 { return r.version }

// AddToolWindow registers (or replaces, by ID) a tool window. With a
// shortcut it also registers the toggle action.
func (r *Registry) AddToolWindow(t ToolWindow) {
	r.addToggle(t)
	for i := range r.tools {
		if r.tools[i].ID == t.ID {
			r.tools[i] = t
			r.version++
			return
		}
	}
	r.tools = append(r.tools, t)
	sort.SliceStable(r.tools, func(i, j int) bool { return r.tools[i].Order < r.tools[j].Order })
	r.version++
}

func (r *Registry) addToggle(t ToolWindow) {
	if t.Mac == "" && t.PC == "" && t.ActionID == "" {
		return
	}
	id, before := t.ID, t.BeforeToggle
	r.AddAction(Action{ID: t.toggleAction(), Title: t.Title, Mac: t.Mac, PC: t.PC, Menu: "View/Tool Windows",
		Run: func(v *View) {
			if before != nil && before() {
				return
			}
			v.ToggleTool(id)
		}})
}

// RemoveToolWindow removes a tool window (e.g. when its module is
// disabled) and its toggle action.
func (r *Registry) RemoveToolWindow(id string) {
	for i := range r.tools {
		if r.tools[i].ID == id {
			t := r.tools[i]
			r.tools = append(r.tools[:i], r.tools[i+1:]...)
			if t.Mac != "" || t.PC != "" || t.ActionID != "" {
				r.RemoveAction(t.toggleAction())
			}
			r.version++
			return
		}
	}
}

// AllToolWindows returns all tool windows in order.
func (r *Registry) AllToolWindows() []ToolWindow { return r.tools }

// ToolWindow returns the tool window with id.
func (r *Registry) ToolWindow(id string) (ToolWindow, bool) {
	for _, t := range r.tools {
		if t.ID == id {
			return t, true
		}
	}
	return ToolWindow{}, false
}

// order returns the Order of tool window id.
func (r *Registry) toolOrder(id string) int {
	if t, ok := r.ToolWindow(id); ok {
		return t.Order
	}
	return 1 << 20
}

// AddGutterProvider registers a marker provider; markers of all providers
// are combined in registration order.
func (r *Registry) AddGutterProvider(p GutterProvider) { r.gutters = append(r.gutters, p) }

// AddGutterClickHandler registers a gutter click handler.
func (r *Registry) AddGutterClickHandler(h GutterClickHandler) {
	r.gutterClicks = append(r.gutterClicks, h)
}

// AddAction registers (or replaces) an action.
func (r *Registry) AddAction(a Action) {
	if _, ok := r.actions[a.ID]; !ok {
		r.order = append(r.order, a.ID)
	}
	r.actions[a.ID] = a
	r.mirror(a)
	r.version++
}

// RemoveAction removes an action and its shortcuts.
func (r *Registry) RemoveAction(id keymap.Action) {
	if _, ok := r.actions[id]; !ok {
		return
	}
	delete(r.actions, id)
	r.unmirror(id)
	for i, a := range r.order {
		if a == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.version++
}

// Action returns a registered action.
func (r *Registry) Action(id keymap.Action) (Action, bool) {
	a, ok := r.actions[id]
	return a, ok
}

// Actions returns all actions in registration order.
func (r *Registry) Actions() []Action {
	out := make([]Action, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.actions[id])
	}
	return out
}

// AddStatusProvider registers extra status bar items.
func (r *Registry) AddStatusProvider(p StatusProvider) { r.status = append(r.status, p) }

// AddStatusClickHandler registers a handler for status item clicks.
func (r *Registry) AddStatusClickHandler(h StatusClickHandler) {
	r.statusClicks = append(r.statusClicks, h)
}

// AddFrameHandler registers a per-frame handler.
func (r *Registry) AddFrameHandler(h FrameHandler) { r.frames = append(r.frames, h) }

// AddContextMenu registers entries for the context menu at ctx.
func (r *Registry) AddContextMenu(ctx ContextID, p ContextMenuProvider) {
	r.contexts[ctx] = append(r.contexts[ctx], p)
}

// ContextMenu collects the entries of all providers for ctx.
func (r *Registry) ContextMenu(v *View, ctx ContextID, t ContextTarget) []MenuItem {
	var out []MenuItem
	for _, p := range r.contexts[ctx] {
		items := p(v, t)
		if len(items) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, MenuItem{Separator: true})
		}
		out = append(out, items...)
	}
	return out
}

// AddTabType registers (or replaces) the virtual tab type t.Kind.
func (r *Registry) AddTabType(t TabType) { r.tabTypes[t.Kind] = t }

// RemoveTabType removes a virtual tab type.
func (r *Registry) RemoveTabType(kind string) { delete(r.tabTypes, kind) }

// TabType returns the virtual tab type of kind.
func (r *Registry) TabType(kind string) (TabType, bool) {
	t, ok := r.tabTypes[kind]
	return t, ok
}

// markers combines the gutter providers for file.
func (r *Registry) markers(file string) editorview.MarkerProvider {
	if len(r.gutters) == 0 {
		return nil
	}
	ps := make([]editorview.MarkerProvider, 0, len(r.gutters))
	for _, g := range r.gutters {
		if p := g(file); p != nil {
			ps = append(ps, p)
		}
	}
	switch len(ps) {
	case 0:
		return nil
	case 1:
		return ps[0]
	}
	return editorview.MarkerFunc(func(line int) []editorview.Marker {
		var out []editorview.Marker
		for _, p := range ps {
			out = append(out, p.Markers(line)...)
		}
		return out
	})
}
