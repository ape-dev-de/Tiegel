package buffer

import "slices"

// piece references an immutable byte range of either the original or the
// add buffer. text and lfs are full-slice-expression sub-slices, so neither
// can ever be appended to through a piece.
type piece struct {
	text []byte // the bytes of this piece
	lfs  []int  // absolute source positions of every '\n' inside text
	base int    // absolute source position of text[0]
	add  bool   // true if text lives in the add buffer
}

// lines returns the number of line feeds inside the piece.
func (p piece) lines() int { return len(p.lfs) }

// split cuts the piece at relative offset r (0 < r < len(text)).
func (p piece) split(r int) (piece, piece) {
	idx, _ := slices.BinarySearch(p.lfs, p.base+r)
	left := piece{text: p.text[:r:r], lfs: p.lfs[:idx:idx], base: p.base, add: p.add}
	right := piece{text: p.text[r:], lfs: p.lfs[idx:], base: p.base + r, add: p.add}
	return left, right
}

// lfsBefore returns how many line feeds lie in text[:r].
func (p piece) lfsBefore(r int) int {
	idx, _ := slices.BinarySearch(p.lfs, p.base+r)
	return idx
}

// node is a node of a persistent (path-copying) treap ordered by document
// position. Nodes are never mutated after construction, which makes any
// root pointer an immutable snapshot that is safe for concurrent readers.
type node struct {
	left, right *node
	p           piece
	prio        uint32
	size        int // bytes in subtree
	lines       int // line feeds in subtree
}

func (n *node) sz() int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *node) lf() int {
	if n == nil {
		return 0
	}
	return n.lines
}

// mk builds a new node and computes its aggregates.
func mk(left, right *node, p piece, prio uint32) *node {
	return &node{
		left:  left,
		right: right,
		p:     p,
		prio:  prio,
		size:  left.sz() + right.sz() + len(p.text),
		lines: left.lf() + right.lf() + p.lines(),
	}
}

// split returns two trees: the first holds the first off bytes, the second
// the rest. Pieces straddling off are cut in two.
func split(t *node, off int) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	// Splitting at either end needs no copies.
	if off <= 0 {
		return nil, t
	}
	if off >= t.size {
		return t, nil
	}
	ls := t.left.sz()
	if off <= ls {
		a, b := split(t.left, off)
		return a, mk(b, t.right, t.p, t.prio)
	}
	r := off - ls
	pl := len(t.p.text)
	if r >= pl {
		a, b := split(t.right, r-pl)
		return mk(t.left, a, t.p, t.prio), b
	}
	pa, pb := t.p.split(r)
	// Both halves inherit t's priority; the heap property still holds
	// because t's priority dominates both of its former subtrees.
	return mk(t.left, nil, pa, t.prio), mk(nil, t.right, pb, t.prio)
}

// merge concatenates two trees (all of a before all of b).
func merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio >= b.prio {
		return mk(a.left, merge(a.right, b), a.p, a.prio)
	}
	return mk(merge(a, b.left), b.right, b.p, b.prio)
}

// last returns the rightmost piece of t.
func last(t *node) (piece, bool) {
	if t == nil {
		return piece{}, false
	}
	for t.right != nil {
		t = t.right
	}
	return t.p, true
}

// replaceLast path-copies the right spine and swaps the last piece.
func replaceLast(t *node, p piece) *node {
	if t.right == nil {
		return mk(t.left, nil, p, t.prio)
	}
	return mk(t.left, replaceLast(t.right, p), t.p, t.prio)
}

// lineStart returns the byte offset at which line i (0-based) starts. The
// caller guarantees 0 < i <= t.lines.
func lineStart(t *node, i int) int {
	off := 0
	for t != nil {
		ll := t.left.lf()
		if i <= ll {
			t = t.left
			continue
		}
		i -= ll
		off += t.left.sz()
		if i <= t.p.lines() {
			return off + t.p.lfs[i-1] - t.p.base + 1
		}
		i -= t.p.lines()
		off += len(t.p.text)
		t = t.right
	}
	return off
}

// linesBefore returns the number of line feeds in the first off bytes.
func linesBefore(t *node, off int) int {
	n := 0
	for t != nil {
		ls := t.left.sz()
		if off < ls {
			t = t.left
			continue
		}
		off -= ls
		n += t.left.lf()
		if off < len(t.p.text) {
			return n + t.p.lfsBefore(off)
		}
		off -= len(t.p.text)
		n += t.p.lines()
		t = t.right
	}
	return n
}

// byteAt returns the byte at off; the caller guarantees 0 <= off < size.
func byteAt(t *node, off int) byte {
	for {
		ls := t.left.sz()
		if off < ls {
			t = t.left
			continue
		}
		off -= ls
		if off < len(t.p.text) {
			return t.p.text[off]
		}
		off -= len(t.p.text)
		t = t.right
	}
}

// walk calls fn for every non-empty chunk intersecting [start, end) in
// document order. base is the document offset of t's first byte. It
// returns false if fn stopped the iteration.
func walk(t *node, base, start, end int, fn func(off int, chunk []byte) bool) bool {
	if t == nil || start >= end || end <= base || start >= base+t.size {
		return true
	}
	ls := t.left.sz()
	if !walk(t.left, base, start, end, fn) {
		return false
	}
	ps := base + ls
	pe := ps + len(t.p.text)
	if s, e := max(start, ps), min(end, pe); s < e {
		if !fn(s, t.p.text[s-ps:e-ps:e-ps]) {
			return false
		}
	}
	return walk(t.right, pe, start, end, fn)
}

// walkRev is walk in reverse document order.
func walkRev(t *node, base, start, end int, fn func(off int, chunk []byte) bool) bool {
	if t == nil || start >= end || end <= base || start >= base+t.size {
		return true
	}
	ls := t.left.sz()
	ps := base + ls
	pe := ps + len(t.p.text)
	if !walkRev(t.right, pe, start, end, fn) {
		return false
	}
	if s, e := max(start, ps), min(end, pe); s < e {
		if !fn(s, t.p.text[s-ps:e-ps:e-ps]) {
			return false
		}
	}
	return walkRev(t.left, base, start, end, fn)
}

// count returns the number of nodes (pieces) in t.
func count(t *node) int {
	if t == nil {
		return 0
	}
	return 1 + count(t.left) + count(t.right)
}

// depth returns the height of t.
func depth(t *node) int {
	if t == nil {
		return 0
	}
	return 1 + max(depth(t.left), depth(t.right))
}
