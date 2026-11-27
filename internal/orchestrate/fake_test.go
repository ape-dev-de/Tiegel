package orchestrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Role markers of the prompt templates (first line of each task).
var roleMarkers = []struct{ marker, role string }{
	{"Role: architecture and data flow planner", "arch"},
	{"Role: affected code and edge cases planner", "code"},
	{"Role: test strategy planner", "tests"},
	{"Role: validator", "validator"},
	{"Role: senior QA. Challenge the plan", "qa-review"},
	{"Role: summarizer. Write the summary the user reads", "summary"},
	{"Role: planner, revision round", "revise"},
	{"Role: senior DEV. Implement", "dev"},
	{"Role: senior QA. Write the tests", "qa"},
	{"Role: critic", "critic"},
	{"Role: senior QA rating DEV", "rate-impl"},
	{"Role: senior DEV rating QA", "rate-tests"},
	{"Role: mediator", "mediator"},
	{"Role: summarizer. Write the final report", "final"},
}

func roleOf(prompt string) string {
	for _, m := range roleMarkers {
		if strings.Contains(prompt, m.marker) {
			return m.role
		}
	}
	return ""
}

// reply is a scripted answer: the JSON (marshalled) or raw text.
type reply func(ctx context.Context, t *acptest.Turn, n int) any

// fakeAgent scripts the roles of a plan on an acptest agent.
type fakeAgent struct {
	t       *testing.T
	agent   *acptest.Agent
	mu      sync.Mutex
	replies map[string]reply
	calls   map[string]int
	last    map[string]string // session → role (for repairs)
	prompts map[string][]string
	running int
	maxRun  int
}

func newFake(t *testing.T, replies map[string]reply) *fakeAgent {
	f := &fakeAgent{t: t, replies: replies, calls: map[string]int{}, last: map[string]string{}, prompts: map[string][]string{}}
	f.agent = &acptest.Agent{Script: f.script}
	return f
}

func (f *fakeAgent) script(ctx context.Context, t *acptest.Turn) string {
	f.mu.Lock()
	role := roleOf(t.Prompt)
	if role == "" && strings.Contains(t.Prompt, "previous answer could not be used") {
		role = f.last[t.SessionID] + "/repair"
	}
	if role == "critic" {
		if strings.Contains(t.Prompt, "## Diff of the tests") {
			role = "critic-tests"
		} else {
			role = "critic-impl"
		}
	}
	f.last[t.SessionID] = strings.TrimSuffix(role, "/repair")
	n := f.calls[role]
	f.calls[role]++
	f.prompts[role] = append(f.prompts[role], t.Prompt)
	f.running++
	f.maxRun = max(f.maxRun, f.running)
	r := f.replies[role]
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	// Give parallel roles a chance to overlap.
	time.Sleep(5 * time.Millisecond)
	if r == nil {
		r = defaultReplies[strings.TrimSuffix(role, "/repair")]
	}
	if r == nil {
		f.t.Errorf("no reply for role %q", role)
		t.Text("{}")
		return ""
	}
	v := r(ctx, t, n)
	if ctx.Err() != nil {
		return acp.StopCancelled
	}
	switch x := v.(type) {
	case string:
		t.Text(x)
	default:
		b, _ := json.Marshal(x)
		t.Text("Here is my answer.\n```json\n" + string(b) + "\n```")
	}
	return ""
}

func (f *fakeAgent) count(role string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[role]
}

func (f *fakeAgent) prompt(role string, i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 {
		i += len(f.prompts[role])
	}
	if i < 0 || i >= len(f.prompts[role]) {
		return ""
	}
	return f.prompts[role][i]
}

func static(v any) reply { return func(context.Context, *acptest.Turn, int) any { return v } }

// defaultReplies plan a tiny change: Calc::add must add.
var defaultReplies = map[string]reply{
	"arch": static(map[string]any{
		"context":  "Calc lives in `src/Calc.php:3`.",
		"steps":    []map[string]any{{"title": "Implement Calc::add", "files": []string{"src/Calc.php"}}},
		"findings": []map[string]any{{"claim": "`Calc::add` exists", "evidence": "src/Calc.php:4"}},
		"risks":    []map[string]any{{"text": "Callers rely on the old result", "kind": "risk"}},
	}),
	"code": static(map[string]any{
		"findings": []map[string]any{{"claim": "`Ghost` handles nulls", "evidence": "src/Ghost.php:10"}},
	}),
	"tests": static(map[string]any{
		"criteria": []map[string]any{{"text": "add returns the sum", "tests": []map[string]any{
			{"title": "adds two numbers", "given": "a calc", "when": "add(2,3)", "then": "5", "file": "tests/CalcTest.php", "kind": "new"}}}},
	}),
	"validator": static(map[string]any{"findings": []map[string]any{
		{"id": "F1", "status": "verified", "note": "seen"}, {"id": "F2", "status": "verified", "note": "trusting"}}}),
	"qa-review": static(map[string]any{"issues": []map[string]any{
		{"severity": "major", "section": "tests", "claim": "negative numbers are not tested", "suggestion": "add a case"}}}),
	"summary": static(map[string]any{"summary": "Make add add.", "resolutions": []map[string]any{{"item": "Q1", "resolution": "covered by T1"}}}),
	"dev": func(ctx context.Context, t *acptest.Turn, n int) any {
		_ = t.WriteFile(ctx, "src/Calc.php", "<?php\nclass Calc\n{\n    public function add(int $a, int $b): int\n    {\n        return $a + $b;\n    }\n}\n")
		return map[string]any{"summary": "done", "steps": []map[string]any{{"id": "S1", "status": "done"}}}
	},
	"qa": func(ctx context.Context, t *acptest.Turn, n int) any {
		_ = t.WriteFile(ctx, "tests/CalcTest.php", goodTest)
		return map[string]any{"summary": "tests written", "tests": []map[string]any{{"case": "T1", "file": "tests/CalcTest.php", "tests": []string{"CalcTest::testAdds"}}}}
	},
	"critic-impl":  static(map[string]any{"issues": []any{}}),
	"critic-tests": static(map[string]any{"issues": []any{}}),
	"rate-impl":    static(map[string]any{"issues": []any{}}),
	"rate-tests":   static(map[string]any{"issues": []any{}}),
	"mediator":     static(map[string]any{"decisions": []map[string]any{{"item": "Q1", "winner": "qa", "reason": "the test is right"}}}),
	"final":        static(map[string]any{"summary": "Calc::add adds now."}),
	"revise": static(map[string]any{"replace": []string{"plan"}, "steps": []map[string]any{
		{"id": "S1", "title": "Implement Calc::add with overflow check", "files": []string{"src/Calc.php"}}}}),
}

const goodTest = `<?php
use PHPUnit\Framework\TestCase;

final class CalcTest extends TestCase
{
    public function testAdds(): void
    {
        $this->assertSame(5, (new Calc())->add(2, 3));
    }
}
`

const baseCalc = "<?php\nclass Calc\n{\n    public function add(int $a, int $b): int\n    {\n        return 0;\n    }\n}\n"

// calcRunner passes CalcTest::testAdds when src/Calc.php adds.
type calcRunner struct {
	mu   sync.Mutex
	runs []TestRequest
	// pass overrides the decision.
	pass func(dir string) bool
}

func (r *calcRunner) Run(_ context.Context, req TestRequest) (TestReport, error) {
	r.mu.Lock()
	r.runs = append(r.runs, req)
	r.mu.Unlock()
	if _, err := os.Stat(filepath.Join(req.Dir, "tests/CalcTest.php")); err != nil {
		return TestReport{Error: "no tests"}, nil
	}
	src, _ := os.ReadFile(filepath.Join(req.Dir, "src/Calc.php"))
	ok := strings.Contains(string(src), "return $a + $b")
	if r.pass != nil {
		ok = r.pass(req.Dir)
	}
	res := TestResult{ID: "CalcTest::testAdds", File: "tests/CalcTest.php", Status: TestPassed}
	if !ok {
		res.Status, res.Message, res.FailFile, res.FailLine = TestFailed, "Failed asserting that 0 is identical to 5.", "tests/CalcTest.php", 8
	}
	return TestReport{Tests: []TestResult{res}}, nil
}

func (r *calcRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

// harness is a project with a plan engine on the fake agent.
type harness struct {
	t      *testing.T
	root   string
	store  *Store
	fake   *fakeAgent
	agent  *ACPAgent
	runner *calcRunner
	cfg    Config
	mu     sync.Mutex
	acts   []string
	ws     Workspace
}

func newHarness(t *testing.T, replies map[string]reply, cfg Config) *harness {
	t.Helper()
	root := t.TempDir()
	for p, c := range map[string]string{"src/Calc.php": baseCalc, "composer.json": "{}"} {
		if err := writeFile(root, p, []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{t: t, root: root, store: &Store{Dir: filepath.Join(t.TempDir(), "plans")}, fake: newFake(t, replies), runner: &calcRunner{}, cfg: cfg}
	a, err := StartACP(context.Background(), ACPOptions{Start: h.fake.agent.Start, Command: acp.Command{Path: "fake"}, ProjectRoot: root,
		Policy: api.AgentPolicy{Edit: api.PolicyAllow, Shell: api.PolicyAsk, Web: api.PolicyDeny, Other: api.PolicyAsk},
		OnEdit: func(abs string) {
			h.mu.Lock()
			ws := h.ws
			h.mu.Unlock()
			if ws != nil {
				ws.BeforeEdit(abs)
			}
		},
		OnActivity: func(role string, e api.AgentEntry, _ string) {
			h.mu.Lock()
			h.acts = append(h.acts, role+":"+e.Kind)
			h.mu.Unlock()
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	h.agent = a
	return h
}

func (h *harness) engine(st *State, ws Workspace) *Engine {
	h.mu.Lock()
	h.ws = ws
	h.mu.Unlock()
	return New(h.cfg, Deps{Agent: h.agent, Store: h.store, Root: h.root, Workspace: ws, Tests: h.runner}, st)
}

func (h *harness) newPlan() *State {
	id := NewID(time.Now())
	return &State{Doc: NewDocument(id, "Make Calc::add return the sum. It returns 0 today.", time.Now())}
}

func (h *harness) planned() (*Engine, *State) {
	h.t.Helper()
	st := h.newPlan()
	e := h.engine(st, nil)
	if err := e.Plan(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	return e, st
}

func (h *harness) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(h.root, rel))
	return string(b)
}
