// Package syntaxmod is the "syntax" module: it serves syntax highlighting,
// folding regions and the structure outline of documents over the API
// (syntax.highlights, syntax.folds, syntax.outline).
//
// The GUI in the same process can use internal/syntax directly; these
// methods exist for clients without Tree-sitter, above all a Windows UI built
// without cgo that talks to a Tiegel backend in WSL (decision D80). Open
// documents are parsed incrementally: doc.edited events are applied to a
// per-document syntax.Document and the tree is reparsed lazily on the next
// request. Files that are not open are parsed on demand.
//
// In a build without cgo the methods fail with api.CodeUnavailable for
// Tree-sitter languages (PHP, JSON, YAML, XML); dotenv and plain text still
// work.
package syntaxmod

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/langdetect"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// ID is the module ID.
const ID = "syntax"

// Module is the syntax module.
type Module struct {
	mu   sync.Mutex
	app  *api.App
	docs map[string]*entry // open documents by absolute path
}

// entry is the parse state of one open document.
type entry struct {
	lang syntax.Language
	doc  *syntax.Document
	// parsed is the buffer version doc was last parsed at; edited is the
	// version of the last doc.edited event applied with Document.Edit.
	parsed, edited uint64
}

// New creates the module.
func New() *Module { return &Module{} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string {
	return []string{"syntax.highlights", "syntax.folds", "syntax.outline"}
}

// Start implements module.Module.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("syntax: needs an api.App context")
	}
	m.mu.Lock()
	m.app, m.docs = app, map[string]*entry{}
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "syntax.highlights", "Syntax highlighting spans (byte offsets and roles) of a document or file", m.highlights)
	api.Add(p, "syntax.folds", "Folding regions (line ranges) of a document or file", m.folds)
	api.Add(p, "syntax.outline", "Structure outline (classes, functions, members) of a PHP document or file", m.outline)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(ctx.Bus().Subscribe("doc.*", m.onDoc))
	return nil
}

// Stop implements module.Module.
func (m *Module) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.docs {
		e.doc.Close()
	}
	m.docs = nil
	return nil
}

func (m *Module) onDoc(ev bus.Event) {
	switch p := ev.Payload.(type) {
	case api.DocEditEvent:
		m.mu.Lock()
		defer m.mu.Unlock()
		e, ok := m.docs[p.File]
		switch {
		case !ok || p.Version <= e.edited:
			// unknown document or already contained in the parsed text
		case p.Version != e.edited+1:
			// missed a change: start over on the next request
			e.doc.Close()
			delete(m.docs, p.File)
		default:
			e.doc.Edit(syntax.InputEdit{
				StartByte: p.Start, OldEndByte: p.OldEnd, NewEndByte: p.NewEnd,
				StartPoint:  syntax.Point{Line: p.StartPoint.Line, Column: p.StartPoint.Column},
				OldEndPoint: syntax.Point{Line: p.OldEndPoint.Line, Column: p.OldEndPoint.Column},
				NewEndPoint: syntax.Point{Line: p.NewEndPoint.Line, Column: p.NewEndPoint.Column},
			})
			e.edited = p.Version
		}
	case api.DocCloseResult:
		abs, err := m.app.ResolvePath(p.Path)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if e, ok := m.docs[abs]; ok {
			e.doc.Close()
			delete(m.docs, abs)
		}
	}
}

// Source selects the text a method works on: an open document or file
// (Path), or literal Text (Language or the extension of Path decide the
// language).
type Source struct {
	// Path of an open document or a file; relative to the project.
	Path string `json:"path,omitempty"`
	// Text, if set, is analysed instead of the document or file.
	Text *string `json:"text,omitempty"`
	// Language overrides detection: php, json, yaml, xml, dotenv (or env),
	// plaintext.
	Language string `json:"language,omitempty"`
}

// HighlightsParams are the params of syntax.highlights. Without a range the
// whole text is highlighted; Start/End (byte offsets) take precedence over
// StartLine/EndLine (1-based, inclusive).
type HighlightsParams struct {
	Source
	Start     *int `json:"start,omitempty"`
	End       *int `json:"end,omitempty"`
	StartLine int  `json:"startLine,omitempty"`
	EndLine   int  `json:"endLine,omitempty"`
	// Plain includes spans without highlighting (role "plain"), so that the
	// spans tile the range exactly.
	Plain bool `json:"plain,omitempty"`
}

// Span is one highlighted byte range [Start, End).
type Span struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Role  string `json:"role"`
}

// Info describes the analysed text.
type Info struct {
	// Path is the display path ("" for literal text).
	Path string `json:"path,omitempty"`
	// Language is the syntax language: php, json, yaml, xml, env or "" (plain).
	Language string `json:"language"`
	// Version is the buffer version of an open document (0 otherwise).
	Version uint64 `json:"version"`
	// Open reports whether an open document was analysed.
	Open bool `json:"open"`
	// Length is the text length in bytes.
	Length int `json:"length"`
}

// HighlightsResult is the result of syntax.highlights.
type HighlightsResult struct {
	Info
	// Start and End are the highlighted byte range.
	Start int    `json:"start"`
	End   int    `json:"end"`
	Spans []Span `json:"spans"`
}

// Fold is a foldable line range (1-based, inclusive).
type Fold struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
}

// FoldsResult is the result of syntax.folds.
type FoldsResult struct {
	Info
	Folds []Fold `json:"folds"`
}

// Position is a location in the text: byte offset and 1-based line and
// column (in characters, like api.Pos).
type Position struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Col    int `json:"col"`
}

// Symbol is one outline entry.
type Symbol struct {
	// Kind: namespace, class, interface, trait, enum, enum case, method,
	// function, property, constant.
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Detail    string   `json:"detail,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
	Start     Position `json:"start"`
	End       Position `json:"end"`
	// NameStart is where the name is, for navigation.
	NameStart Position `json:"nameStart"`
	Children  []Symbol `json:"children,omitempty"`
}

// OutlineResult is the result of syntax.outline.
type OutlineResult struct {
	Info
	Symbols []Symbol `json:"symbols"`
}

func (m *Module) highlights(ctx context.Context, p HighlightsParams) (HighlightsResult, error) {
	var res HighlightsResult
	err := m.with(ctx, p.Source, &res.Info, func(d *syntax.Document, src []byte, lines *lineIndex) error {
		start, end := 0, len(src)
		switch {
		case p.Start != nil || p.End != nil:
			if p.Start != nil {
				start = *p.Start
			}
			if p.End != nil {
				end = *p.End
			}
			if start < 0 || end > len(src) || start > end {
				return api.Errorf(api.CodeInvalidParams, "range %d..%d outside of 0..%d", start, end, len(src))
			}
		case p.StartLine > 0 || p.EndLine > 0:
			if p.StartLine > 0 {
				start = lines.lineStart(p.StartLine - 1)
			}
			if p.EndLine > 0 {
				end = lines.lineStart(p.EndLine)
			}
			if start > end {
				return api.Errorf(api.CodeInvalidParams, "startLine after endLine")
			}
		}
		res.Start, res.End = start, end
		res.Spans = []Span{}
		for _, s := range d.Highlights(start, end) {
			if s.Role == syntax.RolePlain && !p.Plain {
				continue
			}
			res.Spans = append(res.Spans, Span{Start: s.Start, End: s.End, Role: s.Role.String()})
		}
		return nil
	})
	return res, err
}

func (m *Module) folds(ctx context.Context, p Source) (FoldsResult, error) {
	var res FoldsResult
	err := m.with(ctx, p, &res.Info, func(d *syntax.Document, _ []byte, _ *lineIndex) error {
		res.Folds = []Fold{}
		for _, r := range d.Folds() {
			res.Folds = append(res.Folds, Fold{StartLine: r.StartLine + 1, EndLine: r.EndLine + 1})
		}
		return nil
	})
	return res, err
}

func (m *Module) outline(ctx context.Context, p Source) (OutlineResult, error) {
	var res OutlineResult
	err := m.with(ctx, p, &res.Info, func(d *syntax.Document, src []byte, lines *lineIndex) error {
		res.Symbols = convertSymbols(d.Outline(), src, lines)
		return nil
	})
	return res, err
}

func convertSymbols(in []syntax.Symbol, src []byte, lines *lineIndex) []Symbol {
	out := make([]Symbol, 0, len(in))
	for _, s := range in {
		out = append(out, Symbol{
			Kind: s.Kind.String(), Name: s.Name, Detail: s.Detail, Modifiers: s.Modifiers,
			Start:     lines.position(src, s.Start),
			End:       lines.position(src, s.End),
			NameStart: lines.position(src, s.NameStart),
			Children:  childSymbols(s.Children, src, lines),
		})
	}
	return out
}

func childSymbols(in []syntax.Symbol, src []byte, lines *lineIndex) []Symbol {
	if len(in) == 0 {
		return nil
	}
	return convertSymbols(in, src, lines)
}

// with resolves the source, gets a parsed document for it and runs fn.
func (m *Module) with(ctx context.Context, s Source, info *Info, fn func(*syntax.Document, []byte, *lineIndex) error) error {
	if s.Path == "" && s.Text == nil {
		return api.Errorf(api.CodeInvalidParams, "path or text is required")
	}
	lang, langSet, err := parseLanguage(s.Language)
	if err != nil {
		return err
	}
	if s.Text != nil {
		src := []byte(*s.Text)
		if !langSet {
			lang = syntax.DetectLanguage(s.Path, head(src))
		}
		if s.Path != "" {
			info.Path = s.Path
		}
		if err := available(lang); err != nil {
			return err
		}
		d, err := syntax.NewDocument(ctx, lang, src)
		if err != nil {
			return parseError(err)
		}
		defer d.Close()
		info.Language, info.Length = string(lang), len(src)
		return fn(d, src, newLineIndex(src))
	}

	abs, err := m.app.ResolvePath(s.Path)
	if err != nil {
		return err
	}
	info.Path = m.app.DisplayPath(abs)
	if doc, derr := m.app.Document(abs); derr == nil {
		snap := doc.Snapshot()
		if !langSet {
			lang = fromLangdetect(snap.Language)
		}
		if err := available(lang); err != nil {
			return err
		}
		info.Language, info.Version, info.Open, info.Length = string(lang), snap.Version, true, len(snap.Text)
		m.mu.Lock()
		defer m.mu.Unlock()
		d, err := m.docLocked(ctx, abs, lang, snap)
		if err != nil {
			return err
		}
		return fn(d, snap.Text, newLineIndex(snap.Text))
	}
	src, _, err := m.app.ReadText(abs)
	if err != nil {
		return err
	}
	if !langSet {
		lang = syntax.DetectLanguage(abs, head(src))
	}
	if err := available(lang); err != nil {
		return err
	}
	d, err := syntax.NewDocument(ctx, lang, src)
	if err != nil {
		return parseError(err)
	}
	defer d.Close()
	info.Language, info.Length = string(lang), len(src)
	return fn(d, src, newLineIndex(src))
}

// docLocked returns the parse state of an open document, brought up to the
// snapshot's version: reused, reparsed incrementally (all edits up to the
// version were applied from doc.edited) or parsed from scratch.
func (m *Module) docLocked(ctx context.Context, abs string, lang syntax.Language, snap api.DocSnapshot) (*syntax.Document, error) {
	if m.docs == nil {
		return nil, api.Errorf(api.CodeModuleDisabled, "syntax module stopped")
	}
	e, ok := m.docs[abs]
	if ok && e.lang == lang {
		switch {
		case e.parsed == snap.Version:
			return e.doc, nil
		case e.edited == snap.Version:
			if err := e.doc.Reparse(ctx, snap.Text); err != nil {
				return nil, parseError(err)
			}
			e.parsed = snap.Version
			return e.doc, nil
		}
	}
	if ok {
		e.doc.Close()
		delete(m.docs, abs)
	}
	d, err := syntax.NewDocument(ctx, lang, snap.Text)
	if err != nil {
		return nil, parseError(err)
	}
	m.docs[abs] = &entry{lang: lang, doc: d, parsed: snap.Version, edited: snap.Version}
	return d, nil
}

func available(lang syntax.Language) error {
	if lang.HasTree() && !syntax.TreeSitter {
		return api.Errorf(api.CodeUnavailable, "%s: %w", lang, syntax.ErrNoTreeSitter)
	}
	return nil
}

func parseError(err error) error {
	if errors.Is(err, syntax.ErrNoTreeSitter) {
		return api.Errorf(api.CodeUnavailable, "%w", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return api.Errorf(api.CodeFailed, "parse: %w", err)
}

func head(src []byte) []byte { return src[:min(len(src), langdetect.HeadSize)] }

// parseLanguage maps a language parameter to a syntax language.
func parseLanguage(s string) (syntax.Language, bool, error) {
	switch s {
	case "":
		return syntax.LangPlain, false, nil
	case "plaintext", "plain", "text":
		return syntax.LangPlain, true, nil
	case "dotenv", "env":
		return syntax.LangEnv, true, nil
	case "php", "json", "yaml", "xml":
		return syntax.Language(s), true, nil
	}
	return "", false, api.Errorf(api.CodeInvalidParams, "unknown language %q (php, json, yaml, xml, dotenv, plaintext)", s)
}

// fromLangdetect maps the language of an open document to a syntax language.
func fromLangdetect(l string) syntax.Language {
	switch langdetect.Language(l) {
	case langdetect.PHP:
		return syntax.LangPHP
	case langdetect.JSON:
		return syntax.LangJSON
	case langdetect.YAML:
		return syntax.LangYAML
	case langdetect.XML:
		return syntax.LangXML
	case langdetect.DotEnv:
		return syntax.LangEnv
	}
	return syntax.LangPlain
}

// lineIndex maps zero-based lines to byte offsets.
type lineIndex struct {
	starts []int
	size   int
}

func newLineIndex(src []byte) *lineIndex {
	li := &lineIndex{starts: []int{0}, size: len(src)}
	for i := 0; ; {
		j := bytes.IndexByte(src[i:], '\n')
		if j < 0 {
			break
		}
		i += j + 1
		li.starts = append(li.starts, i)
	}
	return li
}

// lineStart returns the offset of zero-based line (the text length past the
// last line).
func (li *lineIndex) lineStart(line int) int {
	if line < 0 {
		return 0
	}
	if line >= len(li.starts) {
		return li.size
	}
	return li.starts[line]
}

// position converts a zero-based line/byte-column point.
func (li *lineIndex) position(src []byte, p syntax.Point) Position {
	ls := li.lineStart(p.Line)
	off := min(ls+p.Column, len(src))
	return Position{Offset: off, Line: p.Line + 1, Col: utf8.RuneCount(src[ls:off]) + 1}
}
