// Package wrap models soft wrapping: it splits document lines into
// visual rows of a fixed width (in monospace cells) and maps between
// document lines, document offsets and visual rows. It knows no GUI; the
// editor widget asks it which rows to draw and where a caret is.
//
// Row counts are cached per line and kept up to date incrementally from
// buffer edits, so typing re-wraps only the edited lines. Lines are
// grouped into blocks of 64 with cached block sums; mapping a row to a
// line is a binary search over the blocks plus a short scan.
package wrap

import (
	"bytes"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// MinWidth is the smallest wrap width in cells.
const MinWidth = 8

// Breaks appends to dst the byte offsets in line at which the
// continuation rows start when line is wrapped at width cells, and
// returns the extended slice (empty for a line that fits).
//
// Every row starts at visual column 0: tabs advance to the next multiple
// of tabWidth counted from the row start, wide clusters take two cells
// (the same grid as buffer.VisualCol). A row is broken after the last
// space or tab that fits; a word longer than the row is broken at the
// last grapheme cluster that fits. Each row holds at least one cluster.
func Breaks(dst []int, line []byte, width, tabWidth int) []int {
	width = max(width, 1)
	if tabWidth <= 0 {
		tabWidth = 4
	}
	if len(line) <= width && !hasTab(line) {
		return dst
	}
	if plainASCII(line) {
		return asciiBreaks(dst, line, width)
	}
	return clusterBreaks(dst, line, width, tabWidth)
}

// plainASCII reports whether line holds only printable ASCII (one cell
// per byte, no tabs, no combining marks).
func plainASCII(line []byte) bool {
	for _, c := range line {
		if c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// asciiBreaks is Breaks for plainASCII lines: whole rows are skipped
// with a backwards search for the last space.
func asciiBreaks(dst []int, line []byte, width int) []int {
	rowStart := 0
	for len(line)-rowStart > width {
		lim := rowStart + width
		brk := lim
		if i := bytes.LastIndexByte(line[rowStart:lim], ' '); i >= 0 {
			brk = rowStart + i + 1
		}
		dst = append(dst, brk)
		rowStart = brk
	}
	return dst
}

// clusterBreaks is Breaks for any line, cluster by cluster.
func clusterBreaks(dst []int, line []byte, width, tabWidth int) []int {
	rowStart, col, lastWS := 0, 0, -1
	for i := 0; i < len(line); {
		var n, w int
		var ws bool
		if c := line[i]; c >= 0x20 && c < 0x7f && (i+1 == len(line) || line[i+1] < utf8.RuneSelf) {
			n, w, ws = 1, 1, c == ' ' // printable ASCII not followed by a combining mark
		} else {
			n, w, ws = clusterAt(line, i, col, tabWidth)
		}
		if col+w > width && i > rowStart {
			brk := i
			if lastWS > rowStart && lastWS < i {
				brk = lastWS
			}
			dst = append(dst, brk)
			rowStart, lastWS = brk, -1
			col = rowWidth(line[brk:i], tabWidth)
			continue // re-check the cluster at i on the new row
		}
		col += w
		i += n
		if ws {
			lastWS = i
		}
	}
	return dst
}

// RowCount returns the number of rows of line wrapped at width cells
// (len(Breaks)+1) without allocating for lines that fit.
func RowCount(line []byte, width, tabWidth int, scratch []int) (int, []int) {
	if len(line) <= max(width, 1) && !hasTab(line) {
		return 1, scratch
	}
	scratch = Breaks(scratch[:0], line, width, tabWidth)
	return len(scratch) + 1, scratch
}

func hasTab(b []byte) bool { return slices.Contains(b, '\t') }

// clusterAt returns the byte length, the cell width at column col and
// whether the cluster at line[i:] is a space or tab.
func clusterAt(line []byte, i, col, tabWidth int) (n, w int, ws bool) {
	c := line[i]
	if c == '\t' {
		return 1, (col/tabWidth+1)*tabWidth - col, true
	}
	if c < utf8.RuneSelf && (i+1 == len(line) || line[i+1] < utf8.RuneSelf) {
		if c < 0x20 || c == 0x7f {
			return 1, 0, false
		}
		return 1, 1, c == ' '
	}
	cl, _, w, _ := uniseg.FirstGraphemeCluster(line[i:], -1)
	return len(cl), w, false
}

// rowWidth returns the cell width of seg drawn from column 0.
func rowWidth(seg []byte, tabWidth int) int {
	col := 0
	for i := 0; i < len(seg); {
		n, w, _ := clusterAt(seg, i, col, tabWidth)
		col += w
		i += n
	}
	return col
}

// Enabled reports whether soft wrap applies to the file name for a mode
// of the editor.softWrap setting: "all" wraps every file, "files" the
// files whose base name matches one of patterns (path.Match syntax,
// case-insensitive, e.g. "*.md"), anything else none.
func Enabled(mode string, patterns []string, name string) bool {
	switch mode {
	case "all":
		return true
	case "files":
		base := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
		for _, p := range patterns {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if ok, err := path.Match(p, base); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// blockShift sets the block size (64 lines) of the row sums.
const blockShift = 6

// Model caches the row count of every document line for one wrap width
// and tab width. It is not safe for concurrent use.
type Model struct {
	width, tab int
	valid      bool
	version    uint64

	rows  []int32 // rows per document line (≥ 1)
	blk   []int32 // rows per block of 64 lines
	pre   []int   // pre[b] = rows in blocks [0, b); valid if preOK
	preOK bool

	brk  []int
	line []byte
}

// Valid reports whether the model was computed for width and tabWidth.
func (m *Model) Valid(width, tabWidth int) bool {
	return m.valid && m.width == max(width, MinWidth) && m.tab == tabWidth
}

// Width returns the wrap width in cells.
func (m *Model) Width() int { return m.width }

// TabWidth returns the tab width the model was computed for.
func (m *Model) TabWidth() int { return m.tab }

// Version changes whenever row counts change.
func (m *Model) Version() uint64 { return m.version }

// Invalidate forgets all row counts; the next Reset recomputes them.
func (m *Model) Invalidate() { m.valid = false }

// Reset computes the rows of every line of s for width (at least
// MinWidth) and tabWidth cells. It scans the document once; only lines
// that may be longer than the row are measured exactly.
func (m *Model) Reset(s buffer.Snapshot, width, tabWidth int) {
	width = max(width, MinWidth)
	if tabWidth <= 0 {
		tabWidth = 4
	}
	m.width, m.tab, m.valid = width, tabWidth, true
	m.version++
	n := s.LineCount()
	m.rows = slices.Grow(m.rows[:0], n)[:n]
	line, length, tabs := 0, 0, false
	finish := func() {
		r := int32(1)
		if length > width || (tabs && length*tabWidth > width) {
			r = int32(m.measure(s, line))
		}
		m.rows[line] = r
		line++
		length, tabs = 0, false
	}
	s.ForEachChunk(0, s.Len(), func(_ int, chunk []byte) bool {
		for len(chunk) > 0 {
			i := bytes.IndexByte(chunk, '\n')
			seg := chunk
			if i >= 0 {
				seg = chunk[:i]
			}
			length += len(seg)
			tabs = tabs || bytes.IndexByte(seg, '\t') >= 0
			if i < 0 {
				return true
			}
			if line < n {
				finish()
			}
			chunk = chunk[i+1:]
		}
		return true
	})
	for line < n {
		finish()
	}
	m.rebuildBlocks(0)
}

// measure returns the exact row count of document line l.
func (m *Model) measure(s buffer.Snapshot, l int) int {
	m.line = s.AppendLine(m.line[:0], l)
	var r int
	r, m.brk = RowCount(m.line, m.width, m.tab, m.brk)
	return r
}

// rebuildBlocks recomputes the block sums from the block of line from.
func (m *Model) rebuildBlocks(from int) {
	nb := (len(m.rows) + (1 << blockShift) - 1) >> blockShift
	b0 := min(from>>blockShift, len(m.blk))
	m.blk = slices.Grow(m.blk[:b0], nb-b0)[:nb]
	for b := b0; b < nb; b++ {
		lo, hi := b<<blockShift, min((b+1)<<blockShift, len(m.rows))
		var sum int32
		for _, r := range m.rows[lo:hi] {
			sum += r
		}
		m.blk[b] = sum
	}
	m.preOK = false
}

// ApplyEdit updates the row counts after a buffer edit; s is the snapshot
// after the edit. Only the edited lines are measured again.
func (m *Model) ApplyEdit(s buffer.Snapshot, e buffer.Edit) {
	if !m.valid {
		return
	}
	start, oldEnd, newEnd := e.StartPoint.Row, e.OldEndPoint.Row, e.NewEndPoint.Row
	if start < 0 || oldEnd >= len(m.rows) || len(m.rows)-(oldEnd-start)+(newEnd-start) != s.LineCount() {
		m.valid = false // out of sync (missed edit); recomputed by the next Reset
		return
	}
	m.version++
	if oldEnd == newEnd {
		for l := start; l <= newEnd; l++ {
			r := int32(m.measure(s, l))
			if d := r - m.rows[l]; d != 0 {
				m.rows[l] = r
				m.blk[l>>blockShift] += d
				m.preOK = false
			}
		}
		return
	}
	fresh := make([]int32, newEnd-start+1)
	for i := range fresh {
		fresh[i] = int32(m.measure(s, start+i))
	}
	m.rows = slices.Replace(m.rows, start, oldEnd+1, fresh...)
	m.rebuildBlocks(start)
}

func (m *Model) prefix() []int {
	if !m.preOK {
		m.pre = slices.Grow(m.pre[:0], len(m.blk)+1)[:len(m.blk)+1]
		m.pre[0] = 0
		for b, v := range m.blk {
			m.pre[b+1] = m.pre[b] + int(v)
		}
		m.preOK = true
	}
	return m.pre
}

// LineCount returns the number of lines the model knows.
func (m *Model) LineCount() int { return len(m.rows) }

// Rows returns the number of rows of document line l (1 outside the
// document).
func (m *Model) Rows(l int) int {
	if l < 0 || l >= len(m.rows) {
		return 1
	}
	return int(m.rows[l])
}

// Total returns the number of rows of the whole document.
func (m *Model) Total() int {
	p := m.prefix()
	return p[len(p)-1]
}

// Before returns the number of rows of the lines [0, l).
func (m *Model) Before(l int) int {
	l = min(max(l, 0), len(m.rows))
	b := l >> blockShift
	sum := m.prefix()[b]
	for _, r := range m.rows[b<<blockShift : l] {
		sum += int(r)
	}
	return sum
}

// LineAt returns the document line containing row and the row index
// within that line. Rows are clamped to [0, Total).
func (m *Model) LineAt(row int) (line, sub int) {
	if len(m.rows) == 0 {
		return 0, 0
	}
	p := m.prefix()
	row = min(max(row, 0), p[len(p)-1]-1)
	// Last block whose start is at or before row.
	b := max(0, sort.Search(len(p), func(i int) bool { return p[i] > row })-1)
	b = min(b, len(m.blk)-1)
	acc := p[b]
	for l := b << blockShift; l < len(m.rows); l++ {
		r := int(m.rows[l])
		if row < acc+r {
			return l, row - acc
		}
		acc += r
	}
	last := len(m.rows) - 1
	return last, int(m.rows[last]) - 1
}
