package gui

import (
	"sort"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The registries are the docking points for GUI features beyond the core
// shell (git tool window, commit dialog, gutter change markers, debug
// panel and breakpoints): they register tool windows, gutter marker
// providers, gutter click handlers, actions with shortcuts and status bar
// items instead of editing the window code.

// Side selects where a tool window is shown.
type Side uint8

// Tool window sides.
const (
	// Bottom tool windows share the collapsible area below the editor.
	Bottom Side = iota
	// Right tool windows are shown in the optional right panel.
	Right
)

// ToolWindow is a panel of the tool window area.
type ToolWindow struct {
	ID    string
	Title string
	Glyph *atoms.Glyph
	Side  Side
	// Order sorts the tabs (lower first).
	Order int
	// Badge returns a short counter/state for the tab (may be nil).
	Badge func() (string, atoms.BadgeKind)
	// Layout draws the content; it fills the given constraints.
	Layout func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	// OnOpen is called when the window is shown (may be nil).
	OnOpen func()
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
	// ScopeEditor shortcuts work while the editor has the focus.
	ScopeEditor
)

// Action is a command with optional shortcuts (menu, palette, keys).
type Action struct {
	ID    keymap.Action
	Title string
	// Mac and PC are the default chords ("Cmd+Shift+O"); empty: none.
	Mac, PC string
	Scope   ActionScope
	// Run executes the action on the UI goroutine.
	Run func(v *View)
}

// StatusProvider returns extra status bar items (right side).
type StatusProvider func() []statusbar.Item

// Registry collects the extension points of the window.
type Registry struct {
	tools        []ToolWindow
	gutters      []GutterProvider
	gutterClicks []GutterClickHandler
	actions      map[keymap.Action]Action
	order        []keymap.Action
	status       []StatusProvider
	version      uint64
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{actions: map[keymap.Action]Action{}} }

// AddToolWindow registers (or replaces, by ID) a tool window.
func (r *Registry) AddToolWindow(t ToolWindow) {
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

// ToolWindows returns the tool windows of a side in order.
func (r *Registry) ToolWindows(side Side) []ToolWindow {
	var out []ToolWindow
	for _, t := range r.tools {
		if t.Side == side {
			out = append(out, t)
		}
	}
	return out
}

// ToolWindow returns the tool window with id.
func (r *Registry) ToolWindow(id string) (ToolWindow, bool) {
	for _, t := range r.tools {
		if t.ID == id {
			return t, true
		}
	}
	return ToolWindow{}, false
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
