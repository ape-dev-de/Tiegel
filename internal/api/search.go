package api

import (
	"context"
	"errors"
	"os"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
)

// SearchParams are the params of "search.inDoc". If the file is not open as
// a document, it is searched on disk.
type SearchParams struct {
	Path          string `json:"path"`
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	WholeWord     bool   `json:"wholeWord,omitempty"`
	// Limit caps the number of matches (default 1000).
	Limit int `json:"limit,omitempty"`
}

// SearchMatch is one hit. Line and Col are 1-based (Col in characters).
type SearchMatch struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Text     string `json:"text"`
	LineText string `json:"lineText"`
}

// SearchResult is the result of "search.inDoc".
type SearchResult struct {
	Path    string        `json:"path"`
	Version uint64        `json:"version,omitempty"`
	Matches []SearchMatch `json:"matches"`
	// Limited is true if more matches exist than were returned.
	Limited bool `json:"limited,omitempty"`
}

// ReplaceParams are the params of "search.replaceInDoc".
type ReplaceParams struct {
	SearchParams
	Replacement string `json:"replacement"`
}

// ReplaceResult is the result of "search.replaceInDoc".
type ReplaceResult struct {
	DocState
	Replaced int `json:"replaced"`
}

func (a *App) registerSearch() {
	mustHandle(a, "search.inDoc", "Find matches in a document or file (text/regex, case, whole word)", a.searchInDoc)
	mustHandle(a, "search.replaceInDoc", "Replace all matches in an open document (one undo step)", a.replaceInDoc)
}

func (p SearchParams) compile() (*search.Query, error) {
	q, err := search.Compile(p.Pattern, search.Options{CaseSensitive: p.CaseSensitive, WholeWord: p.WholeWord, Regex: p.Regex})
	if err != nil {
		return nil, Errorf(CodeInvalidParams, "pattern: %w", err)
	}
	return q, nil
}

func (a *App) searchInDoc(_ context.Context, p SearchParams) (SearchResult, error) {
	q, err := p.compile()
	if err != nil {
		return SearchResult{}, err
	}
	if p.Limit <= 0 {
		p.Limit = 1000
	}
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return SearchResult{}, err
	}
	var buf *buffer.Buffer
	var version uint64
	if d, err := a.doc(abs); err == nil {
		d.mu.Lock()
		buf = buffer.New(d.buf.Bytes())
		version = d.buf.Version()
		d.mu.Unlock()
	} else {
		var nf *Error
		if !errors.As(err, &nf) || nf.Code != CodeNotFound {
			return SearchResult{}, err
		}
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return SearchResult{}, notFoundOr(rerr, "search")
		}
		buf = buffer.New(data)
	}
	matches := q.FindAll(buf.Snapshot(), p.Limit+1)
	res := SearchResult{Path: a.displayPath(abs), Version: version, Matches: make([]SearchMatch, 0, len(matches))}
	if len(matches) > p.Limit {
		matches = matches[:p.Limit]
		res.Limited = true
	}
	for _, m := range matches {
		pos := buf.OffsetToPos(m.Start)
		line := buf.Line(pos.Line)
		res.Matches = append(res.Matches, SearchMatch{
			Start:    m.Start,
			End:      m.End,
			Line:     pos.Line + 1,
			Col:      buffer.RuneCol(line, pos.Col) + 1,
			Text:     string(buf.Slice(m.Start, m.End)),
			LineText: string(line),
		})
	}
	return res, nil
}

func (a *App) replaceInDoc(_ context.Context, p ReplaceParams) (ReplaceResult, error) {
	q, err := p.compile()
	if err != nil {
		return ReplaceResult{}, err
	}
	var n int
	st, err := a.edit(p.Path, nil, func(b *buffer.Buffer) error {
		b.BreakUndo()
		var rerr error
		n, rerr = search.ReplaceAll(b, q, p.Replacement)
		if rerr != nil {
			return Errorf(CodeFailed, "replace: %w", rerr)
		}
		return nil
	})
	return ReplaceResult{DocState: st, Replaced: n}, err
}
