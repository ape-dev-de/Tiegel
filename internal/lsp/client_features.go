package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// References returns the locations referencing the symbol at pos (Find
// Usages), optionally including its declaration.
func (c *Client) References(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position, includeDeclaration bool) ([]protocol.Location, error) {
	if !c.caps.ReferencesProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var raw json.RawMessage
	p := protocol.ReferenceParams{TextDocumentPositionParams: positionParams(uri, pos), Context: protocol.ReferenceContext{IncludeDeclaration: includeDeclaration}}
	if err := c.call(ctx, protocol.MethodReferences, p, &raw); err != nil {
		return nil, fmt.Errorf("lsp: references: %w", err)
	}
	locs, err := protocol.ParseLocations(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: references: %w", err)
	}
	return locs, nil
}

// DocumentHighlight returns the occurrences of the symbol at pos in the
// same document.
func (c *Client) DocumentHighlight(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) ([]protocol.DocumentHighlight, error) {
	if !c.caps.DocumentHighlightProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var hs []protocol.DocumentHighlight
	if err := c.call(ctx, protocol.MethodDocumentHighlight, positionParams(uri, pos), &hs); err != nil {
		return nil, fmt.Errorf("lsp: documentHighlight: %w", err)
	}
	return hs, nil
}

// CodeActions requests the code actions for a range; diags are the
// diagnostics overlapping it (the server uses them for quick fixes).
func (c *Client) CodeActions(ctx context.Context, uri protocol.DocumentURI, r protocol.Range, diags []protocol.Diagnostic, only []string) ([]protocol.CodeAction, error) {
	if !c.caps.CodeActionProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	if diags == nil {
		diags = []protocol.Diagnostic{}
	}
	p := protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Range: r,
		Context: protocol.CodeActionContext{Diagnostics: diags, Only: only, TriggerKind: protocol.CodeActionInvoked},
	}
	var raw json.RawMessage
	if err := c.call(ctx, protocol.MethodCodeAction, p, &raw); err != nil {
		return nil, fmt.Errorf("lsp: codeAction: %w", err)
	}
	acts, err := protocol.ParseCodeActions(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: codeAction: %w", err)
	}
	return acts, nil
}

// CanResolveCodeActions reports whether the server resolves code actions
// lazily (codeAction/resolve).
func (c *Client) CanResolveCodeActions() bool {
	var opts protocol.CodeActionOptions
	return c.caps.CodeActionProvider.Decode(&opts) && opts.ResolveProvider
}

// ResolveCodeAction fills in the missing parts (usually the edit) of a code
// action.
func (c *Client) ResolveCodeAction(ctx context.Context, a protocol.CodeAction) (protocol.CodeAction, error) {
	if !c.CanResolveCodeActions() {
		return a, ErrNotSupported
	}
	var out protocol.CodeAction
	if err := c.call(ctx, protocol.MethodCodeActionResolve, a, &out); err != nil {
		return a, fmt.Errorf("lsp: codeAction/resolve: %w", err)
	}
	return out, nil
}

// ExecuteCommand runs a server command. The server may send
// workspace/applyEdit requests while it runs (Config.OnApplyEdit). The raw
// result is returned.
func (c *Client) ExecuteCommand(ctx context.Context, cmd protocol.Command) (json.RawMessage, error) {
	if c.caps.ExecuteCommandProvider == nil {
		return nil, ErrNotSupported
	}
	var raw json.RawMessage
	if err := c.call(ctx, protocol.MethodExecuteCommand, protocol.ExecuteCommandParams{Command: cmd.Command, Arguments: cmd.Arguments}, &raw); err != nil {
		return nil, fmt.Errorf("lsp: executeCommand %s: %w", cmd.Command, err)
	}
	return raw, nil
}

// Formatting returns the edits that format the whole document.
func (c *Client) Formatting(ctx context.Context, uri protocol.DocumentURI, opts protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	if !c.caps.DocumentFormattingProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var edits []protocol.TextEdit
	p := protocol.DocumentFormattingParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Options: opts}
	if err := c.call(ctx, protocol.MethodFormatting, p, &edits); err != nil {
		return nil, fmt.Errorf("lsp: formatting: %w", err)
	}
	return edits, nil
}

// RangeFormatting returns the edits that format a range.
func (c *Client) RangeFormatting(ctx context.Context, uri protocol.DocumentURI, r protocol.Range, opts protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	if !c.caps.DocumentRangeFormattingProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var edits []protocol.TextEdit
	p := protocol.DocumentRangeFormattingParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Range: r, Options: opts}
	if err := c.call(ctx, protocol.MethodRangeFormatting, p, &edits); err != nil {
		return nil, fmt.Errorf("lsp: rangeFormatting: %w", err)
	}
	return edits, nil
}

// CanPrepareRename reports whether the server supports prepareRename.
func (c *Client) CanPrepareRename() bool {
	var opts protocol.RenameOptions
	return c.caps.RenameProvider.Decode(&opts) && opts.PrepareProvider
}

// PrepareRename checks whether the symbol at pos can be renamed. A nil
// result means it cannot. Without server support it returns
// ErrNotSupported (callers then rename directly).
func (c *Client) PrepareRename(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) (*protocol.PrepareRenameResult, error) {
	if !c.CanPrepareRename() {
		return nil, ErrNotSupported
	}
	var raw json.RawMessage
	if err := c.call(ctx, protocol.MethodPrepareRename, positionParams(uri, pos), &raw); err != nil {
		return nil, fmt.Errorf("lsp: prepareRename: %w", err)
	}
	res, err := protocol.ParsePrepareRename(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: prepareRename: %w", err)
	}
	return res, nil
}

// Rename returns the workspace edit that renames the symbol at pos (nil if
// the server returns null).
func (c *Client) Rename(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	if !c.caps.RenameProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var we *protocol.WorkspaceEdit
	if err := c.call(ctx, protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: positionParams(uri, pos), NewName: newName}, &we); err != nil {
		return nil, fmt.Errorf("lsp: rename: %w", err)
	}
	return we, nil
}

// DocumentSymbols returns the symbols of a document as hierarchy.
func (c *Client) DocumentSymbols(ctx context.Context, uri protocol.DocumentURI) ([]protocol.DocumentSymbol, error) {
	if !c.caps.DocumentSymbolProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var raw json.RawMessage
	if err := c.call(ctx, protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}, &raw); err != nil {
		return nil, fmt.Errorf("lsp: documentSymbol: %w", err)
	}
	syms, err := protocol.ParseDocumentSymbols(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: documentSymbol: %w", err)
	}
	return syms, nil
}

// WorkspaceSymbols searches symbols in the whole workspace.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]protocol.SymbolInformation, error) {
	if !c.caps.WorkspaceSymbolProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var raw json.RawMessage
	if err := c.call(ctx, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: query}, &raw); err != nil {
		return nil, fmt.Errorf("lsp: workspace/symbol: %w", err)
	}
	syms, err := protocol.ParseWorkspaceSymbols(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: workspace/symbol: %w", err)
	}
	return syms, nil
}
