package gui

import (
	"runtime"
	"slices"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// Window actions (in addition to the editor actions of keymap).
const (
	ActQuickOpen      keymap.Action = "file.quickOpen"
	ActRecentFiles    keymap.Action = "file.recent"
	ActCloseTab       keymap.Action = "tab.close"
	ActNextTab        keymap.Action = "tab.next"
	ActPrevTab        keymap.Action = "tab.previous"
	ActSettings       keymap.Action = "app.settings"
	ActBack           keymap.Action = "navigate.back"
	ActForward        keymap.Action = "navigate.forward"
	ActProjectView    keymap.Action = "view.project"
	ActProblemsView   keymap.Action = "view.problems"
	ActComplete       keymap.Action = "editor.complete"
	ActDefinition     keymap.Action = "navigate.definition"
	ActImplementation keymap.Action = "navigate.implementation"
	ActSaveAll        keymap.Action = "file.saveAll"

	ActOpenProject     keymap.Action = "project.open"
	ActRecentProjects  keymap.Action = "project.recent"
	ActCloseProject    keymap.Action = "project.close"
	ActNewWindow       keymap.Action = "window.new"
	ActFindAction      keymap.Action = "app.findAction"
	ActMaximizeEditor  keymap.Action = "view.maximizeEditor"
	ActFocusEditor     keymap.Action = "view.focusEditor"
	ActHideToolWindow  keymap.Action = "view.hideToolWindow"
	ActZoomIn          keymap.Action = "view.zoomIn"
	ActZoomOut         keymap.Action = "view.zoomOut"
	ActZoomReset       keymap.Action = "view.zoomReset"
	ActSplitRight      keymap.Action = "window.splitRight"
	ActSplitDown       keymap.Action = "window.splitDown"
	ActUnsplit         keymap.Action = "window.unsplit"
	ActUnsplitAll      keymap.Action = "window.unsplitAll"
	ActNextGroup       keymap.Action = "window.nextGroup"
	ActPrevGroup       keymap.Action = "window.previousGroup"
	ActMoveToOtherSide keymap.Action = "window.moveTabToOtherGroup"
	ActAbout           keymap.Action = "app.about"
)

// Project actions of a window with several projects (D477).
const (
	ActSwitchProject    keymap.Action = "project.switch"
	ActNextProject      keymap.Action = "project.next"
	ActPrevProject      keymap.Action = "project.previous"
	ActHibernateProject keymap.Action = "project.hibernate"
)

// editorMenuAction is an Edit menu entry running an editor keymap action.
// Its ID is the keymap action: the editor binds the keys, the window does
// not; menus show the keymap's chord.
func editorMenuAction(a keymap.Action, title, section string) Action {
	return Action{ID: a, Title: title, Menu: "Edit", Section: section,
		Run: func(v *View) { v.pendingEdit = a; v.focusEditor = true }}
}

// defaultActions is the global shortcut table (data; Mac and PC chords)
// with the places in the menu bar.
func defaultActions() []Action {
	return []Action{
		// File
		{ID: ActOpenProject, Title: "Open…", Menu: "File", Section: "open", Run: (*View).openProjectDialog},
		{ID: ActRecentProjects, Title: "Recent Projects…", Menu: "File", Section: "open", Run: (*View).openRecentProjects},
		{ID: ActNewWindow, Title: "New Window", Menu: "File", Section: "open", Run: func(v *View) { v.openWindow("") }},
		{ID: ActCloseProject, Title: "Close Project", Menu: "File", Section: "open", Run: (*View).closeProject},
		{ID: ActSwitchProject, Title: "Switch Project…", Menu: "File", Section: "projects", Run: (*View).openProjectSwitcher},
		{ID: ActNextProject, Title: "Next Project", Mac: "Cmd+Alt+Shift+Right", PC: "Ctrl+Alt+Shift+Right", Menu: "File", Section: "projects", Run: func(v *View) { v.hdr.Sessions.Cycle(1) }},
		{ID: ActPrevProject, Title: "Previous Project", Mac: "Cmd+Alt+Shift+Left", PC: "Ctrl+Alt+Shift+Left", Menu: "File", Section: "projects", Run: func(v *View) { v.hdr.Sessions.Cycle(-1) }},
		{ID: ActHibernateProject, Title: "Hibernate Project", Menu: "File", Section: "projects", Run: (*View).hibernateProject},
		{ID: ActSaveAll, Title: "Save All", Mac: "Cmd+Alt+S", PC: "Ctrl+Alt+Shift+S", Menu: "File", Section: "save", Run: func(v *View) { v.ctl.Autosave("focusLost") }},
		{ID: ActSettings, Title: "Settings…", Mac: "Cmd+,", PC: "Ctrl+Alt+S", Menu: "File", Section: "settings", Run: (*View).openSettings},
		{ID: ActImportSettings, Title: "Import Settings…", Menu: "File", Section: "settings", Run: (*View).openImportSettings},
		{ID: ActProjectSetup, Title: "Project Setup…", Menu: "File", Section: "settings", Run: (*View).openProjectSetup},
		// Edit (editor keymap actions; their keys belong to the editor)
		editorMenuAction(keymap.Undo, "Undo", "undo"),
		editorMenuAction(keymap.Redo, "Redo", "undo"),
		editorMenuAction(keymap.Cut, "Cut", "clipboard"),
		editorMenuAction(keymap.Copy, "Copy", "clipboard"),
		editorMenuAction(keymap.Paste, "Paste", "clipboard"),
		editorMenuAction(keymap.SelectAll, "Select All", "clipboard"),
		editorMenuAction(keymap.Find, "Find…", "find"),
		editorMenuAction(keymap.Replace, "Replace…", "find"),
		editorMenuAction(keymap.ToggleComment, "Comment with Line Comment", "code"),
		editorMenuAction(keymap.DuplicateLines, "Duplicate Line or Selection", "code"),
		// View
		{ID: ActFindAction, Title: "Find Action…", Mac: "Cmd+Shift+A", PC: "Ctrl+Shift+A", Menu: "View", Section: "find", Run: (*View).openFindAction},
		{ID: ActMaximizeEditor, Title: "Maximize Editor (Hide All Tool Windows)", Mac: "Cmd+Shift+F12", PC: "Ctrl+Shift+F12", Menu: "View", Section: "tools", Run: func(v *View) { v.ctl.Tools.ToggleMaximize(); v.focusEditor = true }},
		{ID: ActFocusEditor, Title: "Focus Editor", KeyHint: "Escape", Menu: "View", Section: "tools", Run: func(v *View) { v.focusEditor = true }},
		{ID: ActHideToolWindow, Title: "Hide Active Tool Window", KeyHint: "Shift+Escape", Menu: "View", Section: "tools", Run: (*View).hideActiveTool},
		{ID: ActZoomIn, Title: "Increase Editor Font Size", Mac: "Cmd+=", PC: "Ctrl+=", Menu: "View", Section: "zoom", Run: func(v *View) { v.zoom(1) }},
		{ID: ActZoomOut, Title: "Decrease Editor Font Size", Mac: "Cmd+-", PC: "Ctrl+-", Menu: "View", Section: "zoom", Run: func(v *View) { v.zoom(-1) }},
		{ID: ActZoomReset, Title: "Reset Editor Font Size", Menu: "View", Section: "zoom", Run: func(v *View) { v.zoom(0) }},
		{ID: keymap.ViewSoftWrap, Title: "Soft-Wrap", Menu: "View", Section: "editor", Run: (*View).toggleSoftWrap},
		{ID: keymap.ViewShowWhitespace, Title: "Show Whitespaces", Menu: "View", Section: "editor", Run: (*View).toggleWhitespace},
		// Navigate
		{ID: ActQuickOpen, Title: "Go to File…", Mac: "Cmd+Shift+O", PC: "Ctrl+Shift+N", Menu: "Navigate", Section: "goto", Run: (*View).openQuickOpen},
		{ID: ActRecentFiles, Title: "Recent Files…", Mac: "Cmd+E", PC: "Ctrl+E", Menu: "Navigate", Section: "goto", Run: (*View).openRecent},
		{ID: ActBack, Title: "Back", Mac: "Cmd+[", PC: "Ctrl+Alt+Left", Menu: "Navigate", Section: "history", Run: func(v *View) { v.ctl.NavigateBack(v.currentLocation()) }},
		{ID: ActForward, Title: "Forward", Mac: "Cmd+]", PC: "Ctrl+Alt+Right", Menu: "Navigate", Section: "history", Run: func(v *View) { v.ctl.NavigateForward(v.currentLocation()) }},
		{ID: ActDefinition, Title: "Go to Definition", Mac: "Cmd+B", PC: "Ctrl+B", Scope: ScopeEditor, Menu: "Navigate", Section: "symbol", Run: func(v *View) { v.goToDefinition("lsp.definition") }},
		{ID: ActImplementation, Title: "Go to Implementation", Mac: "Cmd+Alt+B", PC: "Ctrl+Alt+B", Scope: ScopeEditor, Menu: "Navigate", Section: "symbol", Run: func(v *View) { v.goToDefinition("lsp.implementation") }},
		{ID: ActComplete, Title: "Complete", Mac: "Ctrl+Space", PC: "Ctrl+Space", Scope: ScopeEditor, Run: (*View).completeAtCaret},
		// Window
		{ID: ActSplitRight, Title: "Split Right", Menu: "Window", Section: "split", Run: func(v *View) { v.ctl.Split(false); v.focusEditor = true }},
		{ID: ActSplitDown, Title: "Split Down", Menu: "Window", Section: "split", Run: func(v *View) { v.ctl.Split(true); v.focusEditor = true }},
		{ID: ActUnsplit, Title: "Unsplit", Menu: "Window", Section: "split", Run: func(v *View) { v.ctl.Unsplit(); v.focusEditor = true }},
		{ID: ActUnsplitAll, Title: "Unsplit All", Menu: "Window", Section: "split", Run: func(v *View) { v.ctl.UnsplitAll(); v.focusEditor = true }},
		{ID: ActNextGroup, Title: "Next Editor Group", Mac: "Cmd+Alt+]", PC: "Ctrl+Alt+]", Menu: "Window", Section: "group", Run: func(v *View) { v.ctl.CycleGroup(1); v.focusEditor = true }},
		{ID: ActPrevGroup, Title: "Previous Editor Group", Mac: "Cmd+Alt+[", PC: "Ctrl+Alt+[", Menu: "Window", Section: "group", Run: func(v *View) { v.ctl.CycleGroup(-1); v.focusEditor = true }},
		{ID: ActMoveToOtherSide, Title: "Move Tab to Next Group", Menu: "Window", Section: "group", Run: func(v *View) {
			if n := len(v.ctl.Groups()); n > 1 {
				v.ctl.MoveTabToGroup((v.ctl.GroupIndex(v.ctl.ActiveGroup()) + 1) % n)
			}
		}},
		{ID: ActNextTab, Title: "Next Tab", Mac: "Ctrl+Tab", PC: "Ctrl+Tab", Menu: "Window", Section: "tabs", Run: func(v *View) { v.ctl.CycleTab(1) }},
		{ID: ActPrevTab, Title: "Previous Tab", Mac: "Ctrl+Shift+Tab", PC: "Ctrl+Shift+Tab", Menu: "Window", Section: "tabs", Run: func(v *View) { v.ctl.CycleTab(-1) }},
		{ID: ActCloseTab, Title: "Close Tab", Mac: "Cmd+W", PC: "Ctrl+F4", Menu: "Window", Section: "tabs", Run: func(v *View) { v.ctl.CloseTab(v.ctl.Active) }},
		// Help
		{ID: ActAbout, Title: "About Tiegel", Menu: "Help", Run: func(v *View) { v.runMenuID("app.about") }},
	}
}

// shortcuts maps the registered actions to key filters per scope.
type shortcuts struct {
	version uint64
	focus   event.Tag
	keys    *keymap.Keymap
	filters []event.Filter
	// keep (optional) limits the shortcuts to the chords it accepts.
	keep func(keymap.Chord) bool
}

// ---- keymap scheme (M13) -------------------------------------------------------

// Scheme returns the resolved keymap (catalog, preset, user overrides);
// before SetScheme it is the OS default.
func (r *Registry) Scheme() *keymap.Scheme {
	if r.scheme == nil {
		r.scheme = keymap.NewScheme(keymap.PresetFor(r.goos()), nil)
		r.editorKeys = r.scheme.EditorKeymap()
	}
	return r.scheme
}

// SetScheme replaces the keymap (live, e.g. after keymap.set): window
// shortcuts, editor keys and the shortcuts shown in menus follow.
func (r *Registry) SetScheme(s *keymap.Scheme) {
	r.scheme, r.editorKeys = s, s.EditorKeymap()
	for _, id := range r.order {
		r.mirror(r.actions[id])
	}
	r.version++
}

// EditorKeymap returns the bindings of the editor widget.
func (r *Registry) EditorKeymap() *keymap.Keymap {
	r.Scheme()
	return r.editorKeys
}

func (r *Registry) goos() string {
	if r.modelGOOS != "" {
		return r.modelGOOS
	}
	return runtime.GOOS
}

// schemeFor returns the window's scheme for its own OS and the default
// scheme of another OS's preset (shortcut texts for other platforms).
func (r *Registry) schemeFor(goos string) *keymap.Scheme {
	if goos == r.goos() {
		return r.Scheme()
	}
	return keymap.NewScheme(keymap.PresetFor(goos), nil)
}

// chordsOf returns the chords bound to a: the scheme's for catalog
// actions, otherwise the action's own Mac or PC chord (by preset).
func (r *Registry) chordsOf(a Action) []keymap.Chord {
	s := r.Scheme()
	if s.Has(a.ID) {
		return s.Chords(a.ID)
	}
	chord := a.PC
	if s.Preset() == keymap.PresetMac {
		chord = a.Mac
	}
	if c, err := keymap.ParseChord(chord); err == nil {
		return []keymap.Chord{c}
	}
	return nil
}

// Gesture runs the action bound to a pointer or gesture input
// (keymap.KeyMouseBack, KeySwipeForward, KeyDoubleShift, ...) in the
// focus context; it reports whether one ran.
func (v *View) Gesture(name string) bool {
	if v.keymapRecording() {
		// The keymap editor records it as a shortcut instead.
		return v.settings.Keymap.Gesture(name)
	}
	ctx := keymap.CtxGlobal
	if s := v.activeSession(); s != nil && s.ed.Focused() {
		ctx = keymap.CtxEditor
	}
	id, ok := v.Registry.Scheme().Lookup(ctx, keymap.Chord{Key: name})
	if !ok {
		return false
	}
	if _, registered := v.Registry.Action(id); !registered {
		return false
	}
	v.RunAction(id)
	return true
}

// gioKey translates a keymap key name into a Gio key name.
func gioKey(k string) key.Name {
	switch k {
	case keymap.KeyLeft:
		return key.NameLeftArrow
	case keymap.KeyRight:
		return key.NameRightArrow
	case keymap.KeyUp:
		return key.NameUpArrow
	case keymap.KeyDown:
		return key.NameDownArrow
	case keymap.KeyEnter:
		return key.NameReturn
	case keymap.KeyTab:
		return key.NameTab
	case keymap.KeyEscape:
		return key.NameEscape
	case keymap.KeySpace:
		return key.NameSpace
	case keymap.KeyBackspace:
		return key.NameDeleteBackward
	case keymap.KeyDelete:
		return key.NameDeleteForward
	case keymap.KeyHome:
		return key.NameHome
	case keymap.KeyEnd:
		return key.NameEnd
	case keymap.KeyPageUp:
		return key.NamePageUp
	case keymap.KeyPageDown:
		return key.NamePageDown
	}
	return key.Name(k)
}

func gioMods(m keymap.Mods) key.Modifiers {
	var g key.Modifiers
	if m&keymap.Shift != 0 {
		g |= key.ModShift
	}
	if m&keymap.Ctrl != 0 {
		g |= key.ModCtrl
	}
	if m&keymap.Alt != 0 {
		g |= key.ModAlt
	}
	if m&keymap.Cmd != 0 {
		g |= key.ModCommand
	}
	return g
}

// build rebuilds the keymap and filters of one scope from the scheme.
func (s *shortcuts) build(r *Registry, scope ActionScope, focus event.Tag) {
	if s.keys != nil && s.version == r.version && s.focus == focus {
		return
	}
	s.version, s.focus = r.version, focus
	s.keys = keymap.New("window")
	s.filters = s.filters[:0]
	// User bindings first: they shadow a default of the same chord (D247).
	sch := r.Scheme()
	acts := r.Actions()
	slices.SortStableFunc(acts, func(x, y Action) int {
		return boolRank(sch.IsUser(y.ID)) - boolRank(sch.IsUser(x.ID))
	})
	for _, a := range acts {
		if a.Scope != scope {
			continue
		}
		for _, c := range r.chordsOf(a) {
			if keymap.IsGesture(c) {
				continue // pointer and gestures: View.Gesture
			}
			if s.keep != nil && !s.keep(c) {
				continue
			}
			if _, taken := s.keys.Lookup(c); taken {
				continue
			}
			s.keys.Bind(c, a.ID)
			s.filters = append(s.filters, key.Filter{Focus: focus, Name: gioKey(c.Key), Required: gioMods(c.Mods)})
		}
	}
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// process runs the actions whose shortcuts were pressed. It must run
// before the focused widgets process their input so the window wins.
func (s *shortcuts) process(gtx layout.Context, v *View) {
	if len(s.filters) == 0 {
		return
	}
	for {
		ev, ok := gtx.Event(s.filters...)
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		if id, ok := s.keys.Lookup(editorview.ChordOf(e)); ok {
			v.RunAction(id)
		}
	}
}

// ChordText returns the shortcut of an action for the OS, for tooltips
// (the first keyboard chord of the scheme for catalog actions).
func (r *Registry) ChordText(id keymap.Action, goos string) string {
	a, ok := r.actions[id]
	if !ok {
		return ""
	}
	if s := r.schemeFor(goos); s.Has(id) {
		return s.ChordText(id)
	}
	chord := a.PC
	if goos == "darwin" {
		chord = a.Mac
	}
	if chord != "" {
		return chord
	}
	return a.KeyHint
}
