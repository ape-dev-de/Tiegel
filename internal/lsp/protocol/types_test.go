package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustUnmarshal(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("unmarshal %s: %v", s, err)
	}
}

func TestIntOrString(t *testing.T) {
	var v IntOrString
	mustUnmarshal(t, `42`, &v)
	if !v.IsInt || v.String() != "42" {
		t.Fatalf("%+v", v)
	}
	b, _ := json.Marshal(v)
	if string(b) != "42" {
		t.Fatalf("marshal %s", b)
	}
	mustUnmarshal(t, `"E1"`, &v)
	if v.IsInt || v.Value != "E1" {
		t.Fatalf("%+v", v)
	}
	b, _ = json.Marshal(v)
	if string(b) != `"E1"` {
		t.Fatalf("marshal %s", b)
	}
	if err := json.Unmarshal([]byte(`{}`), &v); err == nil {
		t.Fatal("expected error")
	}
}

func TestMarkupContent(t *testing.T) {
	cases := map[string]MarkupContent{
		`null`:                                  {},
		`"plain"`:                               {Kind: Markdown, Value: "plain"},
		`{"kind":"plaintext","value":"x"}`:      {Kind: PlainText, Value: "x"},
		`{"language":"php","value":"<?php"}`:    {Kind: Markdown, Value: "```php\n<?php\n```"},
		`["a",{"language":"php","value":"$x"}]`: {Kind: Markdown, Value: "a\n\n```php\n$x\n```"},
	}
	for in, want := range cases {
		var m MarkupContent
		mustUnmarshal(t, in, &m)
		if m != want {
			t.Errorf("%s: got %+v, want %+v", in, m, want)
		}
	}
	for _, bad := range []string{`[1]`, `{"kind":1}`, `"x`} {
		var m MarkupContent
		if err := json.Unmarshal([]byte(bad), &m); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestCompletionList(t *testing.T) {
	var l CompletionList
	mustUnmarshal(t, `[{"label":"a"}]`, &l)
	if len(l.Items) != 1 || l.IsIncomplete {
		t.Fatalf("%+v", l)
	}
	mustUnmarshal(t, `{"isIncomplete":true,"items":[{"label":"b","insertTextFormat":2,
		"textEdit":{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}},"newText":"b($1)"},
		"documentation":"doc"}]}`, &l)
	if !l.IsIncomplete || l.Items[0].InsertTextFormat != InsertTextSnippet || l.Items[0].Documentation.Value != "doc" {
		t.Fatalf("%+v", l)
	}
	if r := l.Items[0].TextEdit.EditRange(); r.Start != (Position{1, 2}) {
		t.Fatalf("range %+v", r)
	}
	mustUnmarshal(t, `null`, &l)
	if l.Items != nil {
		t.Fatal("null not reset")
	}
	for _, bad := range []string{`[1]`, `{"items":1}`} {
		if err := json.Unmarshal([]byte(bad), &l); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
	ins := Range{Start: Position{0, 1}}
	if (&CompletionTextEdit{Insert: &ins}).EditRange() != ins {
		t.Error("insert range")
	}
	if (&CompletionTextEdit{}).EditRange() != (Range{}) {
		t.Error("empty range")
	}
}

func TestParameterLabel(t *testing.T) {
	var p ParameterLabel
	mustUnmarshal(t, `[4, 11]`, &p)
	if p.Offsets == nil || *p.Offsets != [2]int{4, 11} {
		t.Fatalf("%+v", p)
	}
	b, _ := json.Marshal(p)
	if string(b) != "[4,11]" {
		t.Fatalf("%s", b)
	}
	mustUnmarshal(t, `"int $a"`, &p)
	if p.Text != "int $a" || p.Offsets != nil {
		t.Fatalf("%+v", p)
	}
	b, _ = json.Marshal(p)
	if string(b) != `"int $a"` {
		t.Fatalf("%s", b)
	}
	if err := json.Unmarshal([]byte(`["x"]`), &p); err == nil {
		t.Error("expected error")
	}
	if err := json.Unmarshal([]byte(`1`), &p); err == nil {
		t.Error("expected error")
	}
}

func TestParseLocations(t *testing.T) {
	r := `{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}`
	want := []Location{{URI: "file:///a.php", Range: Range{Position{1, 2}, Position{1, 5}}}}
	for _, in := range []string{
		`{"uri":"file:///a.php","range":` + r + `}`,
		`[{"uri":"file:///a.php","range":` + r + `}]`,
		`[{"targetUri":"file:///a.php","targetRange":` + r + `,"targetSelectionRange":` + r + `}]`,
	} {
		got, err := ParseLocations([]byte(in))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v %v", in, got, err)
		}
	}
	if got, err := ParseLocations([]byte(`null`)); got != nil || err != nil {
		t.Error("null")
	}
	for _, bad := range []string{`{"uri":1}`, `[1]`, `[`} {
		if _, err := ParseLocations([]byte(bad)); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestDecodeSemanticTokens(t *testing.T) {
	legend := SemanticTokensLegend{TokenTypes: []string{"class", "variable"}, TokenModifiers: []string{"declaration", "static", "readonly"}}
	got, err := DecodeSemanticTokens([]uint32{
		2, 5, 3, 0, 1, // line 2 col 5
		0, 4, 2, 1, 0b110, // same line, col 9
		3, 1, 1, 7, 0b1000, // line 5 col 1, unknown type/modifier
	}, legend)
	if err != nil {
		t.Fatal(err)
	}
	want := []SemanticToken{
		{Line: 2, StartChar: 5, Length: 3, Type: "class", Modifiers: []string{"declaration"}},
		{Line: 2, StartChar: 9, Length: 2, Type: "variable", Modifiers: []string{"static", "readonly"}},
		{Line: 5, StartChar: 1, Length: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := DecodeSemanticTokens([]uint32{1, 2}, legend); err == nil {
		t.Fatal("expected length error")
	}
}

func TestTextDocumentSync(t *testing.T) {
	cases := map[string]TextDocumentSyncOptions{
		`null`:                          {},
		`0`:                             {},
		`1`:                             {OpenClose: true, Change: SyncFull, Save: &SaveOptions{}},
		`2`:                             {OpenClose: true, Change: SyncIncremental, Save: &SaveOptions{}},
		`{"openClose":true,"change":2}`: {OpenClose: true, Change: SyncIncremental},
		`{"openClose":true,"change":1,"save":true}`: {OpenClose: true, Change: SyncFull, Save: &SaveOptions{}},
		`{"change":1,"save":false}`:                 {Change: SyncFull},
		`{"change":1,"save":{"includeText":true}}`:  {Change: SyncFull, Save: &SaveOptions{IncludeText: true}},
	}
	for in, want := range cases {
		var o TextDocumentSyncOptions
		mustUnmarshal(t, in, &o)
		if !reflect.DeepEqual(o, want) {
			t.Errorf("%s: got %+v", in, o)
		}
	}
	for _, bad := range []string{`"x"`, `{"save":"x"}`} {
		var o TextDocumentSyncOptions
		if err := json.Unmarshal([]byte(bad), &o); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestProvider(t *testing.T) {
	var caps ServerCapabilities
	mustUnmarshal(t, `{"hoverProvider":true,"definitionProvider":{"workDoneProgress":true},
		"implementationProvider":false,"semanticTokensProvider":{"legend":{"tokenTypes":["a"],"tokenModifiers":[]},"full":{"delta":true}}}`, &caps)
	if !caps.HoverProvider.IsEnabled() || !caps.DefinitionProvider.IsEnabled() || caps.ImplementationProvider.IsEnabled() {
		t.Fatalf("%+v", caps)
	}
	if !caps.SemanticTokensProvider.Full.IsEnabled() || caps.SemanticTokensProvider.Range.IsEnabled() {
		t.Fatal("semantic tokens")
	}
	var nilP *Provider
	if nilP.IsEnabled() {
		t.Fatal("nil provider enabled")
	}
	b, _ := json.Marshal(Provider{Enabled: true})
	if string(b) != "true" {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(*caps.DefinitionProvider)
	if string(b) != `{"workDoneProgress":true}` {
		t.Fatalf("%s", b)
	}
	var p Provider
	mustUnmarshal(t, `null`, &p)
	if p.Enabled {
		t.Fatal("null enabled")
	}
}
