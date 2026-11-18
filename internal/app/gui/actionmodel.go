package gui

import (
	"fmt"
	"image"
	"runtime"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// The action model (internal/actions, D130) is the single source of menus
// (D160): every Registry action is mirrored into it, the standard actions
// (actions.Standard) are there from the start. Labels, icons, shortcut
// texts and enabled/checked states come from the model; the Registry
// keeps the handlers (Action.Run, the "handler map" ID → function) and the
// key bindings. Resolved entries without a handler are shown disabled, so
// a menu can list actions of features that are not installed (yet).
//
// Shortcuts are bound exactly once: by the window (Action.Mac/PC, scope
// global or editor) or by the editor keymap (Edit menu entries, KeyHint);
// menus only display them (the native menu bar never triggers them, D111).

// SetGOOS selects the platform of the model's labels (call before adding
// actions).
func (r *Registry) SetGOOS(goos string) {
	if r.model == nil {
		r.modelGOOS = goos
	}
}

// Model returns the action model. It starts with actions.Standard.
func (r *Registry) Model() *actions.Registry {
	if r.model == nil {
		goos := r.modelGOOS
		if goos == "" {
			goos = runtime.GOOS
		}
		r.model = actions.Standard(goos)
	}
	return r.model
}

// standardIDs are the IDs of actions.StandardActions.
var standardIDs = func() map[string]actions.Action {
	m := map[string]actions.Action{}
	for _, a := range actions.StandardActions() {
		m[a.ID] = a
	}
	return m
}()

// mirror copies a into the model. Standard actions keep their label,
// icon and state functions; the window's chord (if any) becomes the
// displayed shortcut so menus show what the keys do.
func (r *Registry) mirror(a Action) {
	m := r.Model()
	id := string(a.ID)
	na, had := m.Get(id)
	if !had {
		na = actions.Action{ID: id, Label: a.Title}
	}
	if na.Label == "" {
		na.Label = a.Title
	}
	if na.Label == "" {
		na.Label = id
	}
	switch {
	case r.Scheme().Has(a.ID):
		na.Shortcut = actions.Shortcut{Mac: r.schemeFor("darwin").ChordText(a.ID), PC: r.schemeFor("linux").ChordText(a.ID)}
	case a.Mac != "" || a.PC != "":
		na.Shortcut = actions.Shortcut{Mac: a.Mac, PC: a.PC}
	case a.KeyHint != "":
		na.Shortcut = actions.Shortcut{Mac: a.KeyHint, PC: a.KeyHint}
	case !had:
		na.Shortcut = editorChords(keymap.Action(id))
	}
	m.Remove(id)
	if err := m.Add(na); err != nil {
		na.Shortcut = actions.Shortcut{}
		_ = m.Add(na)
	}
}

// unmirror removes the action from the model; standard actions stay (as
// entries without a handler).
func (r *Registry) unmirror(id keymap.Action) {
	m := r.Model()
	m.Remove(string(id))
	if a, ok := standardIDs[string(id)]; ok {
		_ = m.Add(a)
	}
}

// editorChords returns the editor keymap's chords of a (display only).
func editorChords(a keymap.Action) actions.Shortcut {
	var s actions.Shortcut
	if cs := keymap.ForOS("darwin").ChordsFor(a); len(cs) > 0 {
		s.Mac = cs[0].String()
	}
	if cs := keymap.ForOS("windows").ChordsFor(a); len(cs) > 0 {
		s.PC = cs[0].String()
	}
	return s
}

// HasHandler reports whether action id can run.
func (r *Registry) HasHandler(id string) bool {
	a, ok := r.actions[keymap.Action(id)]
	return ok && a.Run != nil
}

// syncMain rebuilds the main menu of the model from the menu places of the
// registered actions (Action.Menu, Section).
func (r *Registry) syncMain() {
	m := r.Model()
	if r.mainSeen == r.version && len(m.Menu(actions.CtxMain)) > 0 {
		return
	}
	r.mainSeen = r.version
	var conv func(n *menuNode) []actions.MenuItem
	conv = func(n *menuNode) []actions.MenuItem {
		var out []actions.MenuItem
		sub := 0
		addSubs := func(at int) {
			for sub < len(n.subs) && n.subAt[sub] == at {
				out = append(out, actions.Sub(n.subs[sub].title, conv(n.subs[sub])...))
				sub++
			}
		}
		for i, a := range n.actions {
			addSubs(i)
			if i > 0 && n.sections[i] != n.sections[i-1] {
				out = append(out, actions.Separator())
			}
			out = append(out, actions.Item(string(a.ID)))
		}
		addSubs(len(n.actions))
		return out
	}
	var top []actions.MenuItem
	for _, n := range r.buildMenus() {
		top = append(top, actions.Sub(n.title, conv(n)...))
	}
	m.SetMenu(actions.CtxMain, top...)
}

// resolve resolves model menu items for c into menu items; actions
// without a handler are disabled.
func (r *Registry) resolve(items []actions.MenuItem, c actions.Context, goos string) []MenuItem {
	return r.entries(r.Model().ResolveItems(items, c, nil, goos))
}

func (r *Registry) entries(es []actions.Entry) []MenuItem {
	out := make([]MenuItem, 0, len(es))
	for _, e := range es {
		it := MenuItem{Title: e.Label, Action: keymap.Action(e.ID), Shortcut: e.Shortcut, Disabled: e.Disabled,
			Checked: e.Checked, Separator: e.Separator, Icon: e.Icon, Children: r.entries(e.Children)}
		if e.ID != "" && !r.HasHandler(e.ID) {
			it.Disabled = true
		}
		out = append(out, it)
	}
	return out
}

// mainMenu is a resolved top-level menu.
type mainMenu struct {
	Title string
	Items []MenuItem
}

// MainMenus resolves the main menu for c.
func (r *Registry) MainMenus(c actions.Context, goos string) []mainMenu {
	r.syncMain()
	c.Menu = actions.CtxMain
	var out []mainMenu
	for _, it := range r.resolve(r.Model().Menu(actions.CtxMain), c, goos) {
		if len(it.Children) > 0 {
			out = append(out, mainMenu{Title: it.Title, Items: it.Children})
		}
	}
	return out
}

// ContextEntries resolves the model menu of ctx.
func (r *Registry) ContextEntries(ctx actions.ContextID, c actions.Context, goos string) []MenuItem {
	c.Menu = ctx
	return r.resolve(r.Model().Menu(ctx), c, goos)
}

// actionContext describes the active editor for the action states.
func (v *View) actionContext(menu actions.ContextID) actions.Context {
	s := v.activeSession()
	if s == nil {
		return v.baseContext(menu)
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	return v.sessionContext(s, menu)
}

// baseContext is the context without an editor.
func (v *View) baseContext(menu actions.ContextID) actions.Context {
	c := actions.Context{Menu: menu, Values: map[string]string{}}
	if v.ctl.Vcs != nil && v.ctl.Vcs.Available {
		c.Values["git"] = "true"
	}
	if v.ctl.Status.LSPRunning {
		c.Values[actions.ValueLSP] = "true"
	}
	if _, ok := v.ctl.QA.Formatter(); ok {
		c.Values[actions.ValueQAFormat] = "true"
	}
	for _, t := range v.ctl.QA.Tools {
		if t.Analyzer {
			c.Values[actions.ValueQAInspect] = "true"
		}
	}
	return c
}

// sessionContext is actionContext for editor s (document locked).
func (v *View) sessionContext(s *editorSession, menu actions.ContextID) actions.Context {
	c := v.baseContext(menu)
	c.Paths = []string{v.ctl.Rel(s.file)}
	c.Language = s.tab.Language
	c.Modified = s.doc.Buffer().Modified()
	c.Line, c.Selection = s.caretLineSel()
	return c
}

// caretLineSel returns the 1-based caret line and whether the selection
// is non-empty (document locked).
func (s *editorSession) caretLineSel() (int, bool) {
	p := s.ed.Cursor().Primary()
	return s.doc.Buffer().OffsetToPos(p.Head).Line + 1, !p.Empty()
}

// ShowActionMenu shows the model menu of ctx (resolved for ac) followed by
// the entries registered with AddContextMenu.
func (v *View) ShowActionMenu(ctx ContextID, t ContextTarget, ac actions.Context) {
	v.ShowContextMenu(ctx, t, v.Registry.ContextEntries(actions.ContextID(ctx), ac, v.goos))
}

// ---- context menu presenter ---------------------------------------------------

// menuPresenter shows menus with the ContextMenu molecule (D135).
type menuPresenter struct {
	menu   molecules.ContextMenu
	items  map[string]MenuItem
	source []MenuItem
}

// present is the View.MenuPresenter: a popup menu at pos (window
// coordinates; the zero point means at the last pointer press).
func (p *menuPresenter) present(v *View, _ string, items []MenuItem, pos image.Point) {
	if !v.ctl.Project.Open {
		// The welcome screen has no frame handlers to draw the menu.
		v.chooserMenu("", items)
		return
	}
	p.items = map[string]MenuItem{}
	p.source = items
	p.menu.Items = p.convert(items, "")
	if pos != (image.Point{}) {
		p.menu.ShowAt(pos)
	} else {
		p.menu.ShowAtPointer()
	}
	v.Invalidate()
}

func (p *menuPresenter) convert(items []MenuItem, prefix string) []molecules.MenuItem {
	out := make([]molecules.MenuItem, 0, len(items))
	for i, it := range items {
		id := fmt.Sprintf("%s%d", prefix, i)
		mi := molecules.MenuItem{ID: id, Label: it.Title, Shortcut: it.Shortcut, Disabled: it.Disabled, Separator: it.Separator,
			Glyph: atoms.GlyphNamed(it.Icon)}
		if it.Checked {
			mi.Check, mi.Checked = molecules.MenuCheckBox, true
		}
		if len(it.Children) > 0 {
			mi.Children = p.convert(it.Children, id+".")
		}
		p.items[id] = it
		out = append(out, mi)
	}
	return out
}

// frame processes a choice and draws the open menu (on top, deferred).
func (p *menuPresenter) frame(v *View, gtx layout.Context) {
	if id, ok := p.menu.Update(gtx); ok {
		it, found := p.items[id]
		v.focusEditor = true
		if found && !it.Disabled {
			switch {
			case it.Action != "":
				v.runAction(it.Action)
			case it.Run != nil:
				it.Run()
			}
		}
	}
	p.menu.Layout(gtx, v.th)
}

// Opened reports whether a context menu is shown.
func (p *menuPresenter) Opened() bool { return p.menu.Opened() }

// choose runs the leaf whose label is label, or else the first leaf whose
// menu path ("New › PHP Class…") contains label (tests).
func (p *menuPresenter) choose(v *View, label string) bool {
	if !p.menu.Opened() {
		return false
	}
	var exact, partial *MenuItem
	var walk func(prefix string, items []MenuItem)
	walk = func(prefix string, items []MenuItem) {
		for i := range items {
			it := &items[i]
			switch {
			case it.Separator:
			case len(it.Children) > 0:
				walk(prefix+it.Title+" › ", it.Children)
			case it.Title == label && exact == nil:
				exact = it
			case strings.Contains(prefix+it.Title, label) && partial == nil:
				partial = it
			}
		}
	}
	walk("", p.source)
	it := exact
	if it == nil {
		it = partial
	}
	if it == nil {
		return false
	}
	p.menu.Close()
	v.focusEditor = true
	if it.Disabled {
		return true
	}
	switch {
	case it.Action != "":
		v.runAction(it.Action)
	case it.Run != nil:
		it.Run()
	}
	return true
}

// ---- Find Action ----------------------------------------------------------------

// openFindAction searches all actions of the model (Cmd+Shift+A); actions
// without a handler are left out.
func (v *View) openFindAction() {
	r := v.Registry
	r.syncMain()
	ac := v.actionContext("")
	find := func(q string) []actions.Match {
		var out []actions.Match
		for _, m := range r.Model().Find(q, ac, nil, v.goos) {
			if r.HasHandler(m.ID) {
				out = append(out, m)
			}
		}
		if len(out) > 200 {
			out = out[:200]
		}
		return out
	}
	var cur []actions.Match
	items := func(q string) []palette.Item {
		cur = find(q)
		out := make([]palette.Item, 0, len(cur))
		for _, m := range cur {
			label := m.Label
			if m.Path != "" {
				label += "  (" + m.Path + ")"
			}
			detail := m.Shortcut
			if m.Disabled {
				detail = strings.TrimSpace("disabled " + detail)
			}
			g := atoms.GlyphNamed(m.Icon)
			if g == nil {
				g = atoms.GlyphChevronRight
			}
			out = append(out, palette.Item{Label: label, Detail: detail, Glyph: g})
		}
		return out
	}
	v.OpenChooser(ChooserSpec{
		Title: "Find Action", Hint: "Action name", Items: items(""),
		OnQuery: items,
		OnChoose: func(i int, _ string) {
			if i < 0 || i >= len(cur) || cur[i].Disabled {
				return
			}
			v.runAction(keymap.Action(cur[i].ID))
		},
	})
}
