package fakeserver

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// Commands understood by workspace/executeCommand.
const (
	// CommandOrganize sends a workspace/applyEdit inserting OrganizedLine
	// at the top of the document given as first argument.
	CommandOrganize = "fake.organize"
	// CommandFail fails.
	CommandFail = "fake.fail"
	// OrganizedLine is inserted by CommandOrganize.
	OrganizedLine = "// organized\n"
	// DocblockLine is inserted by the resolved "Add docblock" action.
	DocblockLine = "/** doc */\n"
)

// Titles of the code actions offered for every range.
const (
	ActionRemoveErr  = "Remove ERR"
	ActionDocblock   = "Add docblock"
	ActionOrganize   = "Organize imports"
	ActionRunFake    = "Run fake"
	ActionExtract    = "Extract to file"
	ActionDisabled   = "Unavailable"
	ActionDeleteTemp = "Delete scratch"
)

func featureCapabilities() map[string]any {
	return map[string]any{
		"referencesProvider":              true,
		"documentHighlightProvider":       true,
		"codeActionProvider":              map[string]any{"codeActionKinds": []string{"quickfix", "refactor", "source"}, "resolveProvider": true},
		"documentFormattingProvider":      true,
		"documentRangeFormattingProvider": map[string]any{},
		"renameProvider":                  map[string]any{"prepareProvider": true},
		"documentSymbolProvider":          true,
		"workspaceSymbolProvider":         true,
		"executeCommandProvider":          map[string]any{"commands": []string{CommandOrganize, CommandFail}},
	}
}

func (s *Server) featureHandlers() map[string]handler {
	return map[string]handler{
		protocol.MethodReferences:        s.references,
		protocol.MethodDocumentHighlight: s.highlight,
		protocol.MethodCodeAction:        s.codeAction,
		protocol.MethodCodeActionResolve: s.resolveCodeAction,
		protocol.MethodExecuteCommand:    s.executeCommand,
		protocol.MethodFormatting:        s.formatting,
		protocol.MethodRangeFormatting:   s.rangeFormatting,
		protocol.MethodPrepareRename:     s.prepareRename,
		protocol.MethodRename:            s.rename,
		protocol.MethodDocumentSymbol:    s.documentSymbol,
		protocol.MethodWorkspaceSymbol:   s.workspaceSymbol,
	}
}

func isWord(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// wordAt returns the identifier around pos and its byte range.
func wordAt(text string, pos protocol.Position) (string, int, int) {
	off, err := protocol.OffsetOf(text, pos)
	if err != nil {
		return "", 0, 0
	}
	start, end := off, off
	for start > 0 && isWord(text[start-1]) {
		start--
	}
	for end < len(text) && isWord(text[end]) {
		end++
	}
	return text[start:end], start, end
}

// occurrences returns the byte ranges of word as whole word in text.
func occurrences(text, word string) [][2]int {
	var out [][2]int
	if word == "" {
		return nil
	}
	for off := 0; ; {
		i := strings.Index(text[off:], word)
		if i < 0 {
			return out
		}
		start, end := off+i, off+i+len(word)
		if (start == 0 || !isWord(text[start-1])) && (end == len(text) || !isWord(text[end])) {
			out = append(out, [2]int{start, end})
		}
		off = end
	}
}

func rangeOf(text string, start, end int) protocol.Range {
	return protocol.Range{Start: protocol.PositionOf(text, start), End: protocol.PositionOf(text, end)}
}

// doc returns a synced document. Requests are dispatched concurrently with
// the notification loop, so a request may overtake the didOpen sent just
// before it: wait a little for unknown documents.
func (s *Server) doc(uri protocol.DocumentURI) (string, int) {
	for deadline := time.Now().Add(2 * time.Second); ; {
		s.mu.Lock()
		text, ok := s.state.Docs[uri]
		v := s.state.Versions[uri]
		s.mu.Unlock()
		if ok || time.Now().After(deadline) {
			return text, v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sortedDocs returns the synced documents sorted by URI.
func (s *Server) sortedDocs() []protocol.DocumentURI {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.DocumentURI, 0, len(s.state.Docs))
	for u := range s.state.Docs {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// references returns every whole-word occurrence of the word at the
// position in all synced documents. The first occurrence in the requesting
// document counts as declaration.
func (s *Server) references(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.ReferenceParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	word, _, _ := wordAt(text, p.Position)
	out := []protocol.Location{}
	for _, u := range s.sortedDocs() {
		t, _ := s.doc(u)
		for i, o := range occurrences(t, word) {
			if i == 0 && u == p.TextDocument.URI && !p.Context.IncludeDeclaration {
				continue
			}
			out = append(out, protocol.Location{URI: u, Range: rangeOf(t, o[0], o[1])})
		}
	}
	return out, nil
}

// highlight marks occurrences followed by " =" as write, others as read.
func (s *Server) highlight(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.TextDocumentPositionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	word, _, _ := wordAt(text, p.Position)
	if word == "" {
		return nil, nil
	}
	out := []protocol.DocumentHighlight{}
	for _, o := range occurrences(text, word) {
		kind := protocol.HighlightRead
		if strings.HasPrefix(text[o[1]:], " =") && !strings.HasPrefix(text[o[1]:], " ==") {
			kind = protocol.HighlightWrite
		}
		out = append(out, protocol.DocumentHighlight{Range: rangeOf(text, o[0], o[1]), Kind: kind})
	}
	return out, nil
}

type docblockData struct {
	URI  protocol.DocumentURI `json:"uri"`
	Line int                  `json:"line"`
}

func (s *Server) codeAction(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.CodeActionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	uri := p.TextDocument.URI
	var out []any
	for _, d := range p.Context.Diagnostics {
		if d.Message != "error marker" {
			continue
		}
		out = append(out, protocol.CodeAction{
			Title: ActionRemoveErr, Kind: protocol.CodeActionQuickFix, IsPreferred: true, Diagnostics: []protocol.Diagnostic{d},
			Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{uri: {{Range: d.Range}}}},
		})
	}
	data, _ := json.Marshal(docblockData{URI: uri, Line: p.Range.Start.Line})
	dir := path.Dir(string(uri))
	extracted := protocol.DocumentURI(dir + "/Extracted.php")
	moved := protocol.DocumentURI(dir + "/Moved.php")
	out = append(out,
		protocol.CodeAction{Title: ActionDocblock, Kind: protocol.CodeActionRefactorRewrite, Data: data},
		protocol.CodeAction{Title: ActionOrganize, Kind: protocol.CodeActionSourceOrganizeImports, Command: &protocol.Command{
			Title: ActionOrganize, Command: CommandOrganize, Arguments: []json.RawMessage{json.RawMessage(`"` + string(uri) + `"`)},
		}},
		protocol.Command{Title: ActionRunFake, Command: CommandFail},
		protocol.CodeAction{Title: ActionExtract, Kind: protocol.CodeActionRefactorExtract, Edit: &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{
			{Kind: protocol.ResourceCreate, URI: extracted, Options: &protocol.FileOptions{IgnoreIfExists: true}},
			{TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: extracted}, Edits: []protocol.TextEdit{{NewText: "<?php\n// extracted\n"}}},
			{Kind: protocol.ResourceRename, OldURI: extracted, NewURI: moved},
		}}},
		protocol.CodeAction{Title: ActionDeleteTemp, Kind: protocol.CodeActionSource, Edit: &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{
			{Kind: protocol.ResourceDelete, URI: protocol.DocumentURI(dir + "/scratch.txt"), Options: &protocol.FileOptions{IgnoreIfNotExists: true}},
		}}},
		protocol.CodeAction{Title: ActionDisabled, Kind: protocol.CodeActionRefactor, Disabled: &protocol.CodeActionDisabled{Reason: "not here"}},
	)
	return out, nil
}

func (s *Server) resolveCodeAction(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var a protocol.CodeAction
	if err := req.UnmarshalParams(&a); err != nil {
		return nil, err
	}
	var d docblockData
	if a.Title != ActionDocblock || json.Unmarshal(a.Data, &d) != nil {
		return a, nil
	}
	_, version := s.doc(d.URI)
	pos := protocol.Position{Line: d.Line}
	a.Edit = &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{{
		TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: d.URI, Version: &version},
		Edits:        []protocol.TextEdit{{Range: protocol.Range{Start: pos, End: pos}, NewText: DocblockLine}},
	}}}
	return a, nil
}

func (s *Server) executeCommand(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.ExecuteCommandParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.Executed = append(s.state.Executed, p.Command)
	s.mu.Unlock()
	if p.Command != CommandOrganize || len(p.Arguments) == 0 {
		return nil, errors.New("command failed")
	}
	var uri protocol.DocumentURI
	if err := json.Unmarshal(p.Arguments[0], &uri); err != nil {
		return nil, err
	}
	var res protocol.ApplyWorkspaceEditResult
	err := s.conn.Call(ctx, protocol.MethodApplyEdit, protocol.ApplyWorkspaceEditParams{
		Label: ActionOrganize,
		Edit:  protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{uri: {{NewText: OrganizedLine}}}},
	}, &res)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.Applied = append(s.state.Applied, res.Applied)
	s.mu.Unlock()
	return map[string]any{"applied": res.Applied}, nil
}

// formatEdits trims trailing blanks and, with insertSpaces, replaces
// leading tabs on the lines [from, to].
func formatEdits(text string, opts protocol.FormattingOptions, from, to int) []protocol.TextEdit {
	edits := []protocol.TextEdit{}
	for i, line := range strings.Split(text, "\n") {
		if i < from || i > to {
			continue
		}
		trimmed := strings.TrimRight(line, " \t")
		if n := protocol.UTF16Len(trimmed); len(trimmed) < len(line) {
			edits = append(edits, protocol.TextEdit{Range: protocol.Range{
				Start: protocol.Position{Line: i, Character: n},
				End:   protocol.Position{Line: i, Character: protocol.UTF16Len(line)},
			}})
		}
		if opts.InsertSpaces {
			tabs := len(trimmed) - len(strings.TrimLeft(trimmed, "\t"))
			if tabs > 0 && tabs < len(trimmed) {
				edits = append(edits, protocol.TextEdit{
					Range:   protocol.Range{Start: protocol.Position{Line: i}, End: protocol.Position{Line: i, Character: tabs}},
					NewText: strings.Repeat(" ", tabs*opts.TabSize),
				})
			}
		}
	}
	return edits
}

func (s *Server) formatting(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DocumentFormattingParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	return formatEdits(text, p.Options, 0, strings.Count(text, "\n")), nil
}

func (s *Server) rangeFormatting(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DocumentRangeFormattingParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	return formatEdits(text, p.Options, p.Range.Start.Line, p.Range.End.Line), nil
}

func (s *Server) prepareRename(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.TextDocumentPositionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	word, start, end := wordAt(text, p.Position)
	if word == "" {
		return nil, nil
	}
	return map[string]any{"range": rangeOf(text, start, end), "placeholder": word}, nil
}

// rename renames the word in all synced documents (versioned edits).
func (s *Server) rename(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.RenameParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if p.NewName == "" {
		return nil, errors.New("empty name")
	}
	text, _ := s.doc(p.TextDocument.URI)
	word, _, _ := wordAt(text, p.Position)
	if word == "" {
		return nil, nil
	}
	we := protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{}}
	for _, u := range s.sortedDocs() {
		t, v := s.doc(u)
		var edits []protocol.TextEdit
		for _, o := range occurrences(t, word) {
			edits = append(edits, protocol.TextEdit{Range: rangeOf(t, o[0], o[1]), NewText: p.NewName})
		}
		if len(edits) > 0 {
			v := v
			we.DocumentChanges = append(we.DocumentChanges, protocol.DocumentChange{
				TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: u, Version: &v}, Edits: edits,
			})
		}
	}
	return we, nil
}

var (
	classRe = regexp.MustCompile(`^\s*class (\w+)`)
	funcRe  = regexp.MustCompile(`^\s*(?:public |private |protected |static )*function (\w+)`)
)

// symbols scans lines for classes and functions; functions after a class
// line become its children.
func symbols(text string) []protocol.DocumentSymbol {
	var out []protocol.DocumentSymbol
	lines := strings.Split(text, "\n")
	cls := -1
	for i, l := range lines {
		lr := protocol.Range{Start: protocol.Position{Line: i}, End: protocol.Position{Line: i, Character: protocol.UTF16Len(l)}}
		if m := classRe.FindStringSubmatchIndex(l); m != nil {
			sel := protocol.Range{Start: protocol.Position{Line: i, Character: m[2]}, End: protocol.Position{Line: i, Character: m[3]}}
			out = append(out, protocol.DocumentSymbol{Name: l[m[2]:m[3]], Kind: protocol.SymbolClass, Range: lr, SelectionRange: sel})
			cls = len(out) - 1
			continue
		}
		if m := funcRe.FindStringSubmatchIndex(l); m != nil {
			sel := protocol.Range{Start: protocol.Position{Line: i, Character: m[2]}, End: protocol.Position{Line: i, Character: m[3]}}
			sym := protocol.DocumentSymbol{Name: l[m[2]:m[3]], Detail: "()", Kind: protocol.SymbolFunction, Range: lr, SelectionRange: sel}
			if cls >= 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) {
				sym.Kind = protocol.SymbolMethod
				out[cls].Children = append(out[cls].Children, sym)
				out[cls].Range.End = lr.End
				continue
			}
			out = append(out, sym)
		}
	}
	return out
}

func (s *Server) documentSymbol(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DocumentSymbolParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	text, _ := s.doc(p.TextDocument.URI)
	out := symbols(text)
	if out == nil {
		out = []protocol.DocumentSymbol{}
	}
	return out, nil
}

// workspaceSymbol answers classes as SymbolInformation and functions as
// WorkspaceSymbol without range.
func (s *Server) workspaceSymbol(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.WorkspaceSymbolParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	q := strings.ToLower(p.Query)
	out := []any{}
	var walk func(u protocol.DocumentURI, container string, syms []protocol.DocumentSymbol)
	walk = func(u protocol.DocumentURI, container string, syms []protocol.DocumentSymbol) {
		for _, sym := range syms {
			if strings.Contains(strings.ToLower(sym.Name), q) {
				if sym.Kind == protocol.SymbolClass {
					out = append(out, map[string]any{"name": sym.Name, "kind": sym.Kind, "location": protocol.Location{URI: u, Range: sym.SelectionRange}})
				} else {
					out = append(out, map[string]any{"name": sym.Name, "kind": sym.Kind, "containerName": container, "location": map[string]any{"uri": u}})
				}
			}
			walk(u, sym.Name, sym.Children)
		}
	}
	for _, u := range s.sortedDocs() {
		t, _ := s.doc(u)
		walk(u, "", symbols(t))
	}
	return out, nil
}
