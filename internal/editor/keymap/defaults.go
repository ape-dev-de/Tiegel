package keymap

// table builds a keymap from "chord" → action pairs. Invalid chords are
// skipped; the tests check that the built-in tables parse completely.
func table(name string, pairs ...[][2]string) *Keymap {
	k := New(name)
	for _, list := range pairs {
		for _, p := range list {
			if c, err := ParseChord(p[0]); err == nil {
				k.Bind(c, Action(p[1]))
			}
		}
	}
	return k
}

// common lists bindings shared by both platforms (plain navigation keys).
var common = [][2]string{
	{"Left", string(MoveLeft)}, {"Right", string(MoveRight)},
	{"Up", string(MoveUp)}, {"Down", string(MoveDown)},
	{"Shift+Left", string(SelectLeft)}, {"Shift+Right", string(SelectRight)},
	{"Shift+Up", string(SelectUp)}, {"Shift+Down", string(SelectDown)},
	{"Home", string(MoveLineStart)}, {"End", string(MoveLineEnd)},
	{"Shift+Home", string(SelectLineStart)}, {"Shift+End", string(SelectLineEnd)},
	{"PageUp", string(MovePageUp)}, {"PageDown", string(MovePageDown)},
	{"Shift+PageUp", string(SelectPageUp)}, {"Shift+PageDown", string(SelectPageDown)},
	{"Enter", string(Newline)}, {"Shift+Enter", string(Newline)},
	{"Backspace", string(DeleteBackward)}, {"Shift+Backspace", string(DeleteBackward)},
	{"Delete", string(DeleteForward)},
	{"Tab", string(Indent)}, {"Shift+Tab", string(Outdent)},
	{"Escape", string(CollapseSelection)},
}

// Mac returns the default macOS keymap, modelled on common IDE habits:
// Cmd for commands, Alt (Option) for word-wise movement.
func Mac() *Keymap { return table("macOS", common, macPairs) }

var macPairs = [][2]string{
	{"Alt+Left", string(MoveWordLeft)}, {"Alt+Right", string(MoveWordRight)},
	{"Alt+Shift+Left", string(SelectWordLeft)}, {"Alt+Shift+Right", string(SelectWordRight)},
	{"Cmd+Left", string(MoveLineStart)}, {"Cmd+Right", string(MoveLineEnd)},
	{"Cmd+Shift+Left", string(SelectLineStart)}, {"Cmd+Shift+Right", string(SelectLineEnd)},
	{"Cmd+Up", string(MoveDocStart)}, {"Cmd+Down", string(MoveDocEnd)},
	{"Cmd+Home", string(MoveDocStart)}, {"Cmd+End", string(MoveDocEnd)},
	{"Cmd+Shift+Home", string(SelectDocStart)}, {"Cmd+Shift+End", string(SelectDocEnd)},
	{"Cmd+Shift+Up", string(MoveLinesUp)}, {"Cmd+Shift+Down", string(MoveLinesDown)},
	{"Alt+Shift+Up", string(MoveLinesUp)}, {"Alt+Shift+Down", string(MoveLinesDown)},
	{"Alt+Up", string(ExpandSelection)}, {"Alt+Down", string(ShrinkSelection)},
	{"Cmd+Alt+Up", string(AddCursorAbove)}, {"Cmd+Alt+Down", string(AddCursorBelow)},
	{"Alt+Backspace", string(DeleteWordBackward)}, {"Alt+Delete", string(DeleteWordForward)},
	{"Cmd+Backspace", string(DeleteLine)},
	{"Cmd+D", string(DuplicateLines)},
	{"Cmd+/", string(ToggleComment)},
	{"Cmd+Z", string(Undo)}, {"Cmd+Shift+Z", string(Redo)},
	{"Cmd+C", string(Copy)}, {"Cmd+X", string(Cut)}, {"Cmd+V", string(Paste)},
	{"Cmd+A", string(SelectAll)},
	{"Ctrl+G", string(AddNextOccurrence)}, {"Ctrl+Cmd+G", string(SelectAllOccurrences)},
	{"Cmd+F", string(Find)}, {"Cmd+R", string(Replace)},
	{"Cmd+G", string(FindNext)}, {"Cmd+Shift+G", string(FindPrevious)},
	{"Cmd+L", string(GotoLine)}, {"Cmd+S", string(Save)},
	{"Cmd+Alt+-", string(FoldCollapse)}, {"Cmd+Alt+=", string(FoldExpand)},
	{"Cmd+Shift+-", string(FoldCollapseAll)}, {"Cmd+Shift+=", string(FoldExpandAll)},
	{"Ctrl+Shift+J", string(JoinLines)}, {"Cmd+Shift+U", string(ToggleCase)},
	{"Cmd+Shift+V", string(PasteFromHistory)},
}

// PC returns the default keymap for Windows and Linux: Ctrl for commands
// and word-wise movement.
func PC() *Keymap { return table("Windows/Linux", common, pcPairs) }

var pcPairs = [][2]string{
	{"Ctrl+Left", string(MoveWordLeft)}, {"Ctrl+Right", string(MoveWordRight)},
	{"Ctrl+Shift+Left", string(SelectWordLeft)}, {"Ctrl+Shift+Right", string(SelectWordRight)},
	{"Ctrl+Home", string(MoveDocStart)}, {"Ctrl+End", string(MoveDocEnd)},
	{"Ctrl+Shift+Home", string(SelectDocStart)}, {"Ctrl+Shift+End", string(SelectDocEnd)},
	{"Ctrl+Shift+Up", string(MoveLinesUp)}, {"Ctrl+Shift+Down", string(MoveLinesDown)},
	{"Alt+Shift+Up", string(MoveLinesUp)}, {"Alt+Shift+Down", string(MoveLinesDown)},
	{"Ctrl+W", string(ExpandSelection)}, {"Ctrl+Shift+W", string(ShrinkSelection)},
	{"Ctrl+Alt+Up", string(AddCursorAbove)}, {"Ctrl+Alt+Down", string(AddCursorBelow)},
	{"Ctrl+Backspace", string(DeleteWordBackward)}, {"Ctrl+Delete", string(DeleteWordForward)},
	{"Ctrl+Y", string(DeleteLine)},
	{"Ctrl+D", string(DuplicateLines)},
	{"Ctrl+/", string(ToggleComment)},
	{"Ctrl+Z", string(Undo)}, {"Ctrl+Shift+Z", string(Redo)},
	{"Ctrl+C", string(Copy)}, {"Ctrl+X", string(Cut)}, {"Ctrl+V", string(Paste)},
	{"Shift+Delete", string(Cut)},
	{"Ctrl+A", string(SelectAll)},
	{"Alt+J", string(AddNextOccurrence)}, {"Ctrl+Alt+Shift+J", string(SelectAllOccurrences)},
	{"Ctrl+F", string(Find)}, {"Ctrl+R", string(Replace)},
	{"F3", string(FindNext)}, {"Shift+F3", string(FindPrevious)},
	{"Ctrl+G", string(GotoLine)}, {"Ctrl+S", string(Save)},
	{"Ctrl+Alt+-", string(FoldCollapse)}, {"Ctrl+Alt+=", string(FoldExpand)},
	{"Ctrl+Shift+-", string(FoldCollapseAll)}, {"Ctrl+Shift+=", string(FoldExpandAll)},
	{"Ctrl+Shift+J", string(JoinLines)}, {"Ctrl+Shift+U", string(ToggleCase)},
	{"Ctrl+Shift+V", string(PasteFromHistory)},
}

// ForOS returns the default keymap for a GOOS value.
func ForOS(goos string) *Keymap {
	if goos == "darwin" || goos == "ios" {
		return Mac()
	}
	return PC()
}
