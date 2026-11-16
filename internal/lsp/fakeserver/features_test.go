package fakeserver

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// TestFeatureHandlers covers the feature handlers directly (the lsp and
// lang-php tests use them through the client).
func TestFeatureHandlers(t *testing.T) {
	a, b := net.Pipe()
	s := Serve(b, Options{})
	defer s.Close()
	c := jsonrpc2.NewConn(a)
	c.Handle(protocol.MethodApplyEdit, func(context.Context, *jsonrpc2.Request) (any, error) {
		return protocol.ApplyWorkspaceEditResult{Applied: false}, nil
	})
	c.Start()
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uri := protocol.DocumentURI("file:///p/a.php")
	text := "<?php\nclass A {\n\tpublic function run() {}\n}\nfunction helper() {}\n$x = 1;\n"
	if err := c.Notify(ctx, protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri, Version: 1, Text: text}}); err != nil {
		t.Fatal(err)
	}
	pos := func(l, ch int) protocol.TextDocumentPositionParams {
		return protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: protocol.Position{Line: l, Character: ch}}
	}
	var syms []protocol.DocumentSymbol
	if err := c.Call(ctx, protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}}, &syms); err != nil || len(syms) != 2 || len(syms[0].Children) != 1 {
		t.Fatalf("symbols %+v %v", syms, err)
	}
	var ws []json.RawMessage
	if err := c.Call(ctx, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: "RUN"}, &ws); err != nil || len(ws) != 1 {
		t.Fatalf("workspace symbols %s %v", ws, err)
	}
	var hs []protocol.DocumentHighlight
	if err := c.Call(ctx, protocol.MethodDocumentHighlight, pos(5, 1), &hs); err != nil || len(hs) != 1 || hs[0].Kind != protocol.HighlightWrite {
		t.Fatalf("highlight %+v %v", hs, err)
	}
	var raw json.RawMessage
	if err := c.Call(ctx, protocol.MethodDocumentHighlight, pos(3, 1), &raw); err != nil || (len(raw) != 0 && string(raw) != "null") {
		t.Fatalf("highlight on brace %s %v", raw, err)
	}
	if err := c.Call(ctx, protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: pos(3, 1), NewName: "x"}, &raw); err != nil || (len(raw) != 0 && string(raw) != "null") {
		t.Fatalf("rename on brace %s %v", raw, err)
	}
	if err := c.Call(ctx, protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: pos(5, 1)}, nil); err == nil {
		t.Fatal("empty name accepted")
	}
	var res map[string]bool
	err := c.Call(ctx, protocol.MethodExecuteCommand, protocol.ExecuteCommandParams{Command: CommandOrganize, Arguments: []json.RawMessage{json.RawMessage(`"` + string(uri) + `"`)}}, &res)
	if err != nil || res["applied"] {
		t.Fatalf("organize %+v %v", res, err)
	}
	if err := c.Call(ctx, protocol.MethodExecuteCommand, protocol.ExecuteCommandParams{Command: CommandOrganize, Arguments: []json.RawMessage{json.RawMessage(`1`)}}, nil); err == nil {
		t.Fatal("bad argument accepted")
	}
	var act protocol.CodeAction
	if err := c.Call(ctx, protocol.MethodCodeActionResolve, protocol.CodeAction{Title: "other"}, &act); err != nil || act.Title != "other" || act.Edit != nil {
		t.Fatalf("resolve other %+v %v", act, err)
	}
	for _, m := range []string{
		protocol.MethodReferences, protocol.MethodDocumentHighlight, protocol.MethodCodeAction, protocol.MethodCodeActionResolve,
		protocol.MethodExecuteCommand, protocol.MethodFormatting, protocol.MethodRangeFormatting, protocol.MethodPrepareRename,
		protocol.MethodRename, protocol.MethodDocumentSymbol, protocol.MethodWorkspaceSymbol,
	} {
		if err := c.Call(ctx, m, "bad", nil); err == nil {
			t.Errorf("%s: expected invalid params error", m)
		}
	}
	if (protocol.SymbolKind(0)).String() != "unknown" || len(occurrences("x", "")) != 0 {
		t.Error("helpers")
	}
	if w, _, _ := wordAt("x", protocol.Position{Line: 5}); w != "" {
		t.Error("wordAt out of range")
	}
}
