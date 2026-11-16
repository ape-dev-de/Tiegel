// Package tshighlight adapts internal/syntax (Tree-sitter) to the editor's
// highlight.Highlighter interface.
//
// A Highlighter follows one buffer.Buffer: every buffer edit is recorded on
// the UI goroutine (buffer.OnEdit) and handed to a background goroutine that
// applies it to the syntax.Document (syntax.InputEdit) and re-parses
// incrementally, debounced. Spans answers from a small cache on the UI
// goroutine; until the first parse has finished it reports no spans (plain
// text). Between an edit and the next completed parse, cached spans are
// shifted through the pending edits, so colors stay attached to their text
// while typing. Folding ranges are computed after a parse once the document
// is idle (no newer edits) and handed out via TakeFolds.
//
// Whenever new highlights or folds are available the Invalidate callback is
// called from the background goroutine so the host can request a redraw
// (e.g. app.Window.Invalidate).
//
// The package does not import Gio.
package tshighlight

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// DefaultDebounce is the quiet time after the last edit before a re-parse
// starts.
const DefaultDebounce = 40 * time.Millisecond

// windowBytes is the size of the byte window highlighted per cache fill;
// one fill covers a full screen of ordinary code.
const windowBytes = 16 << 10

// windowBefore extends a cache fill backwards so small upward scrolls hit.
const windowBefore = 2 << 10

// detectHead is the number of leading bytes used for language detection.
const detectHead = 512

// Options configures a Highlighter.
type Options struct {
	// Path is the file path used for language detection.
	Path string
	// Language overrides detection when non-empty.
	Language syntax.Language
	// Debounce is the quiet time before a re-parse; zero means
	// DefaultDebounce.
	Debounce time.Duration
	// Invalidate is called (from a background goroutine) when new
	// highlights or folding ranges are available. It must not block.
	Invalidate func()
}

// queued is an edit not yet applied to the document.
type queued struct {
	ie      syntax.InputEdit
	start   int
	oldEnd  int
	newEnd  int
	version uint64 // buffer version after the edit
}

// spanCache holds highlight spans for a byte window of the latest snapshot.
type spanCache struct {
	valid      bool
	gen        uint64
	version    uint64
	start, end int
	spans      []highlight.Span // never mutated in place (callers may alias)
}

// Highlighter implements highlight.Highlighter on top of a syntax.Document.
// Spans, TakeFolds and Close must be called on the goroutine that edits the
// buffer (the UI goroutine); all other methods are safe from any goroutine.
type Highlighter struct {
	buf        *buffer.Buffer
	lang       syntax.Language
	debounce   time.Duration
	invalidate func()
	unsub      func()

	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}

	mu         sync.Mutex
	doc        *syntax.Document // nil until the first parse finished
	err        error            // fatal error (document could not be created)
	latest     buffer.Snapshot
	queue      []queued // edits after docVersion, not yet given to doc
	docVersion uint64   // version whose coordinates doc reports
	parsed     uint64   // version of the last completed parse
	ready      bool
	gen        uint64 // bumped when doc results change
	cache      spanCache
	folds      []fold.Range
	foldsVer   uint64 // version folds describes (valid if foldsSet)
	foldsSet   bool
	taken      []fold.Range // last result of TakeFolds
	takenOnce  bool
	changed    chan struct{} // closed and replaced after every worker step
	closed     bool
}

// New returns a highlighter for buf and starts parsing in the background.
// It must be called on the goroutine that edits buf. Call Close when done.
// For languages without highlighting (syntax.LangPlain) no goroutine is
// started and Spans always returns nil.
func New(buf *buffer.Buffer, opts Options) *Highlighter {
	h := &Highlighter{
		buf:        buf,
		lang:       opts.Language,
		debounce:   opts.Debounce,
		invalidate: opts.Invalidate,
		latest:     buf.Snapshot(),
		changed:    make(chan struct{}),
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	if h.debounce <= 0 {
		h.debounce = DefaultDebounce
	}
	if h.invalidate == nil {
		h.invalidate = func() {}
	}
	if h.lang == syntax.LangPlain {
		h.lang = syntax.DetectLanguage(opts.Path, h.latest.Slice(0, min(detectHead, h.latest.Len())))
	}
	if h.lang == syntax.LangPlain {
		close(h.done)
		return h
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.unsub = buf.OnEdit(h.onEdit)
	go h.run(ctx)
	return h
}

// Language returns the language in use (LangPlain: no highlighting).
func (h *Highlighter) Language() syntax.Language { return h.lang }

// Ready reports whether the first parse has finished.
func (h *Highlighter) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready
}

// Err returns the error that stopped highlighting, if any.
func (h *Highlighter) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// Close stops the background goroutine, releases the syntax tree and
// detaches from the buffer. Further calls are no-ops.
func (h *Highlighter) Close() {
	if h.unsub != nil {
		h.unsub()
		h.unsub = nil
	}
	if h.cancel != nil {
		h.cancel()
	}
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	if h.doc != nil {
		h.doc.Close()
		h.doc = nil
	}
	h.cache = spanCache{}
}

// onEdit runs on the UI goroutine for every buffer change.
func (h *Highlighter) onEdit(e buffer.Edit) {
	snap := h.buf.Snapshot()
	q := queued{
		ie: syntax.InputEdit{
			StartByte:   e.StartByte,
			OldEndByte:  e.OldEndByte,
			NewEndByte:  e.NewEndByte,
			StartPoint:  syntax.Point{Line: e.StartPoint.Row, Column: e.StartPoint.Column},
			OldEndPoint: syntax.Point{Line: e.OldEndPoint.Row, Column: e.OldEndPoint.Column},
			NewEndPoint: syntax.Point{Line: e.NewEndPoint.Row, Column: e.NewEndPoint.Column},
		},
		start: e.StartByte, oldEnd: e.OldEndByte, newEnd: e.NewEndByte,
		version: snap.Version(),
	}
	h.mu.Lock()
	h.queue = append(h.queue, q)
	h.latest = snap
	if c := &h.cache; c.valid {
		// Keep colors attached to their text until the next parse.
		c.spans = shiftSpans(c.spans, q)
		c.start, c.end = mapStart(c.start, q), mapEnd(c.end, q)
		c.version = snap.Version()
	}
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Spans implements highlight.Highlighter. s must be the buffer's current
// snapshot; for any other snapshot (and before the first parse) it returns
// nil, i.e. plain text.
func (h *Highlighter) Spans(s buffer.Snapshot, start, end int) []highlight.Span {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.doc == nil || s.Version() != h.latest.Version() || end <= start {
		return nil
	}
	c := &h.cache
	if !c.valid || c.gen != h.gen || c.version != s.Version() || start < c.start || end > c.end {
		h.fill(s, start, end)
	}
	return window(c.spans, start, end)
}

// fill recomputes the cache for a window around [start, end). Caller holds
// h.mu.
func (h *Highlighter) fill(s buffer.Snapshot, start, end int) {
	ws := max(0, start-windowBefore)
	we := min(s.Len(), max(end, start+windowBytes))
	// Map the window back into document coordinates (before the queued
	// edits), query, and shift the result forward again.
	a, b := ws, we
	for i := len(h.queue) - 1; i >= 0; i-- {
		q := h.queue[i]
		a, b = unmapStart(a, q), unmapEnd(b, q)
	}
	var out []highlight.Span
	for _, sp := range h.doc.Highlights(a, b) {
		if r := Role(sp.Role); r != highlight.Plain {
			out = append(out, highlight.Span{Start: sp.Start, End: sp.End, Role: r})
		}
	}
	for _, q := range h.queue {
		out = shiftSpans(out, q)
	}
	h.cache = spanCache{valid: true, gen: h.gen, version: s.Version(), start: ws, end: we, spans: out}
}

// window returns the spans overlapping [start, end) (a sub-slice).
func window(spans []highlight.Span, start, end int) []highlight.Span {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].End > start })
	j := i
	for j < len(spans) && spans[j].Start < end {
		j++
	}
	return spans[i:j:j]
}

// mapStart maps a position through an edit like ts_tree_edit does:
// positions after the removed text shift, positions up to the edit start
// stay, positions inside the removed text move to the end of the inserted
// text. At a pure insertion point positions shift.
func mapStart(p int, q queued) int {
	switch {
	case p >= q.oldEnd:
		return p + q.newEnd - q.oldEnd
	case p <= q.start:
		return p
	}
	return q.newEnd
}

// mapEnd maps an end position; it is the same mapping as mapStart, so a span
// ending exactly at an insertion point grows to include the inserted text.
func mapEnd(p int, q queued) int { return mapStart(p, q) }

// unmapStart maps a position after the edit back to before it.
func unmapStart(p int, q queued) int {
	switch {
	case p < q.start:
		return p
	case p >= q.newEnd:
		return p - (q.newEnd - q.oldEnd)
	}
	return q.start
}

// unmapEnd is unmapStart for window ends (rounds inserted text outwards).
func unmapEnd(p int, q queued) int {
	switch {
	case p <= q.start:
		return p
	case p >= q.newEnd:
		return p - (q.newEnd - q.oldEnd)
	}
	return q.oldEnd
}

// shiftSpans returns a new slice with spans mapped through q.
func shiftSpans(spans []highlight.Span, q queued) []highlight.Span {
	out := make([]highlight.Span, 0, len(spans))
	for _, sp := range spans {
		sp.Start, sp.End = mapStart(sp.Start, q), mapEnd(sp.End, q)
		if sp.End > sp.Start {
			out = append(out, sp)
		}
	}
	return out
}

// TakeFolds returns the folding ranges of the latest parse once, if they
// are newer than the last call and still describe the current text.
func (h *Highlighter) TakeFolds() ([]fold.Range, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.foldsSet || h.foldsVer != h.latest.Version() {
		return nil, false
	}
	if h.takenOnce && slices.Equal(h.folds, h.taken) {
		return nil, false
	}
	h.taken, h.takenOnce = h.folds, true
	return slices.Clone(h.folds), true
}

// Folds returns the most recent folding ranges and the buffer version they
// describe (ok is false before the first parse).
func (h *Highlighter) Folds() (rs []fold.Range, version uint64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.foldsSet {
		return nil, 0, false
	}
	return slices.Clone(h.folds), h.foldsVer, true
}

// Idle reports whether the latest buffer version has been parsed and its
// folds computed.
func (h *Highlighter) Idle() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.idleLocked()
}

func (h *Highlighter) idleLocked() bool {
	return h.err != nil || h.closed || h.ready && h.parsed == h.latest.Version() && h.foldsSet && h.foldsVer == h.parsed
}

// ErrPlain is returned by Wait for languages without highlighting.
var ErrPlain = errors.New("tshighlight: no highlighting for this language")

// Wait blocks until the highlighter is idle (see Idle) or ctx is done.
func (h *Highlighter) Wait(ctx context.Context) error {
	if h.lang == syntax.LangPlain {
		return ErrPlain
	}
	for {
		h.mu.Lock()
		if h.idleLocked() {
			err := h.err
			h.mu.Unlock()
			return err
		}
		ch := h.changed
		h.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// run is the background goroutine.
func (h *Highlighter) run(ctx context.Context) {
	defer close(h.done)
	if !h.step(ctx) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		}
		// Debounce: wait until no edit arrived for h.debounce.
		t := time.NewTimer(h.debounce)
	quiet:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-h.wake:
				t.Reset(h.debounce)
			case <-t.C:
				break quiet
			}
		}
		if !h.step(ctx) {
			return
		}
	}
}

// step brings the document up to date with the latest snapshot. It reports
// false when the goroutine should stop.
func (h *Highlighter) step(ctx context.Context) bool {
	defer h.signal()
	h.mu.Lock()
	doc, snap := h.doc, h.latest
	if doc == nil {
		h.mu.Unlock()
		d, err := syntax.NewDocument(ctx, h.lang, snap.Bytes())
		h.mu.Lock()
		defer h.mu.Unlock()
		if err != nil {
			if ctx.Err() == nil {
				h.err = err
			}
			return false
		}
		if h.closed {
			d.Close()
			return false
		}
		h.doc, h.docVersion, h.parsed, h.ready = d, snap.Version(), snap.Version(), true
		h.dropQueue(snap.Version())
		h.gen++
		h.updateFoldsLocked()
		return true
	}

	if !h.lang.HasTree() {
		// No incremental tree: re-reading the text is cheap, do it under
		// the lock so coordinates never disagree with the queue.
		defer h.mu.Unlock()
		if err := doc.Reparse(ctx, snap.Bytes()); err != nil {
			return ctx.Err() == nil
		}
		h.docVersion, h.parsed = snap.Version(), snap.Version()
		h.dropQueue(snap.Version())
		h.gen++
		h.updateFoldsLocked()
		return true
	}

	// Hand all queued edits to the tree; from now on the document reports
	// coordinates of snap.
	for _, q := range h.queue {
		doc.Edit(q.ie)
	}
	h.queue = h.queue[:0]
	h.docVersion = snap.Version()
	if h.parsed == snap.Version() {
		h.mu.Unlock()
		return true
	}
	h.mu.Unlock()

	err := doc.Reparse(ctx, snap.Bytes())
	if ctx.Err() != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil || h.closed {
		return !h.closed // retried with the next edit
	}
	h.parsed = snap.Version()
	h.gen++
	h.updateFoldsLocked()
	return true
}

// signal wakes Wait callers and the host.
func (h *Highlighter) signal() {
	h.mu.Lock()
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
	h.invalidate()
}

// dropQueue removes queued edits already contained in version v.
func (h *Highlighter) dropQueue(v uint64) {
	h.queue = slices.DeleteFunc(h.queue, func(q queued) bool { return q.version <= v })
}

// updateFoldsLocked recomputes folds when the document is idle (the parse
// matches the latest text). Caller holds h.mu.
func (h *Highlighter) updateFoldsLocked() {
	if h.parsed != h.latest.Version() || h.docVersion != h.parsed {
		return // folds are delayed until typing pauses
	}
	rs := h.doc.Folds()
	out := make([]fold.Range, 0, len(rs))
	for _, r := range rs {
		out = append(out, fold.Range{StartLine: r.StartLine, EndLine: r.EndLine})
	}
	h.folds, h.foldsVer, h.foldsSet = out, h.parsed, true
}
