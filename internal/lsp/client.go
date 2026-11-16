// Package lsp is Tiegel's Language Server Protocol client. It starts a
// language server as child process (or connects to an existing stream),
// performs the initialize handshake, keeps open documents in sync and
// offers typed methods for the language features the editor uses.
//
// The package does not import any UI code.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// ErrNotSupported is returned when the server lacks the capability for a
// request.
var ErrNotSupported = errors.New("lsp: not supported by server")

// ErrNotOpen is returned for document operations on documents that were not
// opened with DidOpen.
var ErrNotOpen = errors.New("lsp: document not open")

// Config configures a Client.
type Config struct {
	// Command is the server executable; Args are its arguments. Only used by
	// Start.
	Command string
	Args    []string
	// Dir is the working directory of the server process. Defaults to
	// RootDir.
	Dir string
	// Env holds additional "KEY=value" entries appended to the current
	// process environment.
	Env []string

	// RootDir is the absolute project root sent as rootUri and workspace
	// folder. May be empty.
	RootDir string

	// InitializationOptions is sent verbatim as initializationOptions.
	// Phpactor, for example, accepts flat configuration keys such as
	// "php.version" here.
	InitializationOptions map[string]any
	// Settings answers workspace/configuration requests. A requested section
	// "a.b" is looked up as flat key "a.b" first, then as nested maps.
	Settings map[string]any

	// ClientName and ClientVersion are sent as clientInfo.
	ClientName    string
	ClientVersion string

	// Logger receives server stderr, window/logMessage and protocol
	// warnings. Defaults to slog.Default().
	Logger *slog.Logger

	// OnDiagnostics is called for every textDocument/publishDiagnostics
	// notification, in arrival order, from a single goroutine.
	OnDiagnostics func(protocol.PublishDiagnosticsParams)
	// OnShowMessage is called for window/showMessage notifications.
	OnShowMessage func(protocol.LogMessageParams)
}

type document struct {
	languageID string
	version    int
	text       string
}

// Client is a connection to one language server.
type Client struct {
	cfg  Config
	log  *slog.Logger
	conn *jsonrpc2.Conn
	proc *process // nil when connected to a plain stream

	caps protocol.ServerCapabilities
	info *protocol.ServerInfo

	mu   sync.Mutex
	docs map[protocol.DocumentURI]*document
}

// Start launches cfg.Command as child process speaking LSP over stdio and
// initializes it. On failure the process is terminated.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Command == "" {
		return nil, errors.New("lsp: no server command configured")
	}
	log := loggerOf(cfg)
	dir := cfg.Dir
	if dir == "" {
		dir = cfg.RootDir
	}
	p, err := startProcess(cfg.Command, cfg.Args, dir, cfg.Env, log)
	if err != nil {
		return nil, err
	}
	c := newClient(p, cfg, log)
	c.proc = p
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Connect initializes a server reachable over rwc (for example an
// in-process server or a socket). Closing the client closes rwc.
func Connect(ctx context.Context, rwc io.ReadWriteCloser, cfg Config) (*Client, error) {
	c := newClient(rwc, cfg, loggerOf(cfg))
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func loggerOf(cfg Config) *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	return slog.Default()
}

func newClient(rwc io.ReadWriteCloser, cfg Config, log *slog.Logger) *Client {
	c := &Client{
		cfg:  cfg,
		log:  log,
		conn: jsonrpc2.NewConn(rwc),
		docs: make(map[protocol.DocumentURI]*document),
	}
	c.registerHandlers()
	c.conn.Start()
	return c
}

func (c *Client) registerHandlers() {
	null := func(context.Context, *jsonrpc2.Request) (any, error) { return nil, nil }
	c.conn.Handle(protocol.MethodWorkDoneProgressCreate, null)
	c.conn.Handle(protocol.MethodRegisterCapability, null)
	c.conn.Handle(protocol.MethodUnregisterCapability, null)
	c.conn.Handle(protocol.MethodShowMessageRequest, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var p protocol.LogMessageParams
		if err := req.UnmarshalParams(&p); err == nil {
			c.log.Info("lsp: server message", "type", p.Type, "message", p.Message)
		}
		return nil, nil
	})
	c.conn.Handle(protocol.MethodWorkspaceConfiguration, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var p protocol.ConfigurationParams
		if err := req.UnmarshalParams(&p); err != nil {
			return nil, err
		}
		out := make([]any, len(p.Items))
		for i, it := range p.Items {
			out[i] = lookupSetting(c.cfg.Settings, it.Section)
		}
		return out, nil
	})
	c.conn.Handle(protocol.MethodPublishDiagnostics, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var p protocol.PublishDiagnosticsParams
		if err := req.UnmarshalParams(&p); err != nil {
			c.log.Warn("lsp: bad publishDiagnostics", "err", err)
			return nil, nil
		}
		if c.cfg.OnDiagnostics != nil {
			c.cfg.OnDiagnostics(p)
		}
		return nil, nil
	})
	c.conn.Handle(protocol.MethodLogMessage, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var p protocol.LogMessageParams
		if err := req.UnmarshalParams(&p); err == nil {
			c.log.Log(context.Background(), slogLevel(p.Type), "lsp: "+p.Message)
		}
		return nil, nil
	})
	c.conn.Handle(protocol.MethodShowMessage, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var p protocol.LogMessageParams
		if err := req.UnmarshalParams(&p); err == nil {
			c.log.Log(context.Background(), slogLevel(p.Type), "lsp: "+p.Message)
			if c.cfg.OnShowMessage != nil {
				c.cfg.OnShowMessage(p)
			}
		}
		return nil, nil
	})
}

func slogLevel(t protocol.MessageType) slog.Level {
	switch t {
	case protocol.MessageError:
		return slog.LevelError
	case protocol.MessageWarning:
		return slog.LevelWarn
	case protocol.MessageInfo:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

// lookupSetting resolves section in settings: exact flat key first, then a
// walk through nested maps. An empty section returns all settings.
func lookupSetting(settings map[string]any, section string) any {
	if section == "" {
		if settings == nil {
			return nil
		}
		return settings
	}
	if v, ok := settings[section]; ok {
		return v
	}
	var cur any = settings
	for _, part := range strings.Split(section, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[part]; !ok {
			return nil
		}
	}
	return cur
}

func (c *Client) initialize(ctx context.Context) error {
	pid := os.Getpid()
	params := protocol.InitializeParams{
		ProcessID:    &pid,
		Capabilities: clientCapabilities(),
	}
	name := c.cfg.ClientName
	if name == "" {
		name = "Tiegel"
	}
	params.ClientInfo = &protocol.ClientInfo{Name: name, Version: c.cfg.ClientVersion}
	if c.cfg.RootDir != "" {
		uri := protocol.URIFromPath(c.cfg.RootDir)
		params.RootURI = &uri
		params.RootPath = c.cfg.RootDir
		params.WorkspaceFolders = []protocol.WorkspaceFolder{{URI: uri, Name: filepath.Base(c.cfg.RootDir)}}
	}
	if len(c.cfg.InitializationOptions) > 0 {
		params.InitializationOptions = c.cfg.InitializationOptions
	}
	var res protocol.InitializeResult
	if err := c.call(ctx, protocol.MethodInitialize, params, &res); err != nil {
		return fmt.Errorf("lsp: initialize: %w", err)
	}
	if enc := res.Capabilities.PositionEncoding; enc != "" && enc != "utf-16" {
		c.log.Warn("lsp: server chose unsupported position encoding", "encoding", enc)
	}
	c.caps = res.Capabilities
	c.info = res.ServerInfo
	if err := c.notify(ctx, protocol.MethodInitialized, struct{}{}); err != nil {
		return fmt.Errorf("lsp: initialized: %w", err)
	}
	return nil
}

// standardTokenTypes are the predefined LSP semantic token types.
var standardTokenTypes = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function",
	"method", "macro", "keyword", "modifier", "comment", "string", "number",
	"regexp", "operator", "decorator",
}

var standardTokenModifiers = []string{
	"declaration", "definition", "readonly", "static", "deprecated", "abstract",
	"async", "modification", "documentation", "defaultLibrary",
}

func clientCapabilities() protocol.ClientCapabilities {
	md := []string{protocol.Markdown, protocol.PlainText}
	return protocol.ClientCapabilities{
		General: &protocol.GeneralClientCapabilities{PositionEncodings: []string{"utf-16"}},
		Workspace: &protocol.WorkspaceClientCapabilities{Configuration: true, WorkspaceFolders: true,
			FileOperations: &protocol.FileOperationClientCapabilities{DidRename: true}},
		Window: &protocol.WindowClientCapabilities{WorkDoneProgress: true},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			Synchronization: &protocol.SyncClientCapabilities{DidSave: true},
			Completion: &protocol.CompletionClientCapabilities{
				ContextSupport: true,
				CompletionItem: &protocol.CompletionItemClientCapabilities{
					SnippetSupport:      true,
					DocumentationFormat: md,
					DeprecatedSupport:   true,
					PreselectSupport:    true,
				},
			},
			Hover: &protocol.HoverClientCapabilities{ContentFormat: md},
			SignatureHelp: &protocol.SignatureHelpClientCapabilities{
				SignatureInformation: &protocol.SignatureInformationClientCapabilities{
					DocumentationFormat:    md,
					ActiveParameterSupport: true,
				},
			},
			Definition:         &protocol.LinkClientCapabilities{LinkSupport: true},
			Implementation:     &protocol.LinkClientCapabilities{LinkSupport: true},
			PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{VersionSupport: true},
			SemanticTokens: &protocol.SemanticTokensClientCapabilities{
				Requests:       protocol.SemanticTokensRequests{Full: true},
				TokenTypes:     standardTokenTypes,
				TokenModifiers: standardTokenModifiers,
				Formats:        []string{"relative"},
			},
		},
	}
}

// Capabilities returns the capabilities announced by the server.
func (c *Client) Capabilities() protocol.ServerCapabilities { return c.caps }

// ServerInfo returns the server's self-description, or nil.
func (c *Client) ServerInfo() *protocol.ServerInfo { return c.info }

// Done is closed when the connection to the server is gone.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Err returns why the connection ended, or nil while it is alive.
func (c *Client) Err() error {
	err := c.conn.Err()
	if err == nil {
		return nil
	}
	if !errors.Is(err, jsonrpc2.ErrClosed) {
		err = fmt.Errorf("%w: %w", jsonrpc2.ErrClosed, err)
	}
	return c.wrapExit(err)
}

// Call sends an arbitrary request; use it for methods without a typed
// wrapper.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.call(ctx, method, params, result)
}

// Notify sends an arbitrary notification.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	return c.notify(ctx, method, params)
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	return c.wrapExit(c.conn.Call(ctx, method, params, result))
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.wrapExit(c.conn.Notify(ctx, method, params))
}

// wrapExit adds the process exit status to connection errors.
func (c *Client) wrapExit(err error) error {
	if err == nil || c.proc == nil || !errors.Is(err, jsonrpc2.ErrClosed) {
		return err
	}
	if exited, exitErr := c.proc.exitStatus(); exited {
		if exitErr == nil {
			return fmt.Errorf("lsp: server exited: %w", err)
		}
		return fmt.Errorf("lsp: server exited (%v): %w", exitErr, err)
	}
	return err
}

// Shutdown performs the shutdown/exit sequence, closes the connection and
// waits for the server process to exit. If ctx expires first the process is
// killed.
func (c *Client) Shutdown(ctx context.Context) error {
	err := c.call(ctx, protocol.MethodShutdown, nil, nil)
	if err == nil {
		_ = c.notify(ctx, protocol.MethodExit, nil)
	}
	if c.proc != nil {
		if werr := c.proc.wait(ctx); werr != nil && err == nil {
			err = werr
		}
	}
	_ = c.conn.Close()
	if c.proc != nil {
		c.proc.close()
	}
	if err != nil {
		return fmt.Errorf("lsp: shutdown: %w", err)
	}
	return nil
}

// Close tears down the connection immediately and kills the server process
// without the shutdown handshake.
func (c *Client) Close() {
	_ = c.conn.Close()
	if c.proc != nil {
		c.proc.kill()
		c.proc.close()
	}
}

// --- document synchronization ----------------------------------------------

func (c *Client) sync() protocol.TextDocumentSyncOptions {
	if c.caps.TextDocumentSync == nil {
		return protocol.TextDocumentSyncOptions{}
	}
	return *c.caps.TextDocumentSync
}

// DidOpen registers an opened document and notifies the server.
func (c *Client) DidOpen(ctx context.Context, uri protocol.DocumentURI, languageID string, version int, text string) error {
	c.mu.Lock()
	c.docs[uri] = &document{languageID: languageID, version: version, text: text}
	c.mu.Unlock()
	if !c.sync().OpenClose {
		return nil
	}
	return c.notify(ctx, protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: uri, LanguageID: languageID, Version: version, Text: text},
	})
}

// DidChange applies changes to the tracked document and forwards them to
// the server: incrementally if the server supports it, otherwise as full
// text. Changes are in LSP coordinates relative to the previous version.
func (c *Client) DidChange(ctx context.Context, uri protocol.DocumentURI, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	doc, ok := c.docs[uri]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("lsp: didChange %s: %w", uri, ErrNotOpen)
	}
	text, err := protocol.ApplyChanges(doc.text, changes)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("lsp: didChange %s: %w", uri, err)
	}
	doc.text = text
	doc.version = version
	c.mu.Unlock()

	var send []protocol.TextDocumentContentChangeEvent
	switch c.sync().Change {
	case protocol.SyncIncremental:
		send = changes
	case protocol.SyncFull:
		send = []protocol.TextDocumentContentChangeEvent{{Text: text}}
	default:
		return nil
	}
	return c.notify(ctx, protocol.MethodDidChange, protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{URI: uri, Version: version},
		ContentChanges: send,
	})
}

// DidRenameFiles tells the server about renamed files or folders
// (workspace/didRenameFiles) if it registered for them; sent reports
// whether the notification went out. The open documents themselves are
// re-synchronised separately (didClose/didOpen).
func (c *Client) DidRenameFiles(ctx context.Context, files []protocol.FileRename) (sent bool, err error) {
	ws := c.caps.Workspace
	if len(files) == 0 || ws == nil || ws.FileOperations == nil || ws.FileOperations.DidRename == nil {
		return false, nil
	}
	if err := c.notify(ctx, protocol.MethodDidRenameFiles, protocol.RenameFilesParams{Files: files}); err != nil {
		return false, err
	}
	return true, nil
}

// DidSave notifies the server that the document was saved, including its
// text if the server asked for it.
func (c *Client) DidSave(ctx context.Context, uri protocol.DocumentURI) error {
	c.mu.Lock()
	doc, ok := c.docs[uri]
	var text string
	if ok {
		text = doc.text
	}
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("lsp: didSave %s: %w", uri, ErrNotOpen)
	}
	save := c.sync().Save
	if save == nil {
		return nil
	}
	p := protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}
	if save.IncludeText {
		p.Text = &text
	}
	return c.notify(ctx, protocol.MethodDidSave, p)
}

// DidClose forgets the document and notifies the server.
func (c *Client) DidClose(ctx context.Context, uri protocol.DocumentURI) error {
	c.mu.Lock()
	_, ok := c.docs[uri]
	delete(c.docs, uri)
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("lsp: didClose %s: %w", uri, ErrNotOpen)
	}
	if !c.sync().OpenClose {
		return nil
	}
	return c.notify(ctx, protocol.MethodDidClose, protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
}

// Document returns the client's copy of an open document.
func (c *Client) Document(uri protocol.DocumentURI) (text string, version int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, ok := c.docs[uri]
	if !ok {
		return "", 0, false
	}
	return doc.text, doc.version, true
}

// --- language features -----------------------------------------------------

func positionParams(uri protocol.DocumentURI, pos protocol.Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: pos}
}

// Completion requests completion items at pos. cc may be nil. A null result
// yields an empty list.
func (c *Client) Completion(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position, cc *protocol.CompletionContext) (*protocol.CompletionList, error) {
	if c.caps.CompletionProvider == nil {
		return nil, ErrNotSupported
	}
	var list protocol.CompletionList
	if err := c.call(ctx, protocol.MethodCompletion, protocol.CompletionParams{TextDocumentPositionParams: positionParams(uri, pos), Context: cc}, &list); err != nil {
		return nil, fmt.Errorf("lsp: completion: %w", err)
	}
	return &list, nil
}

// Hover requests hover information. It returns nil when there is none.
func (c *Client) Hover(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) (*protocol.Hover, error) {
	if !c.caps.HoverProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	var h *protocol.Hover
	if err := c.call(ctx, protocol.MethodHover, positionParams(uri, pos), &h); err != nil {
		return nil, fmt.Errorf("lsp: hover: %w", err)
	}
	return h, nil
}

// SignatureHelp requests signature help. It returns nil when there is none.
func (c *Client) SignatureHelp(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) (*protocol.SignatureHelp, error) {
	if c.caps.SignatureHelpProvider == nil {
		return nil, ErrNotSupported
	}
	var h *protocol.SignatureHelp
	if err := c.call(ctx, protocol.MethodSignatureHelp, positionParams(uri, pos), &h); err != nil {
		return nil, fmt.Errorf("lsp: signatureHelp: %w", err)
	}
	return h, nil
}

// Definition returns the definition locations of the symbol at pos.
func (c *Client) Definition(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) ([]protocol.Location, error) {
	if !c.caps.DefinitionProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	return c.locations(ctx, protocol.MethodDefinition, uri, pos)
}

// Implementation returns the implementations of the symbol at pos.
func (c *Client) Implementation(ctx context.Context, uri protocol.DocumentURI, pos protocol.Position) ([]protocol.Location, error) {
	if !c.caps.ImplementationProvider.IsEnabled() {
		return nil, ErrNotSupported
	}
	return c.locations(ctx, protocol.MethodImplementation, uri, pos)
}

func (c *Client) locations(ctx context.Context, method string, uri protocol.DocumentURI, pos protocol.Position) ([]protocol.Location, error) {
	var raw json.RawMessage
	if err := c.call(ctx, method, positionParams(uri, pos), &raw); err != nil {
		return nil, fmt.Errorf("lsp: %s: %w", method, err)
	}
	locs, err := protocol.ParseLocations(raw)
	if err != nil {
		return nil, fmt.Errorf("lsp: %s: %w", method, err)
	}
	return locs, nil
}

// SemanticTokensFull requests all semantic tokens of a document and decodes
// them with the server's legend.
func (c *Client) SemanticTokensFull(ctx context.Context, uri protocol.DocumentURI) ([]protocol.SemanticToken, error) {
	st := c.caps.SemanticTokensProvider
	if st == nil || !st.Full.IsEnabled() {
		return nil, ErrNotSupported
	}
	var res *protocol.SemanticTokens
	if err := c.call(ctx, protocol.MethodSemanticTokensFull, protocol.SemanticTokensParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}, &res); err != nil {
		return nil, fmt.Errorf("lsp: semanticTokens: %w", err)
	}
	if res == nil {
		return nil, nil
	}
	toks, err := protocol.DecodeSemanticTokens(res.Data, st.Legend)
	if err != nil {
		return nil, fmt.Errorf("lsp: semanticTokens: %w", err)
	}
	return toks, nil
}
