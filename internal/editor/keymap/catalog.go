package keymap

import "slices"

// Context tells where a binding is active.
type Context string

// Binding contexts. Global bindings work regardless of the focus; editor
// bindings while an editor has the keyboard focus; tool window bindings
// while a tool window (project tree, commit panel, …) has it.
const (
	CtxGlobal     Context = "global"
	CtxEditor     Context = "editor"
	CtxToolWindow Context = "toolWindow"
)

// Contexts lists the binding contexts.
var Contexts = []Context{CtxGlobal, CtxEditor, CtxToolWindow}

// Valid reports whether c is one of Contexts.
func (c Context) Valid() bool { return slices.Contains(Contexts, c) }

// Overlaps reports whether bindings of c and o can see the same key
// press: equal contexts, or one of them global.
func (c Context) Overlaps(o Context) bool {
	return c == o || c == CtxGlobal || o == CtxGlobal
}

// Preset selects the default chords of the catalog.
type Preset string

// Presets: the macOS default keymap (Cmd for commands, Option for
// word-wise movement) and the Windows/Linux one (Ctrl, Alt).
const (
	PresetMac Preset = "mac"
	PresetPC  Preset = "pc"
)

// Presets lists the presets.
var Presets = []Preset{PresetMac, PresetPC}

// PresetFor returns the default preset of a GOOS value.
func PresetFor(goos string) Preset {
	if goos == "darwin" || goos == "ios" {
		return PresetMac
	}
	return PresetPC
}

// Valid reports whether p is one of Presets.
func (p Preset) Valid() bool { return slices.Contains(Presets, p) }

// Actions of the window and of the code intelligence that are bound in
// the catalog but not executed by the editor widget. The GUI registers
// handlers under the same IDs.
const (
	NavBack            Action = "navigate.back"
	NavForward         Action = "navigate.forward"
	NavLastEdit        Action = "navigate.lastEditLocation"
	NavRecentLocations Action = "navigate.recentLocations"
	NavFileStructure   Action = "navigate.fileStructure"
	NavNextError       Action = "navigate.nextError"
	NavPreviousError   Action = "navigate.previousError"
	ParameterInfo      Action = "editor.parameterInfo"
	QuickDocumentation Action = "editor.quickDocumentation"
	ViewFind           Action = "view.find"
)

// Def declares a bindable action with its default chords per preset
// (keymap.ParseChord syntax; several chords per action are allowed).
type Def struct {
	ID      Action
	Title   string
	Context Context
	Mac, PC []string
}

// chords returns the default chords of the preset.
func (d Def) chords(p Preset) []string {
	if p == PresetMac {
		return d.Mac
	}
	return d.PC
}

// appDefs are the window, navigation, tool window, VCS and debugger
// actions. Their IDs are the GUI's action IDs.
var appDefs = []Def{
	// File and application.
	{ID: "file.saveAll", Title: "Save All", Context: CtxGlobal, Mac: []string{"Cmd+Alt+S"}, PC: []string{"Ctrl+Alt+Shift+S"}},
	{ID: "app.settings", Title: "Settings…", Context: CtxGlobal, Mac: []string{"Cmd+,"}, PC: []string{"Ctrl+Alt+S"}},
	{ID: "app.findAction", Title: "Find Action…", Context: CtxGlobal, Mac: []string{"Cmd+Shift+A"}, PC: []string{"Ctrl+Shift+A"}},
	{ID: "file.copyPath", Title: "Copy Absolute Path", Context: CtxGlobal, Mac: []string{"Cmd+Shift+C"}, PC: []string{"Ctrl+Shift+C"}},
	{ID: "file.copyRelativePath", Title: "Copy Path from Project Root", Context: CtxGlobal, Mac: []string{"Cmd+Alt+Shift+C"}, PC: []string{"Ctrl+Alt+Shift+C"}},
	// View.
	{ID: "view.maximizeEditor", Title: "Maximize Editor (Hide All Tool Windows)", Context: CtxGlobal, Mac: []string{"Cmd+Shift+F12"}, PC: []string{"Ctrl+Shift+F12"}},
	{ID: "view.zoomIn", Title: "Increase Editor Font Size", Context: CtxGlobal, Mac: []string{"Cmd+="}, PC: []string{"Ctrl+="}},
	{ID: "view.zoomOut", Title: "Decrease Editor Font Size", Context: CtxGlobal, Mac: []string{"Cmd+-"}, PC: []string{"Ctrl+-"}},
	{ID: "view.selectInProject", Title: "Select in Project View", Context: CtxGlobal, Mac: []string{"Cmd+Shift+1"}, PC: []string{"Alt+Shift+1"}},
	// Tool windows: Cmd/Alt + digit like the reference layout (2 Bookmarks,
	// 4 Run and 8 Services follow with their milestones).
	{ID: "view.commit", Title: "Commit", Context: CtxGlobal, Mac: []string{"Cmd+0"}, PC: []string{"Alt+0"}},
	{ID: "view.project", Title: "Project", Context: CtxGlobal, Mac: []string{"Cmd+1"}, PC: []string{"Alt+1"}},
	{ID: ViewFind, Title: "Find", Context: CtxGlobal, Mac: []string{"Cmd+3"}, PC: []string{"Alt+3"}},
	{ID: "view.debug", Title: "Debug", Context: CtxGlobal, Mac: []string{"Cmd+5"}, PC: []string{"Alt+5"}},
	{ID: "view.problems", Title: "Problems", Context: CtxGlobal, Mac: []string{"Cmd+6"}, PC: []string{"Alt+6"}},
	{ID: "view.structure", Title: "Structure", Context: CtxGlobal, Mac: []string{"Cmd+7"}, PC: []string{"Alt+7"}},
	{ID: "view.git", Title: "Git", Context: CtxGlobal, Mac: []string{"Cmd+9"}, PC: []string{"Alt+9"}},
	// Navigation.
	{ID: "file.quickOpen", Title: "Go to File…", Context: CtxGlobal, Mac: []string{"Cmd+Shift+O"}, PC: []string{"Ctrl+Shift+N"}},
	{ID: "file.recent", Title: "Recent Files…", Context: CtxGlobal, Mac: []string{"Cmd+E"}, PC: []string{"Ctrl+E"}},
	{ID: NavRecentLocations, Title: "Recent Locations…", Context: CtxGlobal, Mac: []string{"Cmd+Shift+E"}, PC: []string{"Ctrl+Shift+E"}},
	{ID: NavBack, Title: "Back", Context: CtxGlobal,
		Mac: []string{"Cmd+[", "Cmd+Alt+Left", KeyMouseBack, KeySwipeBack},
		PC:  []string{"Ctrl+Alt+Left", KeyMouseBack, KeySwipeBack}},
	{ID: NavForward, Title: "Forward", Context: CtxGlobal,
		Mac: []string{"Cmd+]", "Cmd+Alt+Right", KeyMouseForward, KeySwipeForward},
		PC:  []string{"Ctrl+Alt+Right", KeyMouseForward, KeySwipeForward}},
	{ID: NavLastEdit, Title: "Last Edit Location", Context: CtxGlobal, Mac: []string{"Cmd+Shift+Backspace"}, PC: []string{"Ctrl+Shift+Backspace"}},
	{ID: "navigate.gotoClass", Title: "Go to Class…", Context: CtxGlobal, Mac: []string{"Cmd+O"}, PC: []string{"Ctrl+N"}},
	{ID: "navigate.gotoSymbol", Title: "Go to Symbol…", Context: CtxGlobal, Mac: []string{"Cmd+Alt+O"}, PC: []string{"Ctrl+Alt+Shift+N"}},
	{ID: "navigate.searchEverywhere", Title: "Search Everywhere", Context: CtxGlobal, Mac: []string{KeyDoubleShift}, PC: []string{KeyDoubleShift}},
	{ID: "find.inPath", Title: "Find in Files…", Context: CtxGlobal, Mac: []string{"Cmd+Shift+F"}, PC: []string{"Ctrl+Shift+F"}},
	{ID: "find.replaceInPath", Title: "Replace in Files…", Context: CtxGlobal, Mac: []string{"Cmd+Shift+R"}, PC: []string{"Ctrl+Shift+R"}},
	// Code intelligence in the editor.
	{ID: "navigate.definition", Title: "Go to Definition", Context: CtxEditor, Mac: []string{"Cmd+B"}, PC: []string{"Ctrl+B"}},
	{ID: "navigate.implementation", Title: "Go to Implementation", Context: CtxEditor, Mac: []string{"Cmd+Alt+B"}, PC: []string{"Ctrl+Alt+B"}},
	{ID: "navigate.super", Title: "Go to Super Method", Context: CtxEditor, Mac: []string{"Cmd+U"}, PC: []string{"Ctrl+U"}},
	{ID: "navigate.typeHierarchy", Title: "Type Hierarchy", Context: CtxEditor, Mac: []string{"Ctrl+H"}, PC: []string{"Ctrl+H"}},
	{ID: "navigate.usages", Title: "Find Usages", Context: CtxEditor, Mac: []string{"Alt+F7"}, PC: []string{"Alt+F7"}},
	{ID: NavFileStructure, Title: "File Structure", Context: CtxEditor, Mac: []string{"Cmd+F12"}, PC: []string{"Ctrl+F12"}},
	{ID: NavNextError, Title: "Next Highlighted Error", Context: CtxEditor, Mac: []string{"F2"}, PC: []string{"F2"}},
	{ID: NavPreviousError, Title: "Previous Highlighted Error", Context: CtxEditor, Mac: []string{"Shift+F2"}, PC: []string{"Shift+F2"}},
	{ID: "refactor.rename", Title: "Rename Symbol…", Context: CtxEditor, Mac: []string{"Shift+F6"}, PC: []string{"Shift+F6"}},
	{ID: "code.reformat", Title: "Reformat Code", Context: CtxEditor, Mac: []string{"Cmd+Alt+L"}, PC: []string{"Ctrl+Alt+L"}},
	{ID: "code.contextActions", Title: "Show Context Actions", Context: CtxEditor, Mac: []string{"Alt+Enter"}, PC: []string{"Alt+Enter"}},
	{ID: "editor.complete", Title: "Complete", Context: CtxEditor, Mac: []string{"Ctrl+Space"}, PC: []string{"Ctrl+Space"}},
	{ID: ParameterInfo, Title: "Parameter Info", Context: CtxEditor, Mac: []string{"Cmd+P"}, PC: []string{"Ctrl+P"}},
	{ID: QuickDocumentation, Title: "Quick Documentation", Context: CtxEditor, Mac: []string{"F1", "Ctrl+J"}, PC: []string{"Ctrl+Q"}},
	// Window.
	{ID: "window.nextGroup", Title: "Next Editor Group", Context: CtxGlobal, Mac: []string{"Cmd+Alt+]"}, PC: []string{"Ctrl+Alt+]"}},
	{ID: "window.previousGroup", Title: "Previous Editor Group", Context: CtxGlobal, Mac: []string{"Cmd+Alt+["}, PC: []string{"Ctrl+Alt+["}},
	{ID: "tab.next", Title: "Next Tab", Context: CtxGlobal, Mac: []string{"Ctrl+Tab"}, PC: []string{"Ctrl+Tab"}},
	{ID: "tab.previous", Title: "Previous Tab", Context: CtxGlobal, Mac: []string{"Ctrl+Shift+Tab"}, PC: []string{"Ctrl+Shift+Tab"}},
	{ID: "tab.close", Title: "Close Tab", Context: CtxGlobal, Mac: []string{"Cmd+W"}, PC: []string{"Ctrl+F4"}},
	// Version control. Show Diff shares Cmd+D with Duplicate Line: it is
	// bound where files are listed, the editor duplicates.
	{ID: "git.commit", Title: "Commit…", Context: CtxGlobal, Mac: []string{"Cmd+K"}, PC: []string{"Ctrl+K"}},
	{ID: "git.push", Title: "Push", Context: CtxGlobal, Mac: []string{"Cmd+Shift+K"}, PC: []string{"Ctrl+Shift+K"}},
	{ID: "git.rollback", Title: "Rollback Changes", Context: CtxGlobal, Mac: []string{"Cmd+Alt+Z"}, PC: []string{"Ctrl+Alt+Z"}},
	{ID: "git.updateProject", Title: "Update Project…", Context: CtxGlobal, Mac: []string{"Cmd+T"}, PC: []string{"Ctrl+T"}},
	{ID: "git.showDiff", Title: "Show Diff", Context: CtxToolWindow, Mac: []string{"Cmd+D"}, PC: []string{"Ctrl+D"}},
	// Debugger.
	{ID: "debug.resume", Title: "Resume", Context: CtxGlobal, Mac: []string{"F9"}, PC: []string{"F9"}},
	{ID: "debug.stepOver", Title: "Step Over", Context: CtxGlobal, Mac: []string{"F8"}, PC: []string{"F8"}},
	{ID: "debug.stepInto", Title: "Step Into", Context: CtxGlobal, Mac: []string{"F7"}, PC: []string{"F7"}},
	{ID: "debug.stepOut", Title: "Step Out", Context: CtxGlobal, Mac: []string{"Shift+F8"}, PC: []string{"Shift+F8"}},
	{ID: "debug.stop", Title: "Stop", Context: CtxGlobal, Mac: []string{"Cmd+F2"}, PC: []string{"Ctrl+F2"}},
	{ID: "debug.toggleBreakpoint", Title: "Toggle Breakpoint", Context: CtxGlobal, Mac: []string{"Cmd+F8"}, PC: []string{"Ctrl+F8"}},
	// Run (AP15.1). Rerun shares Cmd+R with Replace: it is bound in the
	// Run tool window, the editor replaces.
	{ID: "run.run", Title: "Run", Context: CtxGlobal, Mac: []string{"Ctrl+R"}, PC: []string{"Shift+F10"}},
	{ID: "run.runFile", Title: "Run Current File", Context: CtxGlobal, Mac: []string{"Ctrl+Shift+R"}, PC: []string{"Ctrl+Shift+F10"}},
	{ID: "run.rerun", Title: "Rerun", Context: CtxToolWindow, Mac: []string{"Cmd+R"}, PC: []string{"Ctrl+F5"}},
	{ID: "view.run", Title: "Run Tool Window", Context: CtxGlobal, Mac: []string{"Cmd+4"}, PC: []string{"Alt+4"}},
}

// editorTitles are the titles of the editor widget actions.
var editorTitles = map[Action]string{
	MoveLeft: "Left", MoveRight: "Right", MoveWordLeft: "Move Caret to Previous Word", MoveWordRight: "Move Caret to Next Word",
	MoveUp: "Up", MoveDown: "Down", MoveLineStart: "Move Caret to Line Start", MoveLineEnd: "Move Caret to Line End",
	MovePageUp: "Page Up", MovePageDown: "Page Down", MoveDocStart: "Move Caret to Text Start", MoveDocEnd: "Move Caret to Text End",
	SelectLeft: "Left with Selection", SelectRight: "Right with Selection",
	SelectWordLeft: "Move Caret to Previous Word with Selection", SelectWordRight: "Move Caret to Next Word with Selection",
	SelectUp: "Up with Selection", SelectDown: "Down with Selection",
	SelectLineStart: "Move Caret to Line Start with Selection", SelectLineEnd: "Move Caret to Line End with Selection",
	SelectPageUp: "Page Up with Selection", SelectPageDown: "Page Down with Selection",
	SelectDocStart: "Move Caret to Text Start with Selection", SelectDocEnd: "Move Caret to Text End with Selection",
	AddCursorAbove: "Clone Caret Above", AddCursorBelow: "Clone Caret Below", CollapseSelection: "Remove Secondary Carets",
	SelectAll: "Select All", ExpandSelection: "Extend Selection", ShrinkSelection: "Shrink Selection",
	AddNextOccurrence: "Add Selection for Next Occurrence", SelectAllOccurrences: "Select All Occurrences",
	Newline: "Enter", DeleteBackward: "Backspace", DeleteForward: "Delete",
	DeleteWordBackward: "Delete to Word Start", DeleteWordForward: "Delete to Word End", DeleteLine: "Delete Line",
	Indent: "Indent", Outdent: "Unindent", DuplicateLines: "Duplicate Line or Selection",
	MoveLinesUp: "Move Line Up", MoveLinesDown: "Move Line Down", ToggleComment: "Comment with Line Comment",
	Undo: "Undo", Redo: "Redo", Copy: "Copy", Cut: "Cut", Paste: "Paste",
	JoinLines: "Join Lines", ToggleCase: "Toggle Case", PasteFromHistory: "Paste from History…",
	FoldCollapse: "Collapse", FoldExpand: "Expand", FoldCollapseAll: "Collapse All", FoldExpandAll: "Expand All",
	FindNext: "Find Next", FindPrevious: "Find Previous",
	Find: "Find…", Replace: "Replace…", GotoLine: "Go to Line…", Save: "Save",
}

// editorDefs builds the definitions of the editor widget actions from
// the Mac and PC tables (chords in table order per action).
func editorDefs() []Def {
	collect := func(pairs ...[][2]string) map[Action][]string {
		m := map[Action][]string{}
		for _, list := range pairs {
			for _, p := range list {
				a := Action(p[1])
				m[a] = append(m[a], p[0])
			}
		}
		return m
	}
	mac, pc := collect(common, macPairs), collect(common, pcPairs)
	var ids []Action
	for a := range editorTitles {
		ids = append(ids, a)
	}
	slices.Sort(ids)
	out := make([]Def, 0, len(ids))
	for _, a := range ids {
		out = append(out, Def{ID: a, Title: editorTitles[a], Context: CtxEditor, Mac: mac[a], PC: pc[a]})
	}
	return out
}

// IsWidgetAction reports whether the editor widget executes (or reports
// as External) action a itself, as opposed to window actions that only
// happen to be bound in the editor context.
func IsWidgetAction(a Action) bool {
	_, ok := editorTitles[a]
	return ok
}

// Defs returns the catalog: the editor widget actions followed by the
// window actions. The slice is a copy.
func Defs() []Def {
	out := editorDefs()
	for _, d := range appDefs {
		d.Mac, d.PC = slices.Clone(d.Mac), slices.Clone(d.PC)
		out = append(out, d)
	}
	return out
}
