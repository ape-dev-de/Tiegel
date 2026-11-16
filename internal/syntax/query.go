package syntax

import (
	"errors"
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Query is a compiled Tree-sitter query for one language. A Query is
// immutable and may be shared between goroutines; Close it when no longer
// needed.
type Query struct {
	lang  Language
	q     *ts.Query
	names []string
}

// NewQuery compiles a Tree-sitter query (S-expression syntax) for lang. Text
// predicates (#eq?, #match?, #any-of? and their negations) are supported.
func NewQuery(lang Language, source string) (*Query, error) {
	g := grammars[lang]
	if g == nil {
		return nil, fmt.Errorf("syntax: language %q has no grammar", lang)
	}
	q, qerr := ts.NewQuery(g.lang, source)
	if qerr != nil {
		return nil, fmt.Errorf("syntax: compile query: %w", qerr)
	}
	return &Query{lang: lang, q: q, names: q.CaptureNames()}, nil
}

// Language returns the language the query was compiled for.
func (q *Query) Language() Language { return q.lang }

// CaptureNames returns the capture names of the query, indexed by capture id.
func (q *Query) CaptureNames() []string { return q.names }

// PatternCount returns the number of patterns in the query.
func (q *Query) PatternCount() int { return int(q.q.PatternCount()) }

// PatternStart returns the byte offset in the query source where pattern i
// starts. Together with Match.Pattern it maps matches back to the part of a
// concatenated query they came from.
func (q *Query) PatternStart(i int) int { return int(q.q.StartByteForPattern(uint(i))) }

// Close releases the compiled query.
func (q *Query) Close() {
	if q != nil && q.q != nil {
		q.q.Close()
		q.q = nil
	}
}

// Capture is one captured node of a query match.
type Capture struct {
	Name      string
	Kind      string // node kind, e.g. "enum_declaration"
	StartByte int
	EndByte   int
	Start     Point
	End       Point
}

// Match is one query match with its captures in pattern order.
type Match struct {
	Pattern  int
	Captures []Capture
}

// errWrongLanguage is returned when a query is run against a tree of another
// language.
var errWrongLanguage = errors.New("syntax: query language does not match document")

// runQuery executes q on tree. text must cover the whole tree.
func runQuery(tree *ts.Tree, text []byte, lang Language, q *Query) ([]Match, error) {
	if q == nil || q.q == nil {
		return nil, errors.New("syntax: nil or closed query")
	}
	if q.lang != lang {
		return nil, errWrongLanguage
	}
	if tree == nil {
		return nil, nil
	}
	qc := ts.NewQueryCursor()
	defer qc.Close()
	var out []Match
	ms := qc.Matches(q.q, tree.RootNode(), text)
	for m := ms.Next(); m != nil; m = ms.Next() {
		mm := Match{Pattern: int(m.PatternIndex), Captures: make([]Capture, 0, len(m.Captures))}
		for _, c := range m.Captures {
			mm.Captures = append(mm.Captures, Capture{
				Name:      q.names[c.Index],
				Kind:      c.Node.Kind(),
				StartByte: int(c.Node.StartByte()),
				EndByte:   int(c.Node.EndByte()),
				Start:     fromTSPoint(c.Node.StartPosition()),
				End:       fromTSPoint(c.Node.EndPosition()),
			})
		}
		out = append(out, mm)
	}
	return out, nil
}

// Matches runs q against the current tree.
func (d *Document) Matches(q *Query) ([]Match, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	if d.tree == nil {
		return runQuery(nil, nil, d.lang, q)
	}
	return runQuery(d.tree, d.text(), d.lang, q)
}

// Snapshot is an immutable, independent copy of a Document's tree and
// source. It is meant to be handed to one other goroutine (e.g. a checker);
// a Snapshot itself must not be used from several goroutines at once.
// Close it to free the tree copy.
type Snapshot struct {
	lang Language
	tree *ts.Tree
	src  []byte
}

// Snapshot returns a copy of the current state. The tree copy is cheap
// (Tree-sitter trees are reference counted internally).
func (d *Document) Snapshot() (*Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	s := &Snapshot{lang: d.lang, src: d.src}
	if d.tree != nil {
		s.tree = d.tree.Clone()
		s.src = d.text()
	}
	return s, nil
}

// Language returns the snapshot's language.
func (s *Snapshot) Language() Language { return s.lang }

// Source returns the snapshot's source text; it must not be modified.
func (s *Snapshot) Source() []byte { return s.src }

// Matches runs q against the snapshot's tree.
func (s *Snapshot) Matches(q *Query) ([]Match, error) {
	return runQuery(s.tree, s.src, s.lang, q)
}

// Folds returns the folding regions of the snapshot.
func (s *Snapshot) Folds() []Range { return folds(s.tree, s.src, s.lang) }

// Outline returns the PHP structure of the snapshot.
func (s *Snapshot) Outline() []Symbol { return outline(s.tree, s.src, s.lang) }

// Close frees the tree copy. Further calls are no-ops.
func (s *Snapshot) Close() {
	if s.tree != nil {
		s.tree.Close()
		s.tree = nil
	}
}
