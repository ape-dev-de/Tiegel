package diff

import (
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// Event is an intent reported by a diff organism. The concrete types below
// are the complete set; switch on them.
type Event interface{ diffEvent() }

// RevertRequested asks to replace the right lines of a change block with
// the left lines (the "»" arrow between the columns). Text is the left
// content of the block. A host that gave the window the shared document
// buffer can apply it with DiffWindow.ApplyRevert (one undoable edit).
type RevertRequested struct {
	Block  int
	Change Block
	Text   string
}

// IgnoreWhitespaceChanged reports the "Ignore whitespace" toggle. The
// window recomputes its blocks locally right away; hosts may request a
// fresh git.fileDiff with ignoreWhitespace and pass it to SetBlocks.
type IgnoreWhitespaceChanged struct{ Ignore bool }

// ViewModeChanged reports the switch between side-by-side and unified
// (for persisting the preference).
type ViewModeChanged struct{ Unified bool }

// RightEdited reports an edit of the right (editable) side. The window
// refreshes its blocks itself after a short pause.
type RightEdited struct{ Edit buffer.Edit }

// OpenFileRequested asks to open the right side's file in the editor at
// the 0-based line.
type OpenFileRequested struct {
	Path string
	Line int
}

// HistoryDiffRequested asks for the diff of a commit of a FileHistoryView
// against its first parent: git.fileDiff with path Path, basePath
// ParentPath, base Base (the parent hash, or "empty" for a root commit)
// and target Commit. Answer with FileHistoryView.Diff.SetContent.
type HistoryDiffRequested struct {
	Commit     string
	Base       string
	Path       string
	ParentPath string
}

// HistoryPageRequested asks for the next page of a FileHistoryView
// (git.fileHistory with skip Loaded). Answer with
// FileHistoryView.AppendHistory.
type HistoryPageRequested struct {
	Path   string
	Loaded int
}

// CommitClicked reports a click on the annotation of a line in a
// BlameGutter.
type CommitClicked struct {
	Hash string
	// Line is the 0-based document line that was clicked.
	Line int
}

func (RevertRequested) diffEvent()         {}
func (IgnoreWhitespaceChanged) diffEvent() {}
func (ViewModeChanged) diffEvent()         {}
func (RightEdited) diffEvent()             {}
func (OpenFileRequested) diffEvent()       {}
func (HistoryDiffRequested) diffEvent()    {}
func (HistoryPageRequested) diffEvent()    {}
func (CommitClicked) diffEvent()           {}

// Keymap actions of the diff window.
const (
	ActionNextChange keymap.Action = "diff.nextChange"
	ActionPrevChange keymap.Action = "diff.prevChange"
)

// Shortcuts returns the default key bindings of the diff window: F7 next
// difference, Shift+F7 previous difference (the same on every OS). The
// window handles these keys itself; hosts use the table for menus and
// tooltips.
func Shortcuts() []keymap.Binding {
	return []keymap.Binding{
		{Chord: keymap.MustParse("F7"), Action: ActionNextChange},
		{Chord: keymap.MustParse("Shift+F7"), Action: ActionPrevChange},
	}
}

// queue collects events between Update calls.
type queue struct{ events []Event }

func (q *queue) emit(e Event) { q.events = append(q.events, e) }

func (q *queue) drain() []Event {
	ev := q.events
	q.events = nil
	return ev
}
