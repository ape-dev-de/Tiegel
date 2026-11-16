package buffer

import "bytes"

// Pos is a position in the document: a 0-based line and a 0-based column
// measured in bytes from the start of the line.
type Pos struct {
	Line int
	Col  int
}

// Snapshot is an immutable view of the buffer content at one point in
// time. Taking a snapshot is O(1) and it never changes afterwards, so it is
// safe to hand to background goroutines (Tree-sitter, LSP, search) while the
// Buffer keeps being edited. The zero Snapshot is an empty document.
//
// Lines are separated by '\n'. A "\r\n" sequence counts as one line ending;
// the '\r' is not part of the line content returned by Line. A lone '\r' is
// treated as ordinary text.
type Snapshot struct {
	root    *node
	version uint64
	eol     EOL
}

// Version returns the edit counter of the buffer when the snapshot was
// taken. It increases with every change.
func (s Snapshot) Version() uint64 { return s.version }

// EOL returns the line ending style of the buffer.
func (s Snapshot) EOL() EOL { return s.eol }

// Len returns the document length in bytes.
func (s Snapshot) Len() int { return s.root.sz() }

// LineCount returns the number of lines; an empty document has one line.
func (s Snapshot) LineCount() int { return s.root.lf() + 1 }

// LineStart returns the offset of the first byte of line i. Out-of-range
// lines are clamped.
func (s Snapshot) LineStart(i int) int {
	if i <= 0 {
		return 0
	}
	if i > s.root.lf() {
		i = s.root.lf()
		if i == 0 {
			return 0
		}
	}
	return lineStart(s.root, i)
}

// LineEnd returns the offset just past the content of line i, i.e. the
// position of its line ending ("\n" or "\r\n") or the document end.
func (s Snapshot) LineEnd(i int) int {
	if i >= s.root.lf() {
		return s.Len()
	}
	start := s.LineStart(i)
	end := lineStart(s.root, max(i, 0)+1) - 1 // position of '\n'
	if end > start && byteAt(s.root, end-1) == '\r' {
		end--
	}
	return end
}

// LineLen returns the length in bytes of line i without its line ending.
func (s Snapshot) LineLen(i int) int { return s.LineEnd(i) - s.LineStart(i) }

// Line returns a copy of line i without its line ending. It returns nil
// for lines out of range.
func (s Snapshot) Line(i int) []byte {
	if i < 0 || i >= s.LineCount() {
		return nil
	}
	return s.AppendLine(make([]byte, 0, 64), i)
}

// AppendLine appends line i (without line ending) to dst and returns the
// extended slice. It allows allocation-free line access in render loops.
func (s Snapshot) AppendLine(dst []byte, i int) []byte {
	if i < 0 || i >= s.LineCount() {
		return dst
	}
	return s.appendRange(dst, s.LineStart(i), s.LineEnd(i))
}

// Slice returns a copy of the bytes in [start, end). The range is clamped
// to the document.
func (s Snapshot) Slice(start, end int) []byte {
	start, end = s.clampRange(start, end)
	return s.appendRange(make([]byte, 0, end-start), start, end)
}

// AppendSlice appends the bytes in [start, end) to dst.
func (s Snapshot) AppendSlice(dst []byte, start, end int) []byte {
	start, end = s.clampRange(start, end)
	return s.appendRange(dst, start, end)
}

func (s Snapshot) appendRange(dst []byte, start, end int) []byte {
	walk(s.root, 0, start, end, func(_ int, c []byte) bool {
		dst = append(dst, c...)
		return true
	})
	return dst
}

func (s Snapshot) clampRange(start, end int) (int, int) {
	n := s.Len()
	start = min(max(start, 0), n)
	end = min(max(end, start), n)
	return start, end
}

// Bytes returns a copy of the whole document.
func (s Snapshot) Bytes() []byte { return s.Slice(0, s.Len()) }

// String returns the whole document as a string.
func (s Snapshot) String() string { return string(s.Bytes()) }

// ByteAt returns the byte at off and whether off is inside the document.
func (s Snapshot) ByteAt(off int) (byte, bool) {
	if off < 0 || off >= s.Len() {
		return 0, false
	}
	return byteAt(s.root, off), true
}

// ForEachChunk calls fn with consecutive chunks covering [start, end) in
// document order until fn returns false. off is the document offset of the
// chunk. Chunks alias internal immutable storage and must not be modified.
func (s Snapshot) ForEachChunk(start, end int, fn func(off int, chunk []byte) bool) {
	start, end = s.clampRange(start, end)
	walk(s.root, 0, start, end, fn)
}

// ForEachChunkReverse is like ForEachChunk but walks from end to start.
func (s Snapshot) ForEachChunkReverse(start, end int, fn func(off int, chunk []byte) bool) {
	start, end = s.clampRange(start, end)
	walkRev(s.root, 0, start, end, fn)
}

// OffsetToPos converts a byte offset into a line/byte-column position.
// Offsets are clamped to [0, Len]. An offset between '\r' and '\n' yields
// a column one past the line's content length.
func (s Snapshot) OffsetToPos(off int) Pos {
	off = min(max(off, 0), s.Len())
	line := linesBefore(s.root, off)
	return Pos{Line: line, Col: off - s.LineStart(line)}
}

// PosToOffset converts a position into a byte offset. The line is clamped
// to the document and the column to the line's content length.
func (s Snapshot) PosToOffset(p Pos) int {
	line := min(max(p.Line, 0), s.LineCount()-1)
	start := s.LineStart(line)
	end := s.LineEnd(line)
	return start + min(max(p.Col, 0), end-start)
}

// Point converts a byte offset into a Tree-sitter point (row, byte column).
func (s Snapshot) Point(off int) Point {
	p := s.OffsetToPos(off)
	return Point{Row: p.Line, Column: p.Col}
}

// Index returns the offset of the first occurrence of sep at or after
// start, or -1. It is a convenience for small searches; package search
// offers the full feature set.
func (s Snapshot) Index(start int, sep []byte) int {
	if len(sep) == 0 {
		return min(max(start, 0), s.Len())
	}
	start = max(start, 0)
	if start > s.Len() {
		return -1
	}
	i := bytes.Index(s.Slice(start, s.Len()), sep)
	if i < 0 {
		return -1
	}
	return start + i
}
