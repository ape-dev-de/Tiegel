package cursor

import "github.com/ape-dev-de/tiegel/internal/editor/buffer"

// MaxBlockLines bounds the number of selections a block selection
// creates.
const MaxBlockLines = 10000

// Block is a rectangular (column) selection between two corners given as
// document line and visual column (tabs expanded, wide characters two
// columns). The anchor corner stays where the selection started; the
// head corner follows the pointer or the keyboard.
type Block struct {
	AnchorLine, AnchorCol int
	HeadLine, HeadCol     int
}

// Selections returns one selection per line of the block from the
// anchor column to the head column, in document order, and the index of
// the primary one (on the head side). Columns are clamped to the line:
// a line shorter than both columns gets a caret at its end, one ending
// inside the block a selection up to its end. Lines for which hidden
// reports true (folded away) are skipped; hidden may be nil. At most
// MaxBlockLines selections are returned. Each selection remembers the
// head column as its goal column, so vertical moves keep the column.
func (b Block) Selections(s buffer.Snapshot, tabWidth int, hidden func(line int) bool) ([]Selection, int) {
	l0, l1 := b.AnchorLine, b.HeadLine
	if l0 > l1 {
		l0, l1 = l1, l0
	}
	last := s.LineCount() - 1
	l0, l1 = min(max(l0, 0), last), min(max(l1, 0), last)
	c0, c1 := max(b.AnchorCol, 0), max(b.HeadCol, 0)
	var sels []Selection
	for l := l0; l <= l1 && len(sels) < MaxBlockLines; l++ {
		if hidden != nil && hidden(l) {
			continue
		}
		line, ls := s.Line(l), s.LineStart(l)
		sel := Range(ls+buffer.ByteColFromVisual(line, c0, tabWidth), ls+buffer.ByteColFromVisual(line, c1, tabWidth))
		sel.Goal, sel.HasGoal = c1, true
		sels = append(sels, sel)
	}
	if b.HeadLine < b.AnchorLine || len(sels) == 0 {
		return sels, 0
	}
	return sels, len(sels) - 1
}

// BlockAt returns the block of a single caret or selection on one line:
// anchor and head columns are the visual columns of sel's ends. ok is
// false when sel spans several lines.
func BlockAt(s buffer.Snapshot, sel Selection, tabWidth int) (b Block, ok bool) {
	a, h := s.OffsetToPos(sel.Anchor), s.OffsetToPos(sel.Head)
	if a.Line != h.Line {
		return Block{}, false
	}
	line := s.Line(h.Line)
	return Block{
		AnchorLine: a.Line, AnchorCol: buffer.VisualCol(line, a.Col, tabWidth),
		HeadLine: h.Line, HeadCol: buffer.VisualCol(line, h.Col, tabWidth),
	}, true
}

// CurrentBlock derives the block spanned by the current selections: the
// columns are those of the primary selection's anchor and head (a primary
// selection over several lines contributes only its head), the lines run
// from the first to the last selection with the head on the primary's
// side. It is the starting point for extending a block by keyboard.
func (m *Model) CurrentBlock(tabWidth int) Block {
	s := m.buf.Snapshot()
	p := m.Primary()
	b, ok := BlockAt(s, p, tabWidth)
	if !ok {
		b, _ = BlockAt(s, Caret(p.Head), tabWidth)
	}
	if len(m.sels) > 1 {
		first := s.OffsetToPos(m.sels[0].Head).Line
		last := s.OffsetToPos(m.sels[len(m.sels)-1].Head).Line
		if m.primary == 0 {
			b.AnchorLine, b.HeadLine = last, first
		} else {
			b.AnchorLine, b.HeadLine = first, last
		}
	}
	return b
}

// SetBlock replaces the selections with the block (see Block.Selections),
// keeping the primary selection on the head side.
func (m *Model) SetBlock(b Block, tabWidth int, hidden func(line int) bool) {
	sels, primary := b.Selections(m.buf.Snapshot(), tabWidth, hidden)
	m.SetSelections(sels, primary)
}
