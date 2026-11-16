package fakeserver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// The fake server is exercised thoroughly by the lsp package tests; this
// test covers the raw protocol paths those tests do not reach.
func TestRawProtocol(t *testing.T) {
	a, b := net.Pipe()
	s := Serve(b, Options{})
	defer s.Close()
	c := jsonrpc2.NewConn(a)
	c.Handle(protocol.MethodWorkspaceConfiguration, func(context.Context, *jsonrpc2.Request) (any, error) {
		return []any{1, 2, 3}, nil
	})
	c.Start()
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Call(ctx, protocol.MethodInitialize, map[string]any{"rootUri": "file:///x"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify(ctx, protocol.MethodInitialized, struct{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.ConfigReceived():
	case <-time.After(5 * time.Second):
		t.Fatal("no config")
	}
	if got := s.Snapshot().Config; len(got) != 3 {
		t.Fatalf("config %v", got)
	}
	for _, m := range []string{
		protocol.MethodInitialize, protocol.MethodCompletion, protocol.MethodHover,
		protocol.MethodDefinition, protocol.MethodImplementation,
	} {
		if err := c.Call(ctx, m, "bad", nil); err == nil {
			t.Errorf("%s: expected invalid params error", m)
		}
	}
	for _, m := range []string{protocol.MethodDidOpen, protocol.MethodDidChange, protocol.MethodDidSave, protocol.MethodDidClose} {
		_ = c.Notify(ctx, m, "bad")
	}
	// Change with invalid range is rejected without crashing.
	_ = c.Notify(ctx, protocol.MethodDidChange, protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{URI: "file:///y"},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Range: &protocol.Range{Start: protocol.Position{Line: 9}}}},
	})
	_ = c.Notify(ctx, protocol.MethodDidSave, protocol.DidSaveTextDocumentParams{})
	var st State
	if err := c.Call(ctx, "fake/state", nil, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Saved) != 0 {
		t.Fatalf("saved %v", st.Saved)
	}
	if err := c.Notify(ctx, protocol.MethodExit, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("exit did not close the server")
	}
}
