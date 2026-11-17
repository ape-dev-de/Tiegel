package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as fake composer: it records its arguments and
// working directory and behaves according to the package name.
func TestMain(m *testing.M) {
	if os.Getenv("TIEGEL_FAKE_COMPOSER") == "1" {
		fakeComposer()
		return
	}
	os.Exit(m.Run())
}

func fakeComposer() {
	args := os.Args[1:]
	wd, _ := os.Getwd()
	if log := os.Getenv("TIEGEL_FAKE_COMPOSER_LOG"); log != "" {
		_ = os.WriteFile(log, []byte(strings.Join(args, " ")+"\n"+wd+"\n"+os.Getenv("COMPOSER_NO_INTERACTION")), 0o644)
	}
	pkg := args[len(args)-1]
	fmt.Println("Using version ^1.0 for " + pkg)
	fmt.Fprintln(os.Stderr, "Installing "+pkg)
	switch pkg {
	case "acme/slow":
		time.Sleep(30 * time.Second)
	case "acme/fail":
		fmt.Fprintln(os.Stderr, "Could not find package")
		os.Exit(2)
	}
	os.Exit(0)
}

func fakeEnv(t *testing.T, project string) (Env, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("TIEGEL_FAKE_COMPOSER", "1")
	t.Setenv("TIEGEL_FAKE_COMPOSER_LOG", log)
	return Env{ProjectDir: project, LookPath: func(name string) (string, error) {
		if name != "composer" {
			return "", errors.New("missing")
		}
		return exe, nil
	}}, log
}

func recipe(pkg string, scopes ...Scope) Recipe {
	return Recipe{Plugin: "lsp-x", Tool: "X", Manager: ManagerComposer, Package: pkg, Scopes: scopes}
}

func TestValidate(t *testing.T) {
	bad := []Recipe{
		{},
		{Plugin: "p", Tool: "T"},
		{Plugin: "p", Tool: "T", Manager: "brew", Package: "x", Scopes: []Scope{ScopeGlobal}},
		{Plugin: "p", Tool: "T", Manager: ManagerComposer},
		{Plugin: "p", Tool: "T", Manager: ManagerComposer, Package: "x", Scopes: []Scope{"elsewhere"}},
		{Plugin: "p", Tool: "T", Manager: ManagerNPM, Package: "x", Scopes: []Scope{ScopeProject}},
		{Plugin: "p", Tool: "T", Manager: ManagerComposer, Package: "x", Scopes: []Scope{ScopeGlobal}, Interpreter: "docker"},
	}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Errorf("%d: %+v accepted", i, r)
		}
		if _, err := MakePlan(r, "", Env{}); err == nil {
			t.Errorf("%d: plan of invalid recipe", i)
		}
	}
	if err := (Recipe{Plugin: "p", Tool: "T", Hint: "build it"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlans(t *testing.T) {
	project := t.TempDir()
	env, _ := fakeEnv(t, project)
	r := recipe("phpactor/phpactor", ScopeGlobal, ScopeProject)

	p, err := MakePlan(r, "", env)
	if err != nil || !p.Available || p.Scope != ScopeGlobal || p.ChangesComposerJSON ||
		strings.Join(p.Command[1:], " ") != "global require phpactor/phpactor" || p.Display != "composer global require phpactor/phpactor" {
		t.Fatalf("global %+v %v", p, err)
	}
	p, err = MakePlan(r, ScopeProject, env)
	if err != nil || !p.Available || !p.ChangesComposerJSON || p.Dir != project ||
		strings.Join(p.Command[1:], " ") != "require --dev phpactor/phpactor" || !strings.Contains(p.Effects[0], "creates") {
		t.Fatalf("project %+v %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(project, "composer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ = MakePlan(r, ScopeProject, env); !strings.Contains(p.Effects[0], "adds") {
		t.Fatalf("effects %v", p.Effects)
	}
	if _, err := MakePlan(recipe("x/y", ScopeGlobal), ScopeProject, env); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("scope %v", err)
	}
	// Without project: not available, with a hint.
	if p, _ = MakePlan(r, ScopeProject, Env{LookPath: env.LookPath}); p.Available || !strings.Contains(p.Hint, "Open a project") {
		t.Fatalf("no project %+v", p)
	}
	// Composer missing: hint, never a download.
	none := Env{LookPath: func(string) (string, error) { return "", errors.New("no") }}
	if p, _ = MakePlan(r, "", none); p.Available || !strings.Contains(p.Hint, "Composer was not found") || p.Display == "" {
		t.Fatalf("missing composer %+v", p)
	}
	npm := Recipe{Plugin: "lsp-i", Tool: "I", Manager: ManagerNPM, Package: "intelephense", Scopes: []Scope{ScopeGlobal}, Note: "proprietary"}
	if p, _ = MakePlan(npm, "", none); p.Available || !strings.Contains(p.Hint, "npm was not found") || p.Display != "npm install --global intelephense" || p.Note != "proprietary" {
		t.Fatalf("npm %+v", p)
	}
	// Hint-only recipes.
	hint := Recipe{Plugin: "lsp-h", Tool: "H", Hint: "download a release"}
	if p, _ = MakePlan(hint, "", env); p.Available || p.Hint != "download a release" || len(p.Command) != 0 {
		t.Fatalf("hint %+v", p)
	}
	// Project interpreter: needs a wrapper.
	pr := recipe("laravel/pint", ScopeProject)
	pr.Interpreter = InterpreterProject
	if p, _ = MakePlan(pr, "", env); p.Available || !strings.Contains(p.Hint, "run infrastructure") {
		t.Fatalf("no wrap %+v", p)
	}
	env.Wrap = func(argv []string, dir string) ([]string, error) {
		return append([]string{"docker", "compose", "exec", "php"}, argv...), nil
	}
	if p, _ = MakePlan(pr, "", env); !p.Available || p.Display != "docker compose exec php composer require --dev laravel/pint" {
		t.Fatalf("wrap %+v", p)
	}
	env.Wrap = func([]string, string) ([]string, error) { return nil, errors.New("no service") }
	if _, err := MakePlan(pr, "", env); err == nil {
		t.Fatal("wrap error lost")
	}
	// Default LookPath over PathEnv.
	dir := t.TempDir()
	exe := filepath.Join(dir, "composer")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _ = MakePlan(r, "", Env{PathEnv: dir}); !p.Available || p.Command[0] != exe || len(p.env) != 1 {
		t.Fatalf("PATH lookup %+v", p)
	}
	if p, _ = MakePlan(r, "", Env{PathEnv: t.TempDir()}); p.Available {
		t.Fatal("found in empty PATH")
	}
	if Quote([]string{"a", "b c", "it's", ""}) != `a 'b c' 'it'\''s' ''` {
		t.Fatal(Quote([]string{"a", "b c", "it's", ""}))
	}
}

type sink struct {
	mu    sync.Mutex
	lines []Line
	done  []Result
}

func (s *sink) line(l Line)  { s.mu.Lock(); s.lines = append(s.lines, l); s.mu.Unlock() }
func (s *sink) fin(r Result) { s.mu.Lock(); s.done = append(s.done, r); s.mu.Unlock() }

func TestRunAgainstFakeComposer(t *testing.T) {
	project := t.TempDir()
	env, log := fakeEnv(t, project)
	s := &sink{}
	rn := &Runner{OnLine: s.line, OnDone: s.fin}
	ctx := context.Background()

	// Nothing runs for unavailable plans.
	if _, err := rn.Start(ctx, Plan{Hint: "no"}); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("unavailable %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("fake composer ran without a plan")
	}

	p, _ := MakePlan(recipe("phpactor/phpactor", ScopeProject), "", env)
	id, err := rn.Start(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := rn.Wait(ctx, id)
	if err != nil || res.State != StateSucceeded || res.ExitCode != 0 {
		t.Fatalf("run %+v %v", res, err)
	}
	calls, _ := os.ReadFile(log)
	want := "require --dev phpactor/phpactor\n" + project + "\n1"
	if got := string(calls); got != want {
		// macOS temp dirs may differ by /private symlink.
		if r, _ := filepath.EvalSymlinks(project); got != "require --dev phpactor/phpactor\n"+r+"\n1" {
			t.Fatalf("composer called with %q, want %q", got, want)
		}
	}
	s.mu.Lock()
	if len(s.lines) != 2 || len(s.done) != 1 || s.done[0].State != StateSucceeded {
		t.Fatalf("stream %+v %+v", s.lines, s.done)
	}
	s.mu.Unlock()
	if st, _ := rn.Status(id); len(st.Output) != 2 {
		t.Fatalf("output %+v", st)
	}

	// Failure keeps the exit code and output.
	p, _ = MakePlan(recipe("acme/fail", ScopeGlobal), "", env)
	id, _ = rn.Start(ctx, p)
	if res, _ = rn.Wait(ctx, id); res.State != StateFailed || res.ExitCode != 2 || !strings.Contains(strings.Join(res.Output, "\n"), "Could not find") {
		t.Fatalf("fail %+v", res)
	}

	// Cancel a slow installation; a second one for the plugin is refused meanwhile.
	p, _ = MakePlan(recipe("acme/slow", ScopeGlobal), "", env)
	id, _ = rn.Start(ctx, p)
	if _, err := rn.Start(ctx, p); !errors.Is(err, ErrBusy) {
		t.Fatalf("parallel run %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := rn.Cancel(id); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if res, _ = rn.Wait(wctx, id); res.State != StateCancelled {
		t.Fatalf("cancel %+v", res)
	}
	if err := rn.Cancel("nope"); !errors.Is(err, ErrUnknownRun) {
		t.Fatal(err)
	}
	if _, err := rn.Status("nope"); !errors.Is(err, ErrUnknownRun) {
		t.Fatal(err)
	}
	if _, err := rn.Wait(ctx, "nope"); !errors.Is(err, ErrUnknownRun) {
		t.Fatal(err)
	}

	// A command that cannot start fails without hanging.
	id, err = rn.Start(ctx, Plan{Plugin: "z", Available: true, Command: []string{filepath.Join(t.TempDir(), "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = rn.Wait(ctx, id); res.State != StateFailed || res.Error == "" {
		t.Fatalf("start failure %+v", res)
	}
	// Wait honours its context.
	p, _ = MakePlan(recipe("acme/slow", ScopeGlobal), "", env)
	p.Plugin = "other"
	id, _ = rn.Start(ctx, p)
	short, cancel2 := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel2()
	if _, err := rn.Wait(short, id); err == nil {
		t.Fatal("wait ignored ctx")
	}
	rn.CancelAll()
	if res, _ = rn.Wait(wctx, id); res.State != StateCancelled {
		t.Fatalf("cancel all %+v", res)
	}
}
