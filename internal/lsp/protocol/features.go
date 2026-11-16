package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Method names of the navigation, code action, formatting, rename and
// symbol requests and of the workspace edit requests from the server.
const (
	MethodReferences        = "textDocument/references"
	MethodDocumentHighlight = "textDocument/documentHighlight"
	MethodCodeAction        = "textDocument/codeAction"
	MethodCodeActionResolve = "codeAction/resolve"
	MethodFormatting        = "textDocument/formatting"
	MethodRangeFormatting   = "textDocument/rangeFormatting"
	MethodPrepareRename     = "textDocument/prepareRename"
	MethodRename            = "textDocument/rename"
	MethodDocumentSymbol    = "textDocument/documentSymbol"
	MethodWorkspaceSymbol   = "workspace/symbol"
	MethodExecuteCommand    = "workspace/executeCommand"
	MethodApplyEdit         = "workspace/applyEdit"
)

// --- References / highlights ----------------------------------------------

// ReferenceContext controls textDocument/references.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams are the textDocument/references params.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// DocumentHighlightKind tells how a symbol is used at a highlight.
type DocumentHighlightKind int

// Highlight kinds.
const (
	HighlightText  DocumentHighlightKind = 1
	HighlightRead  DocumentHighlightKind = 2
	HighlightWrite DocumentHighlightKind = 3
)

// DocumentHighlight is one occurrence of the symbol at a position.
type DocumentHighlight struct {
	Range Range                 `json:"range"`
	Kind  DocumentHighlightKind `json:"kind,omitempty"`
}

// --- Workspace edits ------------------------------------------------------

// OptionalVersionedTextDocumentIdentifier identifies a document, optionally
// at a version (null: any version).
type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int        `json:"version"`
}

// FileOptions are the options of resource operations.
type FileOptions struct {
	Overwrite         bool `json:"overwrite,omitempty"`
	IgnoreIfExists    bool `json:"ignoreIfExists,omitempty"`
	Recursive         bool `json:"recursive,omitempty"`
	IgnoreIfNotExists bool `json:"ignoreIfNotExists,omitempty"`
}

// Resource operation kinds of DocumentChange.
const (
	ResourceCreate = "create"
	ResourceRename = "rename"
	ResourceDelete = "delete"
)

// DocumentChange is one entry of WorkspaceEdit.documentChanges: a
// TextDocumentEdit (Kind empty; TextDocument and Edits set) or a resource
// operation (Kind create, rename or delete).
type DocumentChange struct {
	Kind string `json:"kind,omitempty"`
	// TextDocumentEdit
	TextDocument *OptionalVersionedTextDocumentIdentifier `json:"textDocument,omitempty"`
	Edits        []TextEdit                               `json:"edits,omitempty"`
	// CreateFile / DeleteFile
	URI DocumentURI `json:"uri,omitempty"`
	// RenameFile
	OldURI  DocumentURI  `json:"oldUri,omitempty"`
	NewURI  DocumentURI  `json:"newUri,omitempty"`
	Options *FileOptions `json:"options,omitempty"`
}

// WorkspaceEdit is a set of changes to several documents. DocumentChanges
// takes precedence over Changes if both are present.
type WorkspaceEdit struct {
	Changes         map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []DocumentChange           `json:"documentChanges,omitempty"`
}

// IsEmpty reports whether the edit changes nothing.
func (w *WorkspaceEdit) IsEmpty() bool {
	return w == nil || (len(w.Changes) == 0 && len(w.DocumentChanges) == 0)
}

// ApplyWorkspaceEditParams are the workspace/applyEdit params.
type ApplyWorkspaceEditParams struct {
	Label string        `json:"label,omitempty"`
	Edit  WorkspaceEdit `json:"edit"`
}

// ApplyWorkspaceEditResult is the workspace/applyEdit result.
type ApplyWorkspaceEditResult struct {
	Applied       bool   `json:"applied"`
	FailureReason string `json:"failureReason,omitempty"`
	FailedChange  *int   `json:"failedChange,omitempty"`
}

// --- Code actions ---------------------------------------------------------

// Command is a server command (workspace/executeCommand).
type Command struct {
	Title     string            `json:"title"`
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// ExecuteCommandParams are the workspace/executeCommand params.
type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// Code action kinds used by Tiegel.
const (
	CodeActionQuickFix              = "quickfix"
	CodeActionRefactor              = "refactor"
	CodeActionRefactorExtract       = "refactor.extract"
	CodeActionRefactorInline        = "refactor.inline"
	CodeActionRefactorRewrite       = "refactor.rewrite"
	CodeActionSource                = "source"
	CodeActionSourceOrganizeImports = "source.organizeImports"
	CodeActionSourceFixAll          = "source.fixAll"
)

// CodeActionTriggerKind tells why code actions were requested.
type CodeActionTriggerKind int

// Code action trigger kinds.
const (
	CodeActionInvoked   CodeActionTriggerKind = 1
	CodeActionAutomatic CodeActionTriggerKind = 2
)

// CodeActionContext carries the diagnostics at the requested range.
type CodeActionContext struct {
	Diagnostics []Diagnostic          `json:"diagnostics"`
	Only        []string              `json:"only,omitempty"`
	TriggerKind CodeActionTriggerKind `json:"triggerKind,omitempty"`
}

// CodeActionParams are the textDocument/codeAction params.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// CodeActionDisabled explains why an action cannot be applied.
type CodeActionDisabled struct {
	Reason string `json:"reason"`
}

// CodeAction is a code action. A bare Command in a codeAction result is
// normalized to a CodeAction with only Command set (see ParseCodeActions).
type CodeAction struct {
	Title       string              `json:"title"`
	Kind        string              `json:"kind,omitempty"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
	IsPreferred bool                `json:"isPreferred,omitempty"`
	Disabled    *CodeActionDisabled `json:"disabled,omitempty"`
	Edit        *WorkspaceEdit      `json:"edit,omitempty"`
	Command     *Command            `json:"command,omitempty"`
	Data        json.RawMessage     `json:"data,omitempty"`
}

// ParseCodeActions decodes a codeAction result: null or an array of
// Command and CodeAction literals.
func ParseCodeActions(data []byte) ([]CodeAction, error) {
	if isNull(data) {
		return nil, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("protocol: decoding code actions: %w", err)
	}
	out := make([]CodeAction, 0, len(raws))
	for _, r := range raws {
		var probe struct {
			Command json.RawMessage `json:"command"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, fmt.Errorf("protocol: decoding code action: %w", err)
		}
		if len(probe.Command) > 0 && bytes.TrimSpace(probe.Command)[0] == '"' {
			var cmd Command
			if err := json.Unmarshal(r, &cmd); err != nil {
				return nil, fmt.Errorf("protocol: decoding command: %w", err)
			}
			out = append(out, CodeAction{Title: cmd.Title, Command: &cmd})
			continue
		}
		var ca CodeAction
		if err := json.Unmarshal(r, &ca); err != nil {
			return nil, fmt.Errorf("protocol: decoding code action: %w", err)
		}
		out = append(out, ca)
	}
	return out, nil
}

// --- Formatting -----------------------------------------------------------

// FormattingOptions are the formatting preferences.
type FormattingOptions struct {
	TabSize                int  `json:"tabSize"`
	InsertSpaces           bool `json:"insertSpaces"`
	TrimTrailingWhitespace bool `json:"trimTrailingWhitespace,omitempty"`
	InsertFinalNewline     bool `json:"insertFinalNewline,omitempty"`
	TrimFinalNewlines      bool `json:"trimFinalNewlines,omitempty"`
}

// DocumentFormattingParams are the textDocument/formatting params.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// DocumentRangeFormattingParams are the textDocument/rangeFormatting params.
type DocumentRangeFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Options      FormattingOptions      `json:"options"`
}

// --- Rename ---------------------------------------------------------------

// RenameParams are the textDocument/rename params.
type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

// PrepareRenameResult is the normalized prepareRename result. With
// DefaultBehavior the client determines the range itself (the word at the
// position).
type PrepareRenameResult struct {
	Range           *Range
	Placeholder     string
	DefaultBehavior bool
}

// ParsePrepareRename decodes a prepareRename result (null, Range,
// {range, placeholder} or {defaultBehavior}). A null result yields nil.
func ParsePrepareRename(data []byte) (*PrepareRenameResult, error) {
	if isNull(data) {
		return nil, nil
	}
	var probe struct {
		Range           *Range    `json:"range"`
		Placeholder     string    `json:"placeholder"`
		DefaultBehavior bool      `json:"defaultBehavior"`
		Start           *Position `json:"start"`
		End             *Position `json:"end"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("protocol: decoding prepareRename: %w", err)
	}
	switch {
	case probe.Start != nil && probe.End != nil:
		return &PrepareRenameResult{Range: &Range{Start: *probe.Start, End: *probe.End}}, nil
	case probe.Range != nil:
		return &PrepareRenameResult{Range: probe.Range, Placeholder: probe.Placeholder}, nil
	}
	return &PrepareRenameResult{DefaultBehavior: probe.DefaultBehavior}, nil
}

// --- Symbols --------------------------------------------------------------

// SymbolKind classifies symbols.
type SymbolKind int

// Symbol kinds.
const (
	SymbolFile          SymbolKind = 1
	SymbolModule        SymbolKind = 2
	SymbolNamespace     SymbolKind = 3
	SymbolPackage       SymbolKind = 4
	SymbolClass         SymbolKind = 5
	SymbolMethod        SymbolKind = 6
	SymbolProperty      SymbolKind = 7
	SymbolField         SymbolKind = 8
	SymbolConstructor   SymbolKind = 9
	SymbolEnum          SymbolKind = 10
	SymbolInterface     SymbolKind = 11
	SymbolFunction      SymbolKind = 12
	SymbolVariable      SymbolKind = 13
	SymbolConstant      SymbolKind = 14
	SymbolString        SymbolKind = 15
	SymbolNumber        SymbolKind = 16
	SymbolBoolean       SymbolKind = 17
	SymbolArray         SymbolKind = 18
	SymbolObject        SymbolKind = 19
	SymbolKey           SymbolKind = 20
	SymbolNull          SymbolKind = 21
	SymbolEnumMember    SymbolKind = 22
	SymbolStruct        SymbolKind = 23
	SymbolEvent         SymbolKind = 24
	SymbolOperator      SymbolKind = 25
	SymbolTypeParameter SymbolKind = 26
)

var symbolKindNames = [...]string{
	"", "file", "module", "namespace", "package", "class", "method", "property", "field",
	"constructor", "enum", "interface", "function", "variable", "constant", "string",
	"number", "boolean", "array", "object", "key", "null", "enumMember", "struct",
	"event", "operator", "typeParameter",
}

// String returns the API name of the kind ("class", "method", ...).
func (k SymbolKind) String() string {
	if k > 0 && int(k) < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return "unknown"
}

// AllSymbolKinds lists every kind (client capability valueSet).
func AllSymbolKinds() []SymbolKind {
	out := make([]SymbolKind, 0, len(symbolKindNames)-1)
	for k := SymbolFile; k <= SymbolTypeParameter; k++ {
		out = append(out, k)
	}
	return out
}

// DocumentSymbolParams are the textDocument/documentSymbol params.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// WorkspaceSymbolParams are the workspace/symbol params.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// DocumentSymbol is a symbol in a document hierarchy.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is a flat symbol with its location. For workspace
// symbols without a range (WorkspaceSymbol with location {uri}) HasRange
// is false.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Deprecated    bool       `json:"deprecated,omitempty"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
	HasRange      bool       `json:"-"`
}

// ParseDocumentSymbols decodes a documentSymbol result: DocumentSymbol[]
// (hierarchical) or SymbolInformation[] (flat, converted to DocumentSymbols
// without children whose ranges are the locations).
func ParseDocumentSymbols(data []byte) ([]DocumentSymbol, error) {
	if isNull(data) {
		return nil, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("protocol: decoding document symbols: %w", err)
	}
	out := make([]DocumentSymbol, 0, len(raws))
	for _, r := range raws {
		var probe struct {
			Location *Location `json:"location"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, fmt.Errorf("protocol: decoding document symbol: %w", err)
		}
		if probe.Location != nil {
			var si SymbolInformation
			if err := json.Unmarshal(r, &si); err != nil {
				return nil, fmt.Errorf("protocol: decoding symbol information: %w", err)
			}
			out = append(out, DocumentSymbol{Name: si.Name, Detail: si.ContainerName, Kind: si.Kind, Deprecated: si.Deprecated, Range: si.Location.Range, SelectionRange: si.Location.Range})
			continue
		}
		var ds DocumentSymbol
		if err := json.Unmarshal(r, &ds); err != nil {
			return nil, fmt.Errorf("protocol: decoding document symbol: %w", err)
		}
		out = append(out, ds)
	}
	return out, nil
}

// ParseWorkspaceSymbols decodes a workspace/symbol result:
// SymbolInformation[] or WorkspaceSymbol[] (whose location may lack a
// range).
func ParseWorkspaceSymbols(data []byte) ([]SymbolInformation, error) {
	if isNull(data) {
		return nil, nil
	}
	var raws []struct {
		Name          string          `json:"name"`
		Kind          SymbolKind      `json:"kind"`
		Deprecated    bool            `json:"deprecated"`
		ContainerName string          `json:"containerName"`
		Location      json.RawMessage `json:"location"`
	}
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("protocol: decoding workspace symbols: %w", err)
	}
	out := make([]SymbolInformation, 0, len(raws))
	for _, r := range raws {
		var loc struct {
			URI   DocumentURI `json:"uri"`
			Range *Range      `json:"range"`
		}
		if err := json.Unmarshal(r.Location, &loc); err != nil {
			return nil, fmt.Errorf("protocol: decoding symbol location: %w", err)
		}
		si := SymbolInformation{Name: r.Name, Kind: r.Kind, Deprecated: r.Deprecated, ContainerName: r.ContainerName, Location: Location{URI: loc.URI}}
		if loc.Range != nil {
			si.Location.Range, si.HasRange = *loc.Range, true
		}
		out = append(out, si)
	}
	return out, nil
}

// --- Capabilities ---------------------------------------------------------

// RenameOptions is the renameProvider capability in object form.
type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider,omitempty"`
}

// CodeActionOptions is the codeActionProvider capability in object form.
type CodeActionOptions struct {
	CodeActionKinds []string `json:"codeActionKinds,omitempty"`
	ResolveProvider bool     `json:"resolveProvider,omitempty"`
}

// ExecuteCommandOptions is the executeCommandProvider capability.
type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

// Decode unmarshals the object form of a provider capability into v; it
// reports false for the boolean form.
func (p *Provider) Decode(v any) bool {
	if p == nil || len(p.Options) == 0 {
		return false
	}
	return json.Unmarshal(p.Options, v) == nil
}

// WorkspaceEditClientCapabilities are workspace edit capabilities.
type WorkspaceEditClientCapabilities struct {
	DocumentChanges    bool     `json:"documentChanges,omitempty"`
	ResourceOperations []string `json:"resourceOperations,omitempty"`
	FailureHandling    string   `json:"failureHandling,omitempty"`
}

// SymbolKindCapabilities lists the supported symbol kinds.
type SymbolKindCapabilities struct {
	ValueSet []SymbolKind `json:"valueSet,omitempty"`
}

// WorkspaceSymbolClientCapabilities are workspace/symbol capabilities.
type WorkspaceSymbolClientCapabilities struct {
	SymbolKind *SymbolKindCapabilities `json:"symbolKind,omitempty"`
}

// DocumentSymbolClientCapabilities are documentSymbol capabilities.
type DocumentSymbolClientCapabilities struct {
	SymbolKind                        *SymbolKindCapabilities `json:"symbolKind,omitempty"`
	HierarchicalDocumentSymbolSupport bool                    `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// CodeActionKindValueSet lists the supported code action kinds.
type CodeActionKindValueSet struct {
	ValueSet []string `json:"valueSet"`
}

// CodeActionLiteralSupport announces CodeAction literals in results.
type CodeActionLiteralSupport struct {
	CodeActionKind CodeActionKindValueSet `json:"codeActionKind"`
}

// CodeActionResolveSupport lists properties the client can resolve lazily.
type CodeActionResolveSupport struct {
	Properties []string `json:"properties"`
}

// CodeActionClientCapabilities are codeAction capabilities.
type CodeActionClientCapabilities struct {
	CodeActionLiteralSupport *CodeActionLiteralSupport `json:"codeActionLiteralSupport,omitempty"`
	IsPreferredSupport       bool                      `json:"isPreferredSupport,omitempty"`
	DisabledSupport          bool                      `json:"disabledSupport,omitempty"`
	DataSupport              bool                      `json:"dataSupport,omitempty"`
	ResolveSupport           *CodeActionResolveSupport `json:"resolveSupport,omitempty"`
}

// RenameClientCapabilities are rename capabilities.
type RenameClientCapabilities struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

// EmptyClientCapabilities is a capability without options (dynamic
// registration is not supported).
type EmptyClientCapabilities struct{}
