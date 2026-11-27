package gui

import (
	"fmt"
	"image"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Menus are built from the registry: every action with a Menu path shows
// up there (D111). On macOS the menu bar is native (package native,
// shortcuts displayed only, the window keeps handling keys); elsewhere a
// small in-app menu bar opens each menu as a list. Find Action
// (Cmd+Shift+A) searches all actions by title.

// MenuOrder is the order of the top-level menus.
var MenuOrder = []string{"File", "Edit", "View", "Navigate", "Code", "Refactor", "Git", "Run", "Tools", "Window", "Help"}

// menuNode is a menu or submenu built from the registry.
type menuNode struct {
	title    string
	actions  []Action // leaves (in order)
	sections []string // section of each leaf
	subs     []*menuNode
	subAt    []int // leaf index before which each submenu is placed
}

// buildMenus groups the registered actions by their Menu path.
func (r *Registry) buildMenus() []*menuNode {
	top := map[string]*menuNode{}
	var order []*menuNode
	get := func(parent *menuNode, title string) *menuNode {
		for _, s := range parent.subs {
			if s.title == title {
				return s
			}
		}
		n := &menuNode{title: title}
		parent.subs = append(parent.subs, n)
		parent.subAt = append(parent.subAt, len(parent.actions))
		return n
	}
	for _, name := range MenuOrder {
		n := &menuNode{title: name}
		top[name] = n
		order = append(order, n)
	}
	for _, a := range r.Actions() {
		if a.Menu == "" {
			continue
		}
		parts := strings.Split(a.Menu, "/")
		n := top[parts[0]]
		if n == nil {
			n = &menuNode{title: parts[0]}
			top[parts[0]] = n
			order = append(order, n)
		}
		for _, p := range parts[1:] {
			n = get(n, p)
		}
		n.actions = append(n.actions, a)
		n.sections = append(n.sections, a.Section)
	}
	out := order[:0]
	for _, n := range order {
		if len(n.actions) > 0 || len(n.subs) > 0 {
			out = append(out, n)
		}
	}
	return out
}

// nativeMenus converts the resolved main menu (action model, D160) for
// the native menu bar; item IDs are action IDs.
func nativeMenus(r *Registry, goos string, c actions.Context) []native.Menu {
	var conv func(items []MenuItem) []native.MenuItem
	conv = func(items []MenuItem) []native.MenuItem {
		out := make([]native.MenuItem, 0, len(items))
		for _, it := range items {
			out = append(out, native.MenuItem{ID: string(it.Action), Title: it.Title, Shortcut: it.Shortcut,
				Separator: it.Separator, Disabled: it.Disabled, Checked: it.Checked, Children: conv(it.Children)})
		}
		return out
	}
	var out []native.Menu
	for _, m := range r.MainMenus(c, goos) {
		out = append(out, native.Menu{Title: m.Title, Items: conv(m.Items)})
	}
	return out
}

// runAction runs a registered action from a menu or Find Action: editor
// actions get the active editor's document lock like their shortcuts.
func (v *View) runAction(id keymap.Action) {
	a, ok := v.Registry.Action(id)
	if !ok || a.Run == nil {
		return
	}
	if a.Scope == ScopeEditor {
		s := v.activeSession()
		if s == nil {
			return
		}
		s.doc.Lock()
		defer s.doc.Unlock()
	}
	v.safeRun(a)
}

// safeRun runs action a. A failing action (of the shell or a module)
// reports itself in the status bar and on stderr and leaves the window
// running. While it runs, the freeze watchdog knows it as the UI
// goroutine's work (D510).
func (v *View) safeRun(a Action) {
	leave := v.monitor.Enter("action " + orTitle(a.Title, string(a.ID)) + " (" + string(a.ID) + ")")
	defer leave()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "tiegel: action %s panicked: %v\n%s", a.ID, r, debug.Stack())
			v.ctl.Status.Notice = fmt.Sprintf("%s failed: %v", orTitle(a.Title, string(a.ID)), r)
		}
	}()
	a.Run(v)
}

// ---- generic menu presentation ---------------------------------------------

// MenuPresenter shows a menu at a window position and runs the chosen
// item. The View's default presents menus as a chooser list; a context
// menu component (AP10.4) can replace it.
type MenuPresenter func(v *View, title string, items []MenuItem, pos image.Point)

// ShowMenu presents items (a context menu or a menu of the in-app menu
// bar) with the View's MenuPresenter.
func (v *View) ShowMenu(title string, items []MenuItem) {
	v.ShowMenuAt(title, items, image.Point{})
}

// ShowMenuAt is ShowMenu at a window position.
func (v *View) ShowMenuAt(title string, items []MenuItem, pos image.Point) {
	if len(items) == 0 {
		return
	}
	if v.MenuPresenter != nil {
		v.MenuPresenter(v, title, items, pos)
		return
	}
	v.chooserMenu(title, items)
}

// ShowContextMenu collects the registered entries for ctx and shows
// them (nothing happens without entries).
func (v *View) ShowContextMenu(ctx ContextID, t ContextTarget, builtin []MenuItem) {
	items := append([]MenuItem(nil), builtin...)
	if more := v.Registry.ContextMenu(v, ctx, t); len(more) > 0 {
		if len(items) > 0 {
			items = append(items, MenuItem{Separator: true})
		}
		items = append(items, more...)
	}
	v.ShowMenuAt("", items, t.Pos)
}

// chooserMenu is the default presenter: a filterable list of the
// (flattened) items.
func (v *View) chooserMenu(title string, items []MenuItem) {
	type entry struct {
		label string
		item  MenuItem
	}
	var flat []entry
	var walk func(prefix string, items []MenuItem)
	walk = func(prefix string, items []MenuItem) {
		for _, it := range items {
			switch {
			case it.Separator:
			case len(it.Children) > 0:
				walk(prefix+it.Title+" › ", it.Children)
			default:
				flat = append(flat, entry{prefix + it.Title, it})
			}
		}
	}
	walk("", items)
	filter := func(q string) ([]palette.Item, []int) {
		var out []palette.Item
		var idx []int
		labels := make([]string, len(flat))
		for i, e := range flat {
			labels[i] = e.label
		}
		for _, m := range controller.FilterFuzzy(q, labels, 200) {
			e := flat[m.Index]
			detail := keyLabel(v.goos, e.item.Shortcut)
			if detail == "" && e.item.Action != "" {
				detail = v.chordLabel(e.item.Action)
			}
			g := atoms.GlyphChevronRight
			switch {
			case e.item.Checked:
				g = atoms.GlyphCheck
			case e.item.Disabled:
				g = atoms.GlyphRemove
			}
			out = append(out, palette.Item{Label: e.label, Detail: detail, Glyph: g})
			idx = append(idx, m.Index)
		}
		return out, idx
	}
	items0, idx := filter("")
	if title == "" {
		title = "Actions"
	}
	v.OpenChooser(ChooserSpec{
		Title: title, Hint: "Filter", Items: items0,
		OnQuery: func(q string) []palette.Item {
			var its []palette.Item
			its, idx = filter(q)
			return its
		},
		OnChoose: func(i int, _ string) {
			if i < 0 || i >= len(idx) {
				return
			}
			it := flat[idx[i]].item
			if it.Disabled {
				return
			}
			switch {
			case it.Action != "":
				v.runAction(it.Action)
			case it.Run != nil:
				it.Run()
			}
		},
	})
}

// ---- in-app menu bar (platforms without a native menu bar) -----------------

// menuBar is a row of menu titles; a click shows the menu as a list.
type menuBar struct {
	btns map[string]*atoms.Button
}

func (m *menuBar) layout(gtx layout.Context, th *tokens.Theme, v *View) layout.Dimensions {
	if m.btns == nil {
		m.btns = map[string]*atoms.Button{}
	}
	menus := v.Registry.MainMenus(v.actionContext(actions.CtxMain), v.goos)
	h := gtx.Dp(th.Size.ControlH)
	w := gtx.Constraints.Max.X
	bg := gtx
	bg.Constraints = layout.Exact(image.Pt(w, h))
	atoms.Surface{Kind: atoms.SurfaceAlt}.Paint(bg, th)
	children := make([]layout.FlexChild, 0, len(menus))
	for _, n := range menus {
		b := m.btns[n.Title]
		if b == nil {
			b = &atoms.Button{Text: n.Title, Kind: atoms.Ghost}
			m.btns[n.Title] = b
		}
		if b.Clicked(gtx) {
			v.ShowMenu(n.Title, n.Items)
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }))
	}
	layout.Flex{Alignment: layout.Middle}.Layout(bg, children...)
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// ---- native menu bar ---------------------------------------------------------

// nativeMenu installs the native menu bar of the focused window (one per
// process; it follows the window with the focus).
var nativeMenu struct {
	sync.Mutex
	owner   *View
	version uint64
	state   string
}

// syncNativeMenu installs the menu bar when this window has the focus and
// its actions changed. route delivers a chosen item to its window.
func (v *View) syncNativeMenu(focused bool, route func(id string)) {
	if !native.MenuSupported() || !focused {
		return
	}
	nativeMenu.Lock()
	defer nativeMenu.Unlock()
	ver := v.Registry.Version()<<1 | boolBit(v.ctl.Project.Open)
	// Enabled states follow the active editor (language, selection).
	ac := v.actionContext(actions.CtxMain)
	state := fmt.Sprint(ac.Language, ac.Selection, ac.Modified, len(ac.Paths), ac.Values["git"])
	if nativeMenu.owner == v && nativeMenu.version == ver && nativeMenu.state == state {
		return
	}
	nativeMenu.owner, nativeMenu.version, nativeMenu.state = v, ver, state
	native.SetMenus(nativeMenus(v.Registry, v.goos, ac), route)
}

func boolBit(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// runMenuID runs a native menu item (app menu IDs or action IDs).
func (v *View) runMenuID(id string) {
	switch id {
	case "app.about":
		v.OpenPopup(&Popup{Title: "Tiegel", Detail: fmt.Sprintf("PHP IDE · %s", v.welcome.Version), Centered: true,
			Buttons: []*PopupButton{{Text: "OK", Kind: atoms.Primary}}})
	case "app.quit":
		if v.Quit != nil {
			v.rund.confirmClose(v.Quit)
		}
	default:
		if g, ok := strings.CutPrefix(id, gesturePrefix); ok {
			v.Gesture(g)
			return
		}
		v.runAction(keymap.Action(id))
	}
}

// orTitle returns title, or id when it is empty.
func orTitle(title, id string) string {
	if title != "" {
		return strings.TrimSuffix(title, "…")
	}
	return id
}
