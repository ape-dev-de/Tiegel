package gui

import (
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
)

// defaultActions is the global shortcut table (data; Mac and PC chords).
func defaultActions() []Action {
	return []Action{
		{ID: ActQuickOpen, Title: "Go to File…", Mac: "Cmd+Shift+O", PC: "Ctrl+Shift+N", Run: (*View).openQuickOpen},
		{ID: ActRecentFiles, Title: "Recent Files…", Mac: "Cmd+E", PC: "Ctrl+E", Run: (*View).openRecent},
		{ID: ActCloseTab, Title: "Close Tab", Mac: "Cmd+W", PC: "Ctrl+F4", Run: func(v *View) { v.ctl.CloseTab(v.ctl.Active) }},
		{ID: ActNextTab, Title: "Next Tab", Mac: "Ctrl+Tab", PC: "Ctrl+Tab", Run: func(v *View) { v.ctl.CycleTab(1) }},
		{ID: ActPrevTab, Title: "Previous Tab", Mac: "Ctrl+Shift+Tab", PC: "Ctrl+Shift+Tab", Run: func(v *View) { v.ctl.CycleTab(-1) }},
		{ID: ActSettings, Title: "Settings…", Mac: "Cmd+,", PC: "Ctrl+Alt+S", Run: (*View).openSettings},
		{ID: ActBack, Title: "Back", Mac: "Cmd+[", PC: "Ctrl+Alt+Left", Run: func(v *View) { v.ctl.NavigateBack(v.currentLocation()) }},
		{ID: ActForward, Title: "Forward", Mac: "Cmd+]", PC: "Ctrl+Alt+Right", Run: func(v *View) { v.ctl.NavigateForward(v.currentLocation()) }},
		{ID: ActProjectView, Title: "Project", Mac: "Cmd+1", PC: "Alt+1", Run: func(v *View) { v.bench.SidebarVisible = !v.bench.SidebarVisible }},
		{ID: ActProblemsView, Title: "Problems", Mac: "Cmd+6", PC: "Alt+6", Run: func(v *View) { v.toggleTool(toolProblems) }},
		{ID: ActSaveAll, Title: "Save All", Mac: "Cmd+Alt+S", PC: "Ctrl+Alt+Shift+S", Run: func(v *View) { v.ctl.Autosave("focusLost") }},
		{ID: ActComplete, Title: "Complete", Mac: "Ctrl+Space", PC: "Ctrl+Space", Scope: ScopeEditor, Run: (*View).completeAtCaret},
		{ID: ActDefinition, Title: "Go to Definition", Mac: "Cmd+B", PC: "Ctrl+B", Scope: ScopeEditor, Run: func(v *View) { v.goToDefinition("lsp.definition") }},
		{ID: ActImplementation, Title: "Go to Implementation", Mac: "Cmd+Alt+B", PC: "Ctrl+Alt+B", Scope: ScopeEditor, Run: func(v *View) { v.goToDefinition("lsp.implementation") }},
	}
}

// shortcuts maps the registered actions to key filters per scope.
type shortcuts struct {
	version uint64
	focus   event.Tag
	keys    *keymap.Keymap
	filters []event.Filter
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

// build rebuilds the keymap and filters of one scope.
func (s *shortcuts) build(r *Registry, goos string, scope ActionScope, focus event.Tag) {
	if s.keys != nil && s.version == r.version && s.focus == focus {
		return
	}
	s.version, s.focus = r.version, focus
	s.keys = keymap.New("window")
	s.filters = s.filters[:0]
	for _, a := range r.Actions() {
		if a.Scope != scope {
			continue
		}
		chord := a.PC
		if goos == "darwin" {
			chord = a.Mac
		}
		if chord == "" {
			continue
		}
		c, err := keymap.ParseChord(chord)
		if err != nil {
			continue
		}
		s.keys.Bind(c, a.ID)
		s.filters = append(s.filters, key.Filter{Focus: focus, Name: gioKey(c.Key), Required: gioMods(c.Mods)})
	}
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

// ChordText returns the shortcut of an action for the OS, for tooltips.
func (r *Registry) ChordText(id keymap.Action, goos string) string {
	a, ok := r.actions[id]
	if !ok {
		return ""
	}
	if goos == "darwin" {
		return a.Mac
	}
	return a.PC
}
