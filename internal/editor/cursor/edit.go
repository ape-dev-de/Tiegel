package cursor

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// rangeEdit replaces [start, end) with text; anchor and head give the
// resulting selection relative to start after the edit.
type rangeEdit struct {
	start, end   int
	text         string
	anchor, head int
	primary      bool
}

// applyEdits applies one edit per selection as a single undo step and
// places the cursors. Overlapping edit ranges are merged.
func (m *Model) applyEdits(kind buffer.GroupKind, edits []rangeEdit) {
	slices.SortStableFunc(edits, func(a, b rangeEdit) int { return a.start - b.start })
	merged := edits[:0]
	for _, e := range edits {
		if n := len(merged); n > 0 && e.start < merged[n-1].end {
			last := &merged[n-1]
			last.end = max(last.end, e.end)
			last.text += e.text
			last.anchor, last.head = len(last.text), len(last.text)
			last.primary = last.primary || e.primary
			continue
		}
		merged = append(merged, e)
	}
	m.buf.BeginGroup(kind, m.State())
	m.editing = true
	for i := len(merged) - 1; i >= 0; i-- {
		e := merged[i]
		// Ranges come from clamped selections, so errors cannot occur;
		// a failing edit is skipped rather than corrupting the cursors.
		if _, err := m.buf.Replace(e.start, e.end-e.start, []byte(e.text)); err != nil {
			merged[i].text, merged[i].end = "", e.start
		}
	}
	m.editing = false
	sels := make([]Selection, len(merged))
	primary, shift := 0, 0
	for i, e := range merged {
		ns := e.start + shift
		sels[i] = Range(ns+e.anchor, ns+e.head)
		if e.primary {
			primary = i
		}
		shift += len(e.text) - (e.end - e.start)
	}
	m.sels, m.primary = normalize(sels, primary, m.buf.Len())
	m.buf.EndGroup(m.State())
}

// editEach builds one edit per selection with fn.
func (m *Model) editEach(kind buffer.GroupKind, fn func(s buffer.Snapshot, sel Selection) rangeEdit) {
	s := m.buf.Snapshot()
	edits := make([]rangeEdit, len(m.sels))
	for i, sel := range m.sels {
		edits[i] = fn(s, sel)
		edits[i].primary = i == m.primary
	}
	m.applyEdits(kind, edits)
}

// replaceSel returns an edit replacing sel with text, caret after it.
func replaceSel(sel Selection, text string) rangeEdit {
	return rangeEdit{start: sel.Start(), end: sel.End(), text: text, anchor: len(text), head: len(text)}
}

// Insert inserts text at every cursor, replacing selections. Single
// characters are grouped with adjacent typing into one undo step.
func (m *Model) Insert(text string) {
	kind := buffer.GroupOther
	if utf8.RuneCountInString(text) == 1 && text != "\n" {
		kind = buffer.GroupTyping
	}
	m.editEach(kind, func(_ buffer.Snapshot, sel Selection) rangeEdit { return replaceSel(sel, text) })
}

// Paste inserts clipboard text with line endings converted to the
// buffer's style. If there are several cursors and the text has exactly
// one line per cursor, each cursor receives its own line.
func (m *Model) Paste(text string) {
	eol := m.buf.EOL().String()
	text = string(buffer.NormalizeEOL([]byte(text), m.buf.EOL()))
	parts := strings.Split(strings.TrimSuffix(text, eol), eol)
	if len(m.sels) > 1 && len(parts) == len(m.sels) {
		i := 0
		m.editEach(buffer.GroupOther, func(_ buffer.Snapshot, sel Selection) rangeEdit {
			e := replaceSel(sel, parts[i])
			i++
			return e
		})
		return
	}
	m.editEach(buffer.GroupOther, func(_ buffer.Snapshot, sel Selection) rangeEdit { return replaceSel(sel, text) })
}

// Type handles a typed character at every cursor, applying the auto-close
// rules if AutoClose is set: openers insert their closer, typing a closer
// or closing quote steps over an existing one, and typing an opener or
// quote with a selection wraps the selection.
func (m *Model) Type(r rune) {
	if r == '\n' || r == '\r' {
		m.Newline()
		return
	}
	typed := string(r)
	if !m.AutoClose {
		m.Insert(typed)
		return
	}
	m.editEach(buffer.GroupTyping, func(s buffer.Snapshot, sel Selection) rangeEdit {
		if !sel.Empty() {
			if c, ok := closerOf(r); ok {
				inner := string(s.Slice(sel.Start(), sel.End()))
				e := rangeEdit{start: sel.Start(), end: sel.End(), text: typed + inner + string(c)}
				e.anchor, e.head = len(typed), len(typed)+len(inner)
				if sel.Head < sel.Anchor {
					e.anchor, e.head = e.head, e.anchor
				}
				return e
			}
			return replaceSel(sel, typed)
		}
		prev, next := runesAround(s, sel.Head)
		res := AutoClose(r, prev, next)
		if res.Skip {
			n := utf8.RuneLen(next)
			return rangeEdit{start: sel.Head, end: sel.Head, anchor: n, head: n}
		}
		return rangeEdit{start: sel.Head, end: sel.Head, text: res.Insert, anchor: res.Caret, head: res.Caret}
	})
}

// runesAround returns the runes before and after off on its line; 0 marks
// the line start/end.
func runesAround(s buffer.Snapshot, off int) (prev, next rune) {
	pos := s.OffsetToPos(off)
	line := s.Line(pos.Line)
	col := min(pos.Col, len(line))
	if col > 0 {
		prev, _ = utf8.DecodeLastRune(line[:col])
	}
	if col < len(line) {
		next, _ = utf8.DecodeRune(line[col:])
	}
	return prev, next
}

// Newline inserts a line break at every cursor and copies the indentation
// of the current line.
func (m *Model) Newline() {
	eol := m.buf.EOL().String()
	m.editEach(buffer.GroupOther, func(s buffer.Snapshot, sel Selection) rangeEdit {
		pos := s.OffsetToPos(sel.Start())
		line := s.Line(pos.Line)
		line = line[:min(pos.Col, len(line))]
		indent := line[:len(line)-len(bytes.TrimLeft(line, " \t"))]
		return replaceSel(sel, eol+string(indent))
	})
}

// DeleteBackward deletes the selection or the grapheme before each caret
// (backspace). With AutoClose, an empty bracket/quote pair around the
// caret is deleted together.
func (m *Model) DeleteBackward() {
	m.editEach(buffer.GroupDeleting, func(s buffer.Snapshot, sel Selection) rangeEdit {
		if !sel.Empty() {
			return replaceSel(sel, "")
		}
		if m.AutoClose {
			if prev, next := runesAround(s, sel.Head); AutoDeletePair(prev, next) {
				return rangeEdit{start: sel.Head - utf8.RuneLen(prev), end: sel.Head + utf8.RuneLen(next)}
			}
		}
		return rangeEdit{start: s.PrevGrapheme(sel.Head), end: sel.Head}
	})
}

// DeleteForward deletes the selection or the grapheme after each caret.
func (m *Model) DeleteForward() {
	m.editEach(buffer.GroupDeleting, func(s buffer.Snapshot, sel Selection) rangeEdit {
		if !sel.Empty() {
			return replaceSel(sel, "")
		}
		return rangeEdit{start: sel.Head, end: s.NextGrapheme(sel.Head)}
	})
}

// DeleteWordBackward deletes the selection or back to the previous word
// start.
func (m *Model) DeleteWordBackward() {
	m.editEach(buffer.GroupOther, func(s buffer.Snapshot, sel Selection) rangeEdit {
		if !sel.Empty() {
			return replaceSel(sel, "")
		}
		return rangeEdit{start: wordLeft(s, sel.Head), end: sel.Head}
	})
}

// DeleteWordForward deletes the selection or up to the next word end.
func (m *Model) DeleteWordForward() {
	m.editEach(buffer.GroupOther, func(s buffer.Snapshot, sel Selection) rangeEdit {
		if !sel.Empty() {
			return replaceSel(sel, "")
		}
		return rangeEdit{start: sel.Head, end: wordRight(s, sel.Head)}
	})
}

// DeleteLines deletes all lines touched by each selection, including their
// line endings.
func (m *Model) DeleteLines() {
	m.editEach(buffer.GroupOther, func(s buffer.Snapshot, sel Selection) rangeEdit {
		start, end := lineSpan(s, sel)
		if end == s.Len() && start > 0 {
			// Last line: remove the preceding line ending instead.
			start = s.LineEnd(s.OffsetToPos(start).Line - 1)
		}
		return rangeEdit{start: start, end: end}
	})
}

// lineSpan returns the range of the full lines (with line endings) that
// sel touches. A selection ending at column 0 does not include that line.
func lineSpan(s buffer.Snapshot, sel Selection) (int, int) {
	first := s.OffsetToPos(sel.Start()).Line
	endPos := s.OffsetToPos(sel.End())
	last := endPos.Line
	if !sel.Empty() && endPos.Col == 0 && last > first {
		last--
	}
	end := s.Len()
	if last+1 < s.LineCount() {
		end = s.LineStart(last + 1)
	}
	return s.LineStart(first), end
}

// Undo reverts the last undo step and restores the cursors stored with it.
func (m *Model) Undo() bool { return m.history(m.buf.Undo) }

// Redo re-applies the last undone step and restores its cursors.
func (m *Model) Redo() bool { return m.history(m.buf.Redo) }

func (m *Model) history(fn func() ([]buffer.Edit, any, bool)) bool {
	m.editing = true
	edits, st, ok := fn()
	m.editing = false
	if !ok {
		return false
	}
	if state, isState := st.(State); isState {
		m.SetState(state)
	} else if len(edits) > 0 {
		m.SetCaret(edits[len(edits)-1].NewEndByte)
	}
	return true
}

// SelectAll selects the whole document.
func (m *Model) SelectAll() { m.SetSelections([]Selection{Range(0, m.buf.Len())}, 0) }

// SelectWord expands every empty selection to the word at its caret.
func (m *Model) SelectWord() {
	s := m.buf.Snapshot()
	sels := m.Selections()
	for i, sel := range sels {
		if sel.Empty() {
			start, end := WordAt(s, sel.Head)
			sels[i] = Range(start, end)
		}
	}
	m.SetSelections(sels, m.primary)
}

// SelectLines expands every selection to full lines (including the line
// ending).
func (m *Model) SelectLines() {
	s := m.buf.Snapshot()
	sels := m.Selections()
	for i, sel := range sels {
		start, end := lineSpan(s, sel)
		sels[i] = Range(start, end)
	}
	m.SetSelections(sels, m.primary)
}

// AddNextOccurrence implements "add selection for next occurrence": with
// an empty primary selection it selects the word at the caret; otherwise
// it adds a selection for the next occurrence of the primary selection's
// text after the primary (most recently added) selection, wrapping
// around. It returns false if nothing was added.
func (m *Model) AddNextOccurrence() bool {
	p := m.Primary()
	s := m.buf.Snapshot()
	if p.Empty() {
		start, end := WordAt(s, p.Head)
		if start == end {
			return false
		}
		sels := m.Selections()
		sels[m.primary] = Range(start, end)
		m.SetSelections(sels, m.primary)
		return true
	}
	needle := s.Slice(p.Start(), p.End())
	from := m.sels[m.primary].End()
	found := s.Index(from, needle)
	if found < 0 {
		found = s.Index(0, needle)
	}
	for _, sel := range m.sels {
		if sel.Start() == found {
			return false // every occurrence is selected already
		}
	}
	m.AddSelection(Range(found, found+len(needle)))
	return true
}

// SelectedText returns the text of each selection in document order.
func (m *Model) SelectedText() []string {
	s := m.buf.Snapshot()
	out := make([]string, len(m.sels))
	for i, sel := range m.sels {
		out[i] = string(s.Slice(sel.Start(), sel.End()))
	}
	return out
}
