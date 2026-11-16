package buffer

import (
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// The functions in this file convert between column units within a single
// line. line is the line content (as returned by Line) and byte columns
// are offsets into it. Columns beyond the line are clamped.

// RuneCol converts a byte column into a column counted in runes.
func RuneCol(line []byte, byteCol int) int {
	return utf8.RuneCount(line[:clampCol(line, byteCol)])
}

// ByteColFromRunes converts a rune column into a byte column.
func ByteColFromRunes(line []byte, runeCol int) int {
	i := 0
	for n := 0; n < runeCol && i < len(line); n++ {
		_, size := utf8.DecodeRune(line[i:])
		i += size
	}
	return i
}

// UTF16Col converts a byte column into UTF-16 code units, the default
// column unit of the Language Server Protocol.
func UTF16Col(line []byte, byteCol int) int {
	line = line[:clampCol(line, byteCol)]
	n := 0
	for len(line) > 0 {
		r, size := utf8.DecodeRune(line)
		line = line[size:]
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// ByteColFromUTF16 converts a UTF-16 column into a byte column.
func ByteColFromUTF16(line []byte, col int) int {
	i, n := 0, 0
	for n < col && i < len(line) {
		r, size := utf8.DecodeRune(line[i:])
		i += size
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return i
}

// GraphemeCol converts a byte column into a column counted in grapheme
// clusters (user-perceived characters). A byte column inside a cluster
// counts that cluster.
func GraphemeCol(line []byte, byteCol int) int {
	byteCol = clampCol(line, byteCol)
	n, pos, state := 0, 0, -1
	rest := line
	for pos < byteCol && len(rest) > 0 {
		var c []byte
		c, rest, _, state = uniseg.FirstGraphemeCluster(rest, state)
		pos += len(c)
		n++
	}
	return n
}

// ByteColFromGraphemes converts a grapheme column into a byte column.
func ByteColFromGraphemes(line []byte, col int) int {
	pos, state := 0, -1
	rest := line
	for n := 0; n < col && len(rest) > 0; n++ {
		var c []byte
		c, rest, _, state = uniseg.FirstGraphemeCluster(rest, state)
		pos += len(c)
	}
	return pos
}

// VisualCol returns the display column of byteCol in a monospace grid:
// tabs advance to the next multiple of tabWidth and wide (East Asian,
// emoji) clusters take two cells.
func VisualCol(line []byte, byteCol, tabWidth int) int {
	byteCol = clampCol(line, byteCol)
	if tabWidth <= 0 {
		tabWidth = 4
	}
	v, pos, state := 0, 0, -1
	rest := line
	for pos < byteCol && len(rest) > 0 {
		var c []byte
		var w int
		c, rest, w, state = uniseg.FirstGraphemeCluster(rest, state)
		pos += len(c)
		v = advanceVisual(v, c, w, tabWidth)
	}
	return v
}

// ByteColFromVisual returns the byte column of the cluster boundary
// closest to (but not after) the display column vcol.
func ByteColFromVisual(line []byte, vcol, tabWidth int) int {
	if tabWidth <= 0 {
		tabWidth = 4
	}
	v, pos, state := 0, 0, -1
	rest := line
	for len(rest) > 0 {
		var c []byte
		var w int
		c, rest, w, state = uniseg.FirstGraphemeCluster(rest, state)
		nv := advanceVisual(v, c, w, tabWidth)
		if nv > vcol {
			break
		}
		v = nv
		pos += len(c)
	}
	return pos
}

func advanceVisual(v int, cluster []byte, width, tabWidth int) int {
	if len(cluster) == 1 && cluster[0] == '\t' {
		return (v/tabWidth + 1) * tabWidth
	}
	return v + width
}

func clampCol(line []byte, col int) int { return min(max(col, 0), len(line)) }

// graphemeWindow bounds how many bytes are inspected around an offset when
// looking for grapheme boundaries.
const graphemeWindow = 1024

// NextGrapheme returns the offset of the next grapheme cluster boundary
// after off. Line endings ("\n" and "\r\n") count as one step.
func (s Snapshot) NextGrapheme(off int) int {
	off = min(max(off, 0), s.Len())
	line := linesBefore(s.root, off)
	end := s.LineEnd(line)
	if off >= end {
		if line >= s.LineCount()-1 {
			return s.Len()
		}
		return s.LineStart(line + 1)
	}
	win := s.Slice(off, min(end, off+graphemeWindow))
	c, _, _, _ := uniseg.FirstGraphemeCluster(win, -1)
	return off + len(c)
}

// PrevGrapheme returns the offset of the grapheme cluster boundary before
// off. Line endings count as one step.
func (s Snapshot) PrevGrapheme(off int) int {
	off = min(max(off, 0), s.Len())
	line := linesBefore(s.root, off)
	start := s.LineStart(line)
	if off <= start {
		if line == 0 {
			return 0
		}
		return s.LineEnd(line - 1)
	}
	if end := s.LineEnd(line); off > end {
		return end // between '\r' and '\n'
	}
	from := max(start, off-graphemeWindow)
	for from < off && !utf8.RuneStart(byteAt(s.root, from)) {
		from++
	}
	win := s.Slice(from, off)
	pos, prev, state := 0, 0, -1
	for len(win) > 0 {
		var c []byte
		c, win, _, state = uniseg.FirstGraphemeCluster(win, state)
		prev = pos
		pos += len(c)
	}
	return from + prev
}
