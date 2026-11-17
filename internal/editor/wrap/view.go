package wrap

import (
	"sort"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
)

// View combines a wrap Model with folding: the visual rows of the visible
// (not folded away) lines. Row numbers of a View are what the editor
// draws and scrolls; hidden lines have no rows. View implements the
// cursor.RowMapper interface for caret movement by visual row.
type View struct {
	Wrap  *Model
	Folds *fold.Model // nil: nothing is folded

	ok     bool
	wv, fv uint64
	runs   []hiddenRun
	hidden int // rows of all hidden runs

	line []byte
	brk  []int
}

// hiddenRun is a run of hidden lines with its position in visual rows.
type hiddenRun struct {
	from, to int
	vstart   int // visual row right after the fold header
	before   int // rows hidden by the runs before this one
}

func (v *View) sync() {
	var fv uint64
	if v.Folds != nil {
		fv = v.Folds.Version()
	}
	if v.ok && v.wv == v.Wrap.Version() && v.fv == fv {
		return
	}
	v.ok, v.wv, v.fv = true, v.Wrap.Version(), fv
	v.runs = v.runs[:0]
	v.hidden = 0
	if v.Folds == nil {
		return
	}
	for _, r := range v.Folds.HiddenRuns() {
		b := v.Wrap.Before(r.StartLine)
		rows := v.Wrap.Before(r.EndLine+1) - b
		v.runs = append(v.runs, hiddenRun{from: r.StartLine, to: r.EndLine, vstart: b - v.hidden, before: v.hidden})
		v.hidden += rows
	}
}

// RowCount returns the number of visual rows.
func (v *View) RowCount() int {
	v.sync()
	return max(1, v.Wrap.Total()-v.hidden)
}

// LineRow returns the first visual row of document line l. A hidden line
// maps to the first row of its fold header.
func (v *View) LineRow(l int) int {
	v.sync()
	l = min(max(l, 0), max(0, v.Wrap.LineCount()-1))
	i := sort.Search(len(v.runs), func(i int) bool { return v.runs[i].from > l }) - 1
	if i >= 0 && l <= v.runs[i].to {
		l = v.runs[i].from - 1
		i--
	}
	before := 0
	if i >= 0 {
		before = v.runs[i].before + v.Wrap.Before(v.runs[i].to+1) - v.Wrap.Before(v.runs[i].from)
	}
	return v.Wrap.Before(l) - before
}

// LineAt returns the document line shown in visual row row and the row
// index within that line. Rows are clamped to [0, RowCount).
func (v *View) LineAt(row int) (line, sub int) {
	v.sync()
	row = min(max(row, 0), v.RowCount()-1)
	k := sort.Search(len(v.runs), func(i int) bool { return v.runs[i].vstart > row })
	raw := row
	if k > 0 {
		r := v.runs[k-1]
		raw += r.before + v.Wrap.Before(r.to+1) - v.Wrap.Before(r.from)
	}
	return v.Wrap.LineAt(raw)
}

// Segments returns the line-relative byte offsets at which the rows of
// document line l start (the first is always 0) and the line content.
// The slices are reused by the next call.
func (v *View) Segments(s buffer.Snapshot, l int) (starts []int, line []byte) {
	v.line = s.AppendLine(v.line[:0], l)
	v.brk = append(v.brk[:0], 0)
	v.brk = Breaks(v.brk, v.line, v.Wrap.Width(), v.Wrap.TabWidth())
	return v.brk, v.line
}

// segment returns the index and bounds (line-relative) of the row of
// line-relative byte column col: a column at a row break belongs to the
// following row.
func segment(starts []int, n, col int) (i, start, end int) {
	i = sort.Search(len(starts), func(k int) bool { return starts[k] > col }) - 1
	i = max(i, 0)
	end = n
	if i+1 < len(starts) {
		end = starts[i+1]
	}
	return i, starts[i], end
}

// RowOf returns the visual row of document offset off and its visual
// column within that row.
func (v *View) RowOf(s buffer.Snapshot, off int) (row, col int) {
	pos := s.OffsetToPos(off)
	if v.Folds != nil && v.Folds.IsHidden(pos.Line) {
		return v.LineRow(pos.Line), 0
	}
	starts, line := v.Segments(s, pos.Line)
	i, a, _ := segment(starts, len(line), pos.Col)
	return v.LineRow(pos.Line) + i, buffer.VisualCol(line[a:], pos.Col-a, v.Wrap.TabWidth())
}

// OffsetAt returns the document offset at visual column col of visual
// row row. Columns past the end of a continued row stop before its last
// cluster, so the caret stays on that row.
func (v *View) OffsetAt(s buffer.Snapshot, row, col int) int {
	l, sub := v.LineAt(row)
	starts, line := v.Segments(s, l)
	sub = min(sub, len(starts)-1)
	a, b := starts[sub], len(line)
	last := sub+1 >= len(starts)
	if !last {
		b = starts[sub+1]
	}
	c := buffer.ByteColFromVisual(line[a:b], col, v.Wrap.TabWidth())
	ls := s.LineStart(l)
	if !last && a+c == b && b > a {
		return max(ls+a, s.PrevGrapheme(ls+b))
	}
	return ls + a + c
}

// RowBounds returns the document offsets [start, end) of the visual row
// containing off and whether it is the last row of its line (end is then
// the line end).
func (v *View) RowBounds(s buffer.Snapshot, off int) (start, end int, last bool) {
	pos := s.OffsetToPos(off)
	starts, line := v.Segments(s, pos.Line)
	i, a, b := segment(starts, len(line), pos.Col)
	ls := s.LineStart(pos.Line)
	return ls + a, ls + b, i+1 >= len(starts)
}
