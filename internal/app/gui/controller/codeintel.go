package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// LSP features of CodeNav: Find Usages, highlight usages, context actions,
// reformat and rename, plus the structure of a file.

// errNoServer is the notice for LSP features without a running server.
const errNoServer = "needs a language server (none is running)"

// Usages is the result of Find Usages, grouped by file.
type Usages struct {
	// Symbol is the word the search started on.
	Symbol string
	Groups []UsageGroup
	Total  int
}

// UsageGroup are the usages in one file.
type UsageGroup struct {
	Path, File string
	Items      []Usage
}

// Usage is one reference with the text of its line.
type Usage struct {
	Location
	LineText string
}

// usage is an entry of lsp.references.
type usage struct {
	Location
	LineText string `json:"lineText"`
}

// FindUsages lists the references of the symbol at cur (without the
// declaration) in Usages, grouped by file in the order of the server.
func (n *CodeNav) FindUsages(cur Location, symbol string) {
	c := n.c
	if !c.Status.LSPRunning {
		c.Status.Notice = "Find Usages " + errNoServer
		return
	}
	c.Go(func(ctx context.Context) func() {
		var us []usage
		decl := false
		err := c.call(ctx, "lsp.references", map[string]any{"path": cur.File, "at": pos{Offset: cur.Range.Start.Offset}, "includeDeclaration": decl}, &us)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			res := &Usages{Symbol: symbol, Total: len(us)}
			idx := map[string]int{}
			for _, u := range us {
				l := c.normLoc(u.Location)
				i, ok := idx[l.File]
				if !ok {
					i = len(res.Groups)
					idx[l.File] = i
					res.Groups = append(res.Groups, UsageGroup{Path: l.Path, File: l.File})
				}
				res.Groups[i].Items = append(res.Groups[i].Items, Usage{Location: l, LineText: strings.TrimRight(u.LineText, "\r\n")})
			}
			if len(us) == 0 {
				c.Status.Notice = fmt.Sprintf("No usages of %s found", orStr(symbol, "the symbol"))
			}
			n.Usages = res
			n.UsagesVersion++
		}
	})
}

// HighlightRange is a usage of the symbol under the caret in the open
// document (byte offsets).
type HighlightRange struct {
	Start, End int
	// Write marks a write access.
	Write bool
}

// Highlights are the usages of the symbol under the caret of one
// document version.
type Highlights struct {
	File    string
	Version uint64
	Offset  int
	Ranges  []HighlightRange
}

// Highlight asks the language server for the usages of the symbol at off
// in file (document version version); the answer replaces Highlights.
func (n *CodeNav) Highlight(file string, off int, version uint64) {
	c := n.c
	n.hlSeq++
	seq := n.hlSeq
	if !c.Status.LSPRunning {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var hs []struct {
			Range TextRange `json:"range"`
			Kind  string    `json:"kind"`
		}
		err := c.call(ctx, "lsp.documentHighlight", posParams{Path: file, At: pos{Offset: off}}, &hs)
		return func() {
			if seq != n.hlSeq {
				return
			}
			h := Highlights{File: file, Version: version, Offset: off}
			if err == nil {
				for _, x := range hs {
					h.Ranges = append(h.Ranges, HighlightRange{Start: x.Range.Start.Offset, End: x.Range.End.Offset, Write: x.Kind == "write"})
				}
			}
			n.Highlights = h
			n.HighlightsVersion++
		}
	})
}

// ClearHighlights drops the highlights (caret moved away).
func (n *CodeNav) ClearHighlights() {
	n.hlSeq++
	if n.Highlights.File != "" {
		n.Highlights = Highlights{}
		n.HighlightsVersion++
	}
}

// CodeAction is an offered context action.
type CodeAction struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Preferred bool   `json:"preferred"`
	// Disabled is the reason why it cannot be applied.
	Disabled string `json:"disabled"`
}

// CodeActions are the context actions at a position.
type CodeActions struct {
	File  string
	Items []CodeAction
}

// LoadCodeActions asks for the context actions of [start, end) in file
// (Alt+Enter); preferred actions come first.
func (n *CodeNav) LoadCodeActions(file string, start, end int) {
	n.loadCodeActions(file, start, end, false)
}

// LoadCodeActionsWithLocal is LoadCodeActions when the window adds
// actions of its own (e.g. Fix with AI for a PHPStan finding): Actions is
// set even when the language server offers none or does not run.
func (n *CodeNav) LoadCodeActionsWithLocal(file string, start, end int) {
	n.loadCodeActions(file, start, end, true)
}

func (n *CodeNav) loadCodeActions(file string, start, end int, local bool) {
	c := n.c
	if !c.Status.LSPRunning {
		if local {
			n.Actions = &CodeActions{File: file}
			n.ActionsVersion++
			return
		}
		c.Status.Notice = "Context actions " + errNoServer
		return
	}
	c.Go(func(ctx context.Context) func() {
		var as []CodeAction
		p := map[string]any{"path": file, "start": pos{Offset: start}}
		if end > start {
			p["end"] = pos{Offset: end}
		}
		err := c.call(ctx, "lsp.codeActions", p, &as)
		return func() {
			if err != nil && !local {
				c.notice(err)
				return
			}
			if len(as) == 0 && !local {
				c.Status.Notice = "No context actions here"
				return
			}
			sort.SliceStable(as, func(i, j int) bool { return as[i].Preferred && !as[j].Preferred })
			n.Actions = &CodeActions{File: file, Items: as}
			n.ActionsVersion++
		}
	})
}

// ApplyCodeAction runs action i of Actions and closes the list.
func (n *CodeNav) ApplyCodeAction(i int) {
	as := n.Actions
	n.CloseCodeActions()
	if as == nil || i < 0 || i >= len(as.Items) {
		return
	}
	a := as.Items[i]
	if a.Disabled != "" {
		n.c.Status.Notice = a.Title + ": " + a.Disabled
		return
	}
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Applied bool `json:"applied"`
		}
		err := c.call(ctx, "lsp.applyCodeAction", map[string]string{"id": a.ID}, &r)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = "Applied: " + a.Title
		}
	})
}

// CloseCodeActions closes the action list.
func (n *CodeNav) CloseCodeActions() {
	if n.Actions != nil {
		n.Actions = nil
		n.ActionsVersion++
	}
}

// Reformat formats file through the language server (PSR-12 defaults):
// the range [start, end) if it is not empty, otherwise the whole file.
// The edits are one undo step of the open document.
func (n *CodeNav) Reformat(file string, start, end, tabSize int) {
	c := n.c
	if !c.Status.LSPRunning {
		c.Status.Notice = "Reformat " + errNoServer
		return
	}
	method := "lsp.formatting"
	p := map[string]any{"path": file}
	if tabSize > 0 {
		p["tabSize"] = tabSize
	}
	if end > start {
		method = "lsp.rangeFormatting"
		p["start"], p["end"] = pos{Offset: start}, pos{Offset: end}
	}
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Edits   []TextEdit `json:"edits"`
			Applied bool       `json:"applied"`
		}
		err := c.call(ctx, method, p, &r)
		return func() {
			switch {
			case err != nil:
				c.notice(err)
			case len(r.Edits) == 0:
				c.Status.Notice = "Already formatted"
			default:
				c.Status.Notice = fmt.Sprintf("Reformatted (%d changes)", len(r.Edits))
			}
		}
	})
}

// Rename stages.
const (
	RenameInput   = iota // asking for the new name
	RenamePreview        // showing the planned changes
)

// RenameState is a running Rename Symbol.
type RenameState struct {
	File   string
	Offset int
	// Placeholder is the current name.
	Placeholder string
	NewName     string
	Stage       int
	Preview     []RenameFile
	// Busy is true while a request runs.
	Busy bool
}

// RenameFile is a planned change of one file.
type RenameFile struct {
	Kind, Path, NewPath string
	Edits               []TextEdit
}

// Changes counts the planned text changes.
func (r *RenameState) Changes() int {
	n := 0
	for _, f := range r.Preview {
		n += max(len(f.Edits), 1)
	}
	return n
}

// StartRename checks that the symbol at cur can be renamed and asks for
// the new name (RenameState stage RenameInput). word is the fallback
// placeholder.
func (n *CodeNav) StartRename(cur Location, word string) {
	c := n.c
	if !c.Status.LSPRunning {
		c.Status.Notice = "Rename " + errNoServer
		return
	}
	c.Go(func(ctx context.Context) func() {
		var r struct {
			CanRename   bool       `json:"canRename"`
			Range       *TextRange `json:"range"`
			Placeholder string     `json:"placeholder"`
		}
		err := c.call(ctx, "lsp.prepareRename", posParams{Path: cur.File, At: pos{Offset: cur.Range.Start.Offset}}, &r)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			if !r.CanRename {
				c.Status.Notice = "This symbol cannot be renamed"
				return
			}
			n.Rename = &RenameState{File: cur.File, Offset: cur.Range.Start.Offset, Placeholder: orStr(r.Placeholder, word), Stage: RenameInput}
			n.RenameVersion++
		}
	})
}

// PreviewRename asks for the changes of renaming to name (stage
// RenamePreview).
func (n *CodeNav) PreviewRename(name string) {
	rs := n.Rename
	if rs == nil || strings.TrimSpace(name) == "" {
		return
	}
	rs.NewName, rs.Busy = strings.TrimSpace(name), true
	n.RenameVersion++
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Preview []RenameFile `json:"preview"`
		}
		err := c.call(ctx, "lsp.rename", map[string]any{"path": rs.File, "at": pos{Offset: rs.Offset}, "newName": rs.NewName, "preview": true}, &r)
		return func() {
			if n.Rename != rs {
				return
			}
			rs.Busy = false
			if err != nil {
				c.notice(err)
				n.CancelRename()
				return
			}
			if len(r.Preview) == 0 {
				c.Status.Notice = "Nothing to rename"
				n.CancelRename()
				return
			}
			rs.Preview, rs.Stage = r.Preview, RenamePreview
			n.RenameVersion++
		}
	})
}

// ApplyRename renames as previewed.
func (n *CodeNav) ApplyRename() {
	rs := n.Rename
	if rs == nil || rs.NewName == "" {
		return
	}
	n.Rename = nil
	n.RenameVersion++
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Applied bool `json:"applied"`
			Files   []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		err := c.call(ctx, "lsp.rename", map[string]any{"path": rs.File, "at": pos{Offset: rs.Offset}, "newName": rs.NewName}, &r)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Status.Notice = fmt.Sprintf("Renamed %s to %s in %d files", rs.Placeholder, rs.NewName, len(r.Files))
		}
	})
}

// CancelRename stops a rename.
func (n *CodeNav) CancelRename() {
	if n.Rename != nil {
		n.Rename = nil
		n.RenameVersion++
	}
}

// OutlineNode is an entry of the structure of a file.
type OutlineNode struct {
	Kind, Name, Detail string
	Modifiers          []string
	// Offset is where the name is (navigation); Line is 1-based.
	Offset, Line int
	Children     []OutlineNode
}

// Structure is the outline of a file.
type Structure struct {
	File  string
	Nodes []OutlineNode
	Err   string
}

type outlinePos struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
}

type outlineSym struct {
	Kind      string       `json:"kind"`
	Name      string       `json:"name"`
	Detail    string       `json:"detail"`
	Modifiers []string     `json:"modifiers"`
	Start     outlinePos   `json:"start"`
	NameStart outlinePos   `json:"nameStart"`
	Children  []outlineSym `json:"children"`
}

type lspSym struct {
	Name           string    `json:"name"`
	Detail         string    `json:"detail"`
	Kind           string    `json:"kind"`
	SelectionRange TextRange `json:"selectionRange"`
	Children       []lspSym  `json:"children"`
}

// LoadStructure loads the outline of file: syntax.outline (Tree-sitter),
// else the language server's document symbols.
func (n *CodeNav) LoadStructure(file string) {
	n.structSeq++
	seq := n.structSeq
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Symbols []outlineSym `json:"symbols"`
		}
		st := &Structure{File: file}
		err := c.call(ctx, "syntax.outline", pathParams{Path: file}, &r)
		if err == nil {
			st.Nodes = outlineNodes(r.Symbols)
		} else {
			var ls []lspSym
			if lerr := c.call(ctx, "lsp.documentSymbols", pathParams{Path: file}, &ls); lerr == nil {
				st.Nodes, err = lspNodes(ls), nil
			}
		}
		if err != nil {
			st.Err = err.Error()
		}
		return func() {
			if seq != n.structSeq {
				return
			}
			n.Structure = st
			n.StructureVersion++
		}
	})
}

func outlineNodes(in []outlineSym) []OutlineNode {
	out := make([]OutlineNode, 0, len(in))
	for _, s := range in {
		p := s.NameStart
		if p.Line == 0 {
			p = s.Start
		}
		out = append(out, OutlineNode{Kind: s.Kind, Name: s.Name, Detail: s.Detail, Modifiers: s.Modifiers, Offset: p.Offset, Line: p.Line, Children: outlineNodes(s.Children)})
	}
	return out
}

func lspNodes(in []lspSym) []OutlineNode {
	out := make([]OutlineNode, 0, len(in))
	for _, s := range in {
		out = append(out, OutlineNode{Kind: strings.ToLower(s.Kind), Name: s.Name, Detail: s.Detail, Offset: s.SelectionRange.Start.Offset, Line: s.SelectionRange.Start.Line, Children: lspNodes(s.Children)})
	}
	return out
}
