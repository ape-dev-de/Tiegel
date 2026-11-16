// Package fakeserver is a minimal, deterministic language server used by
// the lsp tests. It runs in-process over any io.ReadWriteCloser.
//
// Behaviour:
//   - initialize records params and announces the configured capabilities.
//   - initialized triggers window/workDoneProgress/create,
//     client/registerCapability and workspace/configuration (section "php")
//     requests to the client.
//   - didOpen/didChange keep a copy of each document, then publish one
//     diagnostic per occurrence of "ERR".
//   - completion at line 666 calls Options.Crash.
//   - hover at line 999 blocks until the request is cancelled.
//   - "fake/state" returns the server state (for out-of-process tests).
//   - references, highlights, rename, code actions, formatting and symbols
//     work on words and lines of the synced documents (see features.go).
package fakeserver

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// Lines with special behaviour.
const (
	CrashLine = 666
	BlockLine = 999
)

// Legend is the semantic token legend announced by the server.
var Legend = protocol.SemanticTokensLegend{
	TokenTypes:     []string{"class", "variable", "function"},
	TokenModifiers: []string{"declaration", "static"},
}

// Options configure the fake server.
type Options struct {
	// Sync is the announced text document sync kind (default incremental).
	Sync protocol.TextDocumentSyncKind
	// Crash is called when completion is requested at CrashLine.
	Crash func()
	// Exit is called on the exit notification.
	Exit func()
}

// State is a snapshot of what the server has seen.
type State struct {
	InitOptions   map[string]any                  `json:"initOptions"`
	RootURI       string                          `json:"rootUri"`
	Docs          map[protocol.DocumentURI]string `json:"docs"`
	Versions      map[protocol.DocumentURI]int    `json:"versions"`
	Changes       []int                           `json:"changes"` // number of change events per didChange
	FullSyncSeen  bool                            `json:"fullSyncSeen"`
	Config        []any                           `json:"config"`
	Cancelled     int                             `json:"cancelled"`
	Saved         []string                        `json:"saved"`
	Closed        []protocol.DocumentURI          `json:"closed"`
	Renamed       []protocol.FileRename           `json:"renamed"`
	ShutdownCalls int                             `json:"shutdownCalls"`
	// Executed lists executed commands; Applied the answers of the
	// client to workspace/applyEdit.
	Executed []string `json:"executed"`
	Applied  []bool   `json:"applied"`
}

// Server is a running fake server.
type Server struct {
	conn *jsonrpc2.Conn
	opts Options

	mu    sync.Mutex
	state State
	cfgCh chan struct{}
}

// Serve starts serving on rwc.
func Serve(rwc io.ReadWriteCloser, opts Options) *Server {
	if opts.Sync == 0 {
		opts.Sync = protocol.SyncIncremental
	}
	s := &Server{
		conn: jsonrpc2.NewConn(rwc),
		opts: opts,
		state: State{
			Docs:     map[protocol.DocumentURI]string{},
			Versions: map[protocol.DocumentURI]int{},
		},
		cfgCh: make(chan struct{}),
	}
	s.register()
	s.conn.Start()
	return s
}

// Done is closed when the connection ends.
func (s *Server) Done() <-chan struct{} { return s.conn.Done() }

// Close closes the connection.
func (s *Server) Close() error { return s.conn.Close() }

// ConfigReceived is closed once the workspace/configuration answer arrived.
func (s *Server) ConfigReceived() <-chan struct{} { return s.cfgCh }

// Snapshot returns a copy of the state.
func (s *Server) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	st.Docs = make(map[protocol.DocumentURI]string, len(s.state.Docs))
	for k, v := range s.state.Docs {
		st.Docs[k] = v
	}
	st.Versions = make(map[protocol.DocumentURI]int, len(s.state.Versions))
	for k, v := range s.state.Versions {
		st.Versions[k] = v
	}
	st.Changes = append([]int(nil), s.state.Changes...)
	st.Saved = append([]string(nil), s.state.Saved...)
	st.Closed = append([]protocol.DocumentURI(nil), s.state.Closed...)
	st.Executed = append([]string(nil), s.state.Executed...)
	st.Applied = append([]bool(nil), s.state.Applied...)
	st.Renamed = append([]protocol.FileRename(nil), s.state.Renamed...)
	return st
}

type handler = jsonrpc2.Handler

func (s *Server) register() {
	h := map[string]handler{
		protocol.MethodInitialize:         s.initialize,
		protocol.MethodInitialized:        s.initialized,
		protocol.MethodShutdown:           s.shutdown,
		protocol.MethodExit:               s.exit,
		protocol.MethodDidOpen:            s.didOpen,
		protocol.MethodDidChange:          s.didChange,
		protocol.MethodDidSave:            s.didSave,
		protocol.MethodDidClose:           s.didClose,
		protocol.MethodDidRenameFiles:     s.didRenameFiles,
		protocol.MethodCompletion:         s.completion,
		protocol.MethodHover:              s.hover,
		protocol.MethodSignatureHelp:      s.signatureHelp,
		protocol.MethodDefinition:         s.definition,
		protocol.MethodImplementation:     s.implementation,
		protocol.MethodSemanticTokensFull: s.semanticTokens,
		"fake/state": func(context.Context, *jsonrpc2.Request) (any, error) {
			return s.Snapshot(), nil
		},
	}
	for m, f := range s.featureHandlers() {
		h[m] = f
	}
	for m, f := range h {
		s.conn.Handle(m, f)
	}
}

func (s *Server) initialize(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p struct {
		RootURI     string         `json:"rootUri"`
		InitOptions map[string]any `json:"initializationOptions"`
	}
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.InitOptions = p.InitOptions
	s.state.RootURI = p.RootURI
	s.mu.Unlock()
	// Capabilities are written as raw JSON to exercise the decoders.
	caps := map[string]any{
		"positionEncoding": "utf-16",
		"textDocumentSync": map[string]any{
			"openClose": true,
			"change":    int(s.opts.Sync),
			"save":      map[string]any{"includeText": true},
		},
		"completionProvider":     map[string]any{"triggerCharacters": []string{"$", ">", ":"}},
		"hoverProvider":          true,
		"signatureHelpProvider":  map[string]any{"triggerCharacters": []string{"(", ","}},
		"definitionProvider":     map[string]any{"workDoneProgress": true},
		"implementationProvider": true,
		"semanticTokensProvider": map[string]any{"legend": Legend, "full": true},
		"workspace": map[string]any{"fileOperations": map[string]any{
			"didRename": map[string]any{"filters": []any{map[string]any{"pattern": map[string]any{"glob": "**/*.php"}}}},
		}},
	}
	for k, v := range featureCapabilities() {
		caps[k] = v
	}
	return map[string]any{
		"capabilities": caps,
		"serverInfo":   map[string]any{"name": "fake", "version": "1.0"},
	}, nil
}

func (s *Server) initialized(context.Context, *jsonrpc2.Request) (any, error) {
	go func() {
		ctx := context.Background()
		_ = s.conn.Call(ctx, protocol.MethodWorkDoneProgressCreate, map[string]any{"token": 1}, nil)
		_ = s.conn.Call(ctx, protocol.MethodRegisterCapability, map[string]any{"registrations": []any{}}, nil)
		_ = s.conn.Notify(ctx, protocol.MethodLogMessage, protocol.LogMessageParams{Type: protocol.MessageInfo, Message: "fake ready"})
		_ = s.conn.Notify(ctx, protocol.MethodShowMessage, protocol.LogMessageParams{Type: protocol.MessageWarning, Message: "hello"})
		var cfg []any
		err := s.conn.Call(ctx, protocol.MethodWorkspaceConfiguration, protocol.ConfigurationParams{
			Items: []protocol.ConfigurationItem{{Section: "php"}, {Section: "php.version"}, {Section: "missing"}},
		}, &cfg)
		if err == nil {
			s.mu.Lock()
			s.state.Config = cfg
			s.mu.Unlock()
		}
		close(s.cfgCh)
	}()
	return nil, nil
}

func (s *Server) shutdown(context.Context, *jsonrpc2.Request) (any, error) {
	s.mu.Lock()
	s.state.ShutdownCalls++
	s.mu.Unlock()
	return nil, nil
}

func (s *Server) exit(context.Context, *jsonrpc2.Request) (any, error) {
	if s.opts.Exit != nil {
		s.opts.Exit()
	} else {
		_ = s.conn.Close()
	}
	return nil, nil
}

func (s *Server) didOpen(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DidOpenTextDocumentParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.Docs[p.TextDocument.URI] = p.TextDocument.Text
	s.state.Versions[p.TextDocument.URI] = p.TextDocument.Version
	s.mu.Unlock()
	s.publish(p.TextDocument.URI, p.TextDocument.Version, p.TextDocument.Text)
	return nil, nil
}

func (s *Server) didChange(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DidChangeTextDocumentParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	uri := p.TextDocument.URI
	s.mu.Lock()
	text, err := protocol.ApplyChanges(s.state.Docs[uri], p.ContentChanges)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	for _, ch := range p.ContentChanges {
		if ch.Range == nil {
			s.state.FullSyncSeen = true
		}
	}
	s.state.Docs[uri] = text
	s.state.Versions[uri] = p.TextDocument.Version
	s.state.Changes = append(s.state.Changes, len(p.ContentChanges))
	s.mu.Unlock()
	s.publish(uri, p.TextDocument.Version, text)
	return nil, nil
}

func (s *Server) didSave(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DidSaveTextDocumentParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if p.Text != nil {
		s.state.Saved = append(s.state.Saved, *p.Text)
	}
	s.mu.Unlock()
	return nil, nil
}

func (s *Server) didRenameFiles(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.RenameFilesParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.state.Renamed = append(s.state.Renamed, p.Files...)
	s.mu.Unlock()
	return nil, nil
}

func (s *Server) didClose(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.DidCloseTextDocumentParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.state.Docs, p.TextDocument.URI)
	s.state.Closed = append(s.state.Closed, p.TextDocument.URI)
	s.mu.Unlock()
	return nil, nil
}

func (s *Server) publish(uri protocol.DocumentURI, version int, text string) {
	diags := []protocol.Diagnostic{}
	for off := 0; ; {
		i := strings.Index(text[off:], "ERR")
		if i < 0 {
			break
		}
		start := off + i
		diags = append(diags, protocol.Diagnostic{
			Range:    protocol.Range{Start: protocol.PositionOf(text, start), End: protocol.PositionOf(text, start+3)},
			Severity: protocol.SeverityError,
			Code:     &protocol.IntOrString{Value: "E1"},
			Source:   "fake",
			Message:  "error marker",
		})
		off = start + 3
	}
	v := version
	_ = s.conn.Notify(context.Background(), protocol.MethodPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI: uri, Version: &v, Diagnostics: diags,
	})
}

func (s *Server) completion(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.CompletionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	if p.Position.Line == CrashLine && s.opts.Crash != nil {
		s.opts.Crash()
		<-ctx.Done() // never answer
		return nil, ctx.Err()
	}
	pos := p.Position
	start := protocol.Position{Line: pos.Line, Character: max(0, pos.Character-1)}
	return map[string]any{
		"isIncomplete": true,
		"items": []any{
			map[string]any{"label": "strlen", "kind": protocol.KindFunction, "detail": "strlen(string $s): int",
				"insertTextFormat": protocol.InsertTextSnippet,
				"textEdit": map[string]any{
					"range":   protocol.Range{Start: start, End: pos},
					"newText": "strlen(${1:\\$string})$0",
				},
				"documentation": "Returns the length"},
			map[string]any{"label": "str_contains", "kind": protocol.KindFunction,
				"textEdit": map[string]any{
					"insert":  protocol.Range{Start: start, End: pos},
					"replace": protocol.Range{Start: start, End: pos},
					"newText": "str_contains",
				},
				"documentation": map[string]any{"kind": "markdown", "value": "**since** 8.0"}},
		},
	}, nil
}

func (s *Server) hover(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.TextDocumentPositionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	switch p.Position.Line {
	case BlockLine:
		<-ctx.Done()
		s.mu.Lock()
		s.state.Cancelled++
		s.mu.Unlock()
		return nil, ctx.Err()
	case 1:
		return nil, nil
	case 2:
		// Legacy MarkedString form.
		return map[string]any{"contents": []any{map[string]any{"language": "php", "value": "<?php"}, "text"}}, nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": "hover"},
		"range":    protocol.Range{Start: p.Position, End: p.Position},
	}, nil
}

func (s *Server) signatureHelp(context.Context, *jsonrpc2.Request) (any, error) {
	return map[string]any{
		"signatures": []any{map[string]any{
			"label":         "foo(int $a, string $b)",
			"documentation": "Foo.",
			"parameters": []any{
				map[string]any{"label": []int{4, 11}},
				map[string]any{"label": "string $b", "documentation": map[string]any{"kind": "plaintext", "value": "b"}},
			},
		}},
		"activeSignature": 0,
		"activeParameter": 1,
	}, nil
}

func (s *Server) definition(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.TextDocumentPositionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	r := protocol.Range{Start: protocol.Position{Line: 3, Character: 6}, End: protocol.Position{Line: 3, Character: 9}}
	return []any{map[string]any{
		"targetUri":            p.TextDocument.URI,
		"targetRange":          protocol.Range{End: protocol.Position{Line: 10}},
		"targetSelectionRange": r,
	}}, nil
}

func (s *Server) implementation(_ context.Context, req *jsonrpc2.Request) (any, error) {
	var p protocol.TextDocumentPositionParams
	if err := req.UnmarshalParams(&p); err != nil {
		return nil, err
	}
	return protocol.Location{URI: p.TextDocument.URI, Range: protocol.Range{Start: p.Position, End: p.Position}}, nil
}

func (s *Server) semanticTokens(context.Context, *jsonrpc2.Request) (any, error) {
	// class "Foo" at 1:6 (declaration), variable at 2:4 len 2, function at 2:10 len 3 (static)
	return protocol.SemanticTokens{ResultID: "1", Data: []uint32{
		1, 6, 3, 0, 1,
		1, 4, 2, 1, 0,
		0, 6, 3, 2, 2,
	}}, nil
}

// Stdio is an io.ReadWriteCloser over the process's stdin and stdout.
type Stdio struct{}

func (Stdio) Read(b []byte) (int, error)  { return os.Stdin.Read(b) }
func (Stdio) Write(b []byte) (int, error) { return os.Stdout.Write(b) }

// Close closes stdin and stdout.
func (Stdio) Close() error {
	_ = os.Stdin.Close()
	return os.Stdout.Close()
}

// Main runs the fake server on stdin/stdout until the client exits; the
// fakelsp command and test binaries that double as server use it.
func Main() {
	s := Serve(Stdio{}, Options{Exit: func() { os.Exit(0) }, Crash: func() { os.Exit(3) }})
	<-s.Done()
	os.Exit(0)
}
