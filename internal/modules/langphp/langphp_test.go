package langphp

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fakes connects clients to in-process fake servers.
type fakes struct {
	mu   sync.Mutex
	srvs []*fakeserver.Server
	cfgs []lsp.Config
	fail bool
}

func (f *fakes) connect(ctx context.Context, cfg lsp.Config) (*lsp.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("refused")
	}
	a, b := net.Pipe()
	f.srvs = append(f.srvs, fakeserver.Serve(b, fakeserver.Options{}))
	f.cfgs = append(f.cfgs, cfg)
	return lsp.Connect(ctx, a, cfg)
}

func (f *fakes) last() *fakeserver.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.srvs[len(f.srvs)-1]
}

func (f *fakes) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.srvs)
}

const src = "<?php\necho 'ERR';\nfunction foo(int $a, string $b) {}\nclass Foo {}\n"

func setup(t *testing.T) (*api.App, *Module, *fakes, string) {
	t.Helper()
	f := &fakes{}
	m := New(Options{Connect: f.connect})
	a := modtest.NewApp(t, []module.Module{m}, nil)
	dir := modtest.Project(t, a, map[string]string{"a.php": src, "b.txt": "text", "other.php": "<?php\n// other\n"})
	api.HookPHPVersion.On(a.Hooks(), 0, func(_ context.Context, q api.PHPVersionQuery) (api.PHPVersionQuery, error) {
		return api.PHPVersionQuery{Version: "8.2.1", Short: "8.2", Source: "test"}, nil
	})
	return a, m, f, dir
}

func docText(t *testing.T, a *api.App, path string) string {
	t.Helper()
	var dt api.DocText
	modtest.Call(t, a, "doc.text", api.DocTextParams{Path: path}, &dt)
	return dt.Text
}

// waitSynced waits until the fake server's copy equals the buffer.
func waitSynced(t *testing.T, a *api.App, f *fakes, dir, path string) {
	t.Helper()
	uri := protocol.URIFromPath(filepath.Join(dir, path))
	want := docText(t, a, path)
	modtest.Eventually(t, "server copy of "+path, func() bool { return f.last().Snapshot().Docs[uri] == want })
}

func TestLifecycleSyncAndFeatures(t *testing.T) {
	a, _, f, dir := setup(t)
	diags := modtest.Record(t, a, api.TopicLSPDiagnostics)
	status := modtest.Record(t, a, api.TopicLSPStatus)

	var st Status
	modtest.Call(t, a, "lsp.status", nil, &st)
	if st.Running {
		t.Fatal("running before start")
	}
	modtest.WantCode(t, a, "lsp.hover", PosParams{Path: "a.php", At: api.Pos{Line: 1}}, api.CodeUnavailable)

	// A document opened before the start is synced on start.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "b.txt"}, nil)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake-lsp", Args: []string{"--stdio"}}, &st)
	if !st.Running || st.Server != "custom" || st.PHPVersion != "8.2" || st.Generation != 1 || st.Command != "fake-lsp" || st.ServerInfo != "fake 1.0" {
		t.Fatalf("status %+v", st)
	}
	status.Wait(t, api.TopicLSPStatus, nil)
	modtest.Call(t, a, "lsp.start", StartParams{}, &st) // already running: no second server
	if f.count() != 1 {
		t.Fatal("second server started")
	}
	waitSynced(t, a, f, dir, "a.php")
	if st := f.last().Snapshot(); st.InitOptions["php.version"] != "8.2" || st.RootURI == "" {
		t.Fatalf("init %+v", st)
	}
	if _, ok := f.last().Snapshot().Docs[protocol.URIFromPath(filepath.Join(dir, "b.txt"))]; ok {
		t.Fatal("non-PHP document synced")
	}
	modtest.Call(t, a, "lsp.status", nil, &st)
	if st.OpenDocs != 1 {
		t.Fatalf("open docs %d", st.OpenDocs)
	}

	// Diagnostics: event and method, positions in API coordinates.
	ev := diags.Wait(t, api.TopicLSPDiagnostics, nil).Payload.(Diagnostics)
	if ev.Path != "a.php" || len(ev.Diagnostics) != 1 {
		t.Fatalf("diagnostics %+v", ev)
	}
	d := ev.Diagnostics[0]
	if d.Line != 2 || d.Col != 7 || d.EndCol != 10 || d.Severity != "error" || d.Code != "E1" || d.Source != "fake" || src[d.Start:d.End] != "ERR" {
		t.Fatalf("diagnostic %+v", d)
	}
	var list []Diagnostics
	modtest.Call(t, a, "lsp.diagnostics", DiagnosticsParams{Path: "a.php"}, &list)
	if len(list) != 1 || len(list[0].Diagnostics) != 1 {
		t.Fatalf("lsp.diagnostics %+v", list)
	}
	modtest.Call(t, a, "lsp.diagnostics", nil, &list)
	if len(list) != 1 {
		t.Fatalf("all %+v", list)
	}

	// Incremental changes with non-ASCII text (UTF-16 surrogates).
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "a.php", At: api.Pos{Line: 2, Col: 1}, Text: "/*ä😀x*/ "}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "a.php", At: api.Pos{Line: 2, Col: 5}, Text: "Ü\n"}, nil)
	modtest.Call(t, a, "doc.delete", api.DocDeleteParams{Path: "a.php", Start: api.Pos{Line: 3, Col: 1}, End: api.Pos{Line: 3, Col: 3}}, nil)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "a.php", Start: &api.Pos{Line: 1}, End: &api.Pos{Line: 2}, Text: "<?php // 😀\n"}, nil)
	waitSynced(t, a, f, dir, "a.php")
	// A multi-edit undo step (replace all), then undo.
	modtest.Call(t, a, "search.replaceInDoc", api.ReplaceParams{SearchParams: api.SearchParams{Path: "a.php", Pattern: "o"}, Replacement: "0😀"}, nil)
	waitSynced(t, a, f, dir, "a.php")
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.php"}, nil)
	waitSynced(t, a, f, dir, "a.php")
	// An edit on the shared buffer (like the GUI) reaches the server too.
	doc, err := a.Document("a.php")
	if err != nil {
		t.Fatal(err)
	}
	doc.Lock()
	_, _ = doc.Buffer().Insert(doc.Buffer().Len(), []byte("// gui ERR\n"))
	doc.Unlock()
	waitSynced(t, a, f, dir, "a.php")
	if f.last().Snapshot().FullSyncSeen {
		t.Fatal("expected incremental sync only")
	}
	diags.Wait(t, api.TopicLSPDiagnostics, func(e bus.Event) bool { return len(e.Payload.(Diagnostics).Diagnostics) == 2 })

	// Save → didSave with text.
	modtest.Call(t, a, "doc.save", api.PathParams{Path: "a.php"}, nil)
	modtest.Eventually(t, "didSave", func() bool { return len(f.last().Snapshot().Saved) == 1 })

	// Features (the current text drives the positions).
	text := docText(t, a, "a.php")
	var comp CompletionResult
	modtest.Call(t, a, "lsp.completion", PosParams{Path: "a.php", At: api.Pos{Line: 2, Col: 3}}, &comp)
	if !comp.Incomplete || len(comp.Items) != 2 || comp.Items[0].Kind != "function" || !comp.Items[0].Snippet ||
		comp.Items[0].Edit == nil || comp.Items[0].Edit.Range.Start.Col != 2 || comp.Items[1].Documentation != "**since** 8.0" {
		t.Fatalf("completion %+v", comp)
	}
	var hov HoverResult
	modtest.Call(t, a, "lsp.hover", PosParams{Path: "a.php", At: api.Pos{Line: 4, Col: 2}}, &hov)
	if !hov.Found || hov.Contents != "hover" || hov.Range == nil || hov.Range.Start.Line != 4 {
		t.Fatalf("hover %+v", hov)
	}
	modtest.Call(t, a, "lsp.hover", PosParams{Path: "a.php", At: api.Pos{Line: 2}}, &hov)
	if hov.Found {
		t.Fatalf("hover on line 2 %+v", hov)
	}
	var sig SignatureResult
	modtest.Call(t, a, "lsp.signature", PosParams{Path: "a.php", At: api.Pos{Offset: 3}}, &sig)
	if len(sig.Signatures) != 1 || sig.ActiveParameter != 1 || sig.Signatures[0].Parameters[0].Label != "int $a," || sig.Signatures[0].Parameters[1].Documentation != "b" {
		t.Fatalf("signature %+v", sig)
	}
	var locs []Location
	modtest.Call(t, a, "lsp.definition", PosParams{Path: "a.php", At: api.Pos{Line: 1}}, &locs)
	if len(locs) != 1 || locs[0].Path != "a.php" || locs[0].Range.Start.Line != 4 || locs[0].Range.Start.Col != 7 {
		t.Fatalf("definition %+v (text %q)", locs, text)
	}
	modtest.Call(t, a, "lsp.implementation", PosParams{Path: "a.php", At: api.Pos{Line: 2, Col: 2}}, &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 2 || locs[0].Range.Start.Col != 2 {
		t.Fatalf("implementation %+v", locs)
	}
	var toks []SemanticToken
	modtest.Call(t, a, "lsp.semanticTokens", api.PathParams{Path: "a.php"}, &toks)
	if len(toks) != 3 || toks[0].Type != "class" || toks[0].Modifiers[0] != "declaration" || toks[0].Range.Start.Line != 2 {
		t.Fatalf("tokens %+v", toks)
	}
	modtest.WantCode(t, a, "lsp.hover", PosParams{Path: "b.txt", At: api.Pos{Line: 1}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.hover", PosParams{Path: "other.php", At: api.Pos{Line: 1}}, api.CodeNotFound)
	modtest.WantCode(t, a, "lsp.hover", PosParams{Path: "a.php", At: api.Pos{Line: 99}}, api.CodeInvalidParams)

	// Close → didClose, diagnostics forgotten.
	modtest.Call(t, a, "doc.close", api.PathParams{Path: "a.php"}, nil)
	uri := protocol.URIFromPath(filepath.Join(dir, "a.php"))
	modtest.Eventually(t, "didClose", func() bool {
		cl := f.last().Snapshot().Closed
		return len(cl) == 1 && cl[0] == uri
	})
	modtest.Eventually(t, "diagnostics dropped", func() bool {
		var l []Diagnostics
		modtest.Call(t, a, "lsp.diagnostics", nil, &l)
		return len(l) == 0
	})

	modtest.Call(t, a, "lsp.stop", nil, &st)
	if st.Running {
		t.Fatal("still running")
	}
	modtest.Call(t, a, "lsp.stop", nil, &st)
}

func TestRestartAndModuleToggle(t *testing.T) {
	a, _, f, dir := setup(t)
	status := modtest.Record(t, a, api.TopicLSPStatus)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake", Server: "phpactor"}, nil)
	a.Bus().Publish(api.TopicInterpreterChanged, api.InterpreterChangedEvent{ID: "x"})
	status.Wait(t, api.TopicLSPStatus, func(e bus.Event) bool { s := e.Payload.(Status); return s.Running && s.Generation == 2 })
	waitSynced(t, a, f, dir, "a.php")
	a.Bus().Publish(api.TopicPHPVersionChanged, api.PHPVersionChangedEvent{Override: "8.1"})
	status.Wait(t, api.TopicLSPStatus, func(e bus.Event) bool { s := e.Payload.(Status); return s.Running && s.Generation == 3 })

	// The server dies: the module notices.
	_ = f.last().Close()
	status.Wait(t, api.TopicLSPStatus, func(e bus.Event) bool { s := e.Payload.(Status); return !s.Running && s.Error != "" })
	var st Status
	modtest.Call(t, a, "lsp.status", nil, &st)
	if st.Running {
		t.Fatal("dead server reported running")
	}

	// Disabling the module disables the namespace; enabling restores it.
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake"}, nil)
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "lsp.status", nil, api.CodeModuleDisabled)
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: ID}, nil)
	modtest.Call(t, a, "lsp.status", nil, &st)
	if st.Running {
		t.Fatal("server survived the module stop")
	}
}

func TestServerResolution(t *testing.T) {
	a, m, f, _ := setup(t)
	modtest.WantCode(t, a, "lsp.start", StartParams{Server: "nope"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.start", StartParams{Server: "phpactor"}, api.CodeUnavailable)
	modtest.WantCode(t, a, "lsp.start", StartParams{}, api.CodeUnavailable)
	f.fail = true
	modtest.WantCode(t, a, "lsp.start", StartParams{Command: "x"}, api.CodeUnavailable)
	f.fail = false
	if err := settings.Set(a.SettingsStore(), SettingCommand, "from-settings"); err != nil {
		t.Fatal(err)
	}
	var st Status
	modtest.Call(t, a, "lsp.start", nil, &st)
	if st.Command != "from-settings" {
		t.Fatalf("settings command %+v", st)
	}
	var list []LSPServerInfo
	modtest.Call(t, a, "lsp.servers", LSPServersParams{All: true}, &list)
	if len(list) != 0 {
		t.Fatalf("servers %+v", list)
	}
	modtest.Call(t, a, "lsp.known", nil, &list)
	if len(list) < 3 {
		t.Fatalf("known %+v", list)
	}
	if SeverityName(protocol.SeverityWarning) != "warning" || SeverityName(protocol.SeverityInformation) != "information" ||
		SeverityName(protocol.SeverityHint) != "hint" || SeverityName(0) != "error" {
		t.Fatal("severity names")
	}
	if paramLabel("foo(a)", protocol.ParameterLabel{Offsets: &[2]int{5, 1}}) != "" {
		t.Fatal("bad offsets")
	}
	if err := lspErr("x", lsp.ErrNotSupported); !strings.Contains(err.Error(), "not supported") {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestDetectedServerAndGapResync(t *testing.T) {
	f := &fakes{}
	m := New(Options{Connect: f.connect})
	bin := t.TempDir()
	modtest.WriteFile(t, filepath.Join(bin, "phpactor"), "#!/bin/sh\n")
	_ = osChmod(filepath.Join(bin, "phpactor"))
	a := modtest.NewApp(t, []module.Module{m}, func(o *api.Options) { o.LSP.PathEnv = bin })
	dir := modtest.Project(t, a, map[string]string{"a.php": src})
	var st Status
	modtest.Call(t, a, "lsp.start", nil, &st)
	if st.Server != "phpactor" || st.PHPVersion != "" {
		t.Fatalf("detected %+v", st)
	}
	modtest.Call(t, a, "lsp.stop", nil, nil)
	modtest.Call(t, a, "lsp.start", StartParams{Server: "phpactor"}, &st)
	if st.Server != "phpactor" {
		t.Fatalf("explicit %+v", st)
	}
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	waitSynced(t, a, f, dir, "a.php")
	// Change the buffer behind the module's back and report a gap.
	doc, _ := a.Document("a.php")
	snap := doc.Snapshot()
	m.edited(m.current(), api.DocEditEvent{File: snap.File, Language: languageID, Version: snap.Version + 5})
	waitSynced(t, a, f, dir, "a.php")
	// A late doc.opened for an already synced document changes nothing (the
	// ordered doc.edited events keep it current).
	m.syncMu.Lock()
	m.openLocked(m.current(), api.DocSnapshot{File: snap.File, Version: snap.Version + 10, Text: []byte("<?php\n")})
	m.syncMu.Unlock()
	waitSynced(t, a, f, dir, "a.php")
}
