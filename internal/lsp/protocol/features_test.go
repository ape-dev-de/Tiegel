package protocol

import (
	"encoding/json"
	"testing"
)

func TestParsePrepareRename(t *testing.T) {
	for in, want := range map[string]PrepareRenameResult{
		`{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}`:                             {Range: &Range{Start: Position{1, 2}, End: Position{1, 5}}},
		`{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"placeholder":"x"}`: {Range: &Range{End: Position{0, 1}}, Placeholder: "x"},
		`{"defaultBehavior":true}`: {DefaultBehavior: true},
	} {
		got, err := ParsePrepareRename([]byte(in))
		if err != nil || got == nil || got.Placeholder != want.Placeholder || got.DefaultBehavior != want.DefaultBehavior || (want.Range != nil && (got.Range == nil || *got.Range != *want.Range)) {
			t.Errorf("%s: %+v %v", in, got, err)
		}
	}
	if got, err := ParsePrepareRename([]byte("null")); got != nil || err != nil {
		t.Errorf("null: %+v %v", got, err)
	}
	if _, err := ParsePrepareRename([]byte(`[1]`)); err == nil {
		t.Error("array accepted")
	}
}

func TestParseSymbolsAndActions(t *testing.T) {
	flat := `[{"name":"f","kind":12,"containerName":"C","location":{"uri":"file:///a","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":1}}}},
	{"name":"D","kind":5,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},"selectionRange":{"start":{"line":0,"character":6},"end":{"line":0,"character":7}},"children":[{"name":"m","kind":6,"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}},"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}}}]}]`
	syms, err := ParseDocumentSymbols([]byte(flat))
	if err != nil || len(syms) != 2 || syms[0].Detail != "C" || syms[0].Range.Start.Line != 3 || len(syms[1].Children) != 1 {
		t.Fatalf("%+v %v", syms, err)
	}
	for _, bad := range []string{`{}`, `[1]`, `[{"location":1}]`, `[{"name":1}]`} {
		if _, err := ParseDocumentSymbols([]byte(bad)); err == nil {
			t.Errorf("document symbols %s accepted", bad)
		}
	}
	for _, bad := range []string{`{}`, `[{"location":1}]`} {
		if _, err := ParseWorkspaceSymbols([]byte(bad)); err == nil {
			t.Errorf("workspace symbols %s accepted", bad)
		}
	}
	for _, bad := range []string{`{}`, `[1]`, `[{"command":"x","arguments":1}]`, `[{"title":1}]`} {
		if _, err := ParseCodeActions([]byte(bad)); err == nil {
			t.Errorf("code actions %s accepted", bad)
		}
	}
	if s, _ := ParseDocumentSymbols(nil); s != nil {
		t.Error("null symbols")
	}
	if s, _ := ParseWorkspaceSymbols([]byte("null")); s != nil {
		t.Error("null workspace symbols")
	}
	if a, _ := ParseCodeActions([]byte("null")); a != nil {
		t.Error("null actions")
	}
	acts, err := ParseCodeActions([]byte(`[{"title":"T","command":{"title":"T","command":"c"}},{"title":"C","command":"c2"}]`))
	if err != nil || len(acts) != 2 || acts[0].Command.Command != "c" || acts[1].Command.Command != "c2" {
		t.Fatalf("%+v %v", acts, err)
	}
	if SymbolClass.String() != "class" || SymbolKind(99).String() != "unknown" || len(AllSymbolKinds()) != 26 {
		t.Error("symbol kinds")
	}
	var w *WorkspaceEdit
	if !w.IsEmpty() || (&WorkspaceEdit{Changes: map[DocumentURI][]TextEdit{"a": nil}}).IsEmpty() {
		t.Error("IsEmpty")
	}
	var opts RenameOptions
	p := Provider{Enabled: true}
	if p.Decode(&opts) {
		t.Error("bool provider decoded")
	}
	p = Provider{Enabled: true, Options: json.RawMessage(`{"prepareProvider":true}`)}
	if !p.Decode(&opts) || !opts.PrepareProvider {
		t.Error("object provider")
	}
}
