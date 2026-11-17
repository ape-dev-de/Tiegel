// Package cursor implements the cursor and selection model of the Tiegel
// editor on top of package buffer: multiple cursors with selections,
// movement by grapheme, word (PHP-aware), line, page and document, the
// desired column for vertical movement, editing at all cursors as one undo
// step, bracket matching and auto-closing of brackets and quotes.
//
// The package is UI independent; a Model must only be used from one
// goroutine.
package cursor

import (
	"slices"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Selection is a selected range. Anchor is where the selection started and
// stays fixed when extending; Head is the moving end where the caret is
// drawn. An empty selection (Anchor == Head) is a plain caret.
type Selection struct {
	Anchor int
	Head   int
	// Goal is the desired visual column for vertical movement; it is only
	// meaningful if HasGoal is set. Horizontal movement clears it.
	Goal    int
	HasGoal bool
	// RowEnd is the caret affinity with soft wrap: a Head at a soft
	// break is at the end of the row before the break instead of the
	// start of the next one (set by End, vertical movement and clicks
	// right of a row; any other change clears it).
	RowEnd bool
}

// Caret returns an empty selection at off.
func Caret(off int) Selection { return Selection{Anchor: off, Head: off} }

// Range returns a selection from anchor to head.
func Range(anchor, head int) Selection { return Selection{Anchor: anchor, Head: head} }

// Start returns the smaller end of the selection.
func (s Selection) Start() int { return min(s.Anchor, s.Head) }

// End returns the larger end of the selection.
func (s Selection) End() int { return max(s.Anchor, s.Head) }

// Empty reports whether the selection is a plain caret.
func (s Selection) Empty() bool { return s.Anchor == s.Head }

// Len returns the number of selected bytes.
func (s Selection) Len() int { return s.End() - s.Start() }

// State is the complete cursor state. It is stored with every undo step so
// that undo/redo restore the cursors.
type State struct {
	Sels    []Selection
	Primary int
}

// LineMapper maps document lines to visible rows, e.g. to skip folded
// lines during vertical movement. fold.Model implements it.
type LineMapper interface {
	DocToVisible(line int) int
	VisibleToDoc(row int) int
	VisibleCount() int
}

// RowMapper maps document offsets to visual rows when lines are soft
// wrapped (wrap.View implements it). Rows already exclude folded lines.
type RowMapper interface {
	// RowCount returns the number of visual rows.
	RowCount() int
	// CaretRow returns the visual row of a caret at off with affinity
	// rowEnd (see Selection.RowEnd) and its visual column in it.
	CaretRow(s buffer.Snapshot, off int, rowEnd bool) (row, col int)
	// CaretAt returns the caret at visual column col of row row and
	// whether it is at the end of a continued row.
	CaretAt(s buffer.Snapshot, row, col int) (off int, rowEnd bool)
	// CaretRowBounds returns the offsets [start, end) of the row of a
	// caret at off with affinity rowEnd and whether it is the last row
	// of its line.
	CaretRowBounds(s buffer.Snapshot, off int, rowEnd bool) (start, end int, last bool)
}

// Model holds the cursors of one buffer and implements editing commands.
type Model struct {
	buf     *buffer.Buffer
	sels    []Selection
	primary int
	editing bool
	unsub   func()

	// TabWidth is used to compute visual columns (default 4).
	TabWidth int
	// PageLines is the number of lines moved by PageUp/PageDown
	// (default 30). The widget sets it from the viewport height.
	PageLines int
	// Lines optionally maps document lines to visible rows (folding).
	Lines LineMapper
	// Rows, if set, makes Up/Down/PageUp/PageDown move by visual row and
	// Home/End work on the visual row first (soft wrap). It takes
	// precedence over Lines for vertical movement.
	Rows RowMapper
	// AutoClose enables auto-closing of brackets and quotes in Type.
	AutoClose bool
}

// New creates a model with a single caret at offset 0. Edits made to the
// buffer by others (e.g. replace all, formatting) shift the cursors
// automatically; call Close to stop listening.
func New(b *buffer.Buffer) *Model {
	m := &Model{buf: b, sels: []Selection{Caret(0)}, TabWidth: 4, PageLines: 30, AutoClose: true}
	m.unsub = b.OnEdit(m.onEdit)
	return m
}

// Close detaches the model from its buffer.
func (m *Model) Close() {
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
}

// Buffer returns the underlying buffer.
func (m *Model) Buffer() *buffer.Buffer { return m.buf }

// Selections returns a copy of all selections, sorted by position.
func (m *Model) Selections() []Selection { return slices.Clone(m.sels) }

// Primary returns the primary selection (the one most recently added).
func (m *Model) Primary() Selection { return m.sels[m.primary] }

// PrimaryIndex returns the index of the primary selection.
func (m *Model) PrimaryIndex() int { return m.primary }

// State returns a copy of the cursor state.
func (m *Model) State() State { return State{Sels: m.Selections(), Primary: m.primary} }

// SetState restores a cursor state.
func (m *Model) SetState(st State) { m.SetSelections(st.Sels, st.Primary) }

// SetSelections replaces all selections. Overlapping selections are merged.
// It also sets an undo boundary.
func (m *Model) SetSelections(sels []Selection, primary int) {
	if len(sels) == 0 {
		sels = []Selection{Caret(0)}
		primary = 0
	}
	m.sels, m.primary = normalize(slices.Clone(sels), primary, m.buf.Len())
	m.buf.BreakUndo()
}

// SetCaret replaces all selections with one caret at off.
func (m *Model) SetCaret(off int) { m.SetSelections([]Selection{Caret(off)}, 0) }

// AddSelection adds a selection (or caret) and makes it primary.
func (m *Model) AddSelection(s Selection) {
	m.SetSelections(append(m.Selections(), s), len(m.sels))
}

// AddCursor adds a caret at off and makes it primary.
func (m *Model) AddCursor(off int) { m.AddSelection(Caret(off)) }

// CollapseToPrimary removes all but the primary selection and clears it
// (the usual Escape behaviour): first Escape drops secondary cursors, a
// second one collapses the selection.
func (m *Model) CollapseToPrimary() {
	p := m.Primary()
	if len(m.sels) == 1 {
		p = Caret(p.Head)
	}
	m.SetSelections([]Selection{p}, 0)
}

// onEdit shifts cursors for edits made by others.
func (m *Model) onEdit(e buffer.Edit) {
	if m.editing {
		return
	}
	for i, s := range m.sels {
		m.sels[i] = Selection{Anchor: mapOffset(s.Anchor, e), Head: mapOffset(s.Head, e)}
	}
	m.sels, m.primary = normalize(m.sels, m.primary, m.buf.Len())
}

// mapOffset maps an offset across an edit. Offsets inside the replaced
// range move to its start.
func mapOffset(off int, e buffer.Edit) int {
	switch {
	case off <= e.StartByte:
		return off
	case off >= e.OldEndByte:
		return off + e.NewEndByte - e.OldEndByte
	default:
		return e.StartByte
	}
}

// normalize clamps, sorts and merges selections and returns the new index
// of the primary selection.
func normalize(sels []Selection, primary, docLen int) ([]Selection, int) {
	primary = min(max(primary, 0), len(sels)-1)
	type item struct {
		s       Selection
		primary bool
	}
	items := make([]item, len(sels))
	for i, s := range sels {
		s.Anchor = min(max(s.Anchor, 0), docLen)
		s.Head = min(max(s.Head, 0), docLen)
		items[i] = item{s, i == primary}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		if c := a.s.Start() - b.s.Start(); c != 0 {
			return c
		}
		return a.s.End() - b.s.End()
	})
	out := sels[:0]
	newPrimary := 0
	for _, it := range items {
		if n := len(out); n > 0 {
			last := out[n-1]
			s := it.s
			if s.Start() < last.End() || (s.Start() == last.End() && (s.Empty() || last.Empty())) {
				start, end := last.Start(), max(last.End(), s.End())
				merged := Selection{Anchor: start, Head: end, Goal: last.Goal, HasGoal: last.HasGoal}
				if last.Head < last.Anchor {
					merged.Anchor, merged.Head = end, start
				}
				out[n-1] = merged
				if it.primary {
					newPrimary = n - 1
				}
				continue
			}
		}
		if it.primary {
			newPrimary = len(out)
		}
		out = append(out, it.s)
	}
	return out, newPrimary
}
