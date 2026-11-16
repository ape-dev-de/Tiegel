package syntax

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Point is a zero-based position: Line counts '\n'-separated lines, Column
// counts bytes from the start of the line (not runes).
type Point struct {
	Line   int
	Column int
}

// InputEdit describes a single text change, in the coordinates of the text
// before the change (start, old end) and after it (new end).
type InputEdit struct {
	StartByte   int
	OldEndByte  int
	NewEndByte  int
	StartPoint  Point
	OldEndPoint Point
	NewEndPoint Point
}

// NewInputEdit computes the InputEdit for replacing old[start:oldEnd] with
// inserted. old is the text before the change.
func NewInputEdit(old []byte, start, oldEnd int, inserted []byte) InputEdit {
	sp := PointAt(old, start)
	return InputEdit{
		StartByte:   start,
		OldEndByte:  oldEnd,
		NewEndByte:  start + len(inserted),
		StartPoint:  sp,
		OldEndPoint: PointAt(old, oldEnd),
		NewEndPoint: advance(sp, inserted),
	}
}

// PointAt returns the Point of byte offset off in src (clamped to len(src)).
func PointAt(src []byte, off int) Point {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	return advance(Point{}, src[:off])
}

func advance(p Point, text []byte) Point {
	n := bytes.Count(text, []byte{'\n'})
	if n == 0 {
		return Point{Line: p.Line, Column: p.Column + len(text)}
	}
	return Point{Line: p.Line + n, Column: len(text) - bytes.LastIndexByte(text, '\n') - 1}
}

func (e InputEdit) ts() *ts.InputEdit {
	return &ts.InputEdit{
		StartByte:      uint(e.StartByte),
		OldEndByte:     uint(e.OldEndByte),
		NewEndByte:     uint(e.NewEndByte),
		StartPosition:  e.StartPoint.ts(),
		OldEndPosition: e.OldEndPoint.ts(),
		NewEndPosition: e.NewEndPoint.ts(),
	}
}

func (p Point) ts() ts.Point { return ts.Point{Row: uint(p.Line), Column: uint(p.Column)} }

func fromTSPoint(p ts.Point) Point { return Point{Line: int(p.Row), Column: int(p.Column)} }

// Span is a highlighted byte range [Start, End) of the document.
type Span struct {
	Start int
	End   int
	Role  Role
}

// Range is an inclusive, zero-based line range, used for folding regions.
type Range struct {
	StartLine int
	EndLine   int
}

// readChunk is the maximum number of bytes handed to Tree-sitter per read.
const readChunk = 64 << 10

// cancelState carries the context of the running parse to the progress
// callback. Only accessed by the goroutine holding Document.parseMu (the
// callback runs synchronously inside the parse call).
type cancelState struct {
	ctx context.Context
}

// ErrClosed is returned by operations on a closed Document.
var ErrClosed = errors.New("syntax: document closed")

// Document holds the parse state of one buffer: the source, the Tree-sitter
// parser and the current syntax tree.
//
// Threading: Tree-sitter parsers and trees are not thread-safe. A Document
// owns its parser exclusively and serialises all access to the parser and the
// tree with internal mutexes, so all methods may be called from any
// goroutine. Reparse holds only the parser lock while parsing (it works on a
// private copy of the tree, see Tree.Clone), so Highlights, Folds, Outline and
// Edit stay responsive while a background parse is running. For long-running
// analyses on another goroutine use Snapshot, which hands out an independent
// tree copy.
type Document struct {
	lang Language
	g    *grammar // nil for languages without a Tree-sitter grammar

	parseMu sync.Mutex // guards parser, parseOpts and cancel
	parser  *ts.Parser
	// parseOpts is allocated once per document: go-tree-sitter v0.25 never
	// releases the registration of the options it passes to C, so a fresh
	// closure per parse would leak it. The callback only sees cancel.
	parseOpts *ts.ParseOptions
	cancel    *cancelState

	mu      sync.Mutex // guards everything below
	tree    *ts.Tree
	src     []byte
	pending []InputEdit // edits applied since the running Reparse started
	edited  bool        // Edit was called since the last successful Reparse
	closed  bool
}

// NewDocument creates a Document for lang and parses src synchronously.
// Languages without a grammar (LangEnv, LangPlain) never fail.
func NewDocument(ctx context.Context, lang Language, src []byte) (*Document, error) {
	d := &Document{lang: lang, g: grammars[lang]}
	if d.g != nil {
		if _, _, err := d.g.highlightQuery(); err != nil {
			return nil, err
		}
		d.parser = ts.NewParser()
		if err := d.parser.SetLanguage(d.g.lang); err != nil {
			d.parser.Close()
			return nil, fmt.Errorf("syntax: set language %s: %w", lang, err)
		}
	}
	if err := d.Reparse(ctx, src); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// Language returns the document's language.
func (d *Document) Language() Language { return d.lang }

// Source returns the source text of the last successful Reparse. The
// returned slice must not be modified.
func (d *Document) Source() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.src
}

// Edit informs the document about a text change so that the next Reparse
// can reuse unchanged parts of the tree. Call Edit for every change, in
// order, then Reparse with the new full text. Until Reparse completes,
// Highlights reports positions already shifted by the edit.
func (d *Document) Edit(e InputEdit) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.tree == nil {
		return
	}
	d.tree.Edit(e.ts())
	d.pending = append(d.pending, e)
	d.edited = true
}

// Reparse parses src (the complete new text) incrementally, reusing the
// previous tree as edited via Edit. Without preceding Edit calls the text is
// parsed from scratch (or not at all if it is unchanged). It is safe to call from a background
// goroutine; cancelling ctx aborts the parse and leaves the old tree in place.
// src is copied.
func (d *Document) Reparse(ctx context.Context, src []byte) error {
	src = bytes.Clone(src)
	if src == nil {
		src = []byte{}
	}
	if d.g == nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.closed {
			return ErrClosed
		}
		d.src = src
		return nil
	}

	d.parseMu.Lock()
	defer d.parseMu.Unlock()

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	edited := d.edited
	if !edited && d.tree != nil && bytes.Equal(src, d.src) {
		d.mu.Unlock()
		return nil // nothing changed
	}
	var old *ts.Tree
	// Without Edit calls the old tree does not describe src; reusing it
	// would produce a wrong tree, so parse from scratch.
	if d.tree != nil && edited {
		old = d.tree.Clone()
	}
	d.pending = nil
	d.edited = false
	d.mu.Unlock()

	n := len(src)
	var opts *ts.ParseOptions
	if ctx.Done() != nil {
		if d.parseOpts == nil {
			cs := &cancelState{}
			d.cancel = cs
			d.parseOpts = &ts.ParseOptions{ProgressCallback: func(ts.ParseState) bool {
				return cs.ctx != nil && cs.ctx.Err() != nil
			}}
		}
		d.cancel.ctx = ctx
		opts = d.parseOpts
	}
	tree := d.parser.ParseWithOptions(func(i int, _ ts.Point) []byte {
		// The binding copies every chunk into C memory; bounded chunks keep
		// that linear in the input size.
		if i < n {
			return src[i:min(n, i+readChunk)]
		}
		return nil
	}, old, opts)
	if d.cancel != nil {
		d.cancel.ctx = nil
	}
	if old != nil {
		old.Close()
	}
	if tree == nil {
		// Cancelled: discard the partial parse state; the edits applied to
		// the tree still need an incremental reparse.
		d.parser.Reset()
		d.mu.Lock()
		d.edited = d.edited || edited
		d.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("syntax: parse cancelled: %w", err)
		}
		return errors.New("syntax: parse failed")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		tree.Close()
		return ErrClosed
	}
	// Edits that arrived while parsing describe text newer than src; replay
	// them so positions stay consistent until the next Reparse.
	for _, e := range d.pending {
		tree.Edit(e.ts())
	}
	d.edited = len(d.pending) > 0
	if d.tree != nil {
		d.tree.Close()
	}
	d.tree, d.src = tree, src
	return nil
}

// Close releases the C memory of the parser and tree. It waits for a running
// Reparse to finish (cancel its context first for a fast close). Further
// calls are no-ops.
func (d *Document) Close() {
	d.parseMu.Lock()
	defer d.parseMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	if d.tree != nil {
		d.tree.Close()
		d.tree = nil
	}
	if d.parser != nil {
		d.parser.Close()
		d.parser = nil
	}
}

// HasError reports whether the current tree contains syntax errors.
func (d *Document) HasError() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tree == nil {
		return false
	}
	return d.tree.RootNode().HasError()
}

// SExpr returns the S-expression of the current tree (debugging aid). It is
// empty for languages without a grammar.
func (d *Document) SExpr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tree == nil {
		return ""
	}
	return d.tree.RootNode().ToSexp()
}

// text returns a source buffer that covers the whole tree. While edits are
// pending the tree may extend past the old source; the padding keeps
// predicate evaluation in bounds (results are corrected by the next Reparse).
// Caller holds d.mu.
func (d *Document) text() []byte {
	end := int(d.tree.RootNode().EndByte())
	if end <= len(d.src) {
		return d.src
	}
	t := make([]byte, end)
	copy(t, d.src)
	return t
}

// Highlights returns non-overlapping spans that exactly tile the byte range
// [start, end) (clamped to the document). Gaps are reported as RolePlain.
// When several captures cover the same byte, the innermost node wins; for
// identical nodes the query's precedence convention decides (see grammar).
func (d *Document) Highlights(start, end int) []Span {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	size := len(d.src)
	if d.tree != nil {
		size = max(size, int(d.tree.RootNode().EndByte()))
	}
	start = max(start, 0)
	end = min(end, size)
	if start >= end {
		return nil
	}
	if d.lang == LangEnv {
		return envHighlights(d.src, start, end)
	}
	if d.tree == nil {
		return []Span{{Start: start, End: end, Role: RolePlain}}
	}
	q, roles, _ := d.g.highlightQuery() // compiled successfully in NewDocument

	type capture struct {
		s, e    int
		pattern uint
		role    Role
	}
	var caps []capture
	qc := ts.NewQueryCursor()
	defer qc.Close()
	qc.SetByteRange(uint(start), uint(end))
	// Matches (not Captures): go-tree-sitter v0.25 leaks a C allocation per
	// QueryCaptures.Next call.
	matches := qc.Matches(q, d.tree.RootNode(), d.text())
	for m := matches.Next(); m != nil; m = matches.Next() {
		for _, c := range m.Captures {
			r := roles[c.Index]
			if r == RolePlain {
				continue
			}
			s, e := int(c.Node.StartByte()), int(c.Node.EndByte())
			if e <= start || s >= end || s == e {
				continue
			}
			caps = append(caps, capture{s: max(s, start), e: min(e, end), pattern: m.PatternIndex, role: r})
		}
	}
	// Paint outer (longer) captures first, then inner ones; equal ranges are
	// painted in pattern order so the winning pattern is painted last.
	firstWins := d.g.firstWins
	sort.SliceStable(caps, func(i, j int) bool {
		li, lj := caps[i].e-caps[i].s, caps[j].e-caps[j].s
		if li != lj {
			return li > lj
		}
		if firstWins {
			return caps[i].pattern > caps[j].pattern
		}
		return caps[i].pattern < caps[j].pattern
	})
	paint := make([]Role, end-start)
	for _, c := range caps {
		seg := paint[c.s-start : c.e-start]
		for i := range seg {
			seg[i] = c.role
		}
	}
	return compress(paint, start)
}

// compress turns a per-byte role slice into spans starting at offset base.
func compress(paint []Role, base int) []Span {
	var out []Span
	for i := 0; i < len(paint); {
		j := i + 1
		for j < len(paint) && paint[j] == paint[i] {
			j++
		}
		out = append(out, Span{Start: base + i, End: base + j, Role: paint[i]})
		i = j
	}
	return out
}
