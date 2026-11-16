//go:build !cgo

package syntax

import (
	"bytes"
	"context"
	"sync"
)

// TreeSitter reports whether this build includes Tree-sitter (cgo). Without
// it, documents of Tree-sitter languages cannot be created (ErrNoTreeSitter).
const TreeSitter = false

// Document holds the source of one buffer. This build has no Tree-sitter
// (cgo disabled): only languages without a grammar (LangEnv, LangPlain) are
// supported, NewDocument fails with ErrNoTreeSitter for the others. All
// methods may be called from any goroutine.
type Document struct {
	lang Language

	mu     sync.Mutex
	src    []byte
	closed bool
}

// NewDocument creates a Document for lang. Without Tree-sitter it fails with
// ErrNoTreeSitter for languages that need a grammar.
func NewDocument(_ context.Context, lang Language, src []byte) (*Document, error) {
	if lang.HasTree() {
		return nil, ErrNoTreeSitter
	}
	return &Document{lang: lang, src: bytes.Clone(src)}, nil
}

// Language returns the document's language.
func (d *Document) Language() Language { return d.lang }

// Source returns the source text of the last Reparse; it must not be
// modified.
func (d *Document) Source() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.src
}

// Edit is a no-op without a syntax tree.
func (d *Document) Edit(InputEdit) {}

// Reparse stores the new text.
func (d *Document) Reparse(_ context.Context, src []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	d.src = bytes.Clone(src)
	if d.src == nil {
		d.src = []byte{}
	}
	return nil
}

// Close marks the document closed.
func (d *Document) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
}

// HasError is always false without a syntax tree.
func (d *Document) HasError() bool { return false }

// SExpr is always empty without a syntax tree.
func (d *Document) SExpr() string { return "" }

// Highlights returns spans that tile [start, end): dotenv files are
// tokenized, everything else is RolePlain.
func (d *Document) Highlights(start, end int) []Span {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	start = max(start, 0)
	end = min(end, len(d.src))
	if start >= end {
		return nil
	}
	if d.lang == LangEnv {
		return envHighlights(d.src, start, end)
	}
	return []Span{{Start: start, End: end, Role: RolePlain}}
}

// Folds returns nil without a syntax tree.
func (d *Document) Folds() []Range { return nil }

// Outline returns nil without a syntax tree.
func (d *Document) Outline() []Symbol { return nil }

// Matches fails with ErrNoTreeSitter.
func (d *Document) Matches(*Query) ([]Match, error) { return nil, ErrNoTreeSitter }

// Snapshot returns a copy of the current state.
func (d *Document) Snapshot() (*Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	return &Snapshot{lang: d.lang, src: d.src}, nil
}

// Query is a compiled Tree-sitter query; without Tree-sitter none can be
// created.
type Query struct {
	lang Language
}

// NewQuery fails with ErrNoTreeSitter.
func NewQuery(Language, string) (*Query, error) { return nil, ErrNoTreeSitter }

// Language returns the language the query was compiled for.
func (q *Query) Language() Language { return q.lang }

// CaptureNames returns nil.
func (q *Query) CaptureNames() []string { return nil }

// PatternCount returns 0.
func (q *Query) PatternCount() int { return 0 }

// PatternStart returns 0.
func (q *Query) PatternStart(int) int { return 0 }

// Close is a no-op.
func (q *Query) Close() {}

// Snapshot is an immutable copy of a Document's source.
type Snapshot struct {
	lang Language
	src  []byte
}

// Language returns the snapshot's language.
func (s *Snapshot) Language() Language { return s.lang }

// Source returns the snapshot's source text; it must not be modified.
func (s *Snapshot) Source() []byte { return s.src }

// Matches fails with ErrNoTreeSitter.
func (s *Snapshot) Matches(*Query) ([]Match, error) { return nil, ErrNoTreeSitter }

// Folds returns nil.
func (s *Snapshot) Folds() []Range { return nil }

// Outline returns nil.
func (s *Snapshot) Outline() []Symbol { return nil }

// Close is a no-op.
func (s *Snapshot) Close() {}
