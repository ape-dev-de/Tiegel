package gui

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// The run is the top node of the result tree: selected it shows the
// console output (header and summary of the runner); results are green,
// orange and red; the header shows the progress.
func TestTestDockConsoleColorsProgress(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("test discovery needs Tree-sitter (cgo)")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	e := newEnvWith(t, true, testEnvFiles(), nil)
	t.Cleanup(api.HookRunInterpreter.On(e.app.Hooks(), -10, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
		q.Interpreter, q.Err = interp.NewLocal(php), ""
		return q, nil
	}))
	e.v.ShowTool(ToolTests)
	e.ctl.Tests.RunTests(api.TestScope{}, false)
	e.waitFor("run", func() bool {
		r := e.ctl.Tests.Run
		return r != nil && r.State != run.StateRunning && r.Counts.Total == 2
	})
	e.settle()
	d := e.v.tstd
	if d.root == nil || len(d.tree.Roots) != 1 || d.tree.Roots[0] != d.root || d.root.Tone != toneDanger {
		t.Fatalf("root %+v", d.root)
	}
	// The first failure is selected, not the run.
	if sel := d.data[d.tree.sel]; sel == nil || sel.ID != `Tests\CalcTest::testFail` || d.tree.sel.Tone != toneDanger {
		t.Fatalf("selected %+v", sel)
	}
	for rn, n := range d.data {
		if n.ID == `Tests\CalcTest::testAdd` && rn.Tone != toneSuccess {
			t.Fatalf("passed tone %v", rn.Tone)
		}
	}
	// The run node shows the console.
	d.tree.sel = d.root
	e.waitFor("console", func() bool {
		n := e.ctl.Tests.Detail("")
		return n != nil && strings.Contains(n.Output, "Fake PHPUnit") && strings.Contains(n.Output, "FAILURES!")
	})
	lines := strings.Join(detailLines(e.ctl.Tests.Detail(""), e.ctl.Rel), "\n")
	if strings.Contains(lines, "##teamcity") || !strings.Contains(lines, "FAILURES!") || !strings.HasPrefix(lines, e.ctl.Tests.Run.Name) {
		t.Fatalf("console lines %q", lines)
	}
	if p := runProgress(e.ctl.Tests.Run); p.Value != 1 || !p.Danger {
		t.Fatalf("progress %+v", p)
	}
	e.frame()
}

func TestRunProgressAndTones(t *testing.T) {
	r := &api.TestRun{State: run.StateRunning}
	if p := runProgress(r); !p.Indeterminate() {
		t.Fatalf("no count: %+v", p)
	}
	r.Counts = teamcity.Counts{Expected: 4, Passed: 1, Ignored: 1}
	if p := runProgress(r); p.Value != 0.5 || p.Danger {
		t.Fatalf("half: %+v", p)
	}
	r.State, r.Counts = run.StateExited, teamcity.Counts{Total: 2, Passed: 2}
	if p := runProgress(r); p.Value != 1 || !p.Success || p.Danger {
		t.Fatalf("passed: %+v", p)
	}
	for st, want := range map[teamcity.Status]nodeTone{
		teamcity.StatusPassed: toneSuccess, teamcity.StatusIgnored: toneWarning, teamcity.StatusFailed: toneDanger,
		teamcity.StatusTerminated: toneDanger, teamcity.StatusRunning: toneInfo, "": toneNone,
	} {
		if statusTone(st) != want {
			t.Errorf("tone %q", st)
		}
	}
	if lines := detailLines(&teamcity.Node{Name: "All", Kind: teamcity.KindSuite, Status: teamcity.StatusPassed}, func(s string) string { return s }); lines[0] != "All" || lines[len(lines)-1] != "No output." {
		t.Fatalf("empty suite %q", lines)
	}
}
