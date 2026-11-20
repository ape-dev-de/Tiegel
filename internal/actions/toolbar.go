package actions

import (
	"slices"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// CtxToolbar is the quick-action strip of the header toolbar (between the
// run controls and search/settings). It is not a context menu: modules
// contribute buttons with AddToolbar (e.g. "Claude Code", "opencode"), and
// the GUI shows the resolved Toolbar buttons. The buttons of one AddToolbar
// call form a group; groups are separated by a divider.
const CtxToolbar ContextID = "toolbar"

// ToolbarButton is a resolved quick-action button: pure data for the
// header toolbar organism (no functions, no Gio).
type ToolbarButton struct {
	// ID is the action ID; the host runs it when the button is clicked.
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
	// Icon is the design-system icon name (atoms glyph name).
	Icon     string `json:"icon,omitempty"`
	Shortcut string `json:"shortcut,omitempty"`
	// Tooltip is Label plus the shortcut in parentheses, if any.
	Tooltip string `json:"tooltip,omitempty"`
	// Active draws the button as a pressed toggle (Action.Checked).
	Active   bool `json:"active,omitempty"`
	Disabled bool `json:"disabled,omitempty"`
	// Badge is a short status text (Action.Badge).
	Badge string `json:"badge,omitempty"`
	// Separator divides two groups; all other fields are empty.
	Separator bool `json:"separator,omitempty"`
}

// AddToolbar appends the actions ids as one group to the header toolbar
// (CtxToolbar). The actions themselves are registered with Add; unknown
// IDs are skipped when resolving, so the order of Add and AddToolbar does
// not matter.
func (r *Registry) AddToolbar(ids ...string) {
	items := make([]MenuItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, Item(id))
	}
	r.AppendMenu(CtxToolbar, items...)
}

// RemoveToolbar removes the buttons of ids from the header toolbar (e.g.
// when a module stops), dropping separators that became redundant.
func (r *Registry) RemoveToolbar(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []MenuItem
	for _, it := range r.menus[CtxToolbar] {
		if !it.Separator && slices.Contains(ids, it.Action) {
			continue
		}
		if it.Separator && (len(out) == 0 || out[len(out)-1].Separator) {
			continue
		}
		out = append(out, it)
	}
	if n := len(out); n > 0 && out[n-1].Separator {
		out = out[:n-1]
	}
	r.menus[CtxToolbar] = out
	r.version++
}

// Toolbar resolves the header toolbar buttons in context c (c.Menu is
// set to CtxToolbar): labels, icons, shortcuts, enabled/active states and
// badges. Invisible and unknown actions are left out; submenus are not
// supported in the toolbar and are skipped. keys and goos are as for
// Resolve.
func (r *Registry) Toolbar(c Context, keys *keymap.Keymap, goos string) []ToolbarButton {
	c.Menu = CtxToolbar
	var items []MenuItem
	for _, it := range r.Menu(CtxToolbar) {
		if len(it.Children) == 0 {
			items = append(items, it)
		}
	}
	es := r.ResolveItems(items, c, keys, goos)
	out := make([]ToolbarButton, 0, len(es))
	for _, e := range es {
		if e.Separator {
			out = append(out, ToolbarButton{Separator: true})
			continue
		}
		b := ToolbarButton{
			ID: e.ID, Label: e.Label, Icon: e.Icon, Shortcut: e.Shortcut, Tooltip: e.Label,
			Active: e.Checked, Disabled: e.Disabled,
		}
		if e.Shortcut != "" {
			b.Tooltip += " (" + e.Shortcut + ")"
		}
		if a, ok := r.Get(e.ID); ok && a.Badge != nil {
			b.Badge = a.Badge(c)
		}
		out = append(out, b)
	}
	return out
}
