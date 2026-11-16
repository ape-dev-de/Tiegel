// Package actions is the UI-independent model of user commands and menus.
//
// An Action is a named command ("file.rename", "edit.copy") with a label,
// an icon name, default shortcuts and optional state functions (enabled,
// checked, visible) evaluated against a Context. Menus are trees of
// MenuItems that refer to actions by ID. The same Registry feeds every
// place that shows commands: context menus (one menu per ContextID), the
// main menu and "Find Action", so labels, icons, shortcuts and enabled
// states never disagree.
//
// The package is pure data (no Gio); the GUI resolves a menu with
// Registry.Resolve into Entries and hands them to the ContextMenu molecule
// (internal/ui/molecules.MenuItemsFromEntries). Executing an action is up
// to the host: it maps the chosen ID to a handler (usually an API call).
//
// Action IDs share the namespace of keymap.Action, so a shortcut the user
// rebinds in the keymap shows up in menus automatically (see
// Registry.ShortcutText).
package actions

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// ContextID names a place that has a context menu.
type ContextID string

// Context menus of the workbench.
const (
	CtxEditor       ContextID = "editor"       // editor text area
	CtxEditorGutter ContextID = "editorGutter" // line numbers / breakpoints
	CtxProjectTree  ContextID = "projectTree"  // project file tree
	CtxTab          ContextID = "tab"          // editor tab
	CtxToolWindow   ContextID = "toolWindow"   // tool window header/stripe
	CtxGitChanges   ContextID = "gitChanges"   // commit panel change list
	CtxGitLog       ContextID = "gitLog"       // git log rows
	// CtxMain is the root of the main menu; its items are submenus
	// (File, Edit, View, ...).
	CtxMain ContextID = "main"
)

// Contexts lists the context menus (without CtxMain).
var Contexts = []ContextID{CtxEditor, CtxEditorGutter, CtxProjectTree, CtxTab, CtxToolWindow, CtxGitChanges, CtxGitLog}

// Context is the situation an action is evaluated in: where the menu was
// opened and what is selected there. Fields that do not apply stay zero.
type Context struct {
	// Menu is where the action is shown.
	Menu ContextID
	// Paths are the selected files or directories (project relative,
	// slash separated): tree selection, the tab's file, the editor's file.
	Paths []string
	// Dirs marks which of Paths are directories (same length or nil).
	Dirs []bool
	// Language of the editor or tab document ("php", ...).
	Language string
	// Line is the 1-based line (editor caret or gutter line); 0 if none.
	Line int
	// Selection reports a non-empty editor selection.
	Selection bool
	// Modified reports unsaved changes of the editor or tab document.
	Modified bool
	// Values carries host-specific facts (e.g. "git" -> "true",
	// "commit" -> hash) for actions of modules.
	Values map[string]string
}

// Single reports whether exactly one path is selected.
func (c Context) Single() bool { return len(c.Paths) == 1 }

// IsDir reports whether Paths[i] is a directory.
func (c Context) IsDir(i int) bool { return i < len(c.Dirs) && c.Dirs[i] }

// Value returns Values[key].
func (c Context) Value(key string) string { return c.Values[key] }

// CheckKind selects how a checked action is marked.
type CheckKind uint8

// Check kinds.
const (
	// CheckNone: the action is a plain command.
	CheckNone CheckKind = iota
	// CheckBox: an on/off toggle shown with a check mark.
	CheckBox
	// CheckRadio: one of a group of exclusive options, shown with a dot.
	CheckRadio
)

// Shortcut holds the default chords of an action, in keymap.ParseChord
// syntax ("Cmd+Shift+O"); empty means none.
type Shortcut struct {
	Mac string
	PC  string
}

// For returns the chord for goos ("darwin" uses Mac).
func (s Shortcut) For(goos string) string {
	if goos == "darwin" {
		return s.Mac
	}
	return s.PC
}

// Action is one user command.
type Action struct {
	// ID is unique, "<area>.<verb>", e.g. "file.rename". It is also the
	// keymap action name used to look up rebound shortcuts.
	ID string
	// Label is the menu text, e.g. "Rename…".
	Label string
	// Icon names an icon of the design system (atoms glyph name, e.g.
	// "delete"); empty for none.
	Icon string
	// Shortcut are the default chords.
	Shortcut Shortcut
	// Keywords are extra search terms for Find Action.
	Keywords []string
	// Check makes the action a toggle or radio option; Checked reports
	// its state.
	Check   CheckKind
	Checked func(Context) bool
	// Enabled reports whether the action can run; nil means always.
	Enabled func(Context) bool
	// Visible reports whether the action appears in menus at all; nil
	// means always. Find Action lists only visible actions.
	Visible func(Context) bool
}

func (a *Action) enabled(c Context) bool { return a.Enabled == nil || a.Enabled(c) }
func (a *Action) visible(c Context) bool { return a.Visible == nil || a.Visible(c) }
func (a *Action) checked(c Context) bool { return a.Checked != nil && a.Checked(c) }

// MenuItem is a node of a menu tree: an action reference, a submenu or a
// separator.
type MenuItem struct {
	// Action is the ID of the action (leaf items).
	Action string
	// Label overrides the action's label; for submenus it is the title.
	Label string
	// Icon overrides the action's icon (or sets the submenu icon).
	Icon string
	// Children make the item a submenu.
	Children []MenuItem
	// Separator draws a divider line; all other fields are ignored.
	Separator bool
}

// Item references an action.
func Item(id string) MenuItem { return MenuItem{Action: id} }

// Sub is a submenu.
func Sub(label string, children ...MenuItem) MenuItem {
	return MenuItem{Label: label, Children: children}
}

// Separator is a divider.
func Separator() MenuItem { return MenuItem{Separator: true} }

// Entry is a resolved menu item: everything a menu widget needs to draw
// it, without functions. Separators have Separator set and nothing else.
type Entry struct {
	// ID is the action ID ("" for submenus and separators).
	ID       string `json:"id,omitempty"`
	Label    string `json:"label,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Shortcut string `json:"shortcut,omitempty"`
	// Chord is the parsed shortcut (zero if none).
	Chord     keymap.Chord `json:"-"`
	Disabled  bool         `json:"disabled,omitempty"`
	Check     CheckKind    `json:"check,omitempty"`
	Checked   bool         `json:"checked,omitempty"`
	Separator bool         `json:"separator,omitempty"`
	Children  []Entry      `json:"children,omitempty"`
}

// Registry holds actions and menus. It is safe for concurrent use; the
// state functions of actions are called without the lock held.
type Registry struct {
	mu      sync.RWMutex
	actions map[string]*Action
	menus   map[ContextID][]MenuItem
	version uint64
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{actions: map[string]*Action{}, menus: map[ContextID][]MenuItem{}}
}

// Add registers actions. An empty ID, a missing label or a duplicate ID
// is an error; nothing is added then.
func (r *Registry) Add(as ...Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	for _, a := range as {
		switch {
		case a.ID == "" || a.Label == "":
			return fmt.Errorf("actions: action %q needs an ID and a label", a.ID)
		case r.actions[a.ID] != nil || seen[a.ID]:
			return fmt.Errorf("actions: duplicate action %q", a.ID)
		}
		for _, s := range []string{a.Shortcut.Mac, a.Shortcut.PC} {
			if s == "" {
				continue
			}
			if _, err := keymap.ParseChord(s); err != nil {
				return fmt.Errorf("actions: action %q: %w", a.ID, err)
			}
		}
		seen[a.ID] = true
	}
	for i := range as {
		a := as[i]
		r.actions[a.ID] = &a
	}
	r.version++
	return nil
}

// Remove unregisters actions (e.g. when a module stops). Menu items that
// refer to them disappear from resolved menus.
func (r *Registry) Remove(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		delete(r.actions, id)
	}
	r.version++
}

// Get returns a copy of the action with id.
func (r *Registry) Get(id string) (Action, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[id]
	if !ok {
		return Action{}, false
	}
	return *a, true
}

// All returns all actions sorted by ID.
func (r *Registry) All() []Action {
	r.mu.RLock()
	out := make([]Action, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, *a)
	}
	r.mu.RUnlock()
	slices.SortFunc(out, func(a, b Action) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Version changes with every modification, so hosts can cache derived
// data (key filters, menu layouts).
func (r *Registry) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// SetMenu replaces the menu of ctx.
func (r *Registry) SetMenu(ctx ContextID, items ...MenuItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.menus[ctx] = slices.Clone(items)
	r.version++
}

// AppendMenu adds items at the end of the menu of ctx, preceded by a
// separator if the menu is not empty (modules add their group this way).
func (r *Registry) AppendMenu(ctx ContextID, items ...MenuItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.menus[ctx]
	if len(m) > 0 && len(items) > 0 {
		m = append(m, Separator())
	}
	r.menus[ctx] = append(m, items...)
	r.version++
}

// Menu returns the (unresolved) menu of ctx.
func (r *Registry) Menu(ctx ContextID) []MenuItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.menus[ctx])
}

// Resolve turns the menu of c.Menu into entries for display: actions are
// looked up (unknown or invisible ones are dropped), labels, icons and
// shortcut texts filled in, enabled and checked states evaluated.
// Submenus without visible children disappear; separators are collapsed so
// none is leading, trailing or doubled. keys (optional) supplies rebound
// shortcuts; goos selects the default chord and the display style.
func (r *Registry) Resolve(c Context, keys *keymap.Keymap, goos string) []Entry {
	return r.ResolveItems(r.Menu(c.Menu), c, keys, goos)
}

// ResolveItems is Resolve for an explicit item list.
func (r *Registry) ResolveItems(items []MenuItem, c Context, keys *keymap.Keymap, goos string) []Entry {
	var out []Entry
	for _, it := range items {
		switch {
		case it.Separator:
			out = append(out, Entry{Separator: true})
		case len(it.Children) > 0:
			kids := r.ResolveItems(it.Children, c, keys, goos)
			if len(kids) == 0 {
				continue
			}
			out = append(out, Entry{Label: it.Label, Icon: it.Icon, Children: kids})
		default:
			a, ok := r.Get(it.Action)
			if !ok || !a.visible(c) {
				continue
			}
			e := Entry{
				ID: a.ID, Label: cmp.Or(it.Label, a.Label), Icon: cmp.Or(it.Icon, a.Icon),
				Disabled: !a.enabled(c), Check: a.Check, Checked: a.checked(c),
			}
			if ch, ok := r.chord(a, keys, goos); ok {
				e.Chord, e.Shortcut = ch, FormatChord(ch, goos)
			}
			out = append(out, e)
		}
	}
	return tidySeparators(out)
}

// tidySeparators removes leading, trailing and repeated separators.
func tidySeparators(in []Entry) []Entry {
	out := in[:0]
	for _, e := range in {
		if e.Separator && (len(out) == 0 || out[len(out)-1].Separator) {
			continue
		}
		out = append(out, e)
	}
	if n := len(out); n > 0 && out[n-1].Separator {
		out = out[:n-1]
	}
	return out
}

// chord returns the shortcut of a: the first chord bound to its ID in keys,
// else its default chord for goos.
func (r *Registry) chord(a Action, keys *keymap.Keymap, goos string) (keymap.Chord, bool) {
	if keys != nil {
		if cs := keys.ChordsFor(keymap.Action(a.ID)); len(cs) > 0 {
			return cs[0], true
		}
	}
	s := a.Shortcut.For(goos)
	if s == "" {
		return keymap.Chord{}, false
	}
	c, err := keymap.ParseChord(s)
	return c, err == nil
}

// ShortcutText returns the display text of the shortcut of action id
// (see Resolve), or "".
func (r *Registry) ShortcutText(id string, keys *keymap.Keymap, goos string) string {
	a, ok := r.Get(id)
	if !ok {
		return ""
	}
	if c, ok := r.chord(a, keys, goos); ok {
		return FormatChord(c, goos)
	}
	return ""
}

// Bindings returns the default key bindings of all actions for goos, for
// building the window keymap.
func (r *Registry) Bindings(goos string) []keymap.Binding {
	var out []keymap.Binding
	for _, a := range r.All() {
		if s := a.Shortcut.For(goos); s != "" {
			if c, err := keymap.ParseChord(s); err == nil {
				out = append(out, keymap.Binding{Chord: c, Action: keymap.Action(a.ID)})
			}
		}
	}
	return out
}

// Match is a result of Find.
type Match struct {
	Entry
	// Path is the menu path where the action lives, e.g. "File › New",
	// if it is part of the main menu.
	Path  string `json:"path,omitempty"`
	Score int    `json:"score"`
}

// Find searches visible actions for Find Action: every word of query must
// occur in the label, ID, keywords or main-menu path (case-insensitive).
// Results are ranked (label prefix, word start, substring), then sorted by
// label. An empty query lists all visible actions. Disabled actions are
// included with Disabled set.
func (r *Registry) Find(query string, c Context, keys *keymap.Keymap, goos string) []Match {
	words := strings.Fields(strings.ToLower(query))
	paths := map[string]string{}
	var walk func(items []MenuItem, prefix string)
	walk = func(items []MenuItem, prefix string) {
		for _, it := range items {
			if len(it.Children) > 0 {
				p := it.Label
				if prefix != "" {
					p = prefix + " › " + it.Label
				}
				walk(it.Children, p)
			} else if it.Action != "" && paths[it.Action] == "" {
				paths[it.Action] = prefix
			}
		}
	}
	walk(r.Menu(CtxMain), "")
	var out []Match
	for _, a := range r.All() {
		if !a.visible(c) {
			continue
		}
		label := strings.ToLower(a.Label)
		hay := strings.ToLower(strings.Join(append([]string{a.Label, a.ID, paths[a.ID]}, a.Keywords...), " "))
		score := 0
		ok := true
		for _, w := range words {
			switch {
			case strings.HasPrefix(label, w):
				score += 30
			case wordStart(label, w):
				score += 20
			case strings.Contains(label, w):
				score += 10
			case strings.Contains(hay, w):
				score++
			default:
				ok = false
			}
		}
		if !ok {
			continue
		}
		m := Match{Entry: Entry{ID: a.ID, Label: a.Label, Icon: a.Icon, Disabled: !a.enabled(c), Check: a.Check, Checked: a.checked(c)}, Path: paths[a.ID], Score: score}
		if ch, ok := r.chord(a, keys, goos); ok {
			m.Chord, m.Shortcut = ch, FormatChord(ch, goos)
		}
		out = append(out, m)
	}
	slices.SortStableFunc(out, func(a, b Match) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
	return out
}

// wordStart reports whether w starts a word of s (after a space or
// punctuation).
func wordStart(s, w string) bool {
	for i := 1; i < len(s); i++ {
		if c := s[i-1]; (c == ' ' || c == '-' || c == '.' || c == '/' || c == '(') && strings.HasPrefix(s[i:], w) {
			return true
		}
	}
	return false
}

// FormatChord formats a chord for menus and tooltips in text form, which
// the bundled UI fonts can render: on macOS ("darwin") in keymap order with
// "Cmd" first ("Cmd+Shift+O", arrows as ←→↑↓), elsewhere
// "Ctrl+Alt+Shift+Key" (the Cmd modifier is shown as "Meta").
func FormatChord(c keymap.Chord, goos string) string {
	if c.Key == "" {
		return ""
	}
	order := pcOrder
	if goos == "darwin" {
		order = macOrder
	}
	var parts []string
	for _, m := range order {
		if c.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, textKey(c.Key)), "+")
}

type modName struct {
	mod  keymap.Mods
	name string
}

var (
	pcOrder  = []modName{{keymap.Ctrl, "Ctrl"}, {keymap.Alt, "Alt"}, {keymap.Shift, "Shift"}, {keymap.Cmd, "Meta"}}
	macOrder = []modName{{keymap.Cmd, "Cmd"}, {keymap.Ctrl, "Ctrl"}, {keymap.Alt, "Alt"}, {keymap.Shift, "Shift"}}
	symOrder = []modName{{keymap.Ctrl, "⌃"}, {keymap.Alt, "⌥"}, {keymap.Shift, "⇧"}, {keymap.Cmd, "⌘"}}
)

// FormatChordSymbols formats a chord with the macOS menu symbols in
// Apple's order (⌃⌥⇧⌘ then the key, e.g. "⇧⌘O"). The bundled Go fonts lack
// most of these symbols; use it only where a system font renders the text
// (e.g. a native menu bar).
func FormatChordSymbols(c keymap.Chord) string {
	if c.Key == "" {
		return ""
	}
	var b strings.Builder
	for _, m := range symOrder {
		if c.Mods&m.mod != 0 {
			b.WriteString(m.name)
		}
	}
	b.WriteString(macKey(c.Key))
	return b.String()
}

func macKey(k string) string {
	switch k {
	case keymap.KeyLeft:
		return "←"
	case keymap.KeyRight:
		return "→"
	case keymap.KeyUp:
		return "↑"
	case keymap.KeyDown:
		return "↓"
	case keymap.KeyEnter:
		return "↩"
	case keymap.KeyBackspace:
		return "⌫"
	case keymap.KeyDelete:
		return "⌦"
	case keymap.KeyEscape:
		return "⎋"
	case keymap.KeyTab:
		return "⇥"
	case keymap.KeySpace:
		return "Space"
	case keymap.KeyPageUp:
		return "⇞"
	case keymap.KeyPageDown:
		return "⇟"
	case keymap.KeyHome:
		return "↖"
	case keymap.KeyEnd:
		return "↘"
	}
	return k
}

func textKey(k string) string {
	switch k {
	case keymap.KeyLeft:
		return "←"
	case keymap.KeyRight:
		return "→"
	case keymap.KeyUp:
		return "↑"
	case keymap.KeyDown:
		return "↓"
	case keymap.KeyDelete:
		return "Del"
	case keymap.KeyEscape:
		return "Esc"
	case keymap.KeyPageUp:
		return "PgUp"
	case keymap.KeyPageDown:
		return "PgDn"
	}
	return k
}
