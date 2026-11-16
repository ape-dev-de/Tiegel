// Package buffer implements the text buffer of the Tiegel editor: a piece
// table whose pieces live in a persistent, balanced treap. Every node
// caches the byte length and the number of line feeds of its subtree, so
// inserting, deleting and converting between offsets and line/column
// positions are O(log n) in the number of pieces.
//
// The original content is never modified and the add buffer only grows.
// Since tree nodes are never mutated either, a Snapshot is just a root
// pointer: O(1) to take and safe to read from other goroutines.
//
// A Buffer itself is not safe for concurrent use; all mutations must
// happen on one goroutine (typically the UI goroutine).
package buffer

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrOutOfRange is returned for edits outside the document.
var ErrOutOfRange = errors.New("offset out of range")

// Point is a Tree-sitter style position: 0-based row and byte column.
type Point struct {
	Row    int
	Column int
}

// Edit describes one change of the document. The fields mirror the input
// of Tree-sitter's ts_tree_edit, so syntax trees can be updated
// incrementally; Text and OldText allow LSP incremental sync.
type Edit struct {
	StartByte   int
	OldEndByte  int
	NewEndByte  int
	StartPoint  Point
	OldEndPoint Point
	NewEndPoint Point
	// Text is the inserted text. It aliases immutable buffer storage and
	// must not be modified.
	Text []byte
	// OldText is the removed text.
	OldText []byte
}

// Buffer is an editable text document with undo/redo history.
type Buffer struct {
	snap      Snapshot
	add       []byte
	addLfs    []int
	seed      uint64
	hist      history
	listeners []listener
	nextLis   int
}

type listener struct {
	id int
	fn func(Edit)
}

// New creates a buffer holding a copy of content. The line ending style is
// detected from the content.
func New(content []byte) *Buffer {
	b := &Buffer{seed: 0x9E3779B97F4A7C15}
	b.snap.eol = DetectEOL(content)
	if len(content) > 0 {
		orig := bytes.Clone(content)
		lfs := make([]int, 0, bytes.Count(orig, []byte{'\n'}))
		for i := 0; ; {
			j := bytes.IndexByte(orig[i:], '\n')
			if j < 0 {
				break
			}
			lfs = append(lfs, i+j)
			i += j + 1
		}
		p := piece{text: orig[:len(orig):len(orig)], lfs: lfs[:len(lfs):len(lfs)]}
		b.snap.root = mk(nil, nil, p, b.rand())
	}
	return b
}

// NewString is New for a string.
func NewString(content string) *Buffer { return New([]byte(content)) }

// rand returns a pseudo-random treap priority (xorshift64*).
func (b *Buffer) rand() uint32 {
	b.seed ^= b.seed >> 12
	b.seed ^= b.seed << 25
	b.seed ^= b.seed >> 27
	return uint32((b.seed * 2685821657736338717) >> 32)
}

// Snapshot returns an immutable view of the current content.
func (b *Buffer) Snapshot() Snapshot { return b.snap }

// Version returns a counter that increases with every change.
func (b *Buffer) Version() uint64 { return b.snap.version }

// EOL returns the detected line ending style.
func (b *Buffer) EOL() EOL { return b.snap.eol }

// SetEOL changes the line ending style used for new lines. Existing
// content is not converted.
func (b *Buffer) SetEOL(e EOL) { b.snap.eol = e }

// Len returns the document length in bytes.
func (b *Buffer) Len() int { return b.snap.Len() }

// LineCount returns the number of lines.
func (b *Buffer) LineCount() int { return b.snap.LineCount() }

// Line returns a copy of line i without its line ending.
func (b *Buffer) Line(i int) []byte { return b.snap.Line(i) }

// LineStart returns the offset of the first byte of line i.
func (b *Buffer) LineStart(i int) int { return b.snap.LineStart(i) }

// LineEnd returns the offset just past the content of line i.
func (b *Buffer) LineEnd(i int) int { return b.snap.LineEnd(i) }

// Slice returns a copy of the bytes in [start, end).
func (b *Buffer) Slice(start, end int) []byte { return b.snap.Slice(start, end) }

// Bytes returns a copy of the whole document.
func (b *Buffer) Bytes() []byte { return b.snap.Bytes() }

// String returns the whole document as a string.
func (b *Buffer) String() string { return b.snap.String() }

// OffsetToPos converts a byte offset into a line/byte-column position.
func (b *Buffer) OffsetToPos(off int) Pos { return b.snap.OffsetToPos(off) }

// PosToOffset converts a line/byte-column position into a byte offset.
func (b *Buffer) PosToOffset(p Pos) int { return b.snap.PosToOffset(p) }

// OnEdit registers fn to be called after every change, including changes
// made by Undo and Redo. The returned function unregisters it.
func (b *Buffer) OnEdit(fn func(Edit)) (remove func()) {
	b.nextLis++
	id := b.nextLis
	b.listeners = append(b.listeners, listener{id: id, fn: fn})
	return func() {
		for i, l := range b.listeners {
			if l.id == id {
				b.listeners = append(b.listeners[:i:i], b.listeners[i+1:]...)
				return
			}
		}
	}
}

// Insert inserts text at off.
func (b *Buffer) Insert(off int, text []byte) (Edit, error) { return b.Replace(off, 0, text) }

// Delete removes n bytes starting at off.
func (b *Buffer) Delete(off, n int) (Edit, error) { return b.Replace(off, n, nil) }

// Replace replaces the n bytes at off with text. The change is recorded in
// the undo history.
func (b *Buffer) Replace(off, n int, text []byte) (Edit, error) {
	if off < 0 || n < 0 || off+n > b.Len() {
		return Edit{}, fmt.Errorf("buffer: replace [%d,%d) in document of length %d: %w", off, off+n, b.Len(), ErrOutOfRange)
	}
	e := b.apply(off, n, text)
	if n > 0 || len(text) > 0 {
		b.hist.record(change{off: off, del: e.OldText, ins: e.Text})
	}
	return e, nil
}

// apply performs the change without touching the history.
func (b *Buffer) apply(off, n int, text []byte) Edit {
	s := b.snap
	e := Edit{
		StartByte:   off,
		OldEndByte:  off + n,
		NewEndByte:  off + len(text),
		StartPoint:  s.Point(off),
		OldEndPoint: s.Point(off + n),
	}
	if n == 0 && len(text) == 0 {
		e.NewEndPoint = e.StartPoint
		return e
	}
	e.OldText = s.Slice(off, off+n)
	left, rest := split(s.root, off)
	_, right := split(rest, n)
	if len(text) > 0 {
		left, e.Text = b.appendText(left, text)
	}
	b.snap.root = merge(left, right)
	b.snap.version++
	e.NewEndPoint = advance(e.StartPoint, e.Text)
	for _, l := range b.listeners {
		l.fn(e)
	}
	return e
}

// appendText stores text in the add buffer and appends a piece for it to
// t. Text typed right after the previous insertion extends that piece
// instead of creating a new one, which keeps the tree small.
func (b *Buffer) appendText(t *node, text []byte) (*node, []byte) {
	start, lfStart := len(b.add), len(b.addLfs)
	b.add = append(b.add, text...)
	for i := 0; ; {
		j := bytes.IndexByte(text[i:], '\n')
		if j < 0 {
			break
		}
		b.addLfs = append(b.addLfs, start+i+j)
		i += j + 1
	}
	end, lfEnd := len(b.add), len(b.addLfs)
	stored := b.add[start:end:end]
	if lp, ok := last(t); ok && lp.add && lp.base+len(lp.text) == start {
		from := lfStart - len(lp.lfs)
		np := piece{text: b.add[lp.base:end:end], lfs: b.addLfs[from:lfEnd:lfEnd], base: lp.base, add: true}
		return replaceLast(t, np), stored
	}
	np := piece{text: stored, lfs: b.addLfs[lfStart:lfEnd:lfEnd], base: start, add: true}
	return merge(t, mk(nil, nil, np, b.rand())), stored
}

// advance returns the point reached after inserting text at p.
func advance(p Point, text []byte) Point {
	n := bytes.Count(text, []byte{'\n'})
	if n == 0 {
		return Point{Row: p.Row, Column: p.Column + len(text)}
	}
	return Point{Row: p.Row + n, Column: len(text) - bytes.LastIndexByte(text, '\n') - 1}
}

// pieceCount returns the number of pieces (for tests and benchmarks).
func (b *Buffer) pieceCount() int { return count(b.snap.root) }
