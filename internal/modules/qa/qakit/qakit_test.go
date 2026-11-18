package qakit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// demo is a synthetic tool that formats and analyzes.
func demo() Definition {
	return Definition{
		ID: "qa-demo", Name: "Demo", Order: 5, Program: "demo", FormatProgram: "demofix", Package: "vendor/demo",
		Format:  &FormatSpec{Stdin: true, Args: func(c Call) []string { return []string{c.File} }},
		Analyze: &AnalyzeSpec{Args: func(c Call) []string { return c.Paths }, Parse: func([]byte) ([]Finding, []string, error) { return nil, nil, nil }},
	}
}

func TestSettingSpecs(t *testing.T) {
	p := New(demo())
	specs := p.SettingSpecs()
	if _, errs := module.CheckSpecs(p.ID(), specs); len(errs) > 0 {
		t.Fatal(errs)
	}
	keys := map[string]any{}
	for _, s := range specs {
		keys[strings.TrimPrefix(s.Key, "qa-demo.")] = s.Default
	}
	for _, k := range []string{KeyPath, KeyFormatPath, KeyConfig, KeyArgs, KeyUseInterpreter, KeyTimeout, KeySeverity, KeyReformat, KeyFormatOnSave, KeyInspectOnSave, KeyPaths} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if keys[KeyPath] != "vendor/bin/demo" || keys[KeyFormatPath] != "vendor/bin/demofix" || keys[KeyFormatOnSave] != false || keys[KeyUseInterpreter] != true {
		t.Fatalf("defaults %v", keys)
	}
	// A pure analyzer has no formatting settings and vice versa.
	d := demo()
	d.Format, d.FormatProgram = nil, ""
	for _, s := range New(d).SettingSpecs() {
		if strings.HasSuffix(s.Key, "."+KeyFormatOnSave) || strings.HasSuffix(s.Key, "."+KeyFormatPath) {
			t.Fatalf("analyzer has %s", s.Key)
		}
	}
	d = demo()
	d.Analyze = nil
	for _, s := range New(d).SettingSpecs() {
		if strings.HasSuffix(s.Key, "."+KeyInspectOnSave) || strings.HasSuffix(s.Key, "."+KeySeverity) {
			t.Fatalf("formatter has %s", s.Key)
		}
	}
	r := p.Recipe()
	if r.Validate() != nil || r.Manager != install.ManagerComposer || r.Package != "vendor/demo" || r.Interpreter != install.InterpreterProject {
		t.Fatalf("recipe %+v", r)
	}
	if p.Definition().ID != "qa-demo" || p.DependsOn() != nil || p.RegisterOption() == nil {
		t.Fatal("module basics")
	}
	d.Disabled = true
	if New(d).RegisterOption() == nil {
		t.Fatal("register option")
	}
}

func newApp(t *testing.T, d Definition) (*api.App, *Plugin) {
	t.Helper()
	p := New(d)
	a := modtest.NewApp(t, []module.Module{p}, nil)
	return a, p
}

func set(t *testing.T, a *api.App, key string, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: "qa-demo." + key, Scope: "project", Value: raw}, nil)
}

func TestResolve(t *testing.T) {
	a, p := newApp(t, demo())
	ctx := context.Background()
	if st := p.Status(ctx); st.Available || st.Reason == "" {
		t.Fatalf("no project %+v", st)
	}
	dir := modtest.Project(t, a, map[string]string{"vendor/bin/demo": "x", "tools/demofix": "x"})
	// The fixer is missing: not available.
	if st := p.Status(ctx); st.Available || !strings.Contains(st.Reason, "vendor/bin/demofix") || st.Where != "project interpreter" {
		t.Fatalf("missing fixer %+v", st)
	}
	set(t, a, KeyFormatPath, "tools/demofix")
	if st := p.Status(ctx); !st.Available || st.Command != "vendor/bin/demo" || !st.Reformat || st.FormatOnSave || st.InspectOnSave {
		t.Fatalf("found %+v", st)
	}
	if r := p.resolve(KeyFormatPath); r.Program != "tools/demofix" {
		t.Fatalf("relative in the interpreter %+v", r)
	}
	// Absolute inside the project: relative for the interpreter, absolute on the host.
	abs := filepath.Join(dir, "tools", "demofix")
	set(t, a, KeyFormatPath, abs)
	if r := p.resolve(KeyFormatPath); !r.Found || r.Program != "tools/demofix" {
		t.Fatalf("abs in project %+v", r)
	}
	set(t, a, KeyUseInterpreter, false)
	if r := p.resolve(KeyFormatPath); !r.Found || r.Program != abs {
		t.Fatalf("abs on host %+v", r)
	}
	if r := p.resolve(KeyPath); !r.Found || r.Program != filepath.Join(dir, "vendor", "bin", "demo") || p.Status(ctx).Where != "host" {
		t.Fatalf("relative on host %+v", r)
	}
	// Absolute outside the project: must exist on the host, or is taken
	// as a container path in the interpreter.
	outside := filepath.Join(t.TempDir(), "nope")
	set(t, a, KeyFormatPath, outside)
	if r := p.resolve(KeyFormatPath); r.Found {
		t.Fatalf("missing on host %+v", r)
	}
	set(t, a, KeyUseInterpreter, true)
	if r := p.resolve(KeyFormatPath); !r.Found {
		t.Fatalf("container path %+v", r)
	}
	// A bare name: PATH of the interpreter (unchecked) or of the host.
	set(t, a, KeyFormatPath, "demofix-not-installed")
	if r := p.resolve(KeyFormatPath); !r.Found {
		t.Fatalf("bare in interpreter %+v", r)
	}
	set(t, a, KeyUseInterpreter, false)
	if r := p.resolve(KeyFormatPath); r.Found || !strings.Contains(r.Reason, "PATH") {
		t.Fatalf("bare on host %+v", r)
	}
	set(t, a, KeyTimeout, 7)
	if p.timeout() != 7*time.Second {
		t.Fatalf("timeout %v", p.timeout())
	}
	set(t, a, KeyConfig, filepath.Join(dir, "cfg", "x.xml"))
	if c := p.config(); c != "cfg/x.xml" {
		t.Fatalf("config %q", c)
	}
	set(t, a, KeyArgs, []string{" -v ", ""})
	if l := p.listSetting(KeyArgs); len(l) != 1 || l[0] != "-v" {
		t.Fatalf("args %q", l)
	}
}

func TestHelpers(t *testing.T) {
	if string(JSONPayload([]byte("Warning: x\nmore\n{\"a\":1}\n"))) != `{"a":1}` || string(JSONPayload([]byte(" [1] "))) != "[1]" || string(JSONPayload([]byte("no json"))) != "no json" {
		t.Fatal("JSONPayload")
	}
	for in, want := range map[string]string{"ERROR": SevError, "Warning": SevWarning, "info": SevInfo, "notice": SevInfo, "ignore": SevHint, "??": SevWarning} {
		if Severity(in) != want {
			t.Errorf("Severity(%q)", in)
		}
	}
	if !IsPHP("a/B.PHP") || IsPHP("a.js") {
		t.Fatal("IsPHP")
	}
	if !formatTrigger(autosave.TriggerManual) || formatTrigger(autosave.TriggerIdle) || formatTrigger(autosave.TriggerFocusLost) {
		t.Fatal("formatTrigger")
	}
	root := filepath.Join(t.TempDir(), "p")
	m := compose.Mappings{}.Set(root, "/app")
	if HostPath(root, m, "/app/src/A.php") != filepath.Join(root, "src", "A.php") || HostPath(root, m, "./src/A.php") != filepath.Join(root, "src", "A.php") ||
		HostPath(root, m, "") != "" || HostPath("", m, "rel.php") != "" {
		t.Fatal("HostPath")
	}
	if !IsCheckstyle([]byte("  <checkstyle>")) || IsCheckstyle([]byte("{}")) {
		t.Fatal("IsCheckstyle")
	}
	if _, err := ParseCheckstyle(nil); err == nil {
		t.Fatal("empty checkstyle")
	}
	err := toolError("Demo", result{Code: 3, Stdout: []byte("out")})
	if !strings.Contains(err.Error(), "exit code 3: out") || !strings.Contains(toolError("Demo", result{Code: 2}).Error(), "exit code 2") {
		t.Fatal(err)
	}
	if tail(nil) != "" || !strings.Contains(tail([]byte(strings.Repeat("x", 2000))), "…") {
		t.Fatal("tail")
	}
}

func TestMarkerWriter(t *testing.T) {
	w := &markerWriter{marker: "__PID__"}
	_, _ = w.Write([]byte("__PI"))
	_, _ = w.Write([]byte("D__ 42\nreal error\n"))
	if w.PID() != "42" || string(w.Bytes()) != "real error\n" {
		t.Fatalf("pid %q out %q", w.PID(), w.Bytes())
	}
	w = &markerWriter{marker: "__PID__"}
	_, _ = w.Write([]byte("plain\nmore"))
	if w.PID() != "" || string(w.Bytes()) != "plain\nmore" {
		t.Fatalf("no marker %q", w.Bytes())
	}
	w = &markerWriter{marker: "__PID__"}
	_, _ = w.Write([]byte("partial"))
	if string(w.Bytes()) != "partial" {
		t.Fatal("unterminated head")
	}
	w = &markerWriter{}
	_, _ = w.Write([]byte("x"))
	if string(w.Bytes()) != "x" {
		t.Fatal("no marker configured")
	}
}

// helperEnv makes the test binary act as a tool process.
const helperEnv = "QAKIT_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "cat":
		os.Stderr.WriteString("__PID__ 77\nwarn\n")
		b := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(b)
			os.Stdout.Write(b[:n])
			if err != nil {
				break
			}
		}
		os.Exit(1)
	case "rewrite":
		f := os.Args[len(os.Args)-1]
		b, _ := os.ReadFile(f)
		_ = os.WriteFile(f, []byte(strings.ReplaceAll(string(b), "x", "y")), 0o644)
		os.Exit(0)
	case "sleep":
		os.Stderr.WriteString("__PID__ 78\n")
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func helperLaunch(t *testing.T, mode string, stop func(context.Context, string) error) *run.Launch {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return &run.Launch{Cmd: cmd, Display: "helper " + mode, PIDMarker: "__PID__ ", StopRemote: stop}
}

func TestRunLaunch(t *testing.T) {
	res, err := runLaunch(context.Background(), helperLaunch(t, "cat", nil), []byte("hello"), time.Minute)
	if err != nil || res.Code != 1 || string(res.Stdout) != "hello" || string(res.Stderr) != "warn\n" {
		t.Fatalf("%v %+v %q", err, res, res.Stderr)
	}
	// Timeout: local tree killed, process inside the "container" stopped
	// with the PID from the marker.
	var stopped string
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = runLaunch(ctx, helperLaunch(t, "sleep", func(_ context.Context, pid string) error { stopped = pid; return nil }), nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "timeout") || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %v", err)
	}
	if stopped != "78" {
		t.Fatalf("remote stop pid %q", stopped)
	}
	// Cancelled (not timed out).
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel2() }()
	if _, err := runLaunch(ctx2, helperLaunch(t, "sleep", nil), nil, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	l := &run.Launch{Cmd: exec.Command(filepath.Join(t.TempDir(), "missing")), Display: "missing"}
	if _, err := runLaunch(context.Background(), l, nil, time.Minute); err == nil {
		t.Fatal("missing program started")
	}
}

// Through the plugin: stdin formatting and analysis on the host with the
// helper as tool.
func TestPluginExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper path handling")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := demo()
	d.Format.OK = func(code int) bool { return code <= 1 }
	d.Analyze.OK = func(code int) bool { return code <= 1 }
	d.Analyze.Parse = func(out []byte) ([]Finding, []string, error) {
		return []Finding{{File: "a.php", Line: 1, Message: string(out)}}, []string{"note"}, nil
	}
	a, p := newApp(t, d)
	dir := modtest.Project(t, a, map[string]string{"a.php": "<?php\n"})
	set(t, a, KeyUseInterpreter, false)
	set(t, a, KeyPath, self)
	set(t, a, KeyFormatPath, self)
	t.Setenv(helperEnv, "cat")
	out, err := p.format(context.Background(), api.QAFormatRequest{File: filepath.Join(dir, "a.php"), Content: []byte("<?php // x\n")})
	if err != nil || string(out) != "<?php // x\n" {
		t.Fatalf("format %q %v", out, err)
	}
	if _, err := p.format(context.Background(), api.QAFormatRequest{File: filepath.Join(t.TempDir(), "x.php")}); err == nil {
		t.Fatal("outside the project")
	}
	rep, err := p.analyze(context.Background(), api.QAAnalyzeRequest{Paths: []string{filepath.Join(dir, "a.php")}})
	if err != nil || len(rep.Files) != 1 || rep.Files[0].File != filepath.Join(dir, "a.php") || rep.Messages[0] != "note" {
		t.Fatalf("analyze %+v %v", rep, err)
	}
	if _, err := p.analyze(context.Background(), api.QAAnalyzeRequest{Paths: []string{filepath.Join(t.TempDir(), "x.php")}}); err == nil {
		t.Fatal("analyze outside")
	}
	// Exit code outside OK: an error with the output.
	d.Analyze.OK = func(code int) bool { return code == 0 }
	if _, err := p.analyze(context.Background(), api.QAAnalyzeRequest{}); err == nil || !strings.Contains(err.Error(), "exit code 1") {
		t.Fatalf("bad exit: %v", err)
	}
	// Disabled plugin: nothing to call any more.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: "qa-demo"}, nil)
	var list []api.QAToolInfo
	modtest.Call(t, a, "qa.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("list %+v", list)
	}
}

// Save hooks: temporary-copy formatting on explicit saves, inspection
// after saves.
func TestSaveHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper path handling")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := demo()
	d.FormatProgram = ""
	d.Format = &FormatSpec{Args: func(c Call) []string { return []string{c.File} }}
	d.Analyze.Parse = func([]byte) ([]Finding, []string, error) {
		return []Finding{{File: "a.php", Line: 1, Col: 1, Message: "m", Severity: SevInfo}}, nil, nil
	}
	a, p := newApp(t, d)
	dir := modtest.Project(t, a, map[string]string{"a.php": "<?php // x\n"})
	set(t, a, KeyUseInterpreter, false)
	set(t, a, KeyPath, self)
	set(t, a, KeyFormatOnSave, true)
	set(t, a, KeyInspectOnSave, true)
	t.Setenv(helperEnv, "rewrite")
	rec := modtest.Record(t, a, "qa.*")
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "a.php", "at": map[string]int{"offset": 0}, "text": "x"}, nil)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != "y<?php // y\n" {
		t.Fatalf("saved %q", b)
	}
	rec.Wait(t, api.TopicQADiagnostics, func(e bus.Event) bool {
		return len(e.Payload.(api.QADiagnosticsEvent).Diagnostics) == 1
	})
	// Automatic saves are not formatted.
	ev, err := p.beforeSave(context.Background(), autosave.SaveEvent{Path: filepath.Join(dir, "a.php"), Content: []byte("x"), Trigger: autosave.TriggerIdle})
	if err != nil || string(ev.Content) != "x" {
		t.Fatalf("idle %q %v", ev.Content, err)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	p.onSaved(bus.Event{Payload: api.FileSavedEvent{File: filepath.Join(dir, "a.php")}})
	p.inspectSaved(filepath.Join(dir, "a.php"))
}
