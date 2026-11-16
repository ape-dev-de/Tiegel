package langphp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

const otherSrc = "<?php\nfoo(1, 'x');  \n\tfoo(2, 'y');\n"

// refactorSetup starts the server with a.php and other.php open.
func refactorSetup(t *testing.T) (*api.App, *Module, *fakes, string) {
	t.Helper()
	a, m, f, dir := setup(t)
	modtest.WriteFile(t, filepath.Join(dir, "other.php"), otherSrc)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "other.php"}, nil)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake"}, nil)
	waitSynced(t, a, f, dir, "a.php")
	waitSynced(t, a, f, dir, "other.php")
	return a, m, f, dir
}

func TestReferencesHighlightSymbols(t *testing.T) {
	a, _, _, _ := refactorSetup(t)
	onFoo := api.Pos{Line: 3, Col: 10} // function foo
	var us []Usage
	modtest.Call(t, a, "lsp.references", ReferencesParams{Path: "a.php", At: onFoo}, &us)
	if len(us) != 3 || us[0].Path != "a.php" || us[1].Path != "other.php" || us[1].LineText != "foo(1, 'x');  " || us[1].Range.Start.Line != 2 {
		t.Fatalf("references %+v", us)
	}
	no := false
	modtest.Call(t, a, "lsp.references", ReferencesParams{Path: "a.php", At: onFoo, IncludeDeclaration: &no}, &us)
	if len(us) != 2 {
		t.Fatalf("without declaration %+v", us)
	}

	var hs []Highlight
	modtest.Call(t, a, "lsp.documentHighlight", PosParams{Path: "other.php", At: api.Pos{Line: 2, Col: 2}}, &hs)
	if len(hs) != 2 || hs[0].Kind != "read" || hs[1].Range.Start.Line != 3 {
		t.Fatalf("highlights %+v", hs)
	}
	modtest.Call(t, a, "lsp.documentHighlight", PosParams{Path: "other.php", At: api.Pos{Line: 2, Col: 14}}, &hs)
	if len(hs) != 0 {
		t.Fatalf("highlights on blank %+v", hs)
	}

	var syms []DocumentSymbol
	modtest.Call(t, a, "lsp.documentSymbols", api.PathParams{Path: "a.php"}, &syms)
	if len(syms) != 2 || syms[0].Name != "foo" || syms[0].Kind != "function" || syms[1].Kind != "class" || syms[1].SelectionRange.Start.Col != 7 {
		t.Fatalf("document symbols %+v", syms)
	}
	var ws []WorkspaceSymbol
	modtest.Call(t, a, "lsp.workspaceSymbols", WorkspaceSymbolsParams{Query: "foo"}, &ws)
	if len(ws) != 2 || ws[0].Name != "foo" || ws[0].Range != nil || ws[1].Kind != "class" || ws[1].Range == nil || ws[1].Range.Start.Line != 4 {
		t.Fatalf("workspace symbols %+v", ws)
	}
	modtest.Call(t, a, "lsp.workspaceSymbols", WorkspaceSymbolsParams{Query: "foo", Limit: 1}, &ws)
	if len(ws) != 1 {
		t.Fatalf("limit %+v", ws)
	}
}

func actionByTitle(t *testing.T, acts []CodeActionInfo, title string) CodeActionInfo {
	t.Helper()
	for _, a := range acts {
		if a.Title == title {
			return a
		}
	}
	t.Fatalf("no action %q in %+v", title, acts)
	return CodeActionInfo{}
}

func TestCodeActions(t *testing.T) {
	a, _, f, dir := refactorSetup(t)
	diags := modtest.Record(t, a, api.TopicLSPDiagnostics)
	edits := modtest.Record(t, a, TopicEditApplied)
	modtest.Eventually(t, "diagnostics of a.php", func() bool {
		var list []Diagnostics
		modtest.Call(t, a, "lsp.diagnostics", DiagnosticsParams{Path: "a.php"}, &list)
		return len(list) == 1 && len(list[0].Diagnostics) == 1
	})
	_ = diags

	var acts []CodeActionInfo
	modtest.Call(t, a, "lsp.codeActions", CodeActionsParams{Path: "a.php", Start: api.Pos{Line: 2, Col: 8}, End: &api.Pos{Line: 2, Col: 7}}, &acts)
	fix := actionByTitle(t, acts, fakeserver.ActionRemoveErr)
	if fix.Kind != "quickfix" || !fix.Preferred || !fix.HasEdit || len(fix.Diagnostics) != 1 || fix.Diagnostics[0] != "error marker" || len(fix.Action) == 0 {
		t.Fatalf("quick fix %+v", fix)
	}
	if d := actionByTitle(t, acts, fakeserver.ActionDisabled); d.Disabled != "not here" {
		t.Fatalf("disabled %+v", d)
	}
	var res EditResult
	modtest.Call(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: fix.ID}, &res)
	if !res.Applied || len(res.Files) != 1 || !res.Files[0].Open || res.Files[0].Path != "a.php" {
		t.Fatalf("apply %+v", res)
	}
	if got := docText(t, a, "a.php"); !strings.Contains(got, "echo '';") {
		t.Fatalf("after fix %q", got)
	}
	edits.Wait(t, TopicEditApplied, nil)
	var ur api.UndoResult
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.php"}, &ur)
	if docText(t, a, "a.php") != src {
		t.Fatal("quick fix not undoable in one step")
	}
	waitSynced(t, a, f, dir, "a.php")

	// No diagnostics in range: no quick fix. Lazily resolved action.
	modtest.Call(t, a, "lsp.codeActions", CodeActionsParams{Path: "a.php", Start: api.Pos{Line: 4, Col: 1}, Only: []string{"refactor"}}, &acts)
	for _, x := range acts {
		if x.Title == fakeserver.ActionRemoveErr {
			t.Fatal("quick fix without diagnostic")
		}
	}
	doc := actionByTitle(t, acts, fakeserver.ActionDocblock)
	if doc.HasEdit {
		t.Fatal("docblock should be resolved lazily")
	}
	modtest.Call(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: doc.ID}, &res)
	if got := docText(t, a, "a.php"); !strings.Contains(got, "\n"+fakeserver.DocblockLine+"class Foo") {
		t.Fatalf("docblock %q", got)
	}
	waitSynced(t, a, f, dir, "a.php")

	// Command with workspace/applyEdit from the server.
	org := actionByTitle(t, acts, fakeserver.ActionOrganize)
	modtest.Call(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: org.ID}, &res)
	if res.Command != fakeserver.CommandOrganize || !strings.Contains(string(res.CommandResult), "true") {
		t.Fatalf("organize %+v", res)
	}
	if got := docText(t, a, "a.php"); !strings.HasPrefix(got, fakeserver.OrganizedLine) {
		t.Fatalf("organize %q", got)
	}
	if st := f.last().Snapshot(); len(st.Applied) != 1 || !st.Applied[0] {
		t.Fatalf("server state %+v", st)
	}
	run := actionByTitle(t, acts, fakeserver.ActionRunFake)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: run.ID}, api.CodeFailed)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: actionByTitle(t, acts, fakeserver.ActionDisabled).ID}, api.CodeFailed)

	// Resource operations: create, edit a closed file, rename; delete with
	// ignoreIfNotExists; the raw action works without the cache.
	ext := actionByTitle(t, acts, fakeserver.ActionExtract)
	modtest.Call(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{Action: ext.Action}, &res)
	if len(res.Files) != 3 || res.Files[2].Kind != api.FileOpRename {
		t.Fatalf("extract %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Moved.php"))
	if err != nil || string(data) != "<?php\n// extracted\n" {
		t.Fatalf("moved %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Extracted.php")); err == nil {
		t.Fatal("extracted not renamed")
	}
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: ext.ID}, api.CodeConflict)
	modtest.Call(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: actionByTitle(t, acts, fakeserver.ActionDeleteTemp).ID}, &res)
	if !res.Applied || !res.Files[0].Skipped {
		t.Fatalf("delete %+v", res)
	}

	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: "a999999"}, api.CodeNotFound)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{Action: json.RawMessage(`[`)}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.codeActions", CodeActionsParams{Path: "a.php", Start: api.Pos{Line: 1}, End: &api.Pos{Line: 99}}, api.CodeInvalidParams)

	modtest.Call(t, a, "lsp.executeCommand", ExecuteCommandParams{Command: fakeserver.CommandOrganize, Arguments: []json.RawMessage{json.RawMessage(`"` + string(protocol.URIFromPath(filepath.Join(dir, "other.php"))) + `"`)}}, &res)
	if got := docText(t, a, "other.php"); !strings.HasPrefix(got, fakeserver.OrganizedLine) {
		t.Fatalf("execute %q", got)
	}
	modtest.WantCode(t, a, "lsp.executeCommand", ExecuteCommandParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.executeCommand", ExecuteCommandParams{Command: fakeserver.CommandFail}, api.CodeFailed)

	// After a restart old IDs are outdated.
	modtest.Call(t, a, "lsp.stop", nil, nil)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: fix.ID}, api.CodeUnavailable)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake"}, nil)
	modtest.WantCode(t, a, "lsp.applyCodeAction", ApplyCodeActionParams{ID: fix.ID}, api.CodeNotFound)
}

func TestFormattingAndRename(t *testing.T) {
	a, m, f, dir := refactorSetup(t)
	no := false
	var fr FormatResult
	modtest.Call(t, a, "lsp.formatting", FormatParams{Path: "other.php", Apply: &no}, &fr)
	if fr.Applied || len(fr.Edits) != 2 || fr.Edits[1].NewText != "    " || docText(t, a, "other.php") != otherSrc {
		t.Fatalf("dry run %+v", fr)
	}
	modtest.Call(t, a, "lsp.rangeFormatting", FormatParams{Path: "other.php", Start: &api.Pos{Line: 2}, End: &api.Pos{Line: 2, Col: 3}}, &fr)
	if !fr.Applied || docText(t, a, "other.php") != "<?php\nfoo(1, 'x');\n\tfoo(2, 'y');\n" {
		t.Fatalf("range %+v %q", fr, docText(t, a, "other.php"))
	}
	waitSynced(t, a, f, dir, "other.php")
	modtest.Call(t, a, "lsp.formatting", FormatParams{Path: "other.php", TabSize: 2}, &fr)
	if !fr.Applied || docText(t, a, "other.php") != "<?php\nfoo(1, 'x');\n  foo(2, 'y');\n" {
		t.Fatalf("format %+v %q", fr, docText(t, a, "other.php"))
	}
	waitSynced(t, a, f, dir, "other.php")
	modtest.Call(t, a, "lsp.formatting", FormatParams{Path: "other.php"}, &fr)
	if fr.Applied || len(fr.Edits) != 0 {
		t.Fatalf("already formatted %+v", fr)
	}
	modtest.WantCode(t, a, "lsp.formatting", FormatParams{Path: "other.php", Start: &api.Pos{Line: 1}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.rangeFormatting", FormatParams{Path: "other.php"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.rangeFormatting", FormatParams{Path: "other.php", Start: &api.Pos{Line: 1}, End: &api.Pos{Line: 50}}, api.CodeInvalidParams)

	var pr PrepareRenameResult
	modtest.Call(t, a, "lsp.prepareRename", PosParams{Path: "a.php", At: api.Pos{Line: 3, Col: 11}}, &pr)
	if !pr.CanRename || pr.Placeholder != "foo" || pr.Range == nil || pr.Range.Start.Col != 10 {
		t.Fatalf("prepare %+v", pr)
	}
	modtest.Call(t, a, "lsp.prepareRename", PosParams{Path: "a.php", At: api.Pos{Line: 5}}, &pr)
	if pr.CanRename {
		t.Fatalf("prepare on empty line %+v", pr)
	}
	modtest.WantCode(t, a, "lsp.rename", RenameParams{Path: "a.php", At: api.Pos{Line: 5}, NewName: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.rename", RenameParams{Path: "a.php", At: api.Pos{Line: 3, Col: 11}}, api.CodeInvalidParams)

	var rr RenameResult
	modtest.Call(t, a, "lsp.rename", RenameParams{Path: "a.php", At: api.Pos{Line: 3, Col: 11}, NewName: "bar", Preview: true}, &rr)
	if rr.Applied || len(rr.Preview) != 2 || rr.Preview[1].Path != "other.php" || len(rr.Preview[1].Edits) != 2 || rr.Preview[1].Edits[0].NewText != "bar" {
		t.Fatalf("preview %+v", rr)
	}
	if strings.Contains(docText(t, a, "a.php"), "bar") {
		t.Fatal("preview applied")
	}
	edits := modtest.Record(t, a, TopicEditApplied)
	modtest.Call(t, a, "lsp.rename", RenameParams{Path: "a.php", At: api.Pos{Line: 3, Col: 11}, NewName: "bar"}, &rr)
	if !rr.Applied || len(rr.Files) != 2 || rr.Label != "rename foo to bar" {
		t.Fatalf("rename %+v", rr)
	}
	if !strings.Contains(docText(t, a, "a.php"), "function bar(") || strings.Count(docText(t, a, "other.php"), "bar(") != 2 {
		t.Fatal("not renamed everywhere")
	}
	ev := edits.Wait(t, TopicEditApplied, func(e bus.Event) bool { return len(e.Payload.(EditResult).Files) == 2 })
	if !ev.Payload.(EditResult).Applied {
		t.Fatalf("event %+v", ev)
	}
	// A stale document version makes the edit fail (nothing applied to
	// that file).
	we := &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{{
		TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: protocol.URIFromPath(filepath.Join(dir, "a.php")), Version: new(int)},
		Edits:        []protocol.TextEdit{{NewText: "x"}},
	}}}
	res := m.onApplyEdit(t.Context(), protocol.ApplyWorkspaceEditParams{Edit: *we})
	if res.Applied || res.FailureReason == "" || res.FailedChange == nil || *res.FailedChange != 0 {
		t.Fatalf("stale apply %+v", res)
	}
}

func TestWorkspaceEditConversion(t *testing.T) {
	bad := []protocol.WorkspaceEdit{
		{DocumentChanges: []protocol.DocumentChange{{}}},
		{DocumentChanges: []protocol.DocumentChange{{Kind: "copy"}}},
		{DocumentChanges: []protocol.DocumentChange{{TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: "http://x"}}}},
		{DocumentChanges: []protocol.DocumentChange{{Kind: protocol.ResourceCreate, URI: "http://x"}}},
		{DocumentChanges: []protocol.DocumentChange{{Kind: protocol.ResourceRename, OldURI: "http://x", NewURI: "file:///b"}}},
		{DocumentChanges: []protocol.DocumentChange{{Kind: protocol.ResourceRename, OldURI: "file:///a", NewURI: "http://x"}}},
		{DocumentChanges: []protocol.DocumentChange{{Kind: protocol.ResourceDelete, URI: "http://x"}}},
		{Changes: map[protocol.DocumentURI][]protocol.TextEdit{"http://x": nil}},
	}
	for i, we := range bad {
		if _, err := fileOps(&we); err == nil {
			t.Errorf("%d: accepted", i)
		}
	}
	ops, err := fileOps(&protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
		"file:///b.php": {{NewText: "b"}}, "file:///a.php": {{Range: protocol.Range{End: protocol.Position{Line: 5}}}},
	}})
	if err != nil || len(ops) != 2 || !strings.HasSuffix(ops[0].Path, "a.php") {
		t.Fatalf("%+v %v", ops, err)
	}
	if _, err := ops[0].Changes([]byte("x")); err == nil {
		t.Fatal("out of range edit accepted")
	}
	if ch, err := ops[1].Changes([]byte("x")); err != nil || len(ch) != 1 || ch[0].Text != "b" {
		t.Fatalf("%+v %v", ch, err)
	}
}

func TestDescribeAndKinds(t *testing.T) {
	a, m, _, dir := refactorSetup(t)
	u := func(name string) protocol.DocumentURI { return protocol.URIFromPath(filepath.Join(dir, name)) }
	we := &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{
		{Kind: protocol.ResourceCreate, URI: u("n.php")},
		{TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{URI: u("a.php")}, Edits: []protocol.TextEdit{{NewText: "x"}}},
		{Kind: protocol.ResourceRename, OldURI: u("n.php"), NewURI: u("m.php")},
		{Kind: protocol.ResourceDelete, URI: u("m.php")},
	}}
	got := m.describe(m.newTextCache(nil), we)
	if len(got) != 4 || got[0].Kind != "create" || got[1].Path != "a.php" || got[2].NewPath != "m.php" || got[3].Kind != "delete" {
		t.Fatalf("describe %+v", got)
	}
	got = m.describe(m.newTextCache(nil), &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{u("a.php"): {{NewText: "y"}}, "http://bad": nil}})
	if len(got) != 1 || got[0].Edits[0].NewText != "y" {
		t.Fatalf("describe changes %+v", got)
	}
	for k, want := range map[protocol.DocumentHighlightKind]string{0: "text", 1: "text", 2: "read", 3: "write"} {
		if highlightKind(k) != want {
			t.Errorf("kind %d", k)
		}
	}
	modtest.Call(t, a, "lsp.stop", nil, nil)
	for _, method := range []string{"lsp.references", "lsp.documentHighlight", "lsp.prepareRename", "lsp.documentSymbols", "lsp.formatting", "lsp.codeActions"} {
		modtest.WantCode(t, a, method, map[string]any{"path": "a.php"}, api.CodeUnavailable)
	}
	modtest.WantCode(t, a, "lsp.workspaceSymbols", WorkspaceSymbolsParams{}, api.CodeUnavailable)
	modtest.WantCode(t, a, "lsp.executeCommand", ExecuteCommandParams{Command: "x"}, api.CodeUnavailable)
}
