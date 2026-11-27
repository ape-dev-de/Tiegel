package orchestrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestPlanPhaseSubagents(t *testing.T) {
	// The code planner tries to change a file and run a command: the
	// planning phase is read-only.
	replies := map[string]reply{
		"code": func(ctx context.Context, tt *acptest.Turn, n int) any {
			opt := tt.Permission(ctx, acp.ToolCall{ToolCallID: "e", Name: "edit", Kind: acp.KindEdit, Locations: []acp.Location{{Path: "src/Calc.php"}}})
			werr := tt.WriteFile(ctx, "src/Calc.php", "hacked")
			read := tt.Permission(ctx, acp.ToolCall{ToolCallID: "r", Name: "read", Kind: acp.KindRead})
			mcp := tt.Permission(ctx, acp.ToolCall{ToolCallID: "m", Name: "tiegel-knowledge_knowledge_search"})
			if opt != "reject" || werr == nil || read != "once" || mcp != "once" {
				t.Errorf("planning permissions: edit=%q write=%v read=%q mcp=%q", opt, werr, read, mcp)
			}
			return defaultReplies["code"](ctx, tt, n)
		},
	}
	h := newHarness(t, replies, Config{Subagents: true, MaxParallel: 3, Project: "calc"})
	e, st := h.planned()
	s := e.State()
	if s.Doc.Status != api.PlanReview || s.Doc.Phase != api.PhaseAcceptance {
		t.Fatalf("state %s/%s %s", s.Doc.Status, s.Doc.Phase, s.Error)
	}
	if h.read("src/Calc.php") != baseCalc {
		t.Fatal("planning changed a file")
	}
	if h.agent.Blocked() < 2 {
		t.Fatalf("blocked %d", h.agent.Blocked())
	}
	if h.fake.maxRun < 2 {
		t.Fatalf("planners did not run in parallel (max %d)", h.fake.maxRun)
	}
	// Findings: F1 verified, F2 (missing file) refuted mechanically
	// although the validator said verified.
	if f := Finding(&s.Doc, "F1"); f == nil || f.Status != api.FindingVerified {
		t.Fatalf("F1 %+v", f)
	}
	if f := Finding(&s.Doc, "F2"); f == nil || f.Status != api.FindingRefuted || !strings.Contains(f.Note, "does not exist") {
		t.Fatalf("F2 %+v", f)
	}
	if len(s.Doc.Steps) != 1 || len(s.Doc.Tests) != 1 || len(s.Doc.Criteria) != 1 || s.Doc.Criteria[0].Tests[0] != "T1" {
		t.Fatalf("doc %+v", s.Doc)
	}
	if it := Review(&s.Doc, "Q1"); it == nil || it.Status != api.ReviewResolved || it.From != api.RoleQA {
		t.Fatalf("Q1 %+v", it)
	}
	if g := s.Doc.Gates[0]; g.Name != "criteria" || !g.Passed {
		t.Fatalf("gate %+v", g)
	}
	// The validator does not see the planners' sessions, only the
	// findings; no role gets the whole document.
	vp := h.fake.prompt("validator", 0)
	if !strings.Contains(vp, "F2: `Ghost` handles nulls") || !strings.Contains(vp, "does not exist") || strings.Contains(vp, "## Plan") {
		t.Fatalf("validator prompt:\n%s", vp)
	}
	if p := h.fake.prompt("arch", 0); !strings.Contains(p, "senior software engineer in THIS codebase (calc)") || !strings.Contains(p, "read-only") {
		t.Fatalf("arch prompt:\n%s", p)
	}
	// Document, history, sessions.
	md, err := os.ReadFile(h.store.DocPath(st.Doc.ID))
	if err != nil || !strings.Contains(string(md), "## Findings {#findings}") || !strings.Contains(string(md), "status: awaiting-acceptance") {
		t.Fatalf("%v\n%s", err, md)
	}
	if hist := h.store.History(st.Doc.ID); len(hist) < 3 {
		t.Fatalf("history %v", hist)
	}
	if n := len(h.fake.agent.NewSessions()); n != 6 {
		t.Fatalf("sessions %d (3 planners, validator, qa-review, summarizer)", n)
	}
	if _, err := h.store.LoadTranscript(st.Doc.ID, api.RolePlannerArch); err != nil {
		t.Fatal(err)
	}
	if len(h.acts) == 0 {
		t.Fatal("no activity")
	}
	loaded, err := h.store.Load(st.Doc.ID)
	if err != nil || loaded.Doc.Revision != s.Doc.Revision || len(h.store.List()) != 1 {
		t.Fatalf("%v %+v", err, loaded)
	}
}

func TestPlanPhaseWithoutSubagents(t *testing.T) {
	h := newHarness(t, nil, Config{Subagents: false})
	e, _ := h.planned()
	if s := e.State(); s.Doc.Status != api.PlanReview {
		t.Fatal(s.Doc.Status, s.Error)
	}
	if h.fake.maxRun != 1 {
		t.Fatalf("parallel prompts without subagents: %d", h.fake.maxRun)
	}
	// One shared session for all planning roles, a fresh one for the
	// validator.
	if n := len(h.fake.agent.NewSessions()); n != 2 {
		t.Fatalf("sessions %d", n)
	}
	if p := h.fake.prompt("code", 0); !strings.Contains(p, "the same session now continues as the code and edge case planner") {
		t.Fatalf("no role switch:\n%s", p)
	}
}

func TestRepairInvalidJSON(t *testing.T) {
	replies := map[string]reply{
		"arch":        static("I think we should refactor everything."),
		"arch/repair": func(ctx context.Context, tt *acptest.Turn, n int) any { return defaultReplies["arch"](ctx, tt, n) },
		"tests": func(ctx context.Context, tt *acptest.Turn, n int) any {
			// A criterion without test case violates the schema.
			return map[string]any{"criteria": []map[string]any{{"text": "x", "tests": []any{}}}}
		},
		"tests/repair": func(ctx context.Context, tt *acptest.Turn, n int) any { return defaultReplies["tests"](ctx, tt, n) },
	}
	h := newHarness(t, replies, Config{Subagents: true})
	e, _ := h.planned()
	s := e.State()
	if s.Metrics.Repairs != 2 || len(s.Doc.Steps) != 1 {
		t.Fatalf("repairs %d steps %d", s.Metrics.Repairs, len(s.Doc.Steps))
	}
	if p := h.fake.prompt("tests/repair", 0); !strings.Contains(p, "has no test case") {
		t.Fatal(p)
	}
	// Endless garbage fails the plan after the repairs.
	h2 := newHarness(t, map[string]reply{"summary": static("no json"), "summary/repair": static("{broken")}, Config{Subagents: true, Repairs: 1})
	st := h2.newPlan()
	if err := h2.engine(st, nil).Plan(context.Background()); err == nil || !strings.Contains(err.Error(), "unusable answer") {
		t.Fatal(err)
	}
	if loaded, _ := h2.store.Load(st.Doc.ID); loaded.Doc.Status != api.PlanFailed {
		t.Fatal(loaded.Doc.Status)
	}
}

func TestRevise(t *testing.T) {
	h := newHarness(t, nil, Config{Subagents: true})
	e, _ := h.planned()
	if err := e.Revise(context.Background(), "Also check for overflow."); err != nil {
		t.Fatal(err)
	}
	s := e.State()
	if s.Doc.Status != api.PlanReview || len(s.Doc.Comments) != 1 || s.Doc.Steps[0].ID != "S1" || !strings.Contains(s.Doc.Steps[0].Title, "overflow") {
		t.Fatalf("%+v", s.Doc)
	}
	if p := h.fake.prompt("revise", 0); !strings.Contains(p, "Also check for overflow.") {
		t.Fatal(p)
	}
	if h.fake.count("summary") != 2 {
		t.Fatal("no second summary")
	}
}

func execute(t *testing.T, h *harness, st *State, ws Workspace) *Engine {
	t.Helper()
	st.Doc.Status = api.PlanAccepted
	e := h.engine(st, ws)
	if err := e.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExecuteFixLoop(t *testing.T) {
	// DEV first leaves a stub; the test fails and the unfinished gate
	// reports the stub; DEV fixes it in the second iteration.
	replies := map[string]reply{
		"dev": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if n == 0 {
				// Shell commands are rejected (ask, nobody watches), git
				// commits always.
				if tt.Permission(ctx, acp.ToolCall{ToolCallID: "c", Name: "bash", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"git commit -am x"}`)}) != "reject" {
					t.Error("git commit allowed")
				}
				if tt.Permission(ctx, acp.ToolCall{ToolCallID: "o", Name: "edit", Kind: acp.KindEdit, Locations: []acp.Location{{Path: "/etc/passwd"}}}) != "reject" {
					t.Error("edit outside allowed")
				}
				_ = tt.WriteFile(ctx, "src/Calc.php", "<?php\nclass Calc\n{\n    public function add(int $a, int $b): int\n    {\n        // TODO: implement\n        return 0;\n    }\n}\n")
				return map[string]any{"summary": "done!", "steps": []map[string]any{{"id": "S1", "status": "done"}}}
			}
			if !strings.Contains(tt.Prompt, "CalcTest::testAdds FAILED") || !strings.Contains(tt.Prompt, "added TODO") {
				t.Errorf("failures not fed back:\n%s", tt.Prompt)
			}
			var resp []map[string]any
			for _, id := range []string{"Q2", "Q3"} {
				resp = append(resp, map[string]any{"item": id, "verdict": "accept", "text": "fixed"})
			}
			r := defaultReplies["dev"](ctx, tt, n).(map[string]any)
			r["responses"] = resp
			return r
		},
	}
	h := newHarness(t, replies, Config{Subagents: true, MaxIterations: 4})
	_, st := h.planned()
	ws := NewInPlace(h.root, filepath.Join(t.TempDir(), "orig.json"), "")
	e := execute(t, h, st, ws)
	s := e.State()
	if s.Doc.Status != api.PlanDone {
		t.Fatalf("status %s, gates %+v, review %+v, err %s", s.Doc.Status, s.Doc.Gates, s.Doc.Review, s.Error)
	}
	if s.Iteration != 2 || s.Metrics.IterationsToGreen != 2 || !s.Doc.Criteria[0].Met || s.Doc.Tests[0].Status != TestPassed {
		t.Fatalf("iteration %d metrics %+v", s.Iteration, s.Metrics)
	}
	if !strings.Contains(s.Doc.Summary, "Calc::add adds now.") || !strings.Contains(s.Doc.Summary, "all gates passed") {
		t.Fatal(s.Doc.Summary)
	}
	if g := gate(s, "red"); g.Skipped == "" {
		t.Fatalf("red check in place: %+v", g)
	}
	// The critic ran in fresh sessions after every turn.
	if h.fake.count("critic-impl") != 2 || h.fake.count("critic-tests") != 2 {
		t.Fatal("critic", h.fake.count("critic-impl"), h.fake.count("critic-tests"))
	}
	// QA wrote the tests from the test plan without DEV's code.
	if p := h.fake.prompt("qa", 0); strings.Contains(p, "return $a + $b") || !strings.Contains(p, "adds two numbers") {
		t.Fatal(p)
	}
	// Changes, diff and revert in place.
	cs, _ := ws.Changes(context.Background())
	if len(cs) != 2 || !strings.Contains(cs[0].Unified(), "+        return $a + $b;") {
		t.Fatalf("%+v", cs)
	}
	if err := ws.Revert(context.Background(), "src/Calc.php"); err != nil || h.read("src/Calc.php") != baseCalc {
		t.Fatal(err)
	}
	if err := ws.Revert(context.Background(), "tests/CalcTest.php"); err != nil || fileExists(h.root, "tests/CalcTest.php") {
		t.Fatal("created file not removed", err)
	}
	if err := ws.Revert(context.Background(), "nope"); err == nil {
		t.Fatal("revert of unchanged file")
	}
}

func gate(s State, name string) api.PlanGate {
	for _, g := range s.Doc.Gates {
		if g.Name == name {
			return g
		}
	}
	return api.PlanGate{}
}

// disputeReplies: the critic says the implementation is wrong, DEV
// rejects, QA insists.
func disputeReplies(t *testing.T) map[string]reply {
	return map[string]reply{
		"critic-impl": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if n == 0 {
				return map[string]any{"issues": []map[string]any{{"severity": "major", "target": "impl", "claim": "add ignores overflow", "evidence": "src/Calc.php:6"}}}
			}
			return map[string]any{"issues": []any{}}
		},
		"dev": func(ctx context.Context, tt *acptest.Turn, n int) any {
			r := defaultReplies["dev"](ctx, tt, n).(map[string]any)
			if strings.Contains(tt.Prompt, "Q2 [major, from critic") {
				verdict := "reject"
				if strings.Contains(tt.Prompt, "DECIDED by") {
					verdict = "accept"
				}
				r["responses"] = []map[string]any{{"item": "Q2", "verdict": verdict, "text": "ints cannot overflow here", "evidence": "src/Calc.php:4"}}
			}
			return r
		},
		"rate-impl": func(ctx context.Context, tt *acptest.Turn, n int) any {
			r := map[string]any{"issues": []any{}}
			if strings.Contains(tt.Prompt, "- Q2 [major, from critic, rejected]") {
				r["rebuttals"] = []map[string]any{{"item": "Q2", "verdict": "insist", "text": "PHP_INT_MAX + 1 is a float"}}
			}
			return r
		},
		"mediator": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if !strings.Contains(tt.Prompt, "PHP_INT_MAX + 1 is a float") || !strings.Contains(tt.Prompt, "ints cannot overflow here") {
				t.Errorf("mediator does not see both positions:\n%s", tt.Prompt)
			}
			return map[string]any{"decisions": []map[string]any{{"item": "Q2", "winner": "qa", "reason": "overflow is real"}}}
		},
	}
}

func TestDebateMediatorAlways(t *testing.T) {
	h := newHarness(t, disputeReplies(t), Config{Subagents: true, Mediator: api.MediatorAlways, MaxIterations: 5})
	_, st := h.planned()
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	s := e.State()
	it := Review(&s.Doc, "Q2")
	if it == nil || it.Decision == nil || it.Decision.By != api.RoleMediator || it.Status != api.ReviewAccepted {
		t.Fatalf("%+v", it)
	}
	if s.Metrics.MediatorDecisions != 1 || s.Metrics.FindingsRejected != 1 || s.Metrics.Rounds < 1 || s.Doc.Status != api.PlanDone {
		t.Fatalf("%+v %s", s.Metrics, s.Doc.Status)
	}
	if len(s.Doc.Decisions) != 1 {
		t.Fatal(s.Doc.Decisions)
	}
}

func TestDebateMediatorAuto(t *testing.T) {
	// auto with 2 rounds: the first insist sends the item back to DEV,
	// the second goes to the mediator.
	h := newHarness(t, disputeReplies(t), Config{Subagents: true, Mediator: api.MediatorAuto, MediatorRounds: 2, MaxIterations: 5})
	_, st := h.planned()
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	s := e.State()
	it := Review(&s.Doc, "Q2")
	if it == nil || it.Decision == nil || it.Round != 2 || s.Metrics.FindingsRejected != 2 {
		t.Fatalf("%+v %+v", it, s.Metrics)
	}
	if h.fake.count("mediator") != 1 {
		t.Fatal(h.fake.count("mediator"))
	}
}

func TestDebateNoMediatorEscalates(t *testing.T) {
	h := newHarness(t, disputeReplies(t), Config{Subagents: true, Mediator: api.MediatorOff, MediatorRounds: 1, MaxIterations: 5})
	_, st := h.planned()
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	s := e.State()
	if s.Doc.Status != api.PlanNeedsUser || len(Disputes(&s.Doc)) != 1 || h.fake.count("mediator") != 0 {
		t.Fatalf("%s %+v", s.Doc.Status, Disputes(&s.Doc))
	}
	if err := e.Resolve("Q2", "bogus", ""); err == nil {
		t.Fatal("bad decision accepted")
	}
	if err := e.Resolve("Q9", "dev", ""); err == nil {
		t.Fatal("unknown item accepted")
	}
	// The user says DEV is right: the item is withdrawn, execution goes on.
	if err := e.Resolve("Q2", "dev", "ints are bounded by the caller"); err != nil {
		t.Fatal(err)
	}
	s = e.State()
	if it := Review(&s.Doc, "Q2"); it.Status != api.ReviewWithdrawn || it.Decision.By != "user" || s.Metrics.UserOverrides != 1 || s.Doc.Status != api.PlanExecuting {
		t.Fatalf("%+v %s", it, s.Doc.Status)
	}
	if err := e.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e.State(); s.Doc.Status != api.PlanDone {
		t.Fatal(s.Doc.Status, s.Doc.Gates)
	}
}

func TestMaxIterations(t *testing.T) {
	replies := map[string]reply{
		"dev": static(map[string]any{"summary": "all done, tests pass", "steps": []map[string]any{{"id": "S1", "status": "done"}}}),
	}
	h := newHarness(t, replies, Config{Subagents: true, MaxIterations: 2})
	_, st := h.planned()
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	s := e.State()
	if s.Doc.Status != api.PlanIncomplete || s.Iteration != 2 {
		t.Fatal(s.Doc.Status, s.Iteration)
	}
	// DEV claimed done; Tiegel's status says otherwise.
	for _, want := range []string{"NOT finished after 2 iterations", "gate tests failed", "criterion AC1 not met", "reported done, but none of its files changed"} {
		if !strings.Contains(s.Doc.Summary, want) {
			t.Errorf("summary misses %q:\n%s", want, s.Doc.Summary)
		}
	}
}

func TestSubagentsOffExecution(t *testing.T) {
	h := newHarness(t, nil, Config{Subagents: false})
	_, st := h.planned()
	before := len(h.fake.agent.NewSessions())
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	if s := e.State(); s.Doc.Status != api.PlanDone {
		t.Fatal(s.Doc.Status, s.Doc.Gates)
	}
	// One shared execution session for DEV and QA, fresh sessions for
	// the two critic passes and the final summary.
	if n := len(h.fake.agent.NewSessions()) - before; n != 4 || h.fake.maxRun != 1 {
		t.Fatalf("sessions %d max parallel %d", n, h.fake.maxRun)
	}
}

func TestCancelAndResume(t *testing.T) {
	started := make(chan struct{})
	replies := map[string]reply{
		"dev": func(ctx context.Context, tt *acptest.Turn, n int) any {
			if n == 0 {
				close(started)
				<-ctx.Done()
				return "cancelled"
			}
			return defaultReplies["dev"](ctx, tt, n)
		},
	}
	h := newHarness(t, replies, Config{Subagents: true})
	_, st := h.planned()
	st.Doc.Status = api.PlanAccepted
	e := h.engine(st, NewInPlace(h.root, "", ""))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Execute(ctx) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("no error after cancel")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel hangs")
	}
	loaded, err := h.store.Load(st.Doc.ID)
	if err != nil || loaded.Doc.Status != api.PlanCancelled || loaded.Iteration != 1 {
		t.Fatalf("%v %s %d", err, loaded.Doc.Status, loaded.Iteration)
	}
	// Resume from the stored state in a new engine (fresh sessions): the
	// interrupted iteration is repeated.
	e2 := h.engine(loaded, NewInPlace(h.root, "", ""))
	if err := e2.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e2.State(); s.Doc.Status != api.PlanDone {
		t.Fatal(s.Doc.Status)
	}
}

func TestMutationSurvivorAndHollow(t *testing.T) {
	// QA's first test asserts nothing; the runner passes everything, so
	// the reverted change survives.
	replies := map[string]reply{
		"qa": func(ctx context.Context, tt *acptest.Turn, n int) any {
			src := goodTest
			if n == 0 {
				src = strings.Replace(goodTest, "$this->assertSame(5, (new Calc())->add(2, 3));", "$this->assertTrue(true);", 1)
			}
			_ = tt.WriteFile(ctx, "tests/CalcTest.php", src)
			r := map[string]any{"summary": "ok", "tests": []map[string]any{{"case": "T1", "file": "tests/CalcTest.php", "tests": []string{"CalcTest::testAdds"}}}}
			if n > 0 {
				var resp []map[string]any
				for _, q := range []string{"Q2", "Q3", "Q4"} {
					resp = append(resp, map[string]any{"item": q, "verdict": "accept"})
				}
				r["responses"] = resp
			}
			return r
		},
	}
	h := newHarness(t, replies, Config{Subagents: true, Mutation: true, MaxIterations: 2})
	h.runner.pass = func(string) bool { return true }
	_, st := h.planned()
	e := execute(t, h, st, NewInPlace(h.root, "", ""))
	s := e.State()
	var hollow, mutant bool
	for _, it := range s.Doc.Review {
		hollow = hollow || it.From == "gate:hollow" && strings.Contains(it.Claim, "trivialities")
		mutant = mutant || it.From == "gate:mutation" && strings.Contains(it.Claim, "mutation survived")
	}
	if !hollow || !mutant {
		t.Fatalf("hollow %v mutant %v: %+v", hollow, mutant, s.Doc.Review)
	}
	if h.read("src/Calc.php") != strings.TrimSpace(h.read("src/Calc.php"))+"\n" || !strings.Contains(h.read("src/Calc.php"), "return $a + $b") {
		t.Fatal("mutant not restored:", h.read("src/Calc.php"))
	}
	if s.Doc.Status != api.PlanIncomplete {
		t.Fatal(s.Doc.Status)
	}
}

func TestMCPInbox(t *testing.T) {
	h := newHarness(t, map[string]reply{
		"arch": func(ctx context.Context, tt *acptest.Turn, n int) any {
			// What the MCP server process would write while the turn runs.
			id := strings.TrimSpace(strings.SplitN(strings.SplitN(tt.Prompt, "plan id: ", 2)[1], "\n", 2)[0])
			_ = h0.store.Propose(api.PlanProposeParams{ID: id, Role: api.RolePlannerArch, Op: "finding", Claim: "via mcp", Evidence: "src/Calc.php:1"})
			_ = h0.store.Propose(api.PlanProposeParams{ID: id, Role: api.RolePlannerArch, Op: "update", Section: SecSummary, Content: "not allowed"})
			return defaultReplies["arch"](ctx, tt, n)
		},
	}, Config{Subagents: true, MCP: true})
	h0 = h
	st := h.newPlan()
	st.Doc.Goal += "\nplan id: " + st.Doc.ID
	e := h.engine(st, nil)
	if err := e.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := e.State()
	found := false
	for _, f := range s.Doc.Findings {
		found = found || f.Claim == "via mcp" && strings.Contains(f.By, "mcp")
	}
	if !found || len(s.Notes) == 0 {
		t.Fatalf("%+v %v", s.Doc.Findings, s.Notes)
	}
	if p := h.fake.prompt("arch", 0); !strings.Contains(p, "plan_read(section)") {
		t.Fatal("MCP tools not explained")
	}
}

var h0 *harness
