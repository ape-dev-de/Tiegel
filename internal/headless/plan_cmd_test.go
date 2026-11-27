package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/planmode"
	ot "github.com/ape-dev-de/tiegel/internal/orchestrate/orchestratetest"
)

func runPlanCLI(t *testing.T, cfg string, fake *ot.Fake, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := testEnv(t, strings.NewReader(stdin), &out, &errb)
	env.Options.ConfigDir = cfg
	env.Modules.PlanMode = planmode.Options{Start: fake.Agent.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }, Tests: &ot.Runner{}}
	code := Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func TestPlanCommands(t *testing.T) {
	dir := project(t, map[string]string{"src/Calc.php": ot.BaseCalc, "composer.json": "{}"})
	cfg := filepath.Join(t.TempDir(), "cfg")
	fake := ot.New(nil)
	h := func(args ...string) (int, string, string) {
		return runPlanCLI(t, cfg, fake, "", append([]string{"--headless", "--project", dir}, args...)...)
	}
	// Without --yes only the estimate.
	code, out, errs := h("plan", "start", "Make", "Calc::add", "add")
	if code != 0 || !strings.Contains(out, "nothing was run; add --yes") || fake.Calls("arch") != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, errs = h("settings", "set", "planmode.workspace", "in-place")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, errs = h("plan", "start", "Make", "Calc::add", "add", "--no-subagents", "--yes")
	if code != 0 || !strings.Contains(out, "== draft") || !strings.Contains(out, "planner-architecture: done") || !strings.Contains(out, "state: awaiting-acceptance") ||
		!strings.Contains(out, "next: tiegel --headless plan show | accept") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = h("plan", "show", "--section", "plan")
	if code != 0 || !strings.Contains(out, "**S1** Implement Calc::add") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "list")
	if code != 0 || !strings.Contains(out, "awaiting-acceptance") {
		t.Fatal(out)
	}
	if code, _, _ = h("plan", "execute", "--yes"); code == 0 {
		t.Fatal("executed before accept")
	}
	code, out, _ = h("plan", "accept", "--comment", "ok")
	if code != 0 || !strings.Contains(out, "state: accepted") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "execute")
	if code != 0 || !strings.Contains(out, "add --yes") || fake.Calls("dev") != 0 {
		t.Fatal(out)
	}
	code, out, errs = h("plan", "execute", "--yes")
	if code != 0 || !strings.Contains(out, "state: done") || !strings.Contains(out, "gate tests      passed") || !strings.Contains(out, "== implement") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = h("plan", "diff")
	if code != 0 || !strings.Contains(out, "+        return $a + $b;") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "transcript", "dev")
	if code != 0 || !strings.Contains(out, "Role: senior DEV") && !strings.Contains(out, "senior DEV") {
		t.Fatal(out)
	}
	code, out, _ = h("--json", "plan", "status")
	var st api.PlanStatus
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.State != api.PlanDone || st.Metrics.Turns == 0 {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "revert", "tests/CalcTest.php")
	if code != 0 || !strings.Contains(out, "1 file(s) still changed") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "resolve", "Q9", "dev")
	if code == 0 {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "eval", "x")
	if code != 0 || !strings.Contains(out, "add --yes") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "eval", "Make", "Calc::add", "add", "--modes", "off", "--yes")
	if code != 0 || !strings.Contains(out, "mediator  state") || !strings.Contains(out, "\noff ") || !strings.Contains(out, "provisional") {
		t.Fatal(out)
	}
	code, out, _ = h("plan", "apply", st.ID)
	if code != 0 || !strings.Contains(out, "state: applied") {
		t.Fatal(out)
	}
	if code, _, _ = h("plan", "bogus"); code == 0 {
		t.Fatal("unknown sub command")
	}
}

func TestPlanMCP(t *testing.T) {
	dir := project(t, map[string]string{"src/Calc.php": ot.BaseCalc})
	cfg := filepath.Join(t.TempDir(), "cfg")
	fake := ot.New(nil)
	code, out, errs := runPlanCLI(t, cfg, fake, "", "--headless", "--project", dir, "settings", "set", "planmode.workspace", "in-place")
	if code != 0 {
		t.Fatal(out, errs)
	}
	code, out, errs = runPlanCLI(t, cfg, fake, "", "--headless", "--project", dir, "--json", "plan", "start", "Make", "Calc::add", "add", "--yes")
	var st api.PlanStatus
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plan_read","arguments":{"section":"criteria"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"plan_add_finding","arguments":{"claim":"c","evidence":"src/Calc.php:1"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"plan_comment","arguments":{"item":"Q1","text":"t"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"plan_update","arguments":{"section":"context","content":"x"}}}`,
	}, "\n") + "\n"
	code, out, errs = runPlanCLI(t, cfg, fake, in, "--headless", "--project", dir, "mcp", "plan", "--plan", st.ID, "--role", "planner-architecture")
	if code != 0 || !strings.Contains(out, "plan_add_finding") || !strings.Contains(out, "add returns the sum") || strings.Count(out, "Proposed; Tiegel merges it") != 3 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, _, _ = runPlanCLI(t, cfg, fake, "", "--headless", "--project", dir, "mcp", "plan")
	if code == 0 {
		t.Fatal("mcp plan without plan and role")
	}
}
