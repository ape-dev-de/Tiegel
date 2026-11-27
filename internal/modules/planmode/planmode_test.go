package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/orchestrate"
	ot "github.com/ape-dev-de/tiegel/internal/orchestrate/orchestratetest"
)

type fixture struct {
	t      *testing.T
	app    *api.App
	root   string
	fake   *ot.Fake
	runner *ot.Runner
	rec    *modtest.Recorder
}

// mappings is a stand-in for php-runtime's runtime.mappings.get.
type mappings struct{ list []api.MappingInfo }

func (mappings) ID() string          { return "fake-runtime" }
func (mappings) DependsOn() []string { return nil }
func (mappings) Stop() error         { return nil }
func (m mappings) Start(ctx module.Context) error {
	p := api.NewProvider(ctx)
	api.Add(p, "runtime.mappings.get", "test", func(context.Context, struct{}) (map[string]any, error) {
		return map[string]any{"mappings": m.list, "source": "compose"}, nil
	})
	return p.Err()
}

func newFixture(t *testing.T, replies map[string]ot.Reply, extra ...module.Module) *fixture {
	t.Helper()
	f := &fixture{t: t, fake: ot.New(replies), runner: &ot.Runner{}}
	mod := New(Options{Start: f.fake.Agent.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }, Tests: f.runner,
		Executable: func() (string, error) { return "/bin/tiegel", nil }})
	f.app = modtest.NewApp(t, append([]module.Module{mod}, extra...), nil)
	f.rec = modtest.Record(t, f.app, "plan.*")
	f.root = modtest.Project(t, f.app, map[string]string{"src/Calc.php": ot.BaseCalc, "composer.json": "{}"})
	return f
}

func (f *fixture) call(method string, params, out any) {
	f.t.Helper()
	modtest.Call(f.t, f.app, method, params, out)
}

func (f *fixture) set(key string, v any) {
	f.t.Helper()
	raw, _ := json.Marshal(v)
	f.call("settings.set", api.SettingsSetParams{Key: key, Scope: "project", Value: raw}, nil)
}

func (f *fixture) start() api.PlanStatus {
	f.t.Helper()
	var st api.PlanStatus
	f.call("plan.start", api.PlanStartParams{Goal: "Make Calc::add return the sum.", Wait: true}, &st)
	if st.State != api.PlanReview {
		f.t.Fatalf("after planning: %+v", st)
	}
	return st
}

func (f *fixture) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(f.root, rel))
	return string(b)
}

func TestPlanInPlace(t *testing.T) {
	f := newFixture(t, nil)
	f.set(SettingWorkspace, "in-place")
	modtest.WantCode(t, f.app, "plan.status", nil, api.CodeNotFound)
	modtest.WantCode(t, f.app, "plan.start", api.PlanStartParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "plan.start", api.PlanStartParams{Goal: "x", Mediator: "sometimes"}, api.CodeInvalidParams)
	st := f.start()
	if !strings.HasSuffix(st.Document, st.ID+".md") || len(st.Roles) < 6 || st.Running {
		t.Fatalf("%+v", st)
	}
	if len(f.rec.Events(api.TopicPlanProgress)) == 0 || len(f.rec.Events(api.TopicPlanActivity)) == 0 {
		t.Fatal("no events")
	}
	// The MCP servers of a session: Tiegel's plan tools with plan and role.
	news := f.fake.Agent.NewSessions()
	if len(news) == 0 || !strings.Contains(string(mustJSON(news[0].MCPServers)), `"--plan","`+st.ID) {
		t.Fatalf("%s", mustJSON(news))
	}
	var doc api.PlanDocumentResult
	f.call("plan.document", api.PlanDocumentParams{}, &doc)
	if !strings.Contains(doc.Markdown, "## Plan {#plan}") || len(doc.History) == 0 || doc.Document.Steps[0].Title != "Implement Calc::add" {
		t.Fatal(doc.Markdown)
	}
	f.call("plan.document", api.PlanDocumentParams{Section: "criteria"}, &doc)
	if !strings.HasPrefix(doc.Markdown, "## Acceptance criteria") || strings.Contains(doc.Markdown, "## Plan") {
		t.Fatal(doc.Markdown)
	}
	modtest.WantCode(t, f.app, "plan.document", api.PlanDocumentParams{Section: "nope"}, api.CodeInvalidParams)
	// Nothing is implemented before Accept.
	modtest.WantCode(t, f.app, "plan.execute", api.PlanExecuteParams{}, api.CodeConflict)
	if f.fake.Calls("dev") != 0 || f.read("src/Calc.php") != ot.BaseCalc {
		t.Fatal("executed before acceptance")
	}
	f.call("plan.accept", api.PlanCommentParams{Comment: "go"}, &st)
	if st.State != api.PlanAccepted {
		t.Fatal(st.State)
	}
	modtest.WantCode(t, f.app, "plan.accept", api.PlanCommentParams{}, api.CodeConflict)
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.State != api.PlanDone || st.Workspace == nil || st.Workspace.Mode != "in-place" {
		t.Fatalf("%+v", st)
	}
	if f.read("src/Calc.php") != ot.GoodCalc {
		t.Fatal(f.read("src/Calc.php"))
	}
	var cs []api.PlanChange
	f.call("plan.changes", nil, &cs)
	if len(cs) != 2 {
		t.Fatalf("%+v", cs)
	}
	var d api.PlanDiff
	f.call("plan.diff", api.PlanDiffParams{Path: "src/Calc.php"}, &d)
	if !strings.Contains(d.Unified, "+        return $a + $b;") || d.Original != ot.BaseCalc {
		t.Fatal(d.Unified)
	}
	modtest.WantCode(t, f.app, "plan.diff", api.PlanDiffParams{Path: "nope.php"}, api.CodeNotFound)
	var es []api.AgentEntry
	f.call("plan.transcript", api.PlanTranscriptParams{Role: api.RoleDev}, &es)
	if len(es) < 2 || es[0].Kind != api.EntryUser {
		t.Fatalf("%+v", es)
	}
	f.call("plan.transcript", api.PlanTranscriptParams{Role: "critic"}, &es)
	if n := countKind(es, api.EntryUser); n != 2 {
		t.Fatalf("critic transcripts of both sessions: %d", n)
	}
	modtest.WantCode(t, f.app, "plan.transcript", api.PlanTranscriptParams{}, api.CodeInvalidParams)
	// Revert one file in place.
	f.call("plan.revert", api.PlanDiffParams{Path: "tests/CalcTest.php"}, &cs)
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	var list []api.PlanStatus
	f.call("plan.list", nil, &list)
	if len(list) != 1 || list[0].ID != st.ID {
		t.Fatal(list)
	}
	f.call("plan.apply", api.PlanIDParams{}, &st)
	if st.State != api.PlanApplied {
		t.Fatal(st.State)
	}
}

func countKind(es []api.AgentEntry, kind string) int {
	n := 0
	for _, e := range es {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestPlanWorktreeApply(t *testing.T) {
	modtest.NeedGit(t)
	f := newFixture(t, nil)
	modtest.Git(t, f.root, "init", "-q", "-b", "main")
	modtest.Git(t, f.root, "add", "-A")
	modtest.Git(t, f.root, "commit", "-q", "-m", "init")
	st := f.start()
	f.call("plan.accept", api.PlanCommentParams{}, nil)
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.State != api.PlanDone || st.Workspace.Mode != "worktree" || st.Workspace.Branch != "tiegel/plan-"+st.ID {
		t.Fatalf("%+v %+v", st, st.Workspace)
	}
	// The project is untouched until Apply.
	if f.read("src/Calc.php") != ot.BaseCalc {
		t.Fatal("changed the working copy")
	}
	if got := modtest.Git(t, f.root, "status", "--porcelain"); strings.Contains(got, ".tiegel") {
		t.Fatal(got)
	}
	var red api.PlanGate
	for _, g := range st.Gates {
		if g.Name == "red" {
			red = g
		}
	}
	if !red.Passed {
		t.Fatalf("red check %+v", red)
	}
	f.call("plan.apply", api.PlanIDParams{}, &st)
	if st.State != api.PlanApplied || f.read("src/Calc.php") != ot.GoodCalc || f.read("tests/CalcTest.php") != ot.GoodTest {
		t.Fatal(st.State, f.read("src/Calc.php"))
	}
	if b := modtest.Git(t, f.root, "branch", "--list", "tiegel/plan-*"); b != "" {
		t.Fatal("branch kept:", b)
	}
	if log := modtest.Git(t, f.root, "log", "--oneline"); strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatal("commits on the user's branch:", log)
	}
}

func TestPlanDockerMappingFallback(t *testing.T) {
	modtest.NeedGit(t)
	// The container mounts only /elsewhere: the worktree would be
	// invisible, so the plan executes in place and says so.
	f := newFixture(t, nil, mappings{list: []api.MappingInfo{{Host: "/elsewhere", Remote: "/app"}}})
	modtest.Git(t, f.root, "init", "-q", "-b", "main")
	modtest.Git(t, f.root, "add", "-A")
	modtest.Git(t, f.root, "commit", "-q", "-m", "init")
	st := f.start()
	f.call("plan.accept", api.PlanCommentParams{}, nil)
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.Workspace.Mode != "in-place" || !strings.Contains(strings.Join(st.Notes, " "), "does not mount") {
		t.Fatalf("%+v %v", st.Workspace, st.Notes)
	}
}

func TestPlanDockerMappingCovered(t *testing.T) {
	modtest.NeedGit(t)
	f := newFixture(t, nil, mappings{list: []api.MappingInfo{{Host: "/", Remote: "/host"}}})
	modtest.Git(t, f.root, "init", "-q", "-b", "main")
	modtest.Git(t, f.root, "add", "-A")
	modtest.Git(t, f.root, "commit", "-q", "-m", "init")
	f.set(SettingDirtyTree, "refuse")
	f.start()
	f.call("plan.accept", api.PlanCommentParams{}, nil)
	modtest.WriteFile(t, filepath.Join(f.root, "src/Calc.php"), "<?php // dirty\n")
	modtest.WantCode(t, f.app, "plan.execute", api.PlanExecuteParams{Wait: true}, api.CodeConflict)
	modtest.WriteFile(t, filepath.Join(f.root, "src/Calc.php"), ot.BaseCalc)
	var st api.PlanStatus
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.Workspace.Mode != "worktree" || st.State != api.PlanDone {
		t.Fatalf("%+v", st)
	}
	f.call("plan.keepBranch", api.PlanIDParams{}, &st)
	if st.State != api.PlanDiscarded || modtest.Git(t, f.root, "branch", "--list", "tiegel/plan-*") == "" {
		t.Fatal("branch not kept")
	}
}

func TestPlanChangesCancelDiscardResume(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	f := newFixture(t, map[string]ot.Reply{
		"revise": func(ctx context.Context, tt *acptest.Turn, n int) any {
			started <- struct{}{}
			<-block
			return ot.Defaults["revise"](ctx, tt, n)
		},
	})
	f.set(SettingSubagents, false)
	st := f.start()
	modtest.WantCode(t, f.app, "plan.requestChanges", api.PlanCommentParams{}, api.CodeInvalidParams)
	f.call("plan.requestChanges", api.PlanCommentParams{Comment: "Handle overflow."}, &st)
	if !st.Running {
		t.Fatal("revision not running")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no revision")
	}
	modtest.WantCode(t, f.app, "plan.requestChanges", api.PlanCommentParams{Comment: "again"}, api.CodeConflict)
	close(block)
	modtest.Eventually(t, "revision", func() bool {
		var s api.PlanStatus
		f.call("plan.status", nil, &s)
		return !s.Running
	})
	var s api.PlanStatus
	f.call("plan.status", nil, &s)
	if s.State != api.PlanReview || f.fake.Calls("revise") != 1 {
		t.Fatalf("%s revise %d", s.State, f.fake.Calls("revise"))
	}
	var doc api.PlanDocumentResult
	f.call("plan.document", nil, &doc)
	if len(doc.Document.Comments) != 1 || !strings.Contains(doc.Document.Steps[0].Title, "revised") {
		t.Fatal(doc.Markdown)
	}
	f.call("plan.discard", api.PlanIDParams{}, &s)
	if s.State != api.PlanDiscarded {
		t.Fatal(s.State)
	}
	modtest.WantCode(t, f.app, "plan.resume", api.PlanExecuteParams{}, api.CodeConflict)
}

func TestPlanCancelResume(t *testing.T) {
	gate := make(chan struct{})
	once := false
	f := newFixture(t, map[string]ot.Reply{
		"summary": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if !once {
				once = true
				close(gate)
				<-ctx.Done()
				return "cancelled"
			}
			return ot.Defaults["summary"](ctx, tt, n)
		},
	})
	var st api.PlanStatus
	f.call("plan.start", api.PlanStartParams{Goal: "Make Calc::add return the sum."}, &st)
	<-gate
	f.call("plan.cancel", api.PlanIDParams{}, &st)
	if st.State != api.PlanCancelled || st.Phase != api.PhaseSummary {
		t.Fatalf("%s %s", st.State, st.Phase)
	}
	modtest.WantCode(t, f.app, "plan.execute", api.PlanExecuteParams{}, api.CodeConflict)
	f.call("plan.resume", api.PlanExecuteParams{Wait: true}, &st)
	if st.State != api.PlanReview || f.fake.Calls("arch") != 1 {
		t.Fatalf("%s arch %d", st.State, f.fake.Calls("arch"))
	}
}

func TestPlanDisputeResolve(t *testing.T) {
	f := newFixture(t, map[string]ot.Reply{
		"critic-impl": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if n == 0 {
				return map[string]any{"issues": []map[string]any{{"severity": "major", "target": "impl", "claim": "no overflow check", "evidence": "src/Calc.php:6"}}}
			}
			return map[string]any{"issues": []any{}}
		},
		"dev": func(ctx context.Context, tt *acptest.Turn, n int) any {
			r := ot.Defaults["dev"](ctx, tt, n).(map[string]any)
			if strings.Contains(tt.Prompt, "no overflow check") {
				r["responses"] = []map[string]any{{"item": "Q2", "verdict": "reject", "text": "ints are bounded"}}
			}
			return r
		},
		"rate-impl": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if strings.Contains(tt.Prompt, "Q2 [major, from critic, rejected]") {
				return map[string]any{"issues": []any{}, "rebuttals": []map[string]any{{"item": "Q2", "verdict": "insist", "text": "they are not"}}}
			}
			return map[string]any{"issues": []any{}}
		},
	})
	f.set(SettingWorkspace, "in-place")
	f.set(SettingMediator, api.MediatorOff)
	f.set(SettingMediatorRounds, 1)
	f.start()
	f.call("plan.accept", api.PlanCommentParams{}, nil)
	var st api.PlanStatus
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.State != api.PlanNeedsUser || len(st.Disputes) != 1 || st.Disputes[0].ID != "Q2" {
		t.Fatalf("%+v", st)
	}
	modtest.WantCode(t, f.app, "plan.execute", api.PlanExecuteParams{}, api.CodeConflict)
	modtest.WantCode(t, f.app, "plan.resolve", api.PlanResolveParams{Item: "Q2", Decision: "maybe"}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "plan.resolve", api.PlanResolveParams{}, api.CodeInvalidParams)
	f.call("plan.resolve", api.PlanResolveParams{Item: "Q2", Decision: "qa", Comment: "check it"}, &st)
	if len(st.Disputes) != 0 || st.Metrics.UserOverrides != 1 {
		t.Fatalf("%+v", st)
	}
	f.call("plan.execute", api.PlanExecuteParams{Wait: true}, &st)
	if st.State != api.PlanNeedsUser && st.State != api.PlanDone && st.State != api.PlanIncomplete {
		t.Fatal(st.State)
	}
}

func TestEstimateEvalPropose(t *testing.T) {
	f := newFixture(t, nil)
	f.set(SettingWorkspace, "in-place")
	var e PlanEstimate
	f.call("plan.estimate", nil, &e)
	if e.Agent != "opencode acp" || e.PlanningCalls != 6 || e.Tokens <= 0 || !e.Subagents || e.Mediator != api.MediatorAuto {
		t.Fatalf("%+v", e)
	}
	modtest.WantCode(t, f.app, "plan.eval", PlanEvalParams{Goal: "x"}, api.CodeInvalidParams)
	var rows []PlanEvalRow
	f.call("plan.eval", PlanEvalParams{Goal: "Make Calc::add return the sum.", Modes: []string{api.MediatorOff, api.MediatorAlways}, Yes: true}, &rows)
	if len(rows) != 2 || rows[0].Mediator != api.MediatorOff || rows[0].State != api.PlanDone || rows[1].Metrics.Mediator != api.MediatorAlways {
		t.Fatalf("%+v", rows)
	}
	modtest.WantCode(t, f.app, "plan.propose", api.PlanProposeParams{ID: rows[0].Plan, Role: "dev", Op: "delete"}, api.CodeInvalidParams)
	f.call("plan.propose", api.PlanProposeParams{ID: rows[0].Plan, Role: "dev", Op: "finding", Claim: "x"}, nil)
	modtest.WantCode(t, f.app, "plan.propose", api.PlanProposeParams{Role: "dev", Op: "finding"}, api.CodeInvalidParams)
}

func TestAgentMissing(t *testing.T) {
	f := &fixture{t: t, fake: ot.New(nil), runner: &ot.Runner{}}
	mod := New(Options{Start: f.fake.Agent.Start, LookPath: func(string) (string, error) { return "", errors.New("no") }, Tests: f.runner})
	f.app = modtest.NewApp(t, []module.Module{mod}, nil)
	f.root = modtest.Project(t, f.app, map[string]string{"a.php": "<?php"})
	modtest.WantCode(t, f.app, "plan.start", api.PlanStartParams{Goal: "x", Wait: true}, api.CodeNotFound)
	var st api.PlanStatus
	f.call("plan.status", nil, &st)
	if !strings.Contains(st.Error, "not found") {
		t.Fatal(st)
	}
}

func TestParseTeamCity(t *testing.T) {
	dir := t.TempDir()
	modtest.WriteFile(t, filepath.Join(dir, "tests/CalcTest.php"), "<?php")
	lines := []string{
		"##teamcity[testSuiteStarted name='CalcTest' locationHint='php_qn:///app/.tiegel/worktrees/p1/tests/CalcTest.php::\\CalcTest' flowId='1']",
		"##teamcity[testStarted name='testAdds' locationHint='php_qn:///app/.tiegel/worktrees/p1/tests/CalcTest.php::\\CalcTest::testAdds' flowId='1']",
		"##teamcity[testFailed name='testAdds' message='Failed asserting that 0 is identical to 5.' details=' /app/.tiegel/worktrees/p1/tests/CalcTest.php:8|n' flowId='1']",
		"##teamcity[testFinished name='testAdds' flowId='1']",
		"##teamcity[testStarted name='testOther' locationHint='php_qn:///app/.tiegel/worktrees/p1/tests/CalcTest.php::\\CalcTest::testOther' flowId='1']",
		"##teamcity[testFinished name='testOther' flowId='1']",
		"##teamcity[testSuiteFinished name='CalcTest' flowId='1']",
	}
	rep := Parse(lines, dir, []api.MappingInfo{{Host: "/nowhere", Remote: "/app"}})
	if len(rep.Tests) != 2 || rep.Tests[0].Status != orchestrate.TestFailed || rep.Tests[0].ID != "CalcTest::testAdds" ||
		rep.Tests[0].FailFile != "tests/CalcTest.php" || rep.Tests[0].FailLine != 8 || rep.Tests[1].Status != orchestrate.TestPassed {
		t.Fatalf("%+v", rep.Tests)
	}
}

func TestPHPUnitRunnerMissing(t *testing.T) {
	f := newFixture(t, nil)
	mod := New(Options{})
	mod.app.Store(f.app)
	rep, err := (&phpunitRunner{m: mod, root: f.root}).Run(context.Background(), orchestrate.TestRequest{Dir: f.root})
	if err != nil || !strings.Contains(rep.Error, "PHPUnit not found") {
		t.Fatal(rep, err)
	}
	if !strings.Contains(opencodeConfig(true), `"git push*":"deny"`) || !strings.Contains(opencodeConfig(false), `"edit":"deny"`) {
		t.Fatal(opencodeConfig(true))
	}
	// A worktree: the bootstrap loads the main checkout's vendor/ and
	// the original bootstrap; without an interpreter run.start fails.
	modtest.WriteFile(t, filepath.Join(f.root, "vendor/bin/phpunit"), "#!/bin/sh\n")
	wt := filepath.Join(f.root, ".tiegel/worktrees/p1")
	modtest.WriteFile(t, filepath.Join(wt, "phpunit.xml"), `<phpunit bootstrap="tests/bootstrap.php"></phpunit>`)
	modtest.WriteFile(t, filepath.Join(wt, "composer.json"), `{"autoload":{"psr-4":{"App\\":"src/"}}}`)
	rep, err = (&phpunitRunner{m: mod, root: f.root}).Run(context.Background(), orchestrate.TestRequest{Dir: wt, Files: []string{"tests/CalcTest.php"}, Coverage: true})
	if err != nil || len(rep.Tests) != 0 || rep.Error == "" && rep.CoverageNote == "" {
		t.Fatal(rep, err)
	}
	boot, _ := os.ReadFile(filepath.Join(wt, bootstrapFile))
	if !strings.Contains(string(boot), "../../../vendor/autoload.php") || !strings.Contains(string(boot), "tests/bootstrap.php") {
		t.Fatal(string(boot))
	}
	if tail([]string{"a", "b", "c"}, 2) != "b\nc" {
		t.Fatal("tail")
	}
}
