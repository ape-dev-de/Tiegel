package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules"
	"github.com/ape-dev-de/tiegel/internal/modules/planmode"
	ot "github.com/ape-dev-de/tiegel/internal/orchestrate/orchestratetest"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
)

// TestPlanMode drives plan mode through the agent sidebar with a
// scripted agent: checkbox, plan, acceptance, execution, transcript.
func TestPlanMode(t *testing.T) {
	fake := ot.New(nil)
	envModules = modules.Options{PlanMode: planmode.Options{Start: fake.Agent.Start,
		LookPath: func(n string) (string, error) { return "/bin/" + n, nil }, Tests: &ot.Runner{}}}
	t.Cleanup(func() { envModules = modules.Options{} })
	e := newEnv(t, true)
	if err := os.WriteFile(filepath.Join(e.root, "src", "Calc.php"), []byte(ot.BaseCalc), 0o644); err != nil {
		t.Fatal(err)
	}
	e.v.ShowTool(ToolAgent)
	e.settle()
	d, p := e.v.agtd, e.ctl.Plan
	if !d.chat.PlanAvailable || d.chat.Body != nil {
		t.Fatal("no plan mode checkbox")
	}
	// The checkbox turns Send into "Plan".
	d.layoutEvent(agentchat.PlanModeEvent{On: true})
	d.layoutEvent(agentchat.SendEvent{Text: "Make Calc::add return the sum.", Plan: true})
	e.waitFor("plan to review", func() bool { return p.Status.State == api.PlanReview && !p.Status.Running })
	e.settle()
	if d.chat.Body == nil || d.plan.State != api.PlanReview || len(d.plan.Roles) < 6 || fake.Calls("dev") != 0 || d.plan.Summary == "" {
		t.Fatalf("%+v", d.plan)
	}
	e.shot("plan-review")
	p.Accept()
	e.waitFor("plan done", func() bool { return p.Status.State == api.PlanDone && !p.Status.Running && len(p.Changes) == 2 })
	e.settle()
	if len(d.plan.Gates) == 0 || len(d.plan.Changes) != 2 || d.plan.Worktree {
		t.Fatalf("%+v", d.plan)
	}
	e.shot("plan-done")
	p.ShowRole(api.RoleDev)
	e.waitFor("dev transcript", func() bool { return len(p.Transcript) > 1 })
	e.settle()
	if len(d.roleChat.Entries) < 2 {
		t.Fatal("no transcript")
	}
	e.shot("plan-transcript")
	p.ShowRole("")
	p.OpenDiff("src/Calc.php")
	e.waitFor("diff tab", func() bool { return len(e.ctl.Work.Diffs()) == 1 })
	p.Apply()
	e.waitFor("applied", func() bool { return p.Status.State == api.PlanApplied })
	d.layoutEvent(agentchat.PlanModeEvent{On: false})
	e.settle()
	if d.chat.Body != nil {
		t.Fatal("plan view without plan mode")
	}
	if roleTitle("x") != "x" || elapsed(api.PlanRole{ElapsedMs: 125000}) != "2m05s" {
		t.Fatal("format")
	}
}
