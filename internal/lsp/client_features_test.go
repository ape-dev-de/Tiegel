package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

const featureDoc = "<?php\nclass Foo {\n    function bar() {}\n}\n$foo = 1;\nfoo($foo);  \n\tERR();\n"

func TestRefactoringFeatures(t *testing.T) {
	applied := make(chan protocol.ApplyWorkspaceEditParams, 1)
	c, srv := connectFake(t, fakeserver.Options{}, Config{
		OnApplyEdit: func(_ context.Context, p protocol.ApplyWorkspaceEditParams) protocol.ApplyWorkspaceEditResult {
			applied <- p
			return protocol.ApplyWorkspaceEditResult{Applied: true}
		},
	})
	ctx := timeout(t, 5*time.Second)
	other := protocol.DocumentURI("file:///project/src/Other.php")
	if err := c.DidOpen(ctx, uri, "php", 1, featureDoc); err != nil {
		t.Fatal(err)
	}
	if err := c.DidOpen(ctx, other, "php", 3, "<?php\nfoo();\n"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "documents synced", func() bool { return len(srv.Snapshot().Docs) == 2 })
	onFoo := protocol.Position{Line: 5, Character: 1} // foo( call

	refs, err := c.References(ctx, uri, onFoo, true)
	if err != nil || len(refs) != 4 || refs[0].URI != uri || refs[3].URI != other {
		t.Fatalf("references %+v %v", refs, err)
	}
	refs, _ = c.References(ctx, uri, onFoo, false)
	if len(refs) != 3 {
		t.Fatalf("references without declaration %+v", refs)
	}

	hs, err := c.DocumentHighlight(ctx, uri, protocol.Position{Line: 4, Character: 2})
	if err != nil || len(hs) != 3 || hs[0].Kind != protocol.HighlightWrite || hs[1].Kind != protocol.HighlightRead {
		t.Fatalf("highlights %+v %v", hs, err)
	}

	diag := protocol.Diagnostic{Range: protocol.Range{Start: protocol.Position{Line: 6, Character: 1}, End: protocol.Position{Line: 6, Character: 4}}, Message: "error marker"}
	acts, err := c.CodeActions(ctx, uri, diag.Range, []protocol.Diagnostic{diag}, nil)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]protocol.CodeAction{}
	for _, a := range acts {
		titles[a.Title] = a
	}
	if fix := titles[fakeserver.ActionRemoveErr]; fix.Edit == nil || !fix.IsPreferred || len(fix.Edit.Changes[uri]) != 1 {
		t.Fatalf("quick fix %+v", fix)
	}
	if run := titles[fakeserver.ActionRunFake]; run.Command == nil || run.Command.Command != fakeserver.CommandFail {
		t.Fatalf("bare command %+v", run)
	}
	if d := titles[fakeserver.ActionDisabled]; d.Disabled == nil || d.Disabled.Reason != "not here" {
		t.Fatalf("disabled %+v", d)
	}
	if ex := titles[fakeserver.ActionExtract]; ex.Edit == nil || len(ex.Edit.DocumentChanges) != 3 || ex.Edit.DocumentChanges[2].Kind != protocol.ResourceRename {
		t.Fatalf("extract %+v", ex)
	}
	if !c.CanResolveCodeActions() {
		t.Fatal("resolve support")
	}
	res, err := c.ResolveCodeAction(ctx, titles[fakeserver.ActionDocblock])
	if err != nil || res.Edit == nil || res.Edit.DocumentChanges[0].TextDocument.Version == nil || *res.Edit.DocumentChanges[0].TextDocument.Version != 1 {
		t.Fatalf("resolve %+v %v", res, err)
	}
	raw, err := c.ExecuteCommand(ctx, *titles[fakeserver.ActionOrganize].Command)
	if err != nil || !strings.Contains(string(raw), `"applied":true`) {
		t.Fatalf("execute %s %v", raw, err)
	}
	if p := <-applied; p.Label != fakeserver.ActionOrganize || p.Edit.Changes[uri][0].NewText != fakeserver.OrganizedLine {
		t.Fatalf("applyEdit %+v", p)
	}
	if _, err := c.ExecuteCommand(ctx, protocol.Command{Command: fakeserver.CommandFail}); err == nil {
		t.Fatal("failing command")
	}
	if st := srv.Snapshot(); len(st.Executed) != 2 || len(st.Applied) != 1 || !st.Applied[0] {
		t.Fatalf("server state %+v", st)
	}

	edits, err := c.Formatting(ctx, uri, protocol.FormattingOptions{TabSize: 4, InsertSpaces: true})
	if err != nil || len(edits) != 2 || edits[1].NewText != "    " {
		t.Fatalf("formatting %+v %v", edits, err)
	}
	edits, err = c.RangeFormatting(ctx, uri, protocol.Range{Start: protocol.Position{Line: 5}, End: protocol.Position{Line: 5}}, protocol.FormattingOptions{TabSize: 4})
	if err != nil || len(edits) != 1 {
		t.Fatalf("range formatting %+v %v", edits, err)
	}

	if !c.CanPrepareRename() {
		t.Fatal("prepare support")
	}
	pr, err := c.PrepareRename(ctx, uri, onFoo)
	if err != nil || pr == nil || pr.Placeholder != "foo" || pr.Range == nil {
		t.Fatalf("prepare %+v %v", pr, err)
	}
	if pr, err := c.PrepareRename(ctx, uri, protocol.Position{Line: 3, Character: 1}); err != nil || pr != nil {
		t.Fatalf("prepare on brace %+v %v", pr, err)
	}
	we, err := c.Rename(ctx, uri, onFoo, "baz")
	if err != nil || we == nil || len(we.DocumentChanges) != 2 {
		t.Fatalf("rename %+v %v", we, err)
	}

	syms, err := c.DocumentSymbols(ctx, uri)
	if err != nil || len(syms) != 1 || syms[0].Name != "Foo" || len(syms[0].Children) != 1 || syms[0].Children[0].Kind != protocol.SymbolMethod {
		t.Fatalf("document symbols %+v %v", syms, err)
	}
	ws, err := c.WorkspaceSymbols(ctx, "")
	if err != nil || len(ws) != 2 || !ws[0].HasRange || ws[1].HasRange || ws[1].ContainerName != "Foo" {
		t.Fatalf("workspace symbols %+v %v", ws, err)
	}
}

func TestRefactoringNotSupported(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{}}, nil
	})
	got := make(chan protocol.ApplyWorkspaceEditResult, 1)
	srv.Handle("initialized", func(ctx context.Context, _ *jsonrpc2.Request) (any, error) {
		go func() {
			var res protocol.ApplyWorkspaceEditResult
			_ = srv.Call(context.Background(), protocol.MethodApplyEdit, protocol.ApplyWorkspaceEditParams{}, &res)
			got <- res
		}()
		return nil, nil
	})
	srv.Start()
	defer srv.Close()
	log, _ := testLogger()
	c, err := Connect(timeout(t, 5*time.Second), a, Config{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if res := <-got; res.Applied || res.FailureReason == "" {
		t.Fatalf("applyEdit without handler: %+v", res)
	}
	ctx := timeout(t, 5*time.Second)
	pos := protocol.Position{}
	checks := map[string]error{}
	_, checks["references"] = c.References(ctx, uri, pos, true)
	_, checks["highlight"] = c.DocumentHighlight(ctx, uri, pos)
	_, checks["codeAction"] = c.CodeActions(ctx, uri, protocol.Range{}, nil, nil)
	_, checks["resolve"] = c.ResolveCodeAction(ctx, protocol.CodeAction{})
	_, checks["execute"] = c.ExecuteCommand(ctx, protocol.Command{})
	_, checks["formatting"] = c.Formatting(ctx, uri, protocol.FormattingOptions{})
	_, checks["rangeFormatting"] = c.RangeFormatting(ctx, uri, protocol.Range{}, protocol.FormattingOptions{})
	_, checks["prepareRename"] = c.PrepareRename(ctx, uri, pos)
	_, checks["rename"] = c.Rename(ctx, uri, pos, "x")
	_, checks["documentSymbol"] = c.DocumentSymbols(ctx, uri)
	_, checks["workspaceSymbol"] = c.WorkspaceSymbols(ctx, "")
	for name, err := range checks {
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRefactoringBadResponses(t *testing.T) {
	a, b := net.Pipe()
	srv := jsonrpc2.NewConn(b)
	srv.Handle("initialize", func(context.Context, *jsonrpc2.Request) (any, error) {
		return map[string]any{"capabilities": map[string]any{
			"referencesProvider": true, "documentHighlightProvider": true, "codeActionProvider": true,
			"documentFormattingProvider": true, "documentRangeFormattingProvider": true,
			"renameProvider": map[string]any{"prepareProvider": true}, "documentSymbolProvider": true,
			"workspaceSymbolProvider": true,
		}}, nil
	})
	srv.HandleDefault(func(context.Context, *jsonrpc2.Request) (any, error) {
		return json.RawMessage(`"bogus"`), nil
	})
	srv.Start()
	defer srv.Close()
	log, _ := testLogger()
	c, err := Connect(timeout(t, 5*time.Second), a, Config{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := timeout(t, 5*time.Second)
	pos := protocol.Position{}
	checks := map[string]error{}
	_, checks["references"] = c.References(ctx, uri, pos, true)
	_, checks["highlight"] = c.DocumentHighlight(ctx, uri, pos)
	_, checks["codeAction"] = c.CodeActions(ctx, uri, protocol.Range{}, nil, nil)
	_, checks["formatting"] = c.Formatting(ctx, uri, protocol.FormattingOptions{})
	_, checks["rangeFormatting"] = c.RangeFormatting(ctx, uri, protocol.Range{}, protocol.FormattingOptions{})
	_, checks["prepareRename"] = c.PrepareRename(ctx, uri, pos)
	_, checks["rename"] = c.Rename(ctx, uri, pos, "x")
	_, checks["documentSymbol"] = c.DocumentSymbols(ctx, uri)
	_, checks["workspaceSymbol"] = c.WorkspaceSymbols(ctx, "")
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s: expected decode error", name)
		}
	}
	if c.CanResolveCodeActions() {
		t.Error("boolean codeActionProvider cannot resolve")
	}
}
