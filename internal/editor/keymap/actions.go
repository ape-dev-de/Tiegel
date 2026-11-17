package keymap

// Editor actions. The editor widget executes all of them itself except
// the ones listed in External, which it reports to its host as events.
const (
	// Cursor movement; the Select* variants extend the selection.
	MoveLeft          Action = "cursor.left"
	MoveRight         Action = "cursor.right"
	MoveWordLeft      Action = "cursor.wordLeft"
	MoveWordRight     Action = "cursor.wordRight"
	MoveUp            Action = "cursor.up"
	MoveDown          Action = "cursor.down"
	MoveLineStart     Action = "cursor.lineStart"
	MoveLineEnd       Action = "cursor.lineEnd"
	MovePageUp        Action = "cursor.pageUp"
	MovePageDown      Action = "cursor.pageDown"
	MoveDocStart      Action = "cursor.docStart"
	MoveDocEnd        Action = "cursor.docEnd"
	SelectLeft        Action = "cursor.selectLeft"
	SelectRight       Action = "cursor.selectRight"
	SelectWordLeft    Action = "cursor.selectWordLeft"
	SelectWordRight   Action = "cursor.selectWordRight"
	SelectUp          Action = "cursor.selectUp"
	SelectDown        Action = "cursor.selectDown"
	SelectLineStart   Action = "cursor.selectLineStart"
	SelectLineEnd     Action = "cursor.selectLineEnd"
	SelectPageUp      Action = "cursor.selectPageUp"
	SelectPageDown    Action = "cursor.selectPageDown"
	SelectDocStart    Action = "cursor.selectDocStart"
	SelectDocEnd      Action = "cursor.selectDocEnd"
	AddCursorAbove    Action = "cursor.addAbove"
	AddCursorBelow    Action = "cursor.addBelow"
	CollapseSelection Action = "cursor.collapse"

	// Selection.
	SelectAll            Action = "selection.all"
	ExpandSelection      Action = "selection.expand"
	ShrinkSelection      Action = "selection.shrink"
	AddNextOccurrence    Action = "selection.addNextOccurrence"
	SelectAllOccurrences Action = "selection.allOccurrences"

	// Editing.
	Newline            Action = "edit.newline"
	DeleteBackward     Action = "edit.deleteBackward"
	DeleteForward      Action = "edit.deleteForward"
	DeleteWordBackward Action = "edit.deleteWordBackward"
	DeleteWordForward  Action = "edit.deleteWordForward"
	DeleteLine         Action = "edit.deleteLine"
	Indent             Action = "edit.indent"
	Outdent            Action = "edit.outdent"
	DuplicateLines     Action = "edit.duplicateLines"
	MoveLinesUp        Action = "edit.moveLinesUp"
	MoveLinesDown      Action = "edit.moveLinesDown"
	ToggleComment      Action = "edit.toggleComment"
	Undo               Action = "edit.undo"
	Redo               Action = "edit.redo"
	Copy               Action = "edit.copy"
	Cut                Action = "edit.cut"
	Paste              Action = "edit.paste"
	JoinLines          Action = "edit.joinLines"
	ToggleCase         Action = "edit.toggleCase"

	// Folding.
	FoldCollapse    Action = "fold.collapse"
	FoldExpand      Action = "fold.expand"
	FoldCollapseAll Action = "fold.collapseAll"
	FoldExpandAll   Action = "fold.expandAll"

	// Search inside the editor (uses the query set on the widget).
	FindNext     Action = "search.next"
	FindPrevious Action = "search.previous"

	// Host actions: reported to the host instead of executed.
	Find     Action = "search.find"
	Replace  Action = "search.replace"
	GotoLine Action = "navigate.gotoLine"
	Save     Action = "file.save"
	// PasteFromHistory asks the host to offer the clipboard history.
	PasteFromHistory Action = "edit.pasteFromHistory"
)

// External lists the actions the editor widget does not execute itself
// but reports to its host (search bar, go-to-line dialog, saving, the
// clipboard history).
var External = []Action{Find, Replace, GotoLine, Save, PasteFromHistory}

// IsExternal reports whether a is one of External.
func IsExternal(a Action) bool {
	for _, e := range External {
		if e == a {
			return true
		}
	}
	return false
}
