package langphp

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// TopicEditApplied is published after a workspace edit of the language
// server was applied (code action, rename, workspace/applyEdit) with an
// EditResult payload.
const TopicEditApplied = "lsp.editApplied"

// maxCachedActions bounds the code action cache of lsp.codeActions.
const maxCachedActions = 512

func (m *Module) registerRefactoring(p *api.Provider) {
	api.Add(p, "lsp.references", "Find usages: references of the symbol at a position (with line preview)", m.lspReferences)
	api.Add(p, "lsp.documentHighlight", "Occurrences of the symbol at a position in the document (read/write)", m.lspDocumentHighlight)
	api.Add(p, "lsp.codeActions", "Code actions (quick fixes, refactorings) for a range incl. its diagnostics", m.lspCodeActions)
	api.Add(p, "lsp.applyCodeAction", "Apply a code action from lsp.codeActions (workspace edit, then command)", m.lspApplyCodeAction)
	api.Add(p, "lsp.executeCommand", "Run a language server command (its workspace edits are applied)", m.lspExecuteCommand)
	api.Add(p, "lsp.formatting", "Format a document (applied as one undoable edit unless apply:false)", m.lspFormatting)
	api.Add(p, "lsp.rangeFormatting", "Format a range of a document (applied as one undoable edit unless apply:false)", m.lspRangeFormatting)
	api.Add(p, "lsp.prepareRename", "Check whether the symbol at a position can be renamed", m.lspPrepareRename)
	api.Add(p, "lsp.rename", "Rename the symbol at a position in all files (preview or apply)", m.lspRename)
	api.Add(p, "lsp.documentSymbols", "Symbols of a document as hierarchy", m.lspDocumentSymbols)
	api.Add(p, "lsp.workspaceSymbols", "Search symbols in the workspace", m.lspWorkspaceSymbols)
}

// textCache reads file texts once per request (open document buffer or
// file on disk).
type textCache struct {
	m     *Module
	texts map[string][]byte
}

func (m *Module) newTextCache(r *request) *textCache {
	tc := &textCache{m: m, texts: map[string][]byte{}}
	if r != nil {
		tc.texts[r.snap.File] = r.snap.Text
	}
	return tc
}

func (tc *textCache) get(abs string) []byte {
	if t, ok := tc.texts[abs]; ok {
		return t
	}
	t, _, _ := tc.m.app.ReadText(abs)
	tc.texts[abs] = t
	return t
}

// lineText returns the line of text that contains off (without EOL).
func lineText(text []byte, r api.TextRange) string {
	start := r.Start.Offset
	for start > 0 && text[start-1] != '\n' {
		start--
	}
	end := r.Start.Offset
	for end < len(text) && text[end] != '\n' && text[end] != '\r' {
		end++
	}
	return string(text[start:end])
}

// ReferencesParams are the params of "lsp.references".
type ReferencesParams struct {
	Path string  `json:"path"`
	At   api.Pos `json:"at"`
	// IncludeDeclaration lists the declaration too (default true).
	IncludeDeclaration *bool `json:"includeDeclaration,omitempty"`
}

// Usage is a reference with the text of its line.
type Usage struct {
	Location
	LineText string `json:"lineText"`
}

func (m *Module) lspReferences(ctx context.Context, p ReferencesParams) ([]Usage, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return nil, err
	}
	incl := p.IncludeDeclaration == nil || *p.IncludeDeclaration
	locs, err := r.s.client.References(ctx, r.uri, r.pos, incl)
	if err != nil {
		return nil, lspErr("references", err)
	}
	tc := m.newTextCache(&r)
	out := []Usage{}
	for _, l := range locs {
		abs, err := l.URI.Path()
		if err != nil {
			continue
		}
		text := tc.get(abs)
		rg := textRange(text, l.Range)
		out = append(out, Usage{Location: Location{Path: m.app.DisplayPath(abs), File: abs, Range: rg}, LineText: lineText(text, rg)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Range.Start.Offset < out[j].Range.Start.Offset
	})
	return out, nil
}

// Highlight is an occurrence of a symbol; Kind is text, read or write.
type Highlight struct {
	Range api.TextRange `json:"range"`
	Kind  string        `json:"kind"`
}

func highlightKind(k protocol.DocumentHighlightKind) string {
	switch k {
	case protocol.HighlightRead:
		return "read"
	case protocol.HighlightWrite:
		return "write"
	}
	return "text"
}

func (m *Module) lspDocumentHighlight(ctx context.Context, p PosParams) ([]Highlight, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return nil, err
	}
	hs, err := r.s.client.DocumentHighlight(ctx, r.uri, r.pos)
	if err != nil {
		return nil, lspErr("document highlight", err)
	}
	out := []Highlight{}
	for _, h := range hs {
		out = append(out, Highlight{Range: textRange(r.snap.Text, h.Range), Kind: highlightKind(h.Kind)})
	}
	return out, nil
}

// --- code actions -----------------------------------------------------------

// actionCache maps IDs handed out by lsp.codeActions to the server's code
// actions. Entries are only valid for the server generation they came from.
type actionCache struct {
	mu    sync.Mutex
	seq   uint64
	order []string
	byID  map[string]cachedAction
}

type cachedAction struct {
	gen    int
	path   string
	action protocol.CodeAction
}

func (c *actionCache) put(gen int, path string, a protocol.CodeAction) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID == nil {
		c.byID = map[string]cachedAction{}
	}
	c.seq++
	id := "a" + strconv.FormatUint(c.seq, 10)
	c.byID[id] = cachedAction{gen: gen, path: path, action: a}
	c.order = append(c.order, id)
	if len(c.order) > maxCachedActions {
		delete(c.byID, c.order[0])
		c.order = c.order[1:]
	}
	return id
}

func (c *actionCache) get(id string) (cachedAction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.byID[id]
	return a, ok
}

// CodeActionsParams are the params of "lsp.codeActions". Without End the
// range is the position Start.
type CodeActionsParams struct {
	Path  string   `json:"path"`
	Start api.Pos  `json:"start"`
	End   *api.Pos `json:"end,omitempty"`
	// Only filters by kind prefix (e.g. ["quickfix"], ["source.organizeImports"]).
	Only []string `json:"only,omitempty"`
}

// CodeActionInfo describes an offered code action.
type CodeActionInfo struct {
	// ID identifies the action for lsp.applyCodeAction.
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind,omitempty"`
	Preferred bool   `json:"preferred,omitempty"`
	// Disabled is the reason why the action cannot be applied.
	Disabled string `json:"disabled,omitempty"`
	// Diagnostics are the messages the action fixes.
	Diagnostics []string `json:"diagnostics,omitempty"`
	HasEdit     bool     `json:"hasEdit,omitempty"`
	Command     string   `json:"command,omitempty"`
	// Action is the server's raw code action (for stateless clients, see
	// ApplyCodeActionParams.Action).
	Action json.RawMessage `json:"action,omitempty"`
}

func before(a, b protocol.Position) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
}

// overlaps reports whether diagnostic range d touches r.
func overlaps(d, r protocol.Range) bool {
	return !before(d.End, r.Start) && !before(r.End, d.Start)
}

func (m *Module) lspCodeActions(ctx context.Context, p CodeActionsParams) ([]CodeActionInfo, error) {
	r, err := m.prepare(p.Path, &p.Start)
	if err != nil {
		return nil, err
	}
	rg := protocol.Range{Start: r.pos, End: r.pos}
	if p.End != nil {
		off, err := p.End.ByteOffset(r.snap.Text)
		if err != nil {
			return nil, err
		}
		rg.End = protocol.PositionOf(string(r.snap.Text), off)
		if before(rg.End, rg.Start) {
			rg.Start, rg.End = rg.End, rg.Start
		}
	}
	var diags []protocol.Diagnostic
	m.diagMu.Lock()
	for _, d := range m.diags[r.snap.File].raw {
		if overlaps(d.Range, rg) {
			diags = append(diags, d)
		}
	}
	m.diagMu.Unlock()
	acts, err := r.s.client.CodeActions(ctx, r.uri, rg, diags, p.Only)
	if err != nil {
		return nil, lspErr("code actions", err)
	}
	out := []CodeActionInfo{}
	for _, a := range acts {
		info := CodeActionInfo{
			ID: m.actions.put(r.s.gen, r.snap.File, a), Title: a.Title, Kind: a.Kind, Preferred: a.IsPreferred,
			HasEdit: !a.Edit.IsEmpty(),
		}
		if a.Disabled != nil {
			info.Disabled = a.Disabled.Reason
			if info.Disabled == "" {
				info.Disabled = "disabled"
			}
		}
		if a.Command != nil {
			info.Command = a.Command.Command
		}
		for _, d := range a.Diagnostics {
			info.Diagnostics = append(info.Diagnostics, d.Message)
		}
		info.Action, _ = json.Marshal(a)
		out = append(out, info)
	}
	return out, nil
}

// ApplyCodeActionParams are the params of "lsp.applyCodeAction": the ID of
// an action from lsp.codeActions, or the raw Action itself.
type ApplyCodeActionParams struct {
	ID     string          `json:"id,omitempty"`
	Action json.RawMessage `json:"action,omitempty"`
}

// EditResult reports an applied workspace edit.
type EditResult struct {
	Label   string               `json:"label,omitempty"`
	Applied bool                 `json:"applied"`
	Files   []api.EditStepResult `json:"files"`
	// Command is the executed server command (code actions).
	Command       string          `json:"command,omitempty"`
	CommandResult json.RawMessage `json:"commandResult,omitempty"`
}

func (m *Module) lspApplyCodeAction(ctx context.Context, p ApplyCodeActionParams) (EditResult, error) {
	s := m.current()
	if s == nil {
		return EditResult{}, notRunning()
	}
	var a protocol.CodeAction
	switch {
	case p.ID != "":
		c, ok := m.actions.get(p.ID)
		if !ok || c.gen != s.gen {
			return EditResult{}, api.Errorf(api.CodeNotFound, "code action %s is unknown or outdated (request lsp.codeActions again)", p.ID)
		}
		a = c.action
	case len(p.Action) > 0:
		if err := json.Unmarshal(p.Action, &a); err != nil {
			return EditResult{}, api.Errorf(api.CodeInvalidParams, "action: %v", err)
		}
	default:
		return EditResult{}, api.Errorf(api.CodeInvalidParams, "id or action is required")
	}
	if a.Disabled != nil {
		return EditResult{}, api.Errorf(api.CodeFailed, "code action %q is disabled: %s", a.Title, a.Disabled.Reason)
	}
	if a.Edit.IsEmpty() && a.Command == nil && s.client.CanResolveCodeActions() {
		resolved, err := s.client.ResolveCodeAction(ctx, a)
		if err != nil {
			return EditResult{}, lspErr("resolve code action", err)
		}
		a = resolved
	}
	res := EditResult{Label: a.Title, Applied: true, Files: []api.EditStepResult{}}
	if !a.Edit.IsEmpty() {
		r, err := m.applyWorkspaceEdit(ctx, a.Title, a.Edit)
		if err != nil {
			return r, err
		}
		res.Files = r.Files
	}
	if a.Command != nil {
		res.Command = a.Command.Command
		raw, err := s.client.ExecuteCommand(ctx, *a.Command)
		if err != nil {
			return res, lspErr("command "+a.Command.Command, err)
		}
		if len(raw) > 0 && string(raw) != "null" {
			res.CommandResult = raw
		}
	}
	return res, nil
}

// ExecuteCommandParams are the params of "lsp.executeCommand".
type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

func (m *Module) lspExecuteCommand(ctx context.Context, p ExecuteCommandParams) (EditResult, error) {
	s := m.current()
	if s == nil {
		return EditResult{}, notRunning()
	}
	if p.Command == "" {
		return EditResult{}, api.Errorf(api.CodeInvalidParams, "command is required")
	}
	raw, err := s.client.ExecuteCommand(ctx, protocol.Command{Command: p.Command, Arguments: p.Arguments})
	if err != nil {
		return EditResult{}, lspErr("command "+p.Command, err)
	}
	res := EditResult{Applied: true, Files: []api.EditStepResult{}, Command: p.Command}
	if len(raw) > 0 && string(raw) != "null" {
		res.CommandResult = raw
	}
	return res, nil
}

// --- workspace edits --------------------------------------------------------

func uriPath(u protocol.DocumentURI) (string, error) {
	p, err := u.Path()
	if err != nil {
		return "", api.Errorf(api.CodeInvalidParams, "uri %s: %v", u, err)
	}
	return p, nil
}

// lspChanges converts LSP text edits into byte changes of text.
func lspChanges(edits []protocol.TextEdit) func(text []byte) ([]api.TextChange, error) {
	return func(text []byte) ([]api.TextChange, error) {
		s := string(text)
		out := make([]api.TextChange, 0, len(edits))
		for _, e := range edits {
			start, err := protocol.OffsetOf(s, e.Range.Start)
			if err != nil {
				return nil, api.Errorf(api.CodeConflict, "edit out of range: %v", err)
			}
			end, err := protocol.OffsetOf(s, e.Range.End)
			if err != nil {
				return nil, api.Errorf(api.CodeConflict, "edit out of range: %v", err)
			}
			out = append(out, api.TextChange{Start: start, End: end, Text: e.NewText})
		}
		return out, nil
	}
}

// fileOps converts a workspace edit into file operations (documentChanges
// in order, otherwise changes sorted by file).
func fileOps(we *protocol.WorkspaceEdit) ([]api.FileOp, error) {
	var ops []api.FileOp
	if len(we.DocumentChanges) > 0 {
		for _, dc := range we.DocumentChanges {
			opts := protocol.FileOptions{}
			if dc.Options != nil {
				opts = *dc.Options
			}
			switch dc.Kind {
			case "":
				if dc.TextDocument == nil {
					return nil, api.Errorf(api.CodeInvalidParams, "text document edit without document")
				}
				abs, err := uriPath(dc.TextDocument.URI)
				if err != nil {
					return nil, err
				}
				op := api.FileOp{Kind: api.FileOpEdit, Path: abs, Changes: lspChanges(dc.Edits)}
				if v := dc.TextDocument.Version; v != nil && *v >= 0 {
					op.Version = api.VersionOf(uint64(*v))
				}
				ops = append(ops, op)
			case protocol.ResourceCreate:
				abs, err := uriPath(dc.URI)
				if err != nil {
					return nil, err
				}
				ops = append(ops, api.FileOp{Kind: api.FileOpCreate, Path: abs, Overwrite: opts.Overwrite, IgnoreIfExists: opts.IgnoreIfExists})
			case protocol.ResourceRename:
				from, err := uriPath(dc.OldURI)
				if err != nil {
					return nil, err
				}
				to, err := uriPath(dc.NewURI)
				if err != nil {
					return nil, err
				}
				ops = append(ops, api.FileOp{Kind: api.FileOpRename, Path: from, NewPath: to, Overwrite: opts.Overwrite, IgnoreIfExists: opts.IgnoreIfExists})
			case protocol.ResourceDelete:
				abs, err := uriPath(dc.URI)
				if err != nil {
					return nil, err
				}
				ops = append(ops, api.FileOp{Kind: api.FileOpDelete, Path: abs, Recursive: opts.Recursive, IgnoreIfNotExists: opts.IgnoreIfNotExists})
			default:
				return nil, api.Errorf(api.CodeInvalidParams, "unknown resource operation %q", dc.Kind)
			}
		}
		return ops, nil
	}
	uris := make([]string, 0, len(we.Changes))
	for u := range we.Changes {
		uris = append(uris, string(u))
	}
	sort.Strings(uris)
	for _, u := range uris {
		abs, err := uriPath(protocol.DocumentURI(u))
		if err != nil {
			return nil, err
		}
		ops = append(ops, api.FileOp{Kind: api.FileOpEdit, Path: abs, Changes: lspChanges(we.Changes[protocol.DocumentURI(u)])})
	}
	return ops, nil
}

// applyWorkspaceEdit applies a workspace edit: text edits of open documents
// as undoable buffer edits, closed files atomically with Local History,
// resource operations in order. It stops at the first failure (failure
// handling "abort"); the steps before it stay applied.
func (m *Module) applyWorkspaceEdit(ctx context.Context, label string, we *protocol.WorkspaceEdit) (EditResult, error) {
	res := EditResult{Label: label, Files: []api.EditStepResult{}}
	ops, err := fileOps(we)
	if err != nil {
		return res, err
	}
	if label == "" {
		label = "language server edit"
	}
	files, err := m.app.ApplyFileOps(ctx, label, ops)
	res.Files = append(res.Files, files...)
	if len(files) > 0 {
		m.app.Bus().Publish(TopicEditApplied, EditResult{Label: res.Label, Applied: err == nil, Files: files})
	}
	if err != nil {
		return res, err
	}
	res.Applied = true
	return res, nil
}

// onApplyEdit answers workspace/applyEdit requests of the server.
func (m *Module) onApplyEdit(ctx context.Context, p protocol.ApplyWorkspaceEditParams) protocol.ApplyWorkspaceEditResult {
	res, err := m.applyWorkspaceEdit(ctx, p.Label, &p.Edit)
	if err != nil {
		failed := len(res.Files)
		return protocol.ApplyWorkspaceEditResult{FailureReason: err.Error(), FailedChange: &failed}
	}
	return protocol.ApplyWorkspaceEditResult{Applied: true}
}

// --- formatting -------------------------------------------------------------

// FormatParams are the params of "lsp.formatting" and (with Start/End)
// "lsp.rangeFormatting".
type FormatParams struct {
	Path  string   `json:"path"`
	Start *api.Pos `json:"start,omitempty"`
	End   *api.Pos `json:"end,omitempty"`
	// TabSize defaults to 4, InsertSpaces to true (PSR-12).
	TabSize      int   `json:"tabSize,omitempty"`
	InsertSpaces *bool `json:"insertSpaces,omitempty"`
	// Apply applies the edits as one undoable edit (default true).
	Apply *bool `json:"apply,omitempty"`
}

// FormatResult is the result of "lsp.formatting" and "lsp.rangeFormatting".
type FormatResult struct {
	Path    string     `json:"path"`
	Edits   []TextEdit `json:"edits"`
	Applied bool       `json:"applied"`
	// Version is the document version after applying.
	Version uint64 `json:"version,omitempty"`
}

func (p FormatParams) options() protocol.FormattingOptions {
	o := protocol.FormattingOptions{TabSize: p.TabSize, InsertSpaces: p.InsertSpaces == nil || *p.InsertSpaces}
	if o.TabSize <= 0 {
		o.TabSize = 4
	}
	return o
}

func (m *Module) lspFormatting(ctx context.Context, p FormatParams) (FormatResult, error) {
	if p.Start != nil || p.End != nil {
		return FormatResult{}, api.Errorf(api.CodeInvalidParams, "lsp.formatting formats the whole document; use lsp.rangeFormatting for a range")
	}
	return m.format(ctx, p)
}

func (m *Module) lspRangeFormatting(ctx context.Context, p FormatParams) (FormatResult, error) {
	if p.Start == nil || p.End == nil {
		return FormatResult{}, api.Errorf(api.CodeInvalidParams, "start and end are required")
	}
	return m.format(ctx, p)
}

func (m *Module) format(ctx context.Context, p FormatParams) (FormatResult, error) {
	r, err := m.prepare(p.Path, p.Start)
	if err != nil {
		return FormatResult{}, err
	}
	var edits []protocol.TextEdit
	if p.Start != nil {
		off, err := p.End.ByteOffset(r.snap.Text)
		if err != nil {
			return FormatResult{}, err
		}
		rg := protocol.Range{Start: r.pos, End: protocol.PositionOf(string(r.snap.Text), off)}
		edits, err = r.s.client.RangeFormatting(ctx, r.uri, rg, p.options())
		if err != nil {
			return FormatResult{}, lspErr("range formatting", err)
		}
	} else {
		edits, err = r.s.client.Formatting(ctx, r.uri, p.options())
		if err != nil {
			return FormatResult{}, lspErr("formatting", err)
		}
	}
	res := FormatResult{Path: r.snap.Path, Edits: []TextEdit{}, Version: r.snap.Version}
	for _, e := range edits {
		res.Edits = append(res.Edits, TextEdit{Range: textRange(r.snap.Text, e.Range), NewText: e.NewText})
	}
	if (p.Apply != nil && !*p.Apply) || len(edits) == 0 {
		return res, nil
	}
	files, err := m.app.ApplyFileOps(ctx, "reformat", []api.FileOp{{
		Path: r.snap.File, Version: api.VersionOf(r.snap.Version), Changes: lspChanges(edits),
	}})
	if err != nil {
		return res, err
	}
	res.Applied, res.Version = true, files[0].Version
	return res, nil
}

// --- rename -----------------------------------------------------------------

// PrepareRenameResult is the result of "lsp.prepareRename".
type PrepareRenameResult struct {
	CanRename   bool           `json:"canRename"`
	Range       *api.TextRange `json:"range,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
}

func (m *Module) prepareRename(ctx context.Context, r request) (PrepareRenameResult, error) {
	pr, err := r.s.client.PrepareRename(ctx, r.uri, r.pos)
	if err != nil {
		if !r.s.client.CanPrepareRename() && r.s.client.Capabilities().RenameProvider.IsEnabled() {
			return PrepareRenameResult{CanRename: true}, nil // no prepare support: try the rename
		}
		return PrepareRenameResult{}, lspErr("prepare rename", err)
	}
	if pr == nil {
		return PrepareRenameResult{}, nil
	}
	res := PrepareRenameResult{CanRename: true, Placeholder: pr.Placeholder}
	if pr.Range != nil {
		tr := textRange(r.snap.Text, *pr.Range)
		res.Range = &tr
		if res.Placeholder == "" {
			res.Placeholder = string(r.snap.Text[tr.Start.Offset:tr.End.Offset])
		}
	}
	return res, nil
}

func (m *Module) lspPrepareRename(ctx context.Context, p PosParams) (PrepareRenameResult, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return PrepareRenameResult{}, err
	}
	return m.prepareRename(ctx, r)
}

// RenameParams are the params of "lsp.rename".
type RenameParams struct {
	Path    string  `json:"path"`
	At      api.Pos `json:"at"`
	NewName string  `json:"newName"`
	// Preview only returns the planned changes.
	Preview bool `json:"preview,omitempty"`
}

// FileChange is one planned change of a workspace edit (preview).
type FileChange struct {
	Kind    string     `json:"kind"`
	Path    string     `json:"path"`
	NewPath string     `json:"newPath,omitempty"`
	Edits   []TextEdit `json:"edits,omitempty"`
}

// RenameResult is the result of "lsp.rename".
type RenameResult struct {
	EditResult
	Preview []FileChange `json:"preview,omitempty"`
}

// describe lists the changes of a workspace edit in API coordinates.
func (m *Module) describe(tc *textCache, we *protocol.WorkspaceEdit) []FileChange {
	var out []FileChange
	add := func(u protocol.DocumentURI, edits []protocol.TextEdit) {
		abs, err := u.Path()
		if err != nil {
			return
		}
		text := tc.get(abs)
		fc := FileChange{Kind: api.FileOpEdit, Path: m.app.DisplayPath(abs)}
		for _, e := range edits {
			fc.Edits = append(fc.Edits, TextEdit{Range: textRange(text, e.Range), NewText: e.NewText})
		}
		out = append(out, fc)
	}
	if len(we.DocumentChanges) > 0 {
		for _, dc := range we.DocumentChanges {
			switch dc.Kind {
			case "":
				if dc.TextDocument != nil {
					add(dc.TextDocument.URI, dc.Edits)
				}
			case protocol.ResourceRename:
				from, _ := dc.OldURI.Path()
				to, _ := dc.NewURI.Path()
				out = append(out, FileChange{Kind: api.FileOpRename, Path: m.app.DisplayPath(from), NewPath: m.app.DisplayPath(to)})
			default:
				p, _ := dc.URI.Path()
				out = append(out, FileChange{Kind: dc.Kind, Path: m.app.DisplayPath(p)})
			}
		}
		return out
	}
	uris := make([]string, 0, len(we.Changes))
	for u := range we.Changes {
		uris = append(uris, string(u))
	}
	sort.Strings(uris)
	for _, u := range uris {
		add(protocol.DocumentURI(u), we.Changes[protocol.DocumentURI(u)])
	}
	return out
}

func (m *Module) lspRename(ctx context.Context, p RenameParams) (RenameResult, error) {
	if p.NewName == "" {
		return RenameResult{}, api.Errorf(api.CodeInvalidParams, "newName is required")
	}
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return RenameResult{}, err
	}
	pr, err := m.prepareRename(ctx, r)
	if err != nil {
		return RenameResult{}, err
	}
	if !pr.CanRename {
		return RenameResult{}, api.Errorf(api.CodeInvalidParams, "the element at this position cannot be renamed")
	}
	we, err := r.s.client.Rename(ctx, r.uri, r.pos, p.NewName)
	if err != nil {
		return RenameResult{}, lspErr("rename", err)
	}
	if we.IsEmpty() {
		return RenameResult{EditResult: EditResult{Label: "rename", Files: []api.EditStepResult{}}}, nil
	}
	if p.Preview {
		return RenameResult{EditResult: EditResult{Label: "rename", Files: []api.EditStepResult{}}, Preview: m.describe(m.newTextCache(&r), we)}, nil
	}
	res, err := m.applyWorkspaceEdit(ctx, "rename "+pr.Placeholder+" to "+p.NewName, we)
	return RenameResult{EditResult: res}, err
}

// --- symbols ----------------------------------------------------------------

// DocumentSymbol is a symbol of a document with its children.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           string           `json:"kind"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          api.TextRange    `json:"range"`
	SelectionRange api.TextRange    `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

func docSymbols(text []byte, syms []protocol.DocumentSymbol) []DocumentSymbol {
	out := make([]DocumentSymbol, 0, len(syms))
	for _, s := range syms {
		out = append(out, DocumentSymbol{
			Name: s.Name, Detail: s.Detail, Kind: s.Kind.String(), Deprecated: s.Deprecated,
			Range: textRange(text, s.Range), SelectionRange: textRange(text, s.SelectionRange),
			Children: docSymbols(text, s.Children),
		})
	}
	return out
}

func (m *Module) lspDocumentSymbols(ctx context.Context, p api.PathParams) ([]DocumentSymbol, error) {
	r, err := m.prepare(p.Path, nil)
	if err != nil {
		return nil, err
	}
	syms, err := r.s.client.DocumentSymbols(ctx, r.uri)
	if err != nil {
		return nil, lspErr("document symbols", err)
	}
	return docSymbols(r.snap.Text, syms), nil
}

// WorkspaceSymbolsParams are the params of "lsp.workspaceSymbols".
type WorkspaceSymbolsParams struct {
	Query string `json:"query"`
	// Limit caps the result (default 500).
	Limit int `json:"limit,omitempty"`
}

// WorkspaceSymbol is a symbol found in the workspace. Range is missing when
// the server only knows the file.
type WorkspaceSymbol struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Container  string         `json:"container,omitempty"`
	Deprecated bool           `json:"deprecated,omitempty"`
	Path       string         `json:"path"`
	File       string         `json:"file"`
	Range      *api.TextRange `json:"range,omitempty"`
}

func (m *Module) lspWorkspaceSymbols(ctx context.Context, p WorkspaceSymbolsParams) ([]WorkspaceSymbol, error) {
	s := m.current()
	if s == nil {
		return nil, notRunning()
	}
	if p.Limit <= 0 {
		p.Limit = 500
	}
	syms, err := s.client.WorkspaceSymbols(ctx, p.Query)
	if err != nil {
		return nil, lspErr("workspace symbols", err)
	}
	tc := m.newTextCache(nil)
	out := []WorkspaceSymbol{}
	for _, sym := range syms {
		if len(out) >= p.Limit {
			break
		}
		abs, err := sym.Location.URI.Path()
		if err != nil {
			continue
		}
		ws := WorkspaceSymbol{Name: sym.Name, Kind: sym.Kind.String(), Container: sym.ContainerName, Deprecated: sym.Deprecated, Path: m.app.DisplayPath(abs), File: abs}
		if sym.HasRange {
			tr := textRange(tc.get(abs), sym.Location.Range)
			ws.Range = &tr
		}
		out = append(out, ws)
	}
	return out, nil
}
