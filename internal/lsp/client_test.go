package lsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// The test binary doubles as fake language server when TIEGEL_FAKE_LSP is
// set, so the process code path is tested against a real child process.
func TestMain(m *testing.M) {
	switch os.Getenv("TIEGEL_FAKE_LSP") {
	case "":
		os.Exit(m.Run())
	case "fail":
		fmt.Fprintln(os.Stderr, "fake server refuses to start")
		os.Exit(7)
	default:
		runFakeServer()
	}
}

type stdio struct{}

func (stdio) Read(b []byte) (int, error)  { return os.Stdin.Read(b) }
func (stdio) Write(b []byte) (int, error) { return os.Stdout.Write(b) }
func (stdio) Close() error {
	_ = os.Stdin.Close()
	return os.Stdout.Close()
}

func runFakeServer() {
	fmt.Fprintln(os.Stderr, "fake server starting")
	opts := fakeserver.Options{
		Crash: func() { os.Exit(3) },
		Exit:  func() { os.Exit(0) },
	}
	if os.Getenv("FAKE_SYNC") == "full" {
		opts.Sync = protocol.SyncFull
	}
	if os.Getenv("FAKE_NOEXIT") == "1" {
		opts.Exit = func() {}
	}
	s := fakeserver.Serve(stdio{}, opts)
	<-s.Done()
	if os.Getenv("FAKE_NOEXIT") == "1" {
		time.Sleep(time.Hour) // linger until killed
	}
	os.Exit(0)
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func timeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// diagSink collects diagnostics.
type diagSink struct {
	mu   sync.Mutex
	got  []protocol.PublishDiagnosticsParams
	note chan struct{}
}

func newDiagSink() *diagSink { return &diagSink{note: make(chan struct{}, 100)} }

func (d *diagSink) add(p protocol.PublishDiagnosticsParams) {
	d.mu.Lock()
	d.got = append(d.got, p)
	d.mu.Unlock()
	d.note <- struct{}{}
}

func (d *diagSink) waitVersion(t *testing.T, v int) protocol.PublishDiagnosticsParams {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		d.mu.Lock()
		for _, p := range d.got {
			if p.Version != nil && *p.Version == v {
				d.mu.Unlock()
				return p
			}
		}
		d.mu.Unlock()
		select {
		case <-d.note:
		case <-deadline:
			t.Fatalf("no diagnostics for version %d", v)
		}
	}
}

func connectFake(t *testing.T, opts fakeserver.Options, cfg Config) (*Client, *fakeserver.Server) {
	t.Helper()
	a, b := net.Pipe()
	srv := fakeserver.Serve(b, opts)
	if cfg.Logger == nil {
		cfg.Logger, _ = testLogger()
	}
	c, err := Connect(timeout(t, 5*time.Second), a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		_ = srv.Close()
	})
	return c, srv
}

const uri = protocol.DocumentURI("file:///project/src/Grüße.php")

func TestInitializeAndConfiguration(t *testing.T) {
	log, logs := testLogger()
	shown := make(chan protocol.LogMessageParams, 1)
	c, srv := connectFake(t, fakeserver.Options{}, Config{
		RootDir:               "/project",
		InitializationOptions: map[string]any{"php.version": "8.3"},
		Settings:              map[string]any{"php": map[string]any{"version": "8.3"}},
		Logger:                log,
		OnShowMessage:         func(p protocol.LogMessageParams) { shown <- p },
	})
	if info := c.ServerInfo(); info == nil || info.Name != "fake" {
		t.Fatalf("server info %+v", info)
	}
	caps := c.Capabilities()
	if caps.TextDocumentSync.Change != protocol.SyncIncremental || !caps.TextDocumentSync.Save.IncludeText {
		t.Fatalf("sync caps %+v", caps.TextDocumentSync)
	}
	select {
	case <-srv.ConfigReceived():
	case <-time.After(5 * time.Second):
		t.Fatal("no configuration exchange")
	}
	st := srv.Snapshot()
	if st.InitOptions["php.version"] != "8.3" {
		t.Errorf("init options %v", st.InitOptions)
	}
	if st.RootURI != "file:///project" {
		t.Errorf("root uri %q", st.RootURI)
	}
	want := []any{map[string]any{"version": "8.3"}, "8.3", nil}
	if !reflect.DeepEqual(st.Config, want) {
		t.Errorf("config %#v", st.Config)
	}
	select {
	case p := <-shown:
		if p.Message != "hello" {
			t.Errorf("show message %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no showMessage")
	}
	eventually(t, "log message", func() bool { return strings.Contains(logs.String(), "fake ready") })
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
}

func TestLookupSetting(t *testing.T) {
	s := map[string]any{"a.b": 1, "x": map[string]any{"y": map[string]any{"z": 2}}, "leaf": 3}
	cases := map[string]any{"a.b": 1, "x.y.z": 2, "x.y.q": nil, "leaf.sub": nil, "nope": nil}
	for sec, want := range cases {
		if got := lookupSetting(s, sec); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", sec, got)
		}
	}
	if got := lookupSetting(s, ""); !reflect.DeepEqual(got, s) {
		t.Error("empty section")
	}
	if got := lookupSetting(nil, ""); got != nil {
		t.Error("nil settings")
	}
}

func rng(l1, c1, l2, c2 int) *protocol.Range {
	return &protocol.Range{Start: protocol.Position{Line: l1, Character: c1}, End: protocol.Position{Line: l2, Character: c2}}
}

func testDocumentSync(t *testing.T, sync protocol.TextDocumentSyncKind) {
	diags := newDiagSink()
	c, srv := connectFake(t, fakeserver.Options{Sync: sync}, Config{OnDiagnostics: diags.add})
	ctx := timeout(t, 5*time.Second)

	text := "<?php\n$grüße = '😀 ERR';\n"
	if err := c.DidOpen(ctx, uri, "php", 1, text); err != nil {
		t.Fatal(err)
	}
	d := diags.waitVersion(t, 1)
	// "$grüße = '😀 " is 13 UTF-16 units (emoji counts 2).
	if len(d.Diagnostics) != 1 || d.Diagnostics[0].Range.Start.Line != 1 {
		t.Fatalf("diagnostics v1 %+v", d)
	}
	if got := d.Diagnostics[0].Range; got != *rng(1, 13, 1, 16) {
		t.Fatalf("diagnostic range %+v", got)
	}

	steps := []struct {
		changes []protocol.TextDocumentContentChangeEvent
		want    string
	}{
		{ // replace emoji (2 units) with umlaut word
			[]protocol.TextDocumentContentChangeEvent{{Range: rng(1, 10, 1, 12), Text: "Käse"}},
			"<?php\n$grüße = 'Käse ERR';\n",
		},
		{ // two edits in one notification, second relative to first
			[]protocol.TextDocumentContentChangeEvent{
				{Range: rng(2, 0, 2, 0), Text: "echo $grüße; // ERR 🎉\n"},
				{Range: rng(0, 5, 0, 5), Text: " "},
			},
			"<?php \n$grüße = 'Käse ERR';\necho $grüße; // ERR 🎉\n",
		},
		{ // delete across lines
			[]protocol.TextDocumentContentChangeEvent{{Range: rng(1, 15, 2, 0), Text: ""}},
			"<?php \n$grüße = 'Käse echo $grüße; // ERR 🎉\n",
		},
	}
	for i, st := range steps {
		v := i + 2
		if err := c.DidChange(ctx, uri, v, st.changes); err != nil {
			t.Fatal(err)
		}
		diags.waitVersion(t, v)
		got, ver, ok := c.Document(uri)
		if !ok || got != st.want || ver != v {
			t.Fatalf("step %d client doc %q v%d", i, got, ver)
		}
		if srv := srv.Snapshot().Docs[uri]; srv != st.want {
			t.Fatalf("step %d server doc %q, want %q", i, srv, st.want)
		}
	}
	last := diags.waitVersion(t, 4)
	if len(last.Diagnostics) != 1 || last.Diagnostics[0].Range != *rng(1, 31, 1, 34) {
		t.Fatalf("last diagnostics %+v", last.Diagnostics)
	}

	st := srv.Snapshot()
	if sync == protocol.SyncFull {
		if !st.FullSyncSeen || !reflect.DeepEqual(st.Changes, []int{1, 1, 1}) {
			t.Fatalf("full sync: %+v", st.Changes)
		}
	} else if st.FullSyncSeen || !reflect.DeepEqual(st.Changes, []int{1, 2, 1}) {
		t.Fatalf("incremental sync: %+v", st.Changes)
	}

	if err := c.DidSave(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := c.DidClose(ctx, uri); err != nil {
		t.Fatal(err)
	}
	eventually(t, "close", func() bool { return len(srv.Snapshot().Closed) == 1 })
	if saved := srv.Snapshot().Saved; len(saved) != 1 || saved[0] != steps[2].want {
		t.Fatalf("saved %q", saved)
	}
	if _, _, ok := c.Document(uri); ok {
		t.Fatal("document still tracked")
	}
}

func TestDocumentSyncIncremental(t *testing.T) { testDocumentSync(t, protocol.SyncIncremental) }
func TestDocumentSyncFull(t *testing.T)        { testDocumentSync(t, protocol.SyncFull) }

func TestDocumentNotOpen(t *testing.T) {
	c, _ := connectFake(t, fakeserver.Options{}, Config{})
	ctx := timeout(t, 5*time.Second)
	if err := c.DidChange(ctx, uri, 2, nil); !errors.Is(err, ErrNotOpen) {
		t.Errorf("didChange: %v", err)
	}
	if err := c.DidSave(ctx, uri); !errors.Is(err, ErrNotOpen) {
		t.Errorf("didSave: %v", err)
	}
	if err := c.DidClose(ctx, uri); !errors.Is(err, ErrNotOpen) {
		t.Errorf("didClose: %v", err)
	}
	if err := c.DidOpen(ctx, uri, "php", 1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := c.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Range: rng(9, 0, 9, 0)}}); err == nil {
		t.Error("expected invalid range error")
	}
}

func TestLanguageFeatures(t *testing.T) {
	c, _ := connectFake(t, fakeserver.Options{}, Config{})
	ctx := timeout(t, 5*time.Second)
	pos := protocol.Position{Line: 3, Character: 4}

	list, err := c.Completion(ctx, uri, pos, &protocol.CompletionContext{TriggerKind: protocol.TriggerInvoked})
	if err != nil {
		t.Fatal(err)
	}
	if !list.IsIncomplete || len(list.Items) != 2 {
		t.Fatalf("completion %+v", list)
	}
	it := list.Items[0]
	if it.InsertTextFormat != protocol.InsertTextSnippet || it.TextEdit.NewText != "strlen(${1:\\$string})$0" ||
		it.TextEdit.EditRange() != *rng(3, 3, 3, 4) || it.Documentation.Value != "Returns the length" {
		t.Fatalf("item %+v", it)
	}
	if it2 := list.Items[1]; it2.TextEdit.Insert == nil || it2.Documentation.Kind != protocol.Markdown {
		t.Fatalf("item2 %+v", it2)
	}

	h, err := c.Hover(ctx, uri, pos)
	if err != nil || h == nil || h.Contents.Value != "hover" || h.Range == nil {
		t.Fatalf("hover %+v %v", h, err)
	}
	if h, err := c.Hover(ctx, uri, protocol.Position{Line: 1}); err != nil || h != nil {
		t.Fatalf("null hover %+v %v", h, err)
	}
	if h, err := c.Hover(ctx, uri, protocol.Position{Line: 2}); err != nil || h.Contents.Value != "```php\n<?php\n```\n\ntext" {
		t.Fatalf("legacy hover %+v %v", h, err)
	}

	sh, err := c.SignatureHelp(ctx, uri, pos)
	if err != nil || len(sh.Signatures) != 1 || sh.ActiveParameter != 1 {
		t.Fatalf("signature %+v %v", sh, err)
	}
	if p := sh.Signatures[0].Parameters; *p[0].Label.Offsets != [2]int{4, 11} || p[1].Label.Text != "string $b" {
		t.Fatalf("params %+v", p)
	}

	defs, err := c.Definition(ctx, uri, pos)
	if err != nil || len(defs) != 1 || defs[0].URI != uri || defs[0].Range != *rng(3, 6, 3, 9) {
		t.Fatalf("definition %+v %v", defs, err)
	}
	impls, err := c.Implementation(ctx, uri, pos)
	if err != nil || len(impls) != 1 || impls[0].Range.Start != pos {
		t.Fatalf("implementation %+v %v", impls, err)
	}

	toks, err := c.SemanticTokensFull(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.SemanticToken{
		{Line: 1, StartChar: 6, Length: 3, Type: "class", Modifiers: []string{"declaration"}},
		{Line: 2, StartChar: 4, Length: 2, Type: "variable"},
		{Line: 2, StartChar: 10, Length: 3, Type: "function", Modifiers: []string{"static"}},
	}
	if !reflect.DeepEqual(toks, want) {
		t.Fatalf("tokens %+v", toks)
	}

	var st fakeserver.State
	if err := c.Call(ctx, "fake/state", nil, &st); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify(ctx, "fake/ignored", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCancel(t *testing.T) {
	c, srv := connectFake(t, fakeserver.Options{}, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Hover(ctx, uri, protocol.Position{Line: fakeserver.BlockLine})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	eventually(t, "server-side cancellation", func() bool { return srv.Snapshot().Cancelled == 1 })
	// Connection is still healthy.
	if _, err := c.Hover(timeout(t, 5*time.Second), uri, protocol.Position{}); err != nil {
		t.Fatal(err)
	}
}

func TestServerGoneInProcess(t *testing.T) {
	var srv *fakeserver.Server
	c, srv := connectFake(t, fakeserver.Options{Crash: func() { _ = srv.Close() }}, Config{})
	_, err := c.Completion(timeout(t, 5*time.Second), uri, protocol.Position{Line: fakeserver.CrashLine}, nil)
	if !errors.Is(err, jsonrpc2.ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	<-c.Done()
	if c.Err() == nil {
		t.Fatal("Err should report closed connection")
	}
}

func TestShutdownInProcess(t *testing.T) {
	c, srv := connectFake(t, fakeserver.Options{}, Config{})
	if err := c.Shutdown(timeout(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if srv.Snapshot().ShutdownCalls != 1 {
		t.Fatal("shutdown not received")
	}
	<-c.Done()
	if err := c.Shutdown(timeout(t, time.Second)); err == nil {
		t.Fatal("second shutdown should fail")
	}
}

// minimalServer announces no capabilities at all.
func TestNotSupported(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	notified := make(chan string, 10)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-8"}}, nil
	})
	srv.HandleDefault(func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		notified <- req.Method
		return nil, nil
	})
	srv.Start()
	defer srv.Close()
	log, logs := testLogger()
	c, err := Connect(timeout(t, 5*time.Second), a, Config{Logger: log, ClientName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !strings.Contains(logs.String(), "unsupported position encoding") {
		t.Error("missing encoding warning")
	}
	ctx := timeout(t, 5*time.Second)
	if err := c.DidOpen(ctx, uri, "php", 1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := c.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: "y"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.DidSave(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := c.DidClose(ctx, uri); err != nil {
		t.Fatal(err)
	}
	pos := protocol.Position{}
	checks := map[string]error{}
	_, checks["completion"] = c.Completion(ctx, uri, pos, nil)
	_, checks["hover"] = c.Hover(ctx, uri, pos)
	_, checks["signature"] = c.SignatureHelp(ctx, uri, pos)
	_, checks["definition"] = c.Definition(ctx, uri, pos)
	_, checks["implementation"] = c.Implementation(ctx, uri, pos)
	_, checks["tokens"] = c.SemanticTokensFull(ctx, uri)
	for name, err := range checks {
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Only "initialized" may have reached the server.
	if m := <-notified; m != "initialized" {
		t.Fatalf("first notification %q", m)
	}
	select {
	case m := <-notified:
		t.Fatalf("unexpected %q", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBadServerResponses(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{
			"completionProvider": map[string]any{}, "hoverProvider": true, "signatureHelpProvider": map[string]any{},
			"definitionProvider": true, "implementationProvider": true,
			"semanticTokensProvider": map[string]any{"legend": fakeserver.Legend, "full": true},
		}}, nil
	})
	srv.Handle("textDocument/semanticTokens/full", func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		return map[string]any{"data": []int{1, 2}}, nil
	})
	srv.Handle("textDocument/definition", func(context.Context, *jsonrpc2.Request) (any, error) { return []int{1}, nil })
	srv.HandleDefault(func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		if req.IsNotification() {
			return nil, nil
		}
		return nil, jsonrpc2.NewError(jsonrpc2.CodeRequestFailed, "nope")
	})
	srv.Start()
	defer srv.Close()
	c, err := Connect(timeout(t, 5*time.Second), a, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := timeout(t, 5*time.Second)
	pos := protocol.Position{}
	errs := map[string]error{}
	_, errs["completion"] = c.Completion(ctx, uri, pos, nil)
	_, errs["hover"] = c.Hover(ctx, uri, pos)
	_, errs["signature"] = c.SignatureHelp(ctx, uri, pos)
	_, errs["definition"] = c.Definition(ctx, uri, pos)
	_, errs["implementation"] = c.Implementation(ctx, uri, pos)
	_, errs["tokens"] = c.SemanticTokensFull(ctx, uri)
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSemanticTokensNull(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{
			"semanticTokensProvider": map[string]any{"legend": fakeserver.Legend, "full": map[string]any{"delta": false}},
		}}, nil
	})
	srv.Handle("textDocument/semanticTokens/full", func(context.Context, *jsonrpc2.Request) (any, error) { return nil, nil })
	srv.Start()
	defer srv.Close()
	c, err := Connect(timeout(t, 5*time.Second), a, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	toks, err := c.SemanticTokensFull(timeout(t, 5*time.Second), uri)
	if err != nil || toks != nil {
		t.Fatalf("%v %v", toks, err)
	}
}

func TestConnectInitializeFails(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Start() // no handlers: initialize -> method not found
	defer srv.Close()
	if _, err := Connect(timeout(t, 5*time.Second), a, Config{}); err == nil {
		t.Fatal("expected initialize error")
	}
}

func TestServerMessagesAndBadDiagnostics(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{}}, nil
	})
	srv.Start()
	defer srv.Close()
	log, logs := testLogger()
	called := false
	c, err := Connect(timeout(t, 5*time.Second), a, Config{Logger: log, OnDiagnostics: func(protocol.PublishDiagnosticsParams) { called = true }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := timeout(t, 5*time.Second)
	_ = srv.Notify(ctx, protocol.MethodPublishDiagnostics, map[string]any{"uri": 5})
	for i, typ := range []protocol.MessageType{protocol.MessageError, protocol.MessageWarning, protocol.MessageLog} {
		_ = srv.Notify(ctx, protocol.MethodLogMessage, protocol.LogMessageParams{Type: typ, Message: fmt.Sprintf("msg%d", i)})
	}
	if err := srv.Call(ctx, protocol.MethodShowMessageRequest, protocol.LogMessageParams{Type: 1, Message: "ask"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.Call(ctx, protocol.MethodWorkspaceConfiguration, "bad", nil); err == nil {
		t.Fatal("expected invalid params error")
	}
	var cfg []any
	if err := srv.Call(ctx, protocol.MethodWorkspaceConfiguration, protocol.ConfigurationParams{Items: []protocol.ConfigurationItem{{}}}, &cfg); err != nil || len(cfg) != 1 || cfg[0] != nil {
		t.Fatalf("config %v %v", cfg, err)
	}
	if err := srv.Call(ctx, protocol.MethodUnregisterCapability, nil, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "logs", func() bool {
		s := logs.String()
		return strings.Contains(s, "msg2") && strings.Contains(s, "bad publishDiagnostics") && strings.Contains(s, "ask")
	})
	if called {
		t.Fatal("diagnostics callback called for invalid params")
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "level=DEBUG") {
		t.Fatalf("levels: %s", logs.String())
	}
}

// --- child process ---------------------------------------------------------

func processConfig(t *testing.T, env ...string) (Config, *syncBuffer) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log, logs := testLogger()
	return Config{
		Command: exe,
		Args:    []string{"-test.run=^$"},
		Dir:     t.TempDir(),
		Env:     append([]string{"TIEGEL_FAKE_LSP=1"}, env...),
		Logger:  log,
	}, logs
}

func TestProcessLifecycle(t *testing.T) {
	cfg, logs := processConfig(t, "FAKE_SYNC=full")
	diags := newDiagSink()
	cfg.OnDiagnostics = diags.add
	cfg.RootDir = cfg.Dir
	cfg.Dir = ""
	ctx := timeout(t, 10*time.Second)
	c, err := Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Capabilities().TextDocumentSync.Change != protocol.SyncFull {
		t.Fatal("expected full sync from env")
	}
	if err := c.DidOpen(ctx, uri, "php", 1, "ä\n"); err != nil {
		t.Fatal(err)
	}
	if err := c.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Range: rng(0, 1, 0, 1), Text: "😀ERR"}}); err != nil {
		t.Fatal(err)
	}
	d := diags.waitVersion(t, 2)
	if len(d.Diagnostics) != 1 || d.Diagnostics[0].Range != *rng(0, 3, 0, 6) {
		t.Fatalf("diagnostics %+v", d)
	}
	var st fakeserver.State
	if err := c.Call(ctx, "fake/state", nil, &st); err != nil {
		t.Fatal(err)
	}
	if st.Docs[uri] != "ä😀ERR\n" || !st.FullSyncSeen {
		t.Fatalf("server state %+v", st)
	}
	eventually(t, "stderr in log", func() bool { return strings.Contains(logs.String(), "fake server starting") })
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed after shutdown")
	}
}

func TestProcessCrash(t *testing.T) {
	cfg, _ := processConfig(t)
	c, err := Start(timeout(t, 10*time.Second), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := timeout(t, 10*time.Second)
	_, err = c.Completion(ctx, uri, protocol.Position{Line: fakeserver.CrashLine}, nil)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("expected crash error before timeout, got %v", err)
	}
	if !errors.Is(err, jsonrpc2.ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	eventually(t, "exit status", func() bool {
		e := c.Err()
		return e != nil && strings.Contains(e.Error(), "exit status 3")
	})
	if _, err := c.Hover(ctx, uri, protocol.Position{}); err == nil || !strings.Contains(err.Error(), "server exited") {
		t.Fatalf("hover after crash: %v", err)
	}
	if err := c.Shutdown(ctx); err == nil {
		t.Fatal("shutdown of crashed server should report error")
	}
}

func TestProcessShutdownTimeoutKills(t *testing.T) {
	cfg, _ := processConfig(t, "FAKE_NOEXIT=1")
	c, err := Start(timeout(t, 10*time.Second), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if exited, _ := c.proc.exitStatus(); !exited {
		t.Fatal("process not killed")
	}
}

func TestStartErrors(t *testing.T) {
	ctx := timeout(t, 10*time.Second)
	if _, err := Start(ctx, Config{}); err == nil {
		t.Error("expected error for empty command")
	}
	if _, err := Start(ctx, Config{Command: "/nonexistent/tiegel-lsp"}); err == nil {
		t.Error("expected error for missing binary")
	}
	cfg, logs := processConfig(t)
	cfg.Env = []string{"TIEGEL_FAKE_LSP=fail"}
	_, err := Start(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("err = %v", err)
	}
	eventually(t, "stderr", func() bool { return strings.Contains(logs.String(), "refuses to start") })
}

func TestCloseKillsProcess(t *testing.T) {
	cfg, _ := processConfig(t, "FAKE_NOEXIT=1")
	c, err := Start(timeout(t, 10*time.Second), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if exited, _ := c.proc.exitStatus(); !exited {
		t.Fatal("process still running")
	}
	c.Close() // idempotent
}
