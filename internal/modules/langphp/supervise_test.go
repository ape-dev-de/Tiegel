package langphp

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// pluginApp has fake phpactor and phpantom executables on PATH (the fake
// connector serves both in-process).
func pluginApp(t *testing.T, opts Options, bins ...string) (*api.App, *Module, *fakes, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix executable bits")
	}
	f := &fakes{}
	opts.Connect = f.connect
	opts.Supervisor = fastSupervisor
	m := New(opts)
	bin := t.TempDir()
	for _, b := range bins {
		modtest.WriteFile(t, filepath.Join(bin, b), "#!/bin/sh\n")
		_ = osChmod(filepath.Join(bin, b))
	}
	a := modtest.NewApp(t, withPlugins(m), func(o *api.Options) { o.LSP.PathEnv = bin })
	dir := modtest.Project(t, a, map[string]string{"a.php": src})
	return a, m, f, dir, bin
}

func lastCommand(f *fakes) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfgs[len(f.cfgs)-1].Command
}

func waitState(t *testing.T, a *api.App, what string, pred func(Status) bool) Status {
	t.Helper()
	var st Status
	modtest.Eventually(t, what, func() bool {
		modtest.Call(t, a, "lsp.status", nil, &st)
		return pred(st)
	})
	return st
}

func TestSelectSwitchAndDisable(t *testing.T) {
	a, m, f, dir, bin := pluginApp(t, Options{}, "phpactor", "phpantom_lsp")
	states := modtest.Record(t, a, api.TopicLSPState)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)

	var st Status
	modtest.Call(t, a, "lsp.start", nil, &st)
	if !st.Running || st.Server != "phpactor" || st.Selected != "auto" || st.Plugin != "lsp-phpactor" || lastCommand(f) != filepath.Join(bin, "phpactor") {
		t.Fatalf("auto %+v", st)
	}
	states.Wait(t, api.TopicLSPState, func(e bus.Event) bool { return e.Payload.(Status).State == supervisor.StateRunning })

	// Switch for the project without restarting anything else.
	modtest.Call(t, a, "lsp.select", SelectParams{Server: "phpantom"}, &st)
	if !st.Running || st.Server != "phpantom" || st.Selected != "phpantom" || lastCommand(f) != filepath.Join(bin, "phpantom_lsp") {
		t.Fatalf("switch %+v", st)
	}
	if v, _, _ := settings.Lookup[string](a.ProjectSettings(), SettingServer); v != "phpantom" {
		t.Fatal("selection not stored for the project")
	}
	waitSynced(t, a, f, dir, "a.php") // documents re-opened on the new server
	if f.srvs[0].Snapshot().ShutdownCalls != 1 {
		t.Fatal("old server not shut down")
	}

	// A setting change (e.g. from the settings dialog) switches too.
	if err := settings.Set(a.ProjectSettings(), SettingServer, "phpactor"); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, "switch via settings", func(s Status) bool { return s.Running && s.Server == "phpactor" })

	// Disable: no process, index fallback, reason visible.
	n := f.count()
	modtest.Call(t, a, "lsp.disable", nil, &st)
	if st.State != supervisor.StateOff || st.Running || !strings.Contains(st.Reason, "switched off") {
		t.Fatalf("disabled %+v", st)
	}
	if m.current() != nil {
		t.Fatal("server kept after disable")
	}
	modtest.WantCode(t, a, "lsp.hover", PosParams{Path: "a.php", At: api.Pos{Line: 2}}, api.CodeUnavailable)
	modtest.WantCode(t, a, "lsp.start", nil, api.CodeUnavailable)
	if f.count() != n {
		t.Fatal("a server was started while switched off")
	}
	// Re-enable globally; the project value still says none.
	modtest.Call(t, a, "lsp.select", SelectParams{Server: "auto", Scope: "global"}, &st)
	if st.State != supervisor.StateOff {
		t.Fatalf("project override lost %+v", st)
	}
	modtest.Call(t, a, "lsp.select", SelectParams{Server: "auto"}, &st)
	if !st.Running {
		t.Fatalf("re-enabled %+v", st)
	}
	modtest.WantCode(t, a, "lsp.select", SelectParams{Server: "emacs"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.select", SelectParams{Server: "auto", Scope: "moon"}, api.CodeInvalidParams)

	// Project close stops the server.
	modtest.Call(t, a, "project.close", nil, nil)
	waitState(t, a, "off after close", func(s Status) bool { return s.State == supervisor.StateOff })
	modtest.WantCode(t, a, "lsp.select", SelectParams{Server: "auto", Scope: "project"}, api.CodeNoProject)
}

func TestNotFoundDoctorAndInstall(t *testing.T) {
	a, _, f, _, bin := pluginApp(t, Options{})
	var st Status
	modtest.WantCode(t, a, "lsp.start", nil, api.CodeUnavailable)
	modtest.Call(t, a, "lsp.status", nil, &st)
	if st.State != supervisor.StateNotFound || !strings.Contains(st.Reason, "no PHP language server found") || len(st.Searched) == 0 || st.Plugin != "lsp-phpactor" {
		t.Fatalf("not found %+v", st)
	}
	var doc Doctor
	modtest.Call(t, a, "lsp.doctor", nil, &doc)
	if doc.Selected != "auto" || doc.Source != "default" || doc.InstallPlugin != "lsp-phpactor" || len(doc.Servers) != 3 || doc.Searched[0] != filepath.Join(a.ProjectRoot(), "vendor", "bin") || doc.Resolved != "" {
		t.Fatalf("doctor %+v", doc)
	}
	// Explicit server, not installed.
	modtest.Call(t, a, "lsp.select", SelectParams{Server: "phpantom"}, &st)
	if st.State != supervisor.StateNotFound || !strings.Contains(st.Reason, "PHPantom is not installed") {
		t.Fatalf("phpantom %+v", st)
	}
	// A configured but broken path is explained.
	if err := settings.Set(a.ProjectSettings(), "lsp-phpantom.path", filepath.Join(bin, "nope")); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, "configured path", func(s Status) bool { return strings.Contains(s.Reason, "lsp-phpantom.path") })
	if err := a.ProjectSettings().Delete("lsp-phpantom.path"); err != nil {
		t.Fatal(err)
	}
	// The binary appears (e.g. installed); install.done re-detects.
	modtest.WriteFile(t, filepath.Join(bin, "phpantom_lsp"), "#!/bin/sh\n")
	_ = osChmod(filepath.Join(bin, "phpantom_lsp"))
	a.Bus().Publish(api.TopicInstallDone, install.Result{Plugin: "lsp-phpantom", State: install.StateSucceeded})
	st = waitState(t, a, "running after install", func(s Status) bool { return s.Running })
	if st.Server != "phpantom" || f.count() != 1 {
		t.Fatalf("after install %+v", st)
	}
	modtest.Call(t, a, "lsp.doctor", nil, &doc)
	if doc.Resolved != "phpantom" || doc.Path != filepath.Join(bin, "phpantom_lsp") {
		t.Fatalf("doctor resolved %+v", doc)
	}
	// Disabling the selected plugin: not found with the reason.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: "lsp-phpantom"}, nil)
	st = waitState(t, a, "plugin disabled", func(s Status) bool { return s.State == supervisor.StateNotFound })
	if !strings.Contains(st.Reason, "plugin lsp-phpantom is disabled") {
		t.Fatalf("plugin off %+v", st)
	}
	// All plugins off: auto finds no plugin.
	modtest.Call(t, a, "lsp.select", SelectParams{Server: "auto"}, nil)
	for _, id := range []string{"lsp-phpactor", "lsp-intelephense"} {
		modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: id}, nil)
	}
	waitState(t, a, "no plugin", func(s Status) bool { return strings.Contains(s.Reason, "no PHP language server plugin") })
}

func TestCrashLoopRestartLogTraceAndAutoStart(t *testing.T) {
	a, _, f, dir, _ := pluginApp(t, Options{AutoStart: true}, "phpactor")
	// AutoStart: opening a PHP file starts the server.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	waitState(t, a, "auto start", func(s Status) bool { return s.Running })
	waitSynced(t, a, f, dir, "a.php")
	var tr TraceResult
	on := true
	modtest.Call(t, a, "lsp.trace", TraceParams{On: &on}, &tr)
	if !tr.Trace {
		t.Fatal("trace not on")
	}
	// Crash loop: every restart fails → stopped with reason, no endless loop.
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	_ = f.last().Close()
	st := waitState(t, a, "crash loop stop", func(s Status) bool { return s.State == supervisor.StateStopped })
	if !strings.Contains(st.Reason, "times in a row") || st.Crashes != 3 {
		t.Fatalf("loop %+v", st)
	}
	// A setting change does not revive a crash-looped server automatically…
	if err := settings.Set(a.Settings(), SettingRequestTimeout, 10); err != nil {
		t.Fatal(err)
	}
	// …lsp.restart does.
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	modtest.Call(t, a, "lsp.restart", nil, &st)
	if !st.Running || st.Crashes != 0 {
		t.Fatalf("restart %+v", st)
	}
	waitSynced(t, a, f, dir, "a.php")
	var log []supervisor.Entry
	modtest.Call(t, a, "lsp.log", LogParams{}, &log)
	text := ""
	for _, e := range log {
		text += e.Kind + " " + e.Text + "\n"
	}
	for _, w := range []string{"giving up", "send ", `"method":"textDocument/didOpen"`} {
		if !strings.Contains(text, w) {
			t.Fatalf("log lacks %q:\n%s", w, text)
		}
	}
	last := log[len(log)-1].Seq
	modtest.Call(t, a, "lsp.log", LogParams{After: last - 1, Limit: 1}, &log)
	if len(log) != 1 || log[0].Seq < last {
		t.Fatalf("after %+v", log)
	}
	modtest.Call(t, a, "lsp.trace", TraceParams{}, &tr)
	if !tr.Trace {
		t.Fatal("trace read")
	}
	// Diagnostics of the restarted server arrive.
	uri := protocol.URIFromPath(filepath.Join(dir, "a.php"))
	if _, ok := f.last().Snapshot().Docs[uri]; !ok {
		t.Fatal("not re-synced")
	}
	var list []LSPServerInfo
	modtest.Call(t, a, "lsp.known", nil, &list)
	if len(list) != 3 || list[0].Found || !list[0].AutoSelect || list[2].AutoSelect {
		t.Fatalf("known %+v", list)
	}
}

func TestSetPath(t *testing.T) {
	a, _, f, _, _ := pluginApp(t, Options{})
	other := t.TempDir()
	exe := filepath.Join(other, "my-phpactor")
	modtest.WriteFile(t, exe, "#!/bin/sh\n")
	_ = osChmod(exe)
	var st Status
	modtest.Call(t, a, "lsp.setPath", SetPathParams{Server: "phpactor", Path: exe}, &st)
	if !st.Running || lastCommand(f) != exe || st.Selected != "phpactor" {
		t.Fatalf("set path %+v", st)
	}
	if v, _, _ := settings.Lookup[string](a.ProjectSettings(), "lsp-phpactor.path"); v != exe {
		t.Fatalf("stored %q", v)
	}
	modtest.WantCode(t, a, "lsp.setPath", SetPathParams{Server: "phpactor", Path: filepath.Join(other, "nope")}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.setPath", SetPathParams{Server: "emacs", Path: exe}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.setPath", SetPathParams{Server: "phpactor", Path: exe, Scope: "moon"}, api.CodeInvalidParams)
	modtest.Call(t, a, "lsp.setPath", SetPathParams{Server: "phpactor"}, &st)
	if st.State != supervisor.StateNotFound {
		t.Fatalf("cleared path %+v", st)
	}
}

func TestSettingSpecs(t *testing.T) {
	m := New(Options{})
	for _, s := range m.SettingSpecs() {
		if err := s.Validate(); err != nil || !strings.HasPrefix(s.Key, ID+".") {
			t.Fatalf("%+v: %v", s, err)
		}
	}
}
