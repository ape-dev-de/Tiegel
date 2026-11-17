package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// fakeBackend scripts API answers by method name.
type fakeBackend struct {
	mu       sync.Mutex
	handlers map[string]func(params json.RawMessage) (any, error)
	calls    []string
	params   map[string][]json.RawMessage
	subs     []func(api.Event)
	docs     map[string]*fakeDoc
}

type fakeDoc struct {
	sync.Mutex
	path string
	buf  *buffer.Buffer
}

func (d *fakeDoc) Buffer() *buffer.Buffer { return d.buf }
func (d *fakeDoc) Path() string           { return d.path }

func newFake() *fakeBackend {
	return &fakeBackend{handlers: map[string]func(json.RawMessage) (any, error){}, params: map[string][]json.RawMessage{}, docs: map[string]*fakeDoc{}}
}

func (f *fakeBackend) on(method string, fn func(json.RawMessage) (any, error)) {
	f.mu.Lock()
	f.handlers[method] = fn
	f.mu.Unlock()
}

func (f *fakeBackend) answer(method string, v any) {
	f.on(method, func(json.RawMessage) (any, error) { return v, nil })
}

func (f *fakeBackend) Call(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.params[method] = append(f.params[method], raw)
	h := f.handlers[method]
	f.mu.Unlock()
	if h == nil {
		return api.Errorf(api.CodeMethodNotFound, "no fake for %s", method)
	}
	v, err := h(raw)
	if err != nil {
		return err
	}
	if result != nil {
		data, _ := json.Marshal(v)
		return json.Unmarshal(data, result)
	}
	return nil
}

func (f *fakeBackend) Subscribe(_ context.Context, _ []string, fn func(api.Event)) (func(), error) {
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
	return func() {}, nil
}

func (f *fakeBackend) emit(topic string, payload any) {
	raw, _ := json.Marshal(payload)
	f.mu.Lock()
	subs := append([]func(api.Event){}, f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s(api.Event{Topic: topic, Payload: raw})
	}
}

func (f *fakeBackend) Document(path string) (DocHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docs[path]
	if d == nil {
		return nil, fmt.Errorf("not open: %s", path)
	}
	return d, nil
}

func (f *fakeBackend) called(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

const root = "/proj"

func projectFake() *fakeBackend {
	f := newFake()
	f.answer("settings.get", map[string]any{"found": false})
	f.answer("settings.set", map[string]any{})
	f.answer("autosave.config", map[string]any{"idleSeconds": 0})
	f.answer("autosave.trigger", map[string]any{"saved": []string{}})
	f.answer("project.info", ProjectInfo{})
	f.answer("project.open", ProjectInfo{Open: true, Root: root, Name: "proj", Git: true})
	f.on("files.list", func(raw json.RawMessage) (any, error) {
		var p struct {
			Path  string `json:"path"`
			Depth int    `json:"depth"`
		}
		_ = json.Unmarshal(raw, &p)
		switch {
		case p.Depth == 0:
			return filesListResult{Tree: &FileNode{Dir: true, Children: []*FileNode{
				{Name: "src", Path: "src", Dir: true, Children: []*FileNode{{Name: "Mailer.php", Path: "src/Mailer.php"}}},
				{Name: "index.php", Path: "index.php"},
			}}}, nil
		case p.Path == "":
			return filesListResult{Tree: &FileNode{Dir: true, Children: []*FileNode{
				{Name: "index.php", Path: "index.php"},
				{Name: "src", Path: "src", Dir: true, Collapsed: true},
				{Name: "vendor", Path: "vendor", Dir: true, Library: true, Collapsed: true},
			}}}, nil
		case p.Path == "src":
			return filesListResult{Tree: &FileNode{Dir: true, Children: []*FileNode{{Name: "Mailer.php", Path: "src/Mailer.php"}}}}, nil
		}
		return nil, api.Errorf(api.CodeNotFound, "nope")
	})
	f.answer("git.status", gitStatus{Branch: "main", Files: []GitFile{{Path: "src/Mailer.php", Kind: "ordinary", Index: ".", Worktree: "M"}}})
	f.answer("php.version", PHPVersion{Version: "8.3.4", Short: "8.3", Source: "interpreter"})
	f.answer("interp.list", []Interpreter{{ID: "local:php", Label: "PHP 8.3 (local)", Selected: true}})
	f.on("lsp.start", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeUnavailable, "no language server installed")
	})
	f.on("doc.open", func(raw json.RawMessage) (any, error) {
		var p pathParams
		_ = json.Unmarshal(raw, &p)
		f.mu.Lock()
		f.docs[p.Path] = &fakeDoc{path: p.Path, buf: buffer.NewString("<?php\n$this->send();\n")}
		f.mu.Unlock()
		return DocState{Path: p.Path, Language: "php", EOL: "lf"}, nil
	})
	f.answer("doc.save", DocState{})
	f.answer("doc.close", map[string]any{})
	return f
}

func started(t *testing.T, f *fakeBackend) *Controller {
	t.Helper()
	c := New(f)
	t.Cleanup(c.Close)
	c.Start(root)
	c.Settle(2 * time.Second)
	if !c.Project.Open {
		t.Fatalf("project not open: %+v (notice %q)", c.Project, c.Status.Notice)
	}
	return c
}

func TestStartOpensProjectAndLoadsState(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	if c.Tree == nil || !c.Tree.Root.Loaded || len(c.Tree.Root.Children) != 3 {
		t.Fatalf("tree root %+v", c.Tree.Root)
	}
	if c.Tree.Root.Children[0].Name != "src" || !c.Tree.Root.Children[0].Dir {
		t.Fatalf("dirs first: %+v", c.Tree.Root.Children[0])
	}
	if c.Status.Branch != "main" || c.Status.PHP.Short != "8.3" || c.Status.Interpreter != "PHP 8.3 (local)" {
		t.Fatalf("status %+v", c.Status)
	}
	if c.Status.LSP != "No language server" || !strings.Contains(c.Status.LSPHint, "never downloads") {
		t.Fatalf("lsp hint %+v", c.Status)
	}
	if got := c.GitState("src/Mailer.php"); got != "M" {
		t.Fatalf("git state file %q", got)
	}
	if got := c.GitState("src"); got != "M" {
		t.Fatalf("git state dir %q", got)
	}
	if got := c.GitState("index.php"); got != "" {
		t.Fatalf("clean %q", got)
	}
	if len(c.RecentProjects) != 1 || c.RecentProjects[0] != root {
		t.Fatalf("recent %v", c.RecentProjects)
	}

	c.LoadDir("src")
	c.Settle(time.Second)
	if n := c.Tree.Node("src"); !n.Loaded || len(n.Children) != 1 || c.Tree.Node("src/Mailer.php") == nil {
		t.Fatalf("src %+v", n)
	}
	c.LoadFileIndex()
	c.Settle(time.Second)
	if !reflect.DeepEqual(c.Files, []string{"src/Mailer.php", "index.php"}) {
		t.Fatalf("files %v", c.Files)
	}
}

func TestPrefsFromSettings(t *testing.T) {
	f := projectFake()
	f.on("settings.get", func(raw json.RawMessage) (any, error) {
		var p struct{ Key string }
		_ = json.Unmarshal(raw, &p)
		switch p.Key {
		case KeyTheme:
			return map[string]any{"found": true, "value": "light"}, nil
		case KeyTabWidth:
			return map[string]any{"found": true, "value": 2}, nil
		case KeyRecentProjects:
			return map[string]any{"found": true, "value": []string{"/old"}}, nil
		}
		return map[string]any{"found": false}, nil
	})
	c := New(f)
	defer c.Close()
	c.Start("")
	c.Settle(time.Second)
	if c.Prefs.Theme != "light" || c.Prefs.TabWidth != 2 || !reflect.DeepEqual(c.RecentProjects, []string{"/old"}) || c.Project.Open {
		t.Fatalf("prefs %+v recent %v", c.Prefs, c.RecentProjects)
	}
	c.SetTabWidth(8)
	c.SetTheme("system")
	c.SetIdleSeconds(30)
	c.SetUpdateCheck(true)
	c.Settle(time.Second)
	if f.called("settings.set") != 3 || f.called("autosave.config") != 2 || c.Prefs.TabWidth != 8 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestTabsRecentAndAutosave(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	a, b := filepath.Join(root, "index.php"), filepath.Join(root, "src/Mailer.php")
	c.OpenFile("index.php", nil)
	c.Settle(time.Second)
	c.OpenFile(b, nil)
	c.Settle(time.Second)
	if len(c.Tabs) != 2 || c.Active != 1 || c.ActiveTab().File != b || c.ActiveTab().Doc == nil || c.ActiveTab().Path != "src/Mailer.php" {
		t.Fatalf("tabs %+v active %d (%s)", c.Tabs, c.Active, c.Status.Notice)
	}
	if f.called("autosave.trigger") != 1 {
		t.Fatalf("switching tabs must autosave: %v", f.calls)
	}
	if !reflect.DeepEqual(c.RecentFiles[:2], []string{b, a}) {
		t.Fatalf("recent %v", c.RecentFiles)
	}
	c.OpenFile(a, nil) // already open: just activates
	c.Settle(time.Second)
	if c.Active != 0 || f.called("doc.open") != 2 {
		t.Fatal("reopen")
	}
	c.CycleTab(1)
	if c.Active != 1 {
		t.Fatal("cycle")
	}
	c.CycleTab(-1)
	c.ActiveTab().Modified = true
	c.CloseTab(0)
	c.Settle(time.Second)
	if len(c.Tabs) != 1 || c.Active != 0 || f.called("doc.save") != 1 || f.called("doc.close") != 1 {
		t.Fatalf("close: %+v %v", c.Tabs, f.calls)
	}
	c.CloseTab(0)
	c.Settle(time.Second)
	if c.Active != -1 || c.ActiveTab() != nil {
		t.Fatal("all closed")
	}
}

func TestDiagnosticsEvents(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	file := filepath.Join(root, "index.php")
	v0 := c.DiagVersion
	f.emit("lsp.diagnostics", diagEvent{Path: "index.php", File: file, Diagnostics: []wireDiag{
		{Severity: "warning", Message: "unused", Start: 10, End: 12, Line: 2, Col: 4, Source: "phpactor"},
	}})
	f.emit("php.diagnostics", diagEvent{Path: "index.php", File: file, PHPVersion: "8.0", Diagnostics: []wireDiag{
		{Severity: "error", Message: "enums need PHP 8.1", Feature: "enums", Start: 2, End: 6, Line: 1, Col: 3},
	}})
	c.Settle(time.Second)
	ds := c.Diagnostics(file)
	if c.DiagVersion == v0 || len(ds) != 2 || ds[0].Message != "enums need PHP 8.1" || ds[0].Severity != SevError || ds[1].Source != "phpactor" {
		t.Fatalf("diags %+v", ds)
	}
	if !strings.Contains(ds[0].Source, "php 8.0") {
		t.Fatalf("source %q", ds[0].Source)
	}
	if e, w := c.ProblemCounts(); e != 1 || w != 1 {
		t.Fatalf("counts %d %d", e, w)
	}
	if p := c.Problems(); len(p) != 2 || p[0].Severity != SevError || p[0].Path != "index.php" {
		t.Fatalf("problems %+v", p)
	}
	f.emit("lsp.state", LSPStatus{State: LSPRunning, Running: true, Name: "Phpactor", PHPVersion: "8.3"})
	c.Settle(time.Second)
	if !c.Status.LSPRunning || c.Status.LSP != "Phpactor" {
		t.Fatalf("lsp %+v", c.Status)
	}
	f.emit("lsp.state", LSPStatus{State: LSPCrashed, Name: "Phpactor", Reason: "exited", Error: "exited"})
	c.Settle(time.Second)
	if c.Status.LSPRunning || c.Status.LSP != "Phpactor crashed" {
		t.Fatalf("lsp crash %+v", c.Status)
	}
	f.emit("doc.changed", DocState{Path: "index.php", Modified: true})
	c.OpenFile("index.php", nil)
	c.Settle(time.Second)
	f.emit("doc.changed", DocState{Path: "index.php", Modified: true, EOL: "crlf"})
	c.Settle(time.Second)
	if t0 := c.ActiveTab(); !t0.Modified || t0.EOL != "crlf" {
		t.Fatalf("doc.changed %+v", t0)
	}
}

func TestCompletionFlow(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	file := filepath.Join(root, "index.php")
	c.RequestCompletion(file, 13, 13, "")
	if c.Completion.Open {
		t.Fatal("completion without LSP must not open")
	}
	c.Status.LSPRunning = true
	f.answer("lsp.completion", completionResult{Items: []CompletionItem{
		{Label: "send", Kind: "method", InsertText: "send(${1:\\$to})$0", Snippet: true},
		{Label: "setFrom", Kind: "method"},
		{Label: "subject", Kind: "property", Edit: &TextEdit{Range: TextRange{Start: TextPos{Offset: 13}, End: TextPos{Offset: 13}}, NewText: "subject"},
			Additional: []TextEdit{{Range: TextRange{Start: TextPos{Offset: 6}, End: TextPos{Offset: 6}}, NewText: "use X;\n"}}},
	}})
	c.RequestCompletion(file, 13, 13, "")
	c.Settle(time.Second)
	if !c.Completion.Open || len(c.Completion.Visible) != 3 {
		t.Fatalf("completion %+v", c.Completion)
	}
	if !c.UpdateCompletion("se", 15) || len(c.Completion.Visible) != 3 || c.Completion.Items[c.Completion.Visible[2]].Label != "subject" {
		t.Fatalf("filter se: %v", c.Completion.Visible)
	}
	if it, _ := c.SelectedItem(); it.Label != "send" {
		t.Fatalf("first %s", it.Label)
	}
	c.MoveSelection(1)
	c.MoveSelection(2)
	if it, _ := c.SelectedItem(); it.Label != "send" {
		t.Fatalf("wrap %s", it.Label)
	}
	edits, caret, ok := c.AcceptCompletion(15)
	if !ok || len(edits) != 1 || edits[0] != (Edit{Start: 13, End: 15, Text: "send($to)"}) || caret != 13+len("send(") {
		t.Fatalf("accept %+v caret %d", edits, caret)
	}
	if c.Completion.Open {
		t.Fatal("accept closes")
	}

	c.RequestCompletion(file, 13, 13, "")
	c.Settle(time.Second)
	c.UpdateCompletion("sub", 16)
	edits, caret, _ = c.AcceptCompletion(16)
	want := []Edit{{Start: 13, End: 16, Text: "subject"}, {Start: 6, End: 6, Text: "use X;\n"}}
	if !reflect.DeepEqual(edits, want) || caret != 13+7+7 {
		t.Fatalf("text edit %+v caret %d", edits, caret)
	}

	c.RequestCompletion(file, 13, 13, "")
	c.Settle(time.Second)
	if c.UpdateCompletion("se(", 16) || c.Completion.Open {
		t.Fatal("non-identifier closes")
	}
	c.RequestCompletion(file, 13, 13, "")
	c.Settle(time.Second)
	c.UpdateCompletion("zzz", 16)
	if c.Completion.Open {
		t.Fatal("no match closes")
	}
}

func TestSnippetsAndHelpers(t *testing.T) {
	cases := []struct {
		in, out string
		caret   int
	}{
		{"foo($1)", "foo()", 4},
		{"foo(${1:a}, ${2:b})$0", "foo(a, b)", 4},
		{"if (${1|x,y|}) {$0}", "if (x) {}", 4},
		{`cost \$5`, "cost $5", 7},
		{"plain", "plain", 5},
		{"end$0 x", "end x", 3},
	}
	for _, tc := range cases {
		out, caret := ExpandSnippet(tc.in)
		if out != tc.out || caret != tc.caret {
			t.Errorf("%q → %q,%d want %q,%d", tc.in, out, caret, tc.out, tc.caret)
		}
	}
	if !AutoTrigger("$a->") || !AutoTrigger("A::") || !AutoTrigger("echo $") || AutoTrigger("a-") {
		t.Fatal("auto trigger")
	}
	if r, _ := SignatureTrigger("("); !r {
		t.Fatal("sig (")
	}
	if _, cl := SignatureTrigger(")"); !cl {
		t.Fatal("sig )")
	}
	text := []byte("echo $foo->bar")
	if WordStart(text, 9) != 5 || WordStart(text, 14) != 11 || WordStart(text, 4) != 0 {
		t.Fatalf("word start %d %d", WordStart(text, 9), WordStart(text, 14))
	}
	if l, col, ok := ParseLineCol(" 12:5 "); !ok || l != 12 || col != 5 {
		t.Fatal("line:col")
	}
	if _, _, ok := ParseLineCol("x"); ok {
		t.Fatal("bad line")
	}
	if !IsWordRune("ä") || IsWordRune("(") {
		t.Fatal("word rune")
	}
	if stripMarkdown("```php\nfunction **x**()\n```") != "function x()" {
		t.Fatal("markdown")
	}
}

func TestFuzzy(t *testing.T) {
	files := []string{"src/Mailer.php", "src/Mail/Transport.php", "tests/MailerTest.php", "README.md"}
	m := FilterFuzzy("mailer", files, 0)
	if len(m) != 2 || files[m[0].Index] != "src/Mailer.php" {
		t.Fatalf("fuzzy %+v", m)
	}
	if m := FilterFuzzy("smt", files, 1); len(m) != 1 {
		t.Fatalf("limit %+v", m)
	}
	if _, _, ok := FuzzyMatch("xyz", "abc"); ok {
		t.Fatal("no match")
	}
	if len(FilterFuzzy("", files, 0)) != 4 {
		t.Fatal("empty query matches all")
	}
}

func TestNavigationAndDefinition(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	a, b := filepath.Join(root, "index.php"), filepath.Join(root, "src/Mailer.php")
	c.Status.LSPRunning = true
	f.answer("lsp.definition", []Location{{Path: "src/Mailer.php", Range: TextRange{Start: TextPos{Offset: 7}}}})
	c.GoToDefinition(At(a, 3), "lsp.definition")
	c.Settle(time.Second)
	if c.ActiveTab() == nil || c.ActiveTab().File != b || c.Reveal == nil || c.Reveal.Range.Start.Offset != 7 {
		t.Fatalf("definition: %+v %+v", c.ActiveTab(), c.Reveal)
	}
	if !c.Nav.CanBack() || c.Nav.CanForward() {
		t.Fatal("history after jump")
	}
	c.NavigateBack(At(b, 7))
	c.Settle(time.Second)
	if c.ActiveTab().File != a || c.Reveal.Range.Start.Offset != 3 || !c.Nav.CanForward() {
		t.Fatalf("back: %+v", c.Reveal)
	}
	c.NavigateForward(At(a, 3))
	c.Settle(time.Second)
	if c.ActiveTab().File != b {
		t.Fatal("forward")
	}

	f.answer("lsp.implementation", []Location{{Path: "index.php"}, {Path: "src/Mailer.php"}})
	c.GoToDefinition(At(b, 1), "lsp.implementation")
	c.Settle(time.Second)
	if len(c.Locations) != 2 || c.LocationsTitle != "Choose implementation" || c.Locations[0].File != a {
		t.Fatalf("locations %+v", c.Locations)
	}
	c.ChooseLocation(0)
	c.Settle(time.Second)
	if c.Locations != nil || c.ActiveTab().File != a {
		t.Fatal("choose")
	}
	f.answer("lsp.definition", []Location{})
	c.GoToDefinition(At(a, 1), "lsp.definition")
	c.Settle(time.Second)
	if c.Status.Notice != "No target found" {
		t.Fatalf("notice %q", c.Status.Notice)
	}

	// Signature and hover.
	f.answer("lsp.signature", signatureResult{Signatures: []Signature{{Label: "send(string $to, string $body)", Parameters: []struct {
		Label string `json:"label"`
	}{{Label: "string $to"}, {Label: "string $body"}}}}, ActiveParameter: 1})
	c.RequestSignature(a, 5)
	c.Settle(time.Second)
	if !c.Signature.Open || c.Signature.Label[c.Signature.ParamStart:c.Signature.ParamEnd] != "string $body" {
		t.Fatalf("signature %+v", c.Signature)
	}
	c.CloseSignature()
	f.answer("lsp.hover", hoverResult{Found: true, Contents: "```php\nfunction send()\n```"})
	c.RequestHover(a, 3, []string{"unused variable"})
	if !c.Hover.Open || c.Hover.Text != "unused variable" {
		t.Fatal("hover shows diagnostics immediately")
	}
	c.Settle(time.Second)
	if c.Hover.Text != "unused variable\n\nfunction send()" {
		t.Fatalf("hover %q", c.Hover.Text)
	}
	c.CloseHover()
	if c.Hover.Open {
		t.Fatal("close hover")
	}
}

func TestSettingsData(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	f.answer("modules.list", []ModuleInfo{{ID: "php-runtime", Enabled: true}, {ID: "debug-xdebug", DependsOn: []string{"php-runtime"}, Enabled: true}})
	f.answer("runtime.mappings.get", Mappings{Mappings: []Mapping{{Host: ".", Remote: "/var/www"}}, Source: "compose"})
	f.answer("runtime.diagnose", Diagnosis{Hints: []Hint{{Severity: "warning", Code: "xdebug-mode", Message: "xdebug.mode lacks debug"}}})
	f.answer("modules.disable", map[string]any{})
	f.answer("modules.enable", map[string]any{})
	f.answer("interp.select", map[string]any{})
	f.answer("runtime.mappings.set", Mappings{Mappings: []Mapping{{Host: "a", Remote: "/b"}}, Source: "settings"})
	f.answer("php.setVersionOverride", map[string]any{})
	c.LoadSettingsData()
	c.Settle(time.Second)
	if len(c.Modules) != 2 || c.Modules[0].ID != "debug-xdebug" || len(c.Interpreters) != 1 || c.Mappings.Source != "compose" || len(c.Diagnosis.Hints) != 1 {
		t.Fatalf("settings data %+v %+v %+v", c.Modules, c.Mappings, c.Diagnosis)
	}
	if d := c.Dependents("php-runtime"); !reflect.DeepEqual(d, []string{"debug-xdebug"}) {
		t.Fatalf("dependents %v", d)
	}
	c.SetModuleEnabled("debug-xdebug", false)
	c.SelectInterpreter("local:php")
	c.SetMappings([]Mapping{{Host: "a", Remote: "/b"}}, false)
	c.SetPHPOverride("8.1")
	c.Settle(time.Second)
	if f.called("modules.disable") != 1 || f.called("interp.select") != 1 || f.called("php.setVersionOverride") != 1 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestInProcessBackend(t *testing.T) {
	a, err := api.New(api.Options{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	be := NewInProcess(a)
	dir := t.TempDir()
	var info ProjectInfo
	if err := be.Call(context.Background(), "project.open", map[string]string{"path": dir}, &info); err != nil || !info.Open {
		t.Fatalf("open %v %+v", err, info)
	}
	got := make(chan string, 4)
	cancel, err := be.Subscribe(context.Background(), []string{"doc.*"}, func(e api.Event) { got <- e.Topic })
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := be.Call(context.Background(), "doc.open", map[string]any{"path": "new.php", "create": true}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case tp := <-got:
		if tp != "doc.opened" {
			t.Fatalf("topic %s", tp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	d, err := be.Document("new.php")
	if err != nil || d.Path() != filepath.Join(info.Root, "new.php") {
		t.Fatalf("document %v", err)
	}
	if _, err := be.Document("missing.php"); err == nil {
		t.Fatal("missing doc")
	}
	if err := be.Call(context.Background(), "nope.nope", nil, nil); api.ErrorCode(err) != api.CodeMethodNotFound {
		t.Fatalf("err %v", err)
	}
}

// Close waits for running jobs: the backend is closed right after the
// controller, and a job (e.g. an autosave) must not still be writing.
func TestCloseWaitsForJobs(t *testing.T) {
	c := New(newFake())
	var mu sync.Mutex
	finished := false
	started := make(chan struct{})
	c.Go(func(ctx context.Context) func() {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // work that ignores the cancel for a moment
		mu.Lock()
		finished = true
		mu.Unlock()
		return func() {}
	})
	<-started
	c.Close()
	mu.Lock()
	defer mu.Unlock()
	if !finished {
		t.Fatal("Close returned while a job was running")
	}
}
