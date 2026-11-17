package editorview

import (
	"io"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
)

var movements = map[keymap.Action]struct {
	mv     cursor.Movement
	extend bool
}{
	keymap.MoveLeft: {cursor.Left, false}, keymap.SelectLeft: {cursor.Left, true},
	keymap.MoveRight: {cursor.Right, false}, keymap.SelectRight: {cursor.Right, true},
	keymap.MoveWordLeft: {cursor.WordLeft, false}, keymap.SelectWordLeft: {cursor.WordLeft, true},
	keymap.MoveWordRight: {cursor.WordRight, false}, keymap.SelectWordRight: {cursor.WordRight, true},
	keymap.MoveUp: {cursor.Up, false}, keymap.SelectUp: {cursor.Up, true},
	keymap.MoveDown: {cursor.Down, false}, keymap.SelectDown: {cursor.Down, true},
	keymap.MoveLineStart: {cursor.Home, false}, keymap.SelectLineStart: {cursor.Home, true},
	keymap.MoveLineEnd: {cursor.End, false}, keymap.SelectLineEnd: {cursor.End, true},
	keymap.MovePageUp: {cursor.PageUp, false}, keymap.SelectPageUp: {cursor.PageUp, true},
	keymap.MovePageDown: {cursor.PageDown, false}, keymap.SelectPageDown: {cursor.PageDown, true},
	keymap.MoveDocStart: {cursor.DocStart, false}, keymap.SelectDocStart: {cursor.DocStart, true},
	keymap.MoveDocEnd: {cursor.DocEnd, false}, keymap.SelectDocEnd: {cursor.DocEnd, true},
}

// modifying lists the actions that change the document.
var modifying = map[keymap.Action]bool{
	keymap.Newline: true, keymap.DeleteBackward: true, keymap.DeleteForward: true,
	keymap.DeleteWordBackward: true, keymap.DeleteWordForward: true, keymap.DeleteLine: true,
	keymap.Indent: true, keymap.Outdent: true, keymap.DuplicateLines: true,
	keymap.MoveLinesUp: true, keymap.MoveLinesDown: true, keymap.ToggleComment: true,
	keymap.Undo: true, keymap.Redo: true, keymap.Cut: true, keymap.Paste: true,
	keymap.JoinLines: true, keymap.ToggleCase: true,
}

// Do executes action a as if its shortcut was pressed (for menus and the
// command palette). gtx is needed for clipboard access. External actions
// (keymap.External) are reported as ActionEvent. It returns false for
// unknown actions and for modifications of a read-only editor.
func (e *Editor) Do(gtx layout.Context, a keymap.Action) bool {
	if keymap.IsExternal(a) {
		e.emit(ActionEvent{Action: a})
		return true
	}
	if e.ReadOnly && modifying[a] && a != keymap.Cut {
		return false
	}
	if a != keymap.ExpandSelection && a != keymap.ShrinkSelection {
		e.expand = e.expand[:0]
	}
	if m, ok := movements[a]; ok {
		e.cur.Move(m.mv, m.extend)
		e.RevealCaret()
		return true
	}
	ok := true
	switch a {
	case keymap.AddCursorAbove:
		e.cur.AddCursorVertical(-1)
	case keymap.AddCursorBelow:
		e.cur.AddCursorVertical(1)
	case keymap.CollapseSelection:
		e.cur.CollapseToPrimary()
	case keymap.SelectAll:
		e.cur.SelectAll()
	case keymap.ExpandSelection:
		e.expandSelection()
	case keymap.ShrinkSelection:
		if n := len(e.expand); n > 0 {
			e.cur.SetState(e.expand[n-1])
			e.expand = e.expand[:n-1]
		}
	case keymap.AddNextOccurrence:
		e.cur.AddNextOccurrence()
	case keymap.SelectAllOccurrences:
		e.cur.SelectAllOccurrences()
	case keymap.Newline:
		e.cur.Newline()
	case keymap.DeleteBackward:
		e.cur.DeleteBackward()
	case keymap.DeleteForward:
		e.cur.DeleteForward()
	case keymap.DeleteWordBackward:
		e.cur.DeleteWordBackward()
	case keymap.DeleteWordForward:
		e.cur.DeleteWordForward()
	case keymap.DeleteLine:
		e.cur.DeleteLines()
	case keymap.Indent:
		e.indent()
	case keymap.Outdent:
		e.cur.OutdentLines()
	case keymap.DuplicateLines:
		e.cur.DuplicateLines()
	case keymap.MoveLinesUp:
		e.cur.MoveLines(-1)
	case keymap.MoveLinesDown:
		e.cur.MoveLines(1)
	case keymap.ToggleComment:
		ok = e.cur.ToggleComment(e.LineComment)
	case keymap.JoinLines:
		ok = e.cur.JoinLines()
	case keymap.ToggleCase:
		ok = e.cur.ToggleCase()
	case keymap.Undo:
		ok = e.cur.Undo()
	case keymap.Redo:
		ok = e.cur.Redo()
	case keymap.Copy:
		e.copy(gtx, false)
	case keymap.Cut:
		e.copy(gtx, !e.ReadOnly)
	case keymap.Paste:
		gtx.Execute(clipboard.ReadCmd{Tag: e})
	case keymap.FoldCollapse:
		if r, found := e.foldAtCaret(); found {
			e.folds.Collapse(r.StartLine)
			e.cur.SetCaret(e.buf.LineEnd(r.StartLine))
		}
	case keymap.FoldExpand:
		if r, found := e.foldAtCaret(); found {
			e.folds.Expand(r.StartLine)
		}
	case keymap.FoldCollapseAll:
		e.folds.CollapseAll()
		e.caretsOutOfFolds()
	case keymap.FoldExpandAll:
		e.folds.ExpandAll()
	case keymap.FindNext, keymap.FindPrevious:
		ok = e.findStep(a == keymap.FindNext)
	default:
		return false
	}
	if a != keymap.FoldCollapse && a != keymap.FoldCollapseAll {
		e.revealCarets()
	}
	e.scrollCare = true
	return ok
}

// indent inserts an indentation unit at every caret, or indents the
// touched lines when there is a selection.
func (e *Editor) indent() {
	unit := "\t"
	if e.InsertSpaces {
		unit = strings.Repeat(" ", e.tabWidth())
	}
	for _, sel := range e.cur.Selections() {
		if !sel.Empty() {
			e.cur.IndentLines(unit)
			return
		}
	}
	e.cur.Insert(unit)
}

func (e *Editor) copy(gtx layout.Context, cut bool) {
	text, lines := e.copyText()
	if text == "" {
		return
	}
	e.lineClip = ""
	if lines {
		e.lineClip = text
	}
	if e.OnCopy != nil {
		e.OnCopy(text)
	}
	gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
	if !cut {
		return
	}
	if lines {
		e.cur.DeleteLines()
	} else {
		e.cur.DeleteBackward()
	}
}

// expandSelection grows the primary selection: word → line(s) → all.
func (e *Editor) expandSelection() {
	s := e.buf.Snapshot()
	st := e.cur.State()
	p := e.cur.Primary()
	var next cursor.Selection
	switch {
	case p.Empty():
		start, end := cursor.WordAt(s, p.Head)
		if start == end {
			start, end = lineRange(s, p.Head)
		}
		next = cursor.Range(start, end)
	default:
		ls, _ := lineRange(s, p.Start())
		_, le := lineRange(s, max(p.Start(), p.End()-1))
		if p.Start() != ls || p.End() != le {
			next = cursor.Range(ls, le)
		} else {
			next = cursor.Range(0, s.Len())
		}
	}
	if next.Start() == p.Start() && next.End() == p.End() {
		return
	}
	e.expand = append(e.expand, st)
	e.cur.SetSelections([]cursor.Selection{next}, 0)
}

// foldAtCaret returns the innermost fold range containing the caret line.
func (e *Editor) foldAtCaret() (fold.Range, bool) {
	line := e.buf.OffsetToPos(e.cur.Primary().Head).Line
	var best fold.Range
	found := false
	for _, r := range e.folds.Ranges() {
		if r.StartLine <= line && line <= r.EndLine && (!found || r.StartLine >= best.StartLine) {
			best, found = r, true
		}
	}
	return best, found
}

// findStep selects the next or previous match of the search query.
func (e *Editor) findStep(forward bool) bool {
	if e.query == nil {
		e.emit(ActionEvent{Action: keymap.Find})
		return false
	}
	s := e.buf.Snapshot()
	p := e.cur.Primary()
	var m search.Match
	var ok bool
	if forward {
		m, ok = e.query.Next(s, p.End(), true)
	} else {
		m, ok = e.query.Prev(s, p.Start(), true)
	}
	if !ok {
		return false
	}
	e.cur.SetSelections([]cursor.Selection{cursor.Range(m.Start, m.End)}, 0)
	return true
}

// caretsOutOfFolds moves carets hidden by collapsed folds to the end of
// their fold header line.
func (e *Editor) caretsOutOfFolds() {
	s := e.buf.Snapshot()
	sels := e.cur.Selections()
	moved := false
	for i, sel := range sels {
		l := s.OffsetToPos(sel.Head).Line
		if !e.folds.IsHidden(l) {
			continue
		}
		header := e.folds.VisibleToDoc(e.folds.DocToVisible(l))
		sels[i] = cursor.Caret(s.LineEnd(header))
		moved = true
	}
	if moved {
		e.cur.SetSelections(sels, e.cur.PrimaryIndex())
	}
}
