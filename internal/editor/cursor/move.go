package cursor

import (
	"unicode"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Movement is a cursor movement command.
type Movement uint8

const (
	// Left moves one grapheme cluster left.
	Left Movement = iota
	// Right moves one grapheme cluster right.
	Right
	// WordLeft moves to the start of the current or previous word.
	WordLeft
	// WordRight moves to the end of the current or next word.
	WordRight
	// Up moves one visible line up, keeping the goal column.
	Up
	// Down moves one visible line down, keeping the goal column.
	Down
	// Home moves to the first non-blank character of the line, or to
	// column 0 if already there ("smart home").
	Home
	// End moves to the end of the line.
	End
	// PageUp moves PageLines lines up.
	PageUp
	// PageDown moves PageLines lines down.
	PageDown
	// DocStart moves to the start of the document.
	DocStart
	// DocEnd moves to the end of the document.
	DocEnd
)

// Move moves all cursors. With extend the anchors stay put and the
// selections grow; otherwise selections collapse. Without extend, Left and
// Right on a non-empty selection collapse it to its start/end. Moving sets
// an undo boundary.
func (m *Model) Move(mv Movement, extend bool) {
	s := m.buf.Snapshot()
	for i, sel := range m.sels {
		m.sels[i] = m.move(s, sel, mv, extend)
	}
	m.sels, m.primary = normalize(m.sels, m.primary, s.Len())
	m.buf.BreakUndo()
}

func (m *Model) move(s buffer.Snapshot, sel Selection, mv Movement, extend bool) Selection {
	if !extend && !sel.Empty() {
		switch mv {
		case Left:
			return Caret(sel.Start())
		case Right:
			return Caret(sel.End())
		}
	}
	var head int
	vertical, rowEnd := false, false
	switch mv {
	case Left:
		head = s.PrevGrapheme(sel.Head)
	case Right:
		head = s.NextGrapheme(sel.Head)
	case WordLeft:
		head = wordLeft(s, sel.Head)
	case WordRight:
		head = wordRight(s, sel.Head)
	case Up, Down, PageUp, PageDown:
		vertical = true
		sel = m.vertical(s, sel, m.verticalDelta(mv))
		head, rowEnd = sel.Head, sel.RowEnd
	case Home:
		head = m.home(s, sel)
	case End:
		head, rowEnd = m.end(s, sel)
	case DocStart:
		head = 0
	case DocEnd:
		head = s.Len()
	}
	out := Selection{Anchor: sel.Anchor, Head: head, RowEnd: rowEnd}
	if !extend {
		out.Anchor = head
	}
	if vertical {
		out.Goal, out.HasGoal = sel.Goal, sel.HasGoal
	}
	return out
}

func (m *Model) verticalDelta(mv Movement) int {
	switch mv {
	case Up:
		return -1
	case Down:
		return 1
	case PageUp:
		return -m.pageLines()
	}
	return m.pageLines()
}

func (m *Model) pageLines() int {
	if m.PageLines <= 0 {
		return 30
	}
	return m.PageLines
}

func (m *Model) tabWidth() int {
	if m.TabWidth <= 0 {
		return 4
	}
	return m.TabWidth
}

// vertical moves the head by delta visible rows and returns the selection
// with the new head and its goal column set.
func (m *Model) vertical(s buffer.Snapshot, sel Selection, delta int) Selection {
	if m.Rows != nil {
		row, col := m.Rows.CaretRow(s, sel.Head, sel.RowEnd)
		if !sel.HasGoal {
			sel.Goal, sel.HasGoal = col, true
		}
		sel.RowEnd = false
		switch row += delta; {
		case row < 0:
			sel.Head = 0
		case row >= m.Rows.RowCount():
			sel.Head = s.Len()
		default:
			sel.Head, sel.RowEnd = m.Rows.CaretAt(s, row, sel.Goal)
		}
		return sel
	}
	sel.RowEnd = false
	pos := s.OffsetToPos(sel.Head)
	if !sel.HasGoal {
		sel.Goal = buffer.VisualCol(s.Line(pos.Line), pos.Col, m.tabWidth())
		sel.HasGoal = true
	}
	row, rows := pos.Line+delta, s.LineCount()
	if m.Lines != nil {
		row, rows = m.Lines.DocToVisible(pos.Line)+delta, m.Lines.VisibleCount()
	}
	switch {
	case row < 0:
		sel.Head = 0
		return sel
	case row >= rows:
		sel.Head = s.Len()
		return sel
	}
	line := row
	if m.Lines != nil {
		line = m.Lines.VisibleToDoc(row)
	}
	col := buffer.ByteColFromVisual(s.Line(line), sel.Goal, m.tabWidth())
	sel.Head = s.LineStart(line) + col
	return sel
}

// AddCursorVertical clones the primary caret delta lines above (negative)
// or below (positive), keeping the goal column (also across lines
// shorter than it). The new caret becomes
// primary, so repeated calls keep extending in the same direction.
func (m *Model) AddCursorVertical(delta int) {
	s := m.buf.Snapshot()
	p := m.Primary()
	start := Caret(p.Head)
	start.RowEnd = p.RowEnd
	// Keep the goal column so cloning past a short line returns to the
	// column of the first caret.
	start.Goal, start.HasGoal = p.Goal, p.HasGoal
	moved := m.vertical(s, start, delta)
	if m.Rows != nil {
		r0, _ := m.Rows.CaretRow(s, p.Head, p.RowEnd)
		if r1, _ := m.Rows.CaretRow(s, moved.Head, moved.RowEnd); r1 == r0 {
			return // no row in that direction
		}
	} else if line := s.OffsetToPos(moved.Head).Line; line == s.OffsetToPos(p.Head).Line {
		return // no line in that direction
	}
	moved.Anchor = moved.Head
	m.AddSelection(moved)
}

// home moves the head of sel to the start of its visual row (soft wrap)
// or, on the first row or when already there, to the smart line start.
func (m *Model) home(s buffer.Snapshot, sel Selection) int {
	off := sel.Head
	if m.Rows != nil {
		start, _, _ := m.Rows.CaretRowBounds(s, off, sel.RowEnd)
		if start != s.LineStart(s.OffsetToPos(off).Line) && off != start {
			return start
		}
	}
	return smartHome(s, off)
}

// end moves the head of sel to the end of its visual row or, when already
// there, to the line end. The end of a continued row is its soft break
// with row-end affinity (after the row's last cluster).
func (m *Model) end(s buffer.Snapshot, sel Selection) (off int, rowEnd bool) {
	if m.Rows != nil {
		_, end, last := m.Rows.CaretRowBounds(s, sel.Head, sel.RowEnd)
		if !last && !(sel.RowEnd && sel.Head == end) {
			return end, true
		}
	}
	return s.LineEnd(s.OffsetToPos(sel.Head).Line), false
}

func smartHome(s buffer.Snapshot, off int) int {
	pos := s.OffsetToPos(off)
	line := s.Line(pos.Line)
	indent := 0
	for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	start := s.LineStart(pos.Line)
	if pos.Col == indent {
		return start
	}
	return start + indent
}

// char classes for word movement
const (
	classSpace = iota
	classWord
	classPunct
)

// IsWordRune reports whether r can be part of a PHP identifier.
func IsWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func classOf(r rune) int {
	switch {
	case r == ' ' || r == '\t' || r == '\r':
		return classSpace
	case IsWordRune(r):
		return classWord
	default:
		return classPunct
	}
}

// isVarSigil reports whether line[i] is a '$' that starts a variable.
func isVarSigil(line []byte, i int) bool {
	if i < 0 || i >= len(line) || line[i] != '$' {
		return false
	}
	r, _ := utf8.DecodeRune(line[i+1:])
	return IsWordRune(r)
}

// wordEnd returns the column after the word or punctuation run starting at
// or after col (after skipping blanks). "$name" counts as one word.
func wordEnd(line []byte, col int) int {
	i := col
	for i < len(line) {
		r, size := utf8.DecodeRune(line[i:])
		if classOf(r) != classSpace {
			break
		}
		i += size
	}
	if i >= len(line) {
		return i
	}
	if isVarSigil(line, i) {
		i++
	}
	r, _ := utf8.DecodeRune(line[i:])
	cls := classOf(r)
	for i < len(line) {
		r, size := utf8.DecodeRune(line[i:])
		if classOf(r) != cls || (cls == classPunct && isVarSigil(line, i)) {
			break
		}
		i += size
	}
	return i
}

// wordStart returns the column of the start of the word or punctuation run
// ending at or before col (after skipping blanks backwards).
func wordStart(line []byte, col int) int {
	i := col
	for i > 0 {
		r, size := utf8.DecodeLastRune(line[:i])
		if classOf(r) != classSpace {
			break
		}
		i -= size
	}
	if i == 0 {
		return 0
	}
	r, _ := utf8.DecodeLastRune(line[:i])
	cls := classOf(r)
	for i > 0 {
		r, size := utf8.DecodeLastRune(line[:i])
		if classOf(r) != cls {
			break
		}
		i -= size
	}
	if cls == classWord && isVarSigil(line, i-1) {
		i--
	}
	return i
}

func wordRight(s buffer.Snapshot, off int) int {
	pos := s.OffsetToPos(off)
	line := s.Line(pos.Line)
	if pos.Col >= len(line) {
		return s.NextGrapheme(off)
	}
	return s.LineStart(pos.Line) + wordEnd(line, pos.Col)
}

func wordLeft(s buffer.Snapshot, off int) int {
	pos := s.OffsetToPos(off)
	if pos.Col == 0 {
		return s.PrevGrapheme(off)
	}
	line := s.Line(pos.Line)
	return s.LineStart(pos.Line) + wordStart(line, min(pos.Col, len(line)))
}

// WordAt returns the word (identifier, optionally with its leading '$')
// touching off. If there is none, start == end == off.
func WordAt(s buffer.Snapshot, off int) (start, end int) {
	pos := s.OffsetToPos(off)
	line := s.Line(pos.Line)
	col := min(pos.Col, len(line))
	isWordAt := func(i int) bool {
		if i < 0 || i >= len(line) {
			return false
		}
		r, _ := utf8.DecodeRune(line[i:])
		return IsWordRune(r) || isVarSigil(line, i)
	}
	if !isWordAt(col) {
		if col == 0 {
			return off, off
		}
		_, size := utf8.DecodeLastRune(line[:col])
		if !isWordAt(col - size) {
			return off, off
		}
		col -= size
	}
	b := col
	if line[col] != '$' {
		_, size := utf8.DecodeRune(line[col:])
		b = wordStart(line, col+size)
	}
	e := wordEnd(line, b)
	ls := s.LineStart(pos.Line)
	return ls + b, ls + e
}
