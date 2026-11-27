// Package orchestratetest scripts plan mode roles on a fake ACP agent
// (acptest) and provides a fake test runner, for the tests of the
// planmode module, headless and the GUI. No network, no real agent.
package orchestratetest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/orchestrate"
)

// Reply answers one role call (n counts the calls of the role); a string
// is sent as is, anything else as fenced JSON.
type Reply func(ctx context.Context, t *acptest.Turn, n int) any

var markers = []struct{ marker, role string }{
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

// Fake is a scripted plan mode agent.
type Fake struct {
	Agent   *acptest.Agent
	Replies map[string]Reply

	mu    sync.Mutex
	calls map[string]int
}

// New returns a fake with the default replies (a plan that makes
// Calc::add add), overridden by replies.
func New(replies map[string]Reply) *Fake {
	f := &Fake{Replies: map[string]Reply{}, calls: map[string]int{}}
	for k, v := range Defaults {
		f.Replies[k] = v
	}
	for k, v := range replies {
		f.Replies[k] = v
	}
	f.Agent = &acptest.Agent{Script: f.script}
	return f
}

// Calls returns how often role was asked.
func (f *Fake) Calls(role string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[role]
}

func (f *Fake) script(ctx context.Context, t *acptest.Turn) string {
	role := ""
	for _, m := range markers {
		if strings.Contains(t.Prompt, m.marker) {
			role = m.role
			break
		}
	}
	if role == "critic" {
		role = "critic-impl"
		if strings.Contains(t.Prompt, "## Diff of the tests") {
			role = "critic-tests"
		}
	}
	f.mu.Lock()
	n := f.calls[role]
	f.calls[role]++
	r := f.Replies[role]
	f.mu.Unlock()
	if r == nil {
		t.Text("{}")
		return ""
	}
	v := r(ctx, t, n)
	if ctx.Err() != nil {
		return acp.StopCancelled
	}
	if s, ok := v.(string); ok {
		t.Text(s)
		return ""
	}
	b, _ := json.Marshal(v)
	t.Thought("Checking the code.")
	t.Text("```json\n" + string(b) + "\n```")
	return ""
}

// Static always answers v.
func Static(v any) Reply { return func(context.Context, *acptest.Turn, int) any { return v } }

// BaseCalc is the code before the plan; GoodTest QA's test.
const (
	BaseCalc = "<?php\nclass Calc\n{\n    public function add(int $a, int $b): int\n    {\n        return 0;\n    }\n}\n"
	GoodCalc = "<?php\nclass Calc\n{\n    public function add(int $a, int $b): int\n    {\n        return $a + $b;\n    }\n}\n"
	GoodTest = "<?php\nuse PHPUnit\\Framework\\TestCase;\n\nfinal class CalcTest extends TestCase\n{\n    public function testAdds(): void\n    {\n        $this->assertSame(5, (new Calc())->add(2, 3));\n    }\n}\n"
)

// Defaults plan and execute "Calc::add returns the sum".
var Defaults = map[string]Reply{
	"arch": Static(map[string]any{
		"context":  "Calc lives in `src/Calc.php:2`.",
		"steps":    []map[string]any{{"title": "Implement Calc::add", "files": []string{"src/Calc.php"}}},
		"findings": []map[string]any{{"claim": "`Calc::add` returns 0", "evidence": "src/Calc.php:6"}},
	}),
	"code": Static(map[string]any{"risks": []map[string]any{{"text": "callers expect 0", "kind": "question"}}}),
	"tests": Static(map[string]any{"criteria": []map[string]any{{"text": "add returns the sum", "tests": []map[string]any{
		{"title": "adds two numbers", "given": "a calc", "when": "add(2,3)", "then": "5", "file": "tests/CalcTest.php", "kind": "new"}}}}}),
	"validator": Static(map[string]any{"findings": []map[string]any{{"id": "F1", "status": "verified"}}}),
	"qa-review": Static(map[string]any{"issues": []map[string]any{{"severity": "minor", "section": "tests", "claim": "negative numbers", "suggestion": "add a case"}}}),
	"summary":   Static(map[string]any{"summary": "Calc::add will return the sum; T1 tests it.", "resolutions": []map[string]any{{"item": "Q1", "resolution": "later"}}}),
	"revise":    Static(map[string]any{"replace": []string{"plan"}, "steps": []map[string]any{{"id": "S1", "title": "Implement Calc::add (revised)", "files": []string{"src/Calc.php"}}}}),
	"dev": func(ctx context.Context, t *acptest.Turn, n int) any {
		_ = t.WriteFile(ctx, "src/Calc.php", GoodCalc)
		return map[string]any{"summary": "done", "steps": []map[string]any{{"id": "S1", "status": "done"}}}
	},
	"qa": func(ctx context.Context, t *acptest.Turn, n int) any {
		_ = t.WriteFile(ctx, "tests/CalcTest.php", GoodTest)
		return map[string]any{"summary": "written", "tests": []map[string]any{{"case": "T1", "file": "tests/CalcTest.php", "tests": []string{"CalcTest::testAdds"}}}}
	},
	"critic-impl":  Static(map[string]any{"issues": []any{}}),
	"critic-tests": Static(map[string]any{"issues": []any{}}),
	"rate-impl":    Static(map[string]any{"issues": []any{}}),
	"rate-tests":   Static(map[string]any{"issues": []any{}}),
	"mediator":     Static(map[string]any{"decisions": []map[string]any{{"item": "Q2", "winner": "qa", "reason": "the test is right"}}}),
	"final":        Static(map[string]any{"summary": "Calc::add returns the sum; CalcTest passes."}),
}

// Runner is a fake test runner: CalcTest::testAdds passes when
// src/Calc.php adds; on a base tree (path ends in -base) it fails.
type Runner struct {
	mu   sync.Mutex
	Runs []orchestrate.TestRequest
}

// Run implements orchestrate.TestRunner.
func (r *Runner) Run(_ context.Context, req orchestrate.TestRequest) (orchestrate.TestReport, error) {
	r.mu.Lock()
	r.Runs = append(r.Runs, req)
	r.mu.Unlock()
	if _, err := os.Stat(filepath.Join(req.Dir, "tests", "CalcTest.php")); err != nil {
		return orchestrate.TestReport{Error: "no tests"}, nil
	}
	src, _ := os.ReadFile(filepath.Join(req.Dir, "src", "Calc.php"))
	res := orchestrate.TestResult{ID: "CalcTest::testAdds", File: "tests/CalcTest.php", Status: orchestrate.TestPassed}
	if !strings.Contains(string(src), "return $a + $b") {
		res.Status, res.Message, res.FailFile, res.FailLine = orchestrate.TestFailed, "Failed asserting that 0 is identical to 5.", "tests/CalcTest.php", 8
	}
	rep := orchestrate.TestReport{Tests: []orchestrate.TestResult{res}}
	if req.Coverage {
		rep.CoverageNote = "no coverage driver (fake)"
	}
	return rep, nil
}

// Count returns the number of runs.
func (r *Runner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Runs)
}
