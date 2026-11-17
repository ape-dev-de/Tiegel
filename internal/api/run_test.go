package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// The test binary plays the processes of the run tests (portable, no
// shell scripts): with runHelperEnv set it runs runHelper and exits.
const runHelperEnv = "TIEGEL_API_RUN_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(runHelperEnv); mode != "" {
		os.Exit(runHelper(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func runHelper(mode string, args []string) int {
	switch mode {
	case "echo":
		wd, _ := os.Getwd()
		fmt.Println("wd=" + wd)
		for _, a := range args {
			fmt.Println(a)
		}
		if os.Getenv("FAIL") != "" {
			fmt.Fprintln(os.Stderr, "failing")
			return 4
		}
		return 0
	case "sleep":
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
	}
	return 0
}

func selfExe(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

// helperInterp is an interpreter of a foreign kind whose php is the
// test binary in echo mode.
type helperInterp struct{ self string }

func (helperInterp) ID() string                                    { return "fake:php" }
func (helperInterp) Label() string                                 { return "Fake PHP" }
func (helperInterp) Kind() interp.Kind                             { return "fake" }
func (helperInterp) Version(context.Context) (string, error)       { return "8.3.0", nil }
func (helperInterp) Modules(context.Context) ([]string, error)     { return nil, nil }
func (helperInterp) Ini(context.Context) (interp.IniValues, error) { return nil, nil }
func (h helperInterp) Command(_ context.Context, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(h.self, args...)
	cmd.Env = append(os.Environ(), runHelperEnv+"=echo")
	return cmd, nil
}

// runEvents records run.* events.
type runEvents struct {
	mu   sync.Mutex
	list []string
}

func (r *runEvents) add(s string) { r.mu.Lock(); r.list = append(r.list, s); r.mu.Unlock() }
func (r *runEvents) has(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.list, func(s string) bool { return strings.HasPrefix(s, prefix) })
}

func waitRun(t *testing.T, a *App, id string) run.Info {
	t.Helper()
	var info run.Info
	call(t, a, "run.wait", RunIDParams{RunID: id}, &info)
	return info
}

func outputText(t *testing.T, a *App, id string) string {
	t.Helper()
	var pg run.Page
	call(t, a, "run.output", RunOutputParams{RunID: id}, &pg)
	var b strings.Builder
	for _, l := range pg.Lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func waitOutput(t *testing.T, a *App, id, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(outputText(t, a, id), text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s: no output %q", id, text)
}

func TestRunService(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	ev := &runEvents{}
	unsub := a.Bus().Subscribe("run.*", func(e bus.Event) { ev.add(e.Topic) })
	defer unsub()

	// Ad-hoc command without a project.
	var info run.Info
	call(t, a, "run.start", RunStartParams{Spec: &run.Spec{Program: self, Args: []string{"one", "two"}, Env: []string{runHelperEnv + "=echo", "FAIL=1"}}}, &info)
	if info.State != run.StateRunning || info.Name == "" || info.Where != run.InterpreterHost {
		t.Fatalf("started %+v", info)
	}
	done := waitRun(t, a, info.ID)
	if done.State != run.StateExited || done.ExitCode != 4 {
		t.Fatalf("done %+v", done)
	}
	// stdout and stderr are separate pipes read concurrently: their lines
	// interleave in any order. Only the order within stdout and the final
	// exit line are guaranteed.
	out := outputText(t, a, info.ID)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	one, two := slices.Index(lines, "one"), slices.Index(lines, "two")
	if one < 0 || two < one || !slices.Contains(lines, "failing") || !strings.Contains(lines[len(lines)-1], "exit code 4") {
		t.Fatalf("output %q", out)
	}
	var list []run.Info
	call(t, a, "run.list", RunListParams{}, &list)
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	call(t, a, "run.list", RunListParams{Active: true}, &list)
	if len(list) != 0 {
		t.Fatalf("active %+v", list)
	}
	call(t, a, "run.remove", RunIDParams{RunID: info.ID}, nil)
	wantCode(t, a, "run.remove", RunIDParams{RunID: info.ID}, CodeNotFound)
	wantCode(t, a, "run.output", RunOutputParams{RunID: "nope"}, CodeNotFound)
	wantCode(t, a, "run.stop", RunIDParams{RunID: "nope"}, CodeNotFound)
	wantCode(t, a, "run.rerun", RunIDParams{RunID: "nope"}, CodeNotFound)
	wantCode(t, a, "run.wait", RunIDParams{RunID: "nope"}, CodeNotFound)
	wantCode(t, a, "run.start", RunStartParams{}, CodeInvalidParams)
	wantCode(t, a, "run.start", RunStartParams{File: "a.php", Config: "x"}, CodeInvalidParams)
	wantCode(t, a, "run.start", RunStartParams{Spec: &run.Spec{}}, CodeInvalidParams)
	wantCode(t, a, "run.start", RunStartParams{Spec: &run.Spec{Program: "tiegel-no-such-program"}}, CodeUnavailable)
	wantCode(t, a, "run.start", RunStartParams{Config: "x"}, CodeNoProject)
	wantCode(t, a, "run.start", RunStartParams{File: "a.php"}, CodeNoProject)
	wantCode(t, a, "run.configs.list", nil, CodeNoProject)
	// A specific interpreter without php-runtime is unavailable.
	wantCode(t, a, "run.start", RunStartParams{Spec: &run.Spec{PHP: true, Interpreter: "local:/x"}}, CodeUnavailable)
	// The bus delivers asynchronously (and run.exited is published after
	// run.wait returned): wait until the events arrived.
	topics := []string{TopicRunStarted, TopicRunOutput, TopicRunExited, TopicRunRemoved}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if !slices.ContainsFunc(topics, func(s string) bool { return !ev.has(s) }) {
			break
		}
	}
	for _, topic := range topics {
		if !ev.has(topic) {
			ev.mu.Lock()
			t.Errorf("no %s event (%v)", topic, ev.list)
			ev.mu.Unlock()
		}
	}
}

func TestRunConfigsAndFiles(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bin", "x.php"), "<?php echo 1;")
	writeFile(t, filepath.Join(root, "composer.json"), `{"scripts":{"test":"phpunit"}}`)
	call(t, a, "project.open", PathParams{Path: root}, nil)
	root = a.ProjectRoot()

	var types []RunConfigTypeInfo
	call(t, a, "run.configTypes", nil, &types)
	if len(types) != 3 || types[0].ID != run.TypePHPScript || !slices.Equal(types[2].Suggestions["script"], []string{"test"}) {
		t.Fatalf("types %+v", types)
	}
	// A plugin type with the same ID as a core type loses.
	HookRunConfigTypes.On(a.Hooks(), 0, func(_ context.Context, l []run.ConfigType) ([]run.ConfigType, error) {
		return append(slices.Clone(l), run.ConfigType{ID: run.TypeCommand, Label: "Dup", Build: func(context.Context, run.Config, run.BuildEnv) (run.Spec, error) {
			return run.Spec{}, errors.New("dup")
		}}, run.ConfigType{ID: "broken"}), nil
	})
	call(t, a, "run.configTypes", nil, &types)
	if len(types) != 3 || types[1].Label != "Command" {
		t.Fatalf("types with plugin %+v", types)
	}

	wantCode(t, a, "run.configs.save", run.Config{Name: "x", Type: "nope"}, CodeInvalidParams)
	wantCode(t, a, "run.configs.save", run.Config{Name: "x", Type: run.TypeCommand}, CodeInvalidParams)
	var saved run.Config
	cmdline := run.QuoteWords([]string{self, "hello world"})
	call(t, a, "run.configs.save", run.Config{Name: "Echo", Type: run.TypeCommand, Values: map[string]any{
		"command": cmdline, "env": []string{runHelperEnv + "=echo"}, "workingDir": "bin"}}, &saved)
	if saved.ID == "" || saved.Type != run.TypeCommand {
		t.Fatalf("saved %+v", saved)
	}
	wantCode(t, a, "run.configs.save", run.Config{Name: "echo", Type: run.TypeCommand, Values: map[string]any{"command": "x"}}, CodeConflict)
	var other run.Config
	call(t, a, "run.configs.save", run.Config{Name: "Other", Type: run.TypePHPScript, Values: map[string]any{"file": "bin/x.php"}}, &other)
	// An unknown type stays stored but unavailable.
	store := a.ProjectSettings()
	stored, _, _ := settings.Lookup[[]run.Config](store, run.KeyConfigurations)
	if err := settings.Set(store, run.KeyConfigurations, append(stored, run.Config{ID: "gone", Name: "Gone", Type: "phpunit"})); err != nil {
		t.Fatal(err)
	}
	var cfgs []RunConfigInfo
	call(t, a, "run.configs.list", nil, &cfgs)
	if len(cfgs) != 3 || !cfgs[0].Selected || !cfgs[0].Available || cfgs[0].Display == "" || cfgs[2].Available || cfgs[2].Error == "" {
		t.Fatalf("configs %+v", cfgs)
	}
	wantCode(t, a, "run.start", RunStartParams{Config: "Gone"}, CodeModuleDisabled)
	wantCode(t, a, "run.start", RunStartParams{Config: "missing"}, CodeNotFound)

	// Start by name; the working directory comes from the configuration.
	var info run.Info
	call(t, a, "run.start", RunStartParams{Config: "echo"}, &info)
	waitRun(t, a, info.ID)
	if out := outputText(t, a, info.ID); !strings.Contains(out, "hello world") || !strings.Contains(out, "wd="+filepath.Join(root, "bin")) {
		t.Fatalf("config output %q", out)
	}
	if info.Config != saved.ID || info.Name != "Echo" {
		t.Fatalf("config info %+v", info)
	}
	var sel RunConfigInfo
	call(t, a, "run.configs.select", RunConfigIDParams{ID: other.ID}, &sel)
	if !sel.Selected || sel.ID != other.ID {
		t.Fatalf("select %+v", sel)
	}
	wantCode(t, a, "run.configs.select", RunConfigIDParams{ID: "nope"}, CodeNotFound)
	call(t, a, "run.configs.delete", RunConfigIDParams{ID: "Other"}, nil)
	if settings.Get(store, run.KeySelected, "x") != "x" {
		t.Fatal("selection of a deleted configuration kept")
	}
	wantCode(t, a, "run.configs.delete", RunConfigIDParams{ID: "Other"}, CodeNotFound)

	// Files run with the project interpreter (php-runtime's hook).
	HookRunInterpreter.On(a.Hooks(), 0, func(_ context.Context, q RunInterpreterQuery) (RunInterpreterQuery, error) {
		if q.ID == "broken" {
			q.Err = "gone"
			return q, nil
		}
		q.Interpreter = helperInterp{self: self}
		q.Mappings = []MappingInfo{{Host: root, Remote: "/app"}}
		return q, nil
	})
	call(t, a, "run.start", RunStartParams{File: "bin/x.php", Args: []string{"arg"}}, &info)
	waitRun(t, a, info.ID)
	if out := outputText(t, a, info.ID); !strings.Contains(out, "bin/x.php\narg\n") || info.Where != "Fake PHP" || info.Name != "x.php" {
		t.Fatalf("file output %q %+v", out, info)
	}
	// Container paths in the output link to host files.
	call(t, a, "run.start", RunStartParams{Spec: &run.Spec{PHP: true, Args: []string{"in /app/bin/x.php:3"}}}, &info)
	waitRun(t, a, info.ID)
	var pg run.Page
	call(t, a, "run.output", RunOutputParams{RunID: info.ID}, &pg)
	found := false
	for _, l := range pg.Lines {
		for _, lk := range l.Links {
			found = found || lk.File == filepath.Join(root, "bin", "x.php") && lk.Line == 3
		}
	}
	if !found {
		t.Fatalf("no link in %+v", pg.Lines)
	}
	wantCode(t, a, "run.start", RunStartParams{Spec: &run.Spec{PHP: true}, Interpreter: "broken"}, CodeUnavailable)
}

func TestRunGuardStopRerunAndClose(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.php"), "<?php\n")
	call(t, a, "project.open", PathParams{Path: root}, nil)
	root = a.ProjectRoot()
	sleep := &run.Spec{Name: "sleeper", Program: self, Env: []string{runHelperEnv + "=sleep"}}

	// Autosave before run: a modified document is saved.
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 1}, Text: "echo 1;\n"}, nil)
	var info run.Info
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &info)
	if b, _ := os.ReadFile(filepath.Join(root, "a.php")); !strings.Contains(string(b), "echo 1;") {
		t.Fatalf("not saved before run: %q", b)
	}
	waitOutput(t, a, info.ID, "ready")
	var check RunCloseCheck
	call(t, a, "run.closeCheck", nil, &check)
	if check.Policy != run.CloseAsk || len(check.Active) != 1 {
		t.Fatalf("close check %+v", check)
	}
	// Rerun stops the active run and replaces it.
	var again run.Info
	call(t, a, "run.rerun", RunIDParams{RunID: info.ID}, &again)
	if again.Replaces != info.ID {
		t.Fatalf("rerun %+v", again)
	}
	waitOutput(t, a, again.ID, "ready")
	var stopped run.Info
	call(t, a, "run.stop", RunIDParams{RunID: again.ID}, &stopped)
	if waitRun(t, a, again.ID).State != run.StateStopped {
		t.Fatal("not stopped")
	}
	wantCode(t, a, "run.remove", RunIDParams{RunID: "nope"}, CodeNotFound)

	// stopAll stops the project's runs.
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &info)
	waitOutput(t, a, info.ID, "ready")
	var ids []string
	call(t, a, "run.stopAll", nil, &ids)
	if len(ids) != 1 || waitRun(t, a, info.ID).State != run.StateStopped {
		t.Fatalf("stop all %v", ids)
	}

	// A guard vetoes.
	remove := HookRunBefore.On(a.Hooks(), 0, func(context.Context, RunRequest) error { return errors.New("lint failed") })
	wantCode(t, a, "run.start", RunStartParams{Spec: sleep}, CodeFailed)
	wantCode(t, a, "run.rerun", RunIDParams{RunID: info.ID}, CodeFailed)
	remove()

	// terminate: closing the project stops its runs.
	if err := settings.Set(a.Settings(), run.KeyConfirmClose, run.CloseTerminate); err != nil {
		t.Fatal(err)
	}
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &info)
	waitOutput(t, a, info.ID, "ready")
	call(t, a, "project.close", nil, nil)
	if waitRun(t, a, info.ID).State != run.StateStopped {
		t.Fatal("not stopped on project close")
	}

	// keep: the App leaves them running on close; ask/terminate stop.
	if err := settings.Set(a.Settings(), run.KeyConfirmClose, "bogus"); err != nil {
		t.Fatal(err)
	}
	if a.runClosePolicy() != run.CloseAsk {
		t.Fatal("invalid policy not ask")
	}
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &info)
	waitOutput(t, a, info.ID, "ready")
	a.closeRuns()
	if waitRun(t, a, info.ID).State != run.StateStopped {
		t.Fatal("not stopped on app close")
	}
}

func TestInstallWrapUsesRunTarget(t *testing.T) {
	bin := t.TempDir()
	name := "composer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	composer := filepath.Join(bin, name)
	if err := os.WriteFile(composer, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	a, err := New(Options{
		ConfigDir: filepath.Join(t.TempDir(), "config"), Interp: interpOptions(nil),
		LSP:        servers.DetectOptions{HomeDir: home, PathEnv: bin, ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
		ComposeEnv: map[string]string{}, GOOS: "linux", AppVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := t.TempDir()
	call(t, a, "project.open", PathParams{Path: root}, nil)
	HookInstallRecipes.On(a.Hooks(), 0, func(_ context.Context, l []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(l), install.Recipe{Plugin: "qa-x", Tool: "X", Manager: install.ManagerComposer, Package: "acme/x",
			Scopes: []install.Scope{install.ScopeProject}, Interpreter: install.InterpreterProject}), nil
	})
	// Without php-runtime the project interpreter is the host.
	var plan install.Plan
	call(t, a, "install.plan", InstallPlanParams{Plugin: "qa-x"}, &plan)
	if !plan.Available || plan.Command[0] != composer || !slices.Equal(plan.Command[1:], []string{"require", "--dev", "acme/x"}) {
		t.Fatalf("plan %+v", plan)
	}
	HookRunInterpreter.On(a.Hooks(), 0, func(_ context.Context, q RunInterpreterQuery) (RunInterpreterQuery, error) {
		q.Err = "no interpreter"
		return q, nil
	})
	wantCode(t, a, "install.plan", InstallPlanParams{Plugin: "qa-x"}, CodeFailed)
}
