// Package fold models code folding: foldable line ranges supplied from the
// outside (e.g. by Tree-sitter), their collapsed state, and the mapping
// between document lines and visible rows. Mapping is O(log k) in the
// number of collapsed regions; it is rebuilt lazily after changes.
package fold

import (
	"slices"
	"sort"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Range is a foldable region of whole lines. The header line StartLine
// stays visible; lines StartLine+1 .. EndLine are hidden when collapsed.
type Range struct {
	StartLine int
	EndLine   int
}

type foldRange struct {
	Range
	collapsed bool
}

// hidden is a merged run of hidden document lines [from, to].
type hidden struct{ from, to int }

// Model holds the fold ranges of one document. It is not safe for
// concurrent use.
type Model struct {
	ranges    []foldRange // sorted by StartLine asc, EndLine desc
	lineCount int

	dirty  bool
	hidden []hidden
	prefix []int // prefix[i] = hidden lines in hidden[:i]
}

// New creates an empty model for a document with lineCount lines.
func New(lineCount int) *Model { return &Model{lineCount: max(lineCount, 1), dirty: true} }

// SetLineCount updates the number of document lines; ranges beyond the
// document are dropped.
func (m *Model) SetLineCount(n int) {
	m.lineCount = max(n, 1)
	m.ranges = slices.DeleteFunc(m.ranges, func(r foldRange) bool { return r.EndLine >= m.lineCount })
	m.dirty = true
}

// LineCount returns the number of document lines.
func (m *Model) LineCount() int { return m.lineCount }

// SetRanges replaces the foldable ranges, e.g. after a re-parse. Ranges
// spanning less than two lines or lying outside the document are ignored.
// The collapsed state of ranges whose StartLine did not change is kept.
func (m *Model) SetRanges(rs []Range) {
	was := make(map[int]bool)
	for _, r := range m.ranges {
		if r.collapsed {
			was[r.StartLine] = true
		}
	}
	m.ranges = m.ranges[:0]
	for _, r := range rs {
		if r.StartLine < 0 || r.EndLine <= r.StartLine || r.EndLine >= m.lineCount {
			continue
		}
		m.ranges = append(m.ranges, foldRange{Range: r, collapsed: was[r.StartLine]})
	}
	m.sortRanges()
	m.dirty = true
}

func (m *Model) sortRanges() {
	slices.SortFunc(m.ranges, func(a, b foldRange) int {
		if a.StartLine != b.StartLine {
			return a.StartLine - b.StartLine
		}
		return b.EndLine - a.EndLine
	})
	m.ranges = slices.CompactFunc(m.ranges, func(a, b foldRange) bool {
		if a.Range == b.Range {
			return true
		}
		return false
	})
}

// Ranges returns all foldable ranges sorted by start line.
func (m *Model) Ranges() []Range {
	out := make([]Range, len(m.ranges))
	for i, r := range m.ranges {
		out[i] = r.Range
	}
	return out
}

// CollapsedRanges returns the collapsed ranges sorted by start line.
func (m *Model) CollapsedRanges() []Range {
	var out []Range
	for _, r := range m.ranges {
		if r.collapsed {
			out = append(out, r.Range)
		}
	}
	return out
}

// find returns the index of the outermost range starting at line.
func (m *Model) find(line int) int {
	i := sort.Search(len(m.ranges), func(i int) bool { return m.ranges[i].StartLine >= line })
	if i < len(m.ranges) && m.ranges[i].StartLine == line {
		return i
	}
	return -1
}

// RangeAt returns the outermost range whose header is line (for the gutter
// fold marker).
func (m *Model) RangeAt(line int) (Range, bool) {
	if i := m.find(line); i >= 0 {
		return m.ranges[i].Range, true
	}
	return Range{}, false
}

// IsCollapsed reports whether the range with header line is collapsed.
func (m *Model) IsCollapsed(line int) bool {
	i := m.find(line)
	return i >= 0 && m.ranges[i].collapsed
}

func (m *Model) set(line int, collapsed bool) bool {
	i := m.find(line)
	if i < 0 || m.ranges[i].collapsed == collapsed {
		return false
	}
	m.ranges[i].collapsed = collapsed
	m.dirty = true
	return true
}

// Collapse folds the range with header line. It reports whether anything
// changed.
func (m *Model) Collapse(line int) bool { return m.set(line, true) }

// Expand unfolds the range with header line.
func (m *Model) Expand(line int) bool { return m.set(line, false) }

// Toggle flips the range with header line.
func (m *Model) Toggle(line int) bool { return m.set(line, !m.IsCollapsed(line)) }

// CollapseAll folds every range.
func (m *Model) CollapseAll() { m.setAll(true) }

// ExpandAll unfolds every range.
func (m *Model) ExpandAll() { m.setAll(false) }

func (m *Model) setAll(c bool) {
	for i := range m.ranges {
		m.ranges[i].collapsed = c
	}
	m.dirty = true
}

// Reveal expands every collapsed range that hides line, e.g. when the
// cursor or a search hit moves there. It reports whether anything changed.
func (m *Model) Reveal(line int) bool {
	changed := false
	for i, r := range m.ranges {
		if r.StartLine >= line {
			break
		}
		if r.collapsed && line <= r.EndLine {
			m.ranges[i].collapsed = false
			changed = true
		}
	}
	if changed {
		m.dirty = true
	}
	return changed
}

// ApplyEdit shifts the ranges for a buffer edit so that folds stay in place
// until fresh ranges arrive from the parser. Ranges whose header is
// deleted, or that become shorter than two lines, are dropped.
func (m *Model) ApplyEdit(e buffer.Edit) {
	start, oldEnd, newEnd := e.StartPoint.Row, e.OldEndPoint.Row, e.NewEndPoint.Row
	delta := newEnd - oldEnd
	if delta == 0 {
		return
	}
	mapLine := func(l int) int {
		switch {
		case l <= start:
			return l
		case l > oldEnd:
			return l + delta
		default:
			return newEnd
		}
	}
	m.lineCount = max(m.lineCount+delta, 1)
	out := m.ranges[:0]
	for _, r := range m.ranges {
		if r.StartLine > start && r.StartLine <= oldEnd {
			continue // header line removed or rewritten
		}
		r.StartLine, r.EndLine = mapLine(r.StartLine), min(mapLine(r.EndLine), m.lineCount-1)
		if r.EndLine > r.StartLine {
			out = append(out, r)
		}
	}
	m.ranges = out
	m.sortRanges()
	m.dirty = true
}

// rebuild recomputes the merged hidden intervals.
func (m *Model) rebuild() {
	if !m.dirty {
		return
	}
	m.dirty = false
	m.hidden = m.hidden[:0]
	for _, r := range m.ranges { // sorted by StartLine
		if !r.collapsed {
			continue
		}
		from, to := r.StartLine+1, min(r.EndLine, m.lineCount-1)
		if from > to {
			continue
		}
		if n := len(m.hidden); n > 0 && from <= m.hidden[n-1].to+1 {
			m.hidden[n-1].to = max(m.hidden[n-1].to, to)
			continue
		}
		m.hidden = append(m.hidden, hidden{from, to})
	}
	m.prefix = append(m.prefix[:0], 0)
	for _, h := range m.hidden {
		m.prefix = append(m.prefix, m.prefix[len(m.prefix)-1]+h.to-h.from+1)
	}
}

// HiddenCount returns the number of hidden document lines.
func (m *Model) HiddenCount() int {
	m.rebuild()
	return m.prefix[len(m.prefix)-1]
}

// VisibleCount returns the number of visible rows.
func (m *Model) VisibleCount() int { return m.lineCount - m.HiddenCount() }

// interval returns the index of the last hidden interval starting at or
// before line, or -1.
func (m *Model) interval(line int) int {
	m.rebuild()
	return sort.Search(len(m.hidden), func(i int) bool { return m.hidden[i].from > line }) - 1
}

// IsHidden reports whether the document line is inside a collapsed range.
func (m *Model) IsHidden(line int) bool {
	i := m.interval(line)
	return i >= 0 && line <= m.hidden[i].to
}

// DocToVisible maps a document line to its visible row. A hidden line maps
// to the row of the visible line above it (its fold header).
func (m *Model) DocToVisible(line int) int {
	line = min(max(line, 0), m.lineCount-1)
	i := m.interval(line)
	if i < 0 {
		return line
	}
	if line <= m.hidden[i].to {
		return m.hidden[i].from - 1 - m.prefix[i]
	}
	return line - m.prefix[i+1]
}

// VisibleToDoc maps a visible row to its document line. Rows are clamped
// to [0, VisibleCount).
func (m *Model) VisibleToDoc(row int) int {
	m.rebuild()
	row = min(max(row, 0), m.VisibleCount()-1)
	k := sort.Search(len(m.hidden), func(i int) bool { return m.hidden[i].from-m.prefix[i] > row })
	return row + m.prefix[k]
}
