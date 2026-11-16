package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Method names used by the client.
const (
	MethodInitialize             = "initialize"
	MethodInitialized            = "initialized"
	MethodShutdown               = "shutdown"
	MethodExit                   = "exit"
	MethodDidOpen                = "textDocument/didOpen"
	MethodDidChange              = "textDocument/didChange"
	MethodDidSave                = "textDocument/didSave"
	MethodDidClose               = "textDocument/didClose"
	MethodCompletion             = "textDocument/completion"
	MethodHover                  = "textDocument/hover"
	MethodSignatureHelp          = "textDocument/signatureHelp"
	MethodDefinition             = "textDocument/definition"
	MethodImplementation         = "textDocument/implementation"
	MethodSemanticTokensFull     = "textDocument/semanticTokens/full"
	MethodPublishDiagnostics     = "textDocument/publishDiagnostics"
	MethodWorkspaceConfiguration = "workspace/configuration"
	MethodWorkDoneProgressCreate = "window/workDoneProgress/create"
	MethodRegisterCapability     = "client/registerCapability"
	MethodUnregisterCapability   = "client/unregisterCapability"
	MethodShowMessage            = "window/showMessage"
	MethodShowMessageRequest     = "window/showMessageRequest"
	MethodLogMessage             = "window/logMessage"
	MethodProgress               = "$/progress"
	MethodDidRenameFiles         = "workspace/didRenameFiles"
)

// isNull reports whether data is empty or the JSON literal null.
func isNull(data []byte) bool {
	d := bytes.TrimSpace(data)
	return len(d) == 0 || bytes.Equal(d, []byte("null"))
}

// IntOrString holds a value that LSP allows to be either an integer or a
// string, such as diagnostic codes or progress tokens.
type IntOrString struct {
	Value string
	IsInt bool
}

// String returns the value as text.
func (v IntOrString) String() string { return v.Value }

// MarshalJSON implements json.Marshaler.
func (v IntOrString) MarshalJSON() ([]byte, error) {
	if v.IsInt {
		return []byte(v.Value), nil
	}
	return json.Marshal(v.Value)
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *IntOrString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = IntOrString{Value: s}
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("protocol: expected integer or string, got %s", data)
	}
	*v = IntOrString{Value: n.String(), IsInt: true}
	return nil
}

// --- Text documents -------------------------------------------------------

// TextDocumentIdentifier identifies a document.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// VersionedTextDocumentIdentifier identifies a specific document version.
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"`
}

// TextDocumentItem is an opened document with its content.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// TextDocumentContentChangeEvent describes a change. With Range set it is
// incremental, otherwise Text is the full new content.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// TextDocumentPositionParams addresses a position in a document.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DidOpenTextDocumentParams are the textDocument/didOpen params.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams are the textDocument/didChange params.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// DidSaveTextDocumentParams are the textDocument/didSave params.
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// DidCloseTextDocumentParams are the textDocument/didClose params.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// TextEdit replaces Range with NewText.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// --- Markup ---------------------------------------------------------------

// Markup kinds.
const (
	PlainText = "plaintext"
	Markdown  = "markdown"
)

// MarkupContent is text in plain text or markdown.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// UnmarshalJSON accepts MarkupContent as well as the legacy forms
// string, MarkedString ({language, value}) and arrays of those, which are
// all normalized to MarkupContent.
func (m *MarkupContent) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		*m = MarkupContent{}
		return nil
	}
	switch bytes.TrimSpace(data)[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*m = MarkupContent{Kind: Markdown, Value: s}
		return nil
	case '[':
		var parts []MarkupContent
		if err := json.Unmarshal(data, &parts); err != nil {
			return err
		}
		var buf bytes.Buffer
		for i, p := range parts {
			if i > 0 {
				buf.WriteString("\n\n")
			}
			buf.WriteString(p.Value)
		}
		*m = MarkupContent{Kind: Markdown, Value: buf.String()}
		return nil
	}
	var raw struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Kind == "" {
		// MarkedString {language, value}: a fenced code block.
		*m = MarkupContent{Kind: Markdown, Value: "```" + raw.Language + "\n" + raw.Value + "\n```"}
		return nil
	}
	*m = MarkupContent{Kind: raw.Kind, Value: raw.Value}
	return nil
}

// --- Diagnostics ----------------------------------------------------------

// DiagnosticSeverity ranks diagnostics.
type DiagnosticSeverity int

// Diagnostic severities.
const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

// DiagnosticTag marks diagnostics for special rendering.
type DiagnosticTag int

// Diagnostic tags.
const (
	TagUnnecessary DiagnosticTag = 1
	TagDeprecated  DiagnosticTag = 2
)

// Diagnostic is a problem reported by the server.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     *IntOrString       `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
	Tags     []DiagnosticTag    `json:"tags,omitempty"`
}

// PublishDiagnosticsParams are the textDocument/publishDiagnostics params.
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- Completion -----------------------------------------------------------

// InsertTextFormat tells how insert text is interpreted.
type InsertTextFormat int

// Insert text formats.
const (
	InsertTextPlain   InsertTextFormat = 1
	InsertTextSnippet InsertTextFormat = 2
)

// CompletionItemKind classifies completion items.
type CompletionItemKind int

// Completion item kinds.
const (
	KindText          CompletionItemKind = 1
	KindMethod        CompletionItemKind = 2
	KindFunction      CompletionItemKind = 3
	KindConstructor   CompletionItemKind = 4
	KindField         CompletionItemKind = 5
	KindVariable      CompletionItemKind = 6
	KindClass         CompletionItemKind = 7
	KindInterface     CompletionItemKind = 8
	KindModule        CompletionItemKind = 9
	KindProperty      CompletionItemKind = 10
	KindUnit          CompletionItemKind = 11
	KindValue         CompletionItemKind = 12
	KindEnum          CompletionItemKind = 13
	KindKeyword       CompletionItemKind = 14
	KindSnippet       CompletionItemKind = 15
	KindColor         CompletionItemKind = 16
	KindFile          CompletionItemKind = 17
	KindReference     CompletionItemKind = 18
	KindFolder        CompletionItemKind = 19
	KindEnumMember    CompletionItemKind = 20
	KindConstant      CompletionItemKind = 21
	KindStruct        CompletionItemKind = 22
	KindEvent         CompletionItemKind = 23
	KindOperator      CompletionItemKind = 24
	KindTypeParameter CompletionItemKind = 25
)

// CompletionTextEdit is either a TextEdit (Range set) or an
// InsertReplaceEdit (Insert and Replace set).
type CompletionTextEdit struct {
	NewText string `json:"newText"`
	Range   *Range `json:"range,omitempty"`
	Insert  *Range `json:"insert,omitempty"`
	Replace *Range `json:"replace,omitempty"`
}

// EditRange returns the range to replace when accepting the item: Range for
// a TextEdit, Insert for an InsertReplaceEdit.
func (e *CompletionTextEdit) EditRange() Range {
	if e.Range != nil {
		return *e.Range
	}
	if e.Insert != nil {
		return *e.Insert
	}
	return Range{}
}

// CompletionItem is a single completion proposal.
type CompletionItem struct {
	Label               string              `json:"label"`
	Kind                CompletionItemKind  `json:"kind,omitempty"`
	Detail              string              `json:"detail,omitempty"`
	Documentation       *MarkupContent      `json:"documentation,omitempty"`
	Deprecated          bool                `json:"deprecated,omitempty"`
	Preselect           bool                `json:"preselect,omitempty"`
	SortText            string              `json:"sortText,omitempty"`
	FilterText          string              `json:"filterText,omitempty"`
	InsertText          string              `json:"insertText,omitempty"`
	InsertTextFormat    InsertTextFormat    `json:"insertTextFormat,omitempty"`
	TextEdit            *CompletionTextEdit `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit          `json:"additionalTextEdits,omitempty"`
	CommitCharacters    []string            `json:"commitCharacters,omitempty"`
	Data                json.RawMessage     `json:"data,omitempty"`
}

// CompletionList is a list of completion items.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// UnmarshalJSON accepts both CompletionList and a bare CompletionItem array.
func (l *CompletionList) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		*l = CompletionList{}
		return nil
	}
	if bytes.TrimSpace(data)[0] == '[' {
		var items []CompletionItem
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		*l = CompletionList{Items: items}
		return nil
	}
	type plain CompletionList
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*l = CompletionList(p)
	return nil
}

// CompletionParams are the textDocument/completion params.
type CompletionParams struct {
	TextDocumentPositionParams
	Context *CompletionContext `json:"context,omitempty"`
}

// CompletionTriggerKind tells how completion was triggered.
type CompletionTriggerKind int

// Completion trigger kinds.
const (
	TriggerInvoked                CompletionTriggerKind = 1
	TriggerCharacter              CompletionTriggerKind = 2
	TriggerForIncompleteCompletes CompletionTriggerKind = 3
)

// CompletionContext describes how completion was triggered.
type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter string                `json:"triggerCharacter,omitempty"`
}

// --- Hover / signature help ----------------------------------------------

// Hover is the result of textDocument/hover.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// ParameterLabel is a parameter label: either a substring of the signature
// label (Text) or an offset pair into it in UTF-16 units (Offsets).
type ParameterLabel struct {
	Text    string
	Offsets *[2]int
}

// MarshalJSON implements json.Marshaler.
func (p ParameterLabel) MarshalJSON() ([]byte, error) {
	if p.Offsets != nil {
		return json.Marshal(p.Offsets)
	}
	return json.Marshal(p.Text)
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *ParameterLabel) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '[' {
		var off [2]int
		if err := json.Unmarshal(data, &off); err != nil {
			return err
		}
		*p = ParameterLabel{Offsets: &off}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*p = ParameterLabel{Text: s}
	return nil
}

// ParameterInformation describes one parameter of a signature.
type ParameterInformation struct {
	Label         ParameterLabel `json:"label"`
	Documentation *MarkupContent `json:"documentation,omitempty"`
}

// SignatureInformation describes one callable signature.
type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   *MarkupContent         `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters,omitempty"`
	ActiveParameter *int                   `json:"activeParameter,omitempty"`
}

// SignatureHelp is the result of textDocument/signatureHelp.
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

// --- Locations ------------------------------------------------------------

// ParseLocations decodes a definition/implementation result, which may be
// null, a Location, a Location array or a LocationLink array. Links are
// converted to locations pointing at their target selection range.
func ParseLocations(data []byte) ([]Location, error) {
	if isNull(data) {
		return nil, nil
	}
	if bytes.TrimSpace(data)[0] != '[' {
		var loc Location
		if err := json.Unmarshal(data, &loc); err != nil {
			return nil, fmt.Errorf("protocol: decoding location: %w", err)
		}
		return []Location{loc}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("protocol: decoding locations: %w", err)
	}
	out := make([]Location, 0, len(raws))
	for _, r := range raws {
		var probe struct {
			URI                  DocumentURI `json:"uri"`
			Range                Range       `json:"range"`
			TargetURI            DocumentURI `json:"targetUri"`
			TargetSelectionRange Range       `json:"targetSelectionRange"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, fmt.Errorf("protocol: decoding location: %w", err)
		}
		if probe.TargetURI != "" {
			out = append(out, Location{URI: probe.TargetURI, Range: probe.TargetSelectionRange})
		} else {
			out = append(out, Location{URI: probe.URI, Range: probe.Range})
		}
	}
	return out, nil
}

// --- Semantic tokens ------------------------------------------------------

// SemanticTokensLegend names the token types and modifiers by index.
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

// SemanticTokens is the result of textDocument/semanticTokens/full.
type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

// SemanticTokensParams are the textDocument/semanticTokens/full params.
type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// SemanticToken is one decoded token with absolute position.
type SemanticToken struct {
	Line      int
	StartChar int // UTF-16 units
	Length    int // UTF-16 units
	Type      string
	Modifiers []string
}

// DecodeSemanticTokens resolves the relative five-integer encoding of data
// into absolute tokens using legend. Unknown type indices yield an empty
// type name; unknown modifier bits are ignored.
func DecodeSemanticTokens(data []uint32, legend SemanticTokensLegend) ([]SemanticToken, error) {
	if len(data)%5 != 0 {
		return nil, fmt.Errorf("protocol: semantic token data length %d is not a multiple of 5", len(data))
	}
	out := make([]SemanticToken, 0, len(data)/5)
	line, char := 0, 0
	for i := 0; i < len(data); i += 5 {
		dl, dc := int(data[i]), int(data[i+1])
		if dl > 0 {
			line += dl
			char = dc
		} else {
			char += dc
		}
		tok := SemanticToken{Line: line, StartChar: char, Length: int(data[i+2])}
		if t := int(data[i+3]); t < len(legend.TokenTypes) {
			tok.Type = legend.TokenTypes[t]
		}
		for bits, bit := data[i+4], 0; bits != 0; bits, bit = bits>>1, bit+1 {
			if bits&1 != 0 && bit < len(legend.TokenModifiers) {
				tok.Modifiers = append(tok.Modifiers, legend.TokenModifiers[bit])
			}
		}
		out = append(out, tok)
	}
	return out, nil
}

// --- Capabilities ---------------------------------------------------------

// TextDocumentSyncKind tells how the server wants document changes.
type TextDocumentSyncKind int

// Sync kinds.
const (
	SyncNone        TextDocumentSyncKind = 0
	SyncFull        TextDocumentSyncKind = 1
	SyncIncremental TextDocumentSyncKind = 2
)

// SaveOptions configures didSave.
type SaveOptions struct {
	IncludeText bool `json:"includeText,omitempty"`
}

// TextDocumentSyncOptions is the normalized textDocumentSync capability.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose,omitempty"`
	Change    TextDocumentSyncKind `json:"change"`
	Save      *SaveOptions         `json:"save,omitempty"`
}

// UnmarshalJSON accepts a bare TextDocumentSyncKind number as well as the
// options object; save may be a bool or SaveOptions.
func (o *TextDocumentSyncOptions) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		*o = TextDocumentSyncOptions{}
		return nil
	}
	if k, err := strconv.Atoi(string(bytes.TrimSpace(data))); err == nil {
		*o = TextDocumentSyncOptions{OpenClose: k != 0, Change: TextDocumentSyncKind(k)}
		if k != 0 {
			o.Save = &SaveOptions{}
		}
		return nil
	}
	var raw struct {
		OpenClose bool                 `json:"openClose"`
		Change    TextDocumentSyncKind `json:"change"`
		Save      json.RawMessage      `json:"save"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = TextDocumentSyncOptions{OpenClose: raw.OpenClose, Change: raw.Change}
	switch s := string(bytes.TrimSpace(raw.Save)); s {
	case "", "null", "false":
	case "true":
		o.Save = &SaveOptions{}
	default:
		var so SaveOptions
		if err := json.Unmarshal(raw.Save, &so); err != nil {
			return err
		}
		o.Save = &so
	}
	return nil
}

// Provider is a capability given either as a bool or as an options object.
type Provider struct {
	Enabled bool
	Options json.RawMessage
}

// MarshalJSON implements json.Marshaler.
func (p Provider) MarshalJSON() ([]byte, error) {
	if len(p.Options) > 0 {
		return p.Options, nil
	}
	return json.Marshal(p.Enabled)
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *Provider) UnmarshalJSON(data []byte) error {
	switch s := string(bytes.TrimSpace(data)); s {
	case "null", "false":
		*p = Provider{}
	case "true":
		*p = Provider{Enabled: true}
	default:
		*p = Provider{Enabled: true, Options: append(json.RawMessage(nil), data...)}
	}
	return nil
}

// CompletionOptions is the completionProvider capability.
type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
	ResolveProvider   bool     `json:"resolveProvider,omitempty"`
}

// SignatureHelpOptions is the signatureHelpProvider capability.
type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// SemanticTokensOptions is the semanticTokensProvider capability.
type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Range  *Provider            `json:"range,omitempty"`
	Full   *Provider            `json:"full,omitempty"`
}

// ServerCapabilities is the subset of server capabilities Tiegel uses.
type ServerCapabilities struct {
	PositionEncoding       string                   `json:"positionEncoding,omitempty"`
	TextDocumentSync       *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	CompletionProvider     *CompletionOptions       `json:"completionProvider,omitempty"`
	HoverProvider          *Provider                `json:"hoverProvider,omitempty"`
	SignatureHelpProvider  *SignatureHelpOptions    `json:"signatureHelpProvider,omitempty"`
	DefinitionProvider     *Provider                `json:"definitionProvider,omitempty"`
	ImplementationProvider *Provider                `json:"implementationProvider,omitempty"`
	SemanticTokensProvider *SemanticTokensOptions   `json:"semanticTokensProvider,omitempty"`

	ReferencesProvider              *Provider                    `json:"referencesProvider,omitempty"`
	DocumentHighlightProvider       *Provider                    `json:"documentHighlightProvider,omitempty"`
	CodeActionProvider              *Provider                    `json:"codeActionProvider,omitempty"`
	DocumentFormattingProvider      *Provider                    `json:"documentFormattingProvider,omitempty"`
	DocumentRangeFormattingProvider *Provider                    `json:"documentRangeFormattingProvider,omitempty"`
	RenameProvider                  *Provider                    `json:"renameProvider,omitempty"`
	DocumentSymbolProvider          *Provider                    `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider         *Provider                    `json:"workspaceSymbolProvider,omitempty"`
	ExecuteCommandProvider          *ExecuteCommandOptions       `json:"executeCommandProvider,omitempty"`
	Workspace                       *WorkspaceServerCapabilities `json:"workspace,omitempty"`
}

// IsEnabled reports whether an optional provider capability is set.
func (p *Provider) IsEnabled() bool { return p != nil && p.Enabled }

// --- Initialize -----------------------------------------------------------

// ClientInfo identifies the client (and, as ServerInfo, the server).
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerInfo identifies the server.
type ServerInfo = ClientInfo

// WorkspaceFolder is a root folder of the workspace.
type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name"`
}

// ClientCapabilities announces what the client supports. Only the fields
// Tiegel needs are modelled.
type ClientCapabilities struct {
	General      *GeneralClientCapabilities      `json:"general,omitempty"`
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Window       *WindowClientCapabilities       `json:"window,omitempty"`
}

// GeneralClientCapabilities are general client capabilities.
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// WorkspaceClientCapabilities are workspace client capabilities.
type WorkspaceClientCapabilities struct {
	Configuration    bool                               `json:"configuration,omitempty"`
	WorkspaceFolders bool                               `json:"workspaceFolders,omitempty"`
	ApplyEdit        bool                               `json:"applyEdit,omitempty"`
	WorkspaceEdit    *WorkspaceEditClientCapabilities   `json:"workspaceEdit,omitempty"`
	Symbol           *WorkspaceSymbolClientCapabilities `json:"symbol,omitempty"`
	ExecuteCommand   *EmptyClientCapabilities           `json:"executeCommand,omitempty"`
	FileOperations   *FileOperationClientCapabilities   `json:"fileOperations,omitempty"`
}

// FileOperationClientCapabilities announce the workspace/did* file
// notifications the client sends.
type FileOperationClientCapabilities struct {
	DidRename bool `json:"didRename,omitempty"`
}

// WorkspaceServerCapabilities are the workspace capabilities of a server.
type WorkspaceServerCapabilities struct {
	FileOperations *FileOperationsServerCapabilities `json:"fileOperations,omitempty"`
}

// FileOperationsServerCapabilities lists the file operations a server
// wants to hear about.
type FileOperationsServerCapabilities struct {
	DidRename *FileOperationRegistrationOptions `json:"didRename,omitempty"`
}

// FileOperationRegistrationOptions carry the filters of a file operation
// registration (kept raw; Tiegel sends all renames).
type FileOperationRegistrationOptions struct {
	Filters []json.RawMessage `json:"filters,omitempty"`
}

// FileRename is one renamed file or folder.
type FileRename struct {
	OldURI DocumentURI `json:"oldUri"`
	NewURI DocumentURI `json:"newUri"`
}

// RenameFilesParams are the params of workspace/didRenameFiles.
type RenameFilesParams struct {
	Files []FileRename `json:"files"`
}

// WindowClientCapabilities are window client capabilities.
type WindowClientCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress,omitempty"`
}

// TextDocumentClientCapabilities are text document client capabilities.
type TextDocumentClientCapabilities struct {
	Synchronization    *SyncClientCapabilities               `json:"synchronization,omitempty"`
	Completion         *CompletionClientCapabilities         `json:"completion,omitempty"`
	Hover              *HoverClientCapabilities              `json:"hover,omitempty"`
	SignatureHelp      *SignatureHelpClientCapabilities      `json:"signatureHelp,omitempty"`
	Definition         *LinkClientCapabilities               `json:"definition,omitempty"`
	Implementation     *LinkClientCapabilities               `json:"implementation,omitempty"`
	PublishDiagnostics *PublishDiagnosticsClientCapabilities `json:"publishDiagnostics,omitempty"`
	SemanticTokens     *SemanticTokensClientCapabilities     `json:"semanticTokens,omitempty"`
	References         *EmptyClientCapabilities              `json:"references,omitempty"`
	DocumentHighlight  *EmptyClientCapabilities              `json:"documentHighlight,omitempty"`
	CodeAction         *CodeActionClientCapabilities         `json:"codeAction,omitempty"`
	Formatting         *EmptyClientCapabilities              `json:"formatting,omitempty"`
	RangeFormatting    *EmptyClientCapabilities              `json:"rangeFormatting,omitempty"`
	Rename             *RenameClientCapabilities             `json:"rename,omitempty"`
	DocumentSymbol     *DocumentSymbolClientCapabilities     `json:"documentSymbol,omitempty"`
}

// SyncClientCapabilities are document sync capabilities.
type SyncClientCapabilities struct {
	DidSave bool `json:"didSave,omitempty"`
}

// CompletionClientCapabilities are completion capabilities.
type CompletionClientCapabilities struct {
	CompletionItem *CompletionItemClientCapabilities `json:"completionItem,omitempty"`
	ContextSupport bool                              `json:"contextSupport,omitempty"`
}

// CompletionItemClientCapabilities are completion item capabilities.
type CompletionItemClientCapabilities struct {
	SnippetSupport          bool     `json:"snippetSupport,omitempty"`
	DocumentationFormat     []string `json:"documentationFormat,omitempty"`
	DeprecatedSupport       bool     `json:"deprecatedSupport,omitempty"`
	PreselectSupport        bool     `json:"preselectSupport,omitempty"`
	InsertReplaceSupport    bool     `json:"insertReplaceSupport,omitempty"`
	LabelDetailsSupport     bool     `json:"labelDetailsSupport,omitempty"`
	CommitCharactersSupport bool     `json:"commitCharactersSupport,omitempty"`
}

// HoverClientCapabilities are hover capabilities.
type HoverClientCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

// SignatureHelpClientCapabilities are signature help capabilities.
type SignatureHelpClientCapabilities struct {
	SignatureInformation *SignatureInformationClientCapabilities `json:"signatureInformation,omitempty"`
}

// SignatureInformationClientCapabilities are signature info capabilities.
type SignatureInformationClientCapabilities struct {
	DocumentationFormat    []string `json:"documentationFormat,omitempty"`
	ActiveParameterSupport bool     `json:"activeParameterSupport,omitempty"`
}

// LinkClientCapabilities are definition/implementation capabilities.
type LinkClientCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

// PublishDiagnosticsClientCapabilities are diagnostics capabilities.
type PublishDiagnosticsClientCapabilities struct {
	VersionSupport bool `json:"versionSupport,omitempty"`
}

// SemanticTokensClientCapabilities are semantic token capabilities.
type SemanticTokensClientCapabilities struct {
	Requests       SemanticTokensRequests `json:"requests"`
	TokenTypes     []string               `json:"tokenTypes"`
	TokenModifiers []string               `json:"tokenModifiers"`
	Formats        []string               `json:"formats"`
}

// SemanticTokensRequests lists supported semantic token requests.
type SemanticTokensRequests struct {
	Full bool `json:"full,omitempty"`
}

// InitializeParams are the initialize params.
type InitializeParams struct {
	ProcessID             *int               `json:"processId"`
	ClientInfo            *ClientInfo        `json:"clientInfo,omitempty"`
	Locale                string             `json:"locale,omitempty"`
	RootURI               *DocumentURI       `json:"rootUri"`
	RootPath              string             `json:"rootPath,omitempty"`
	InitializationOptions any                `json:"initializationOptions,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

// InitializeResult is the initialize result.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ConfigurationItem is one requested configuration section.
type ConfigurationItem struct {
	ScopeURI DocumentURI `json:"scopeUri,omitempty"`
	Section  string      `json:"section,omitempty"`
}

// ConfigurationParams are the workspace/configuration params.
type ConfigurationParams struct {
	Items []ConfigurationItem `json:"items"`
}

// MessageType is the severity of window messages.
type MessageType int

// Message types.
const (
	MessageError   MessageType = 1
	MessageWarning MessageType = 2
	MessageInfo    MessageType = 3
	MessageLog     MessageType = 4
)

// LogMessageParams are the window/logMessage and window/showMessage params.
type LogMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}
