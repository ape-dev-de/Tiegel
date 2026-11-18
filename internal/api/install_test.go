package api

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// fakeComposer writes a composer script that logs its arguments and
// working directory, sleeping for package acme/slow.
func fakeComposer(t *testing.T) (binDir, logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	binDir, logFile = t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\npwd >> " + logFile + "\necho \"Installing $3$4\"\n" +
		"case \"$*\" in *acme/slow*) sleep 30;; esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "composer"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir, logFile
}

func TestInstallService(t *testing.T) {
	bin, logFile := fakeComposer(t)
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
	recipe := install.Recipe{Plugin: "lsp-x", Tool: "X", Manager: install.ManagerComposer, Package: "acme/x",
		Scopes: []install.Scope{install.ScopeGlobal, install.ScopeProject}}
	slow := recipe
	slow.Plugin, slow.Package = "lsp-slow", "acme/slow"
	HookInstallRecipes.On(a.Hooks(), 0, func(_ context.Context, l []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(l), recipe, slow), nil
	})
	events := make(chan string, 64)
	unsub := a.Bus().Subscribe("install.*", func(e bus.Event) {
		switch p := e.Payload.(type) {
		case install.Line:
			events <- "line " + p.Text
		case install.Result:
			events <- "done " + p.State
		}
	})
	defer unsub()

	var recipes []install.Recipe
	call(t, a, "install.recipes", nil, &recipes)
	if len(recipes) != 2 || recipes[0].Plugin != "lsp-slow" {
		t.Fatalf("recipes %+v", recipes)
	}
	var plan install.Plan
	call(t, a, "install.plan", InstallPlanParams{Plugin: "lsp-x"}, &plan)
	if !plan.Available || plan.Display != "composer global require acme/x" || plan.Command[0] != filepath.Join(bin, "composer") {
		t.Fatalf("plan %+v", plan)
	}
	wantCode(t, a, "install.plan", InstallPlanParams{}, CodeInvalidParams)
	wantCode(t, a, "install.plan", InstallPlanParams{Plugin: "nope"}, CodeNotFound)
	wantCode(t, a, "install.plan", InstallPlanParams{Plugin: "lsp-x", Scope: "moon"}, CodeInvalidParams)

	// Without confirmation nothing runs.
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-x"}, CodeInvalidParams)
	if _, err := os.Stat(logFile); err == nil {
		t.Fatal("composer ran without confirmation")
	}
	// Project scope needs a project.
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Scope: "project", Confirm: true, PlanID: planID(t, a, "lsp-x", "project")}, CodeUnavailable)

	project := t.TempDir()
	if _, err := a.OpenProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	var run InstallRunResult
	call(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Scope: "project", Confirm: true, PlanID: planID(t, a, "lsp-x", "project")}, &run)
	if run.RunID == "" || !run.Plan.ChangesComposerJSON {
		t.Fatalf("run %+v", run)
	}
	var res install.Result
	call(t, a, "install.wait", InstallWaitParams{RunID: run.RunID}, &res)
	if res.State != install.StateSucceeded {
		t.Fatalf("result %+v", res)
	}
	calls, _ := os.ReadFile(logFile)
	real, _ := filepath.EvalSymlinks(project)
	if got := string(calls); !strings.HasPrefix(got, "require --dev acme/x\n") || !strings.Contains(got, real) {
		t.Fatalf("composer calls %q", got)
	}
	seen := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for !seen["done succeeded"] {
		select {
		case e := <-events:
			seen[e] = true
		case <-timeout:
			t.Fatalf("events %v", seen)
		}
	}
	if !seen["line Installing acme/x"] {
		t.Fatalf("no output event: %v", seen)
	}
	call(t, a, "install.status", InstallRunIDParams{RunID: run.RunID}, &res)
	if len(res.Output) != 1 {
		t.Fatalf("status %+v", res)
	}

	// Cancel a slow one.
	slowID := planID(t, a, "lsp-slow", "")
	call(t, a, "install.run", InstallRunParams{Plugin: "lsp-slow", Confirm: true, PlanID: slowID}, &run)
	var first install.Result
	call(t, a, "install.status", InstallRunIDParams{RunID: run.RunID}, &first)
	if first.State != install.StateRunning {
		t.Fatalf("slow run not running: %+v", first)
	}
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-slow", Confirm: true, PlanID: slowID}, CodeConflict)
	call(t, a, "install.cancel", InstallRunIDParams{RunID: run.RunID}, nil)
	call(t, a, "install.wait", InstallWaitParams{RunID: run.RunID}, &res)
	if res.State != install.StateCancelled {
		t.Fatalf("cancel %+v", res)
	}
	wantCode(t, a, "install.cancel", InstallRunIDParams{RunID: "x"}, CodeNotFound)
	wantCode(t, a, "install.status", InstallRunIDParams{RunID: "x"}, CodeNotFound)
	wantCode(t, a, "install.wait", InstallWaitParams{RunID: "x"}, CodeNotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.installWait(ctx, InstallWaitParams{RunID: run.RunID}); err != nil {
		t.Fatalf("finished run with cancelled ctx: %v", err) // finished runs return at once
	}
}

func planID(t *testing.T, a *App, plugin, scope string) string {
	t.Helper()
	var plan install.Plan
	call(t, a, "install.plan", InstallPlanParams{Plugin: plugin, Scope: scope}, &plan)
	if plan.ID == "" {
		t.Fatalf("plan without id: %+v", plan)
	}
	return plan.ID
}

// TestInstallRunRequiresConfirmedPlan: install.run executes only the plan
// whose id was confirmed; a changed plan is refused.
func TestInstallRunRequiresConfirmedPlan(t *testing.T) {
	bin, logFile := fakeComposer(t)
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
	recipe := install.Recipe{Plugin: "lsp-x", Tool: "X", Manager: install.ManagerComposer, Package: "acme/x",
		Scopes: []install.Scope{install.ScopeProject}}
	HookInstallRecipes.On(a.Hooks(), 0, func(_ context.Context, l []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(l), recipe), nil
	})
	first := t.TempDir()
	if _, err := a.OpenProject(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	confirmed := planID(t, a, "lsp-x", "")
	if again := planID(t, a, "lsp-x", ""); again != confirmed {
		t.Fatal("plan id not stable")
	}
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Confirm: true}, CodeInvalidParams)
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Confirm: true, PlanID: "bogus"}, CodeConflict)
	// The project changed after the preview: the command would run in
	// another directory.
	if _, err := a.OpenProject(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Confirm: true, PlanID: confirmed}, CodeConflict)
	if _, err := os.Stat(logFile); err == nil {
		t.Fatal("composer ran without a matching confirmation")
	}
	var run InstallRunResult
	call(t, a, "install.run", InstallRunParams{Plugin: "lsp-x", Confirm: true, PlanID: planID(t, a, "lsp-x", "")}, &run)
	var res install.Result
	call(t, a, "install.wait", InstallWaitParams{RunID: run.RunID}, &res)
	if res.State != install.StateSucceeded || run.Plan.ID == "" {
		t.Fatalf("result %+v", res)
	}
}
