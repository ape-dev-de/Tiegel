package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

const failID = `Tests\CalcTest::testFail`

func lightRun(id, project string) api.TestRun {
	return api.TestRun{RunID: id, Name: id, State: run.StateExited, Project: project, Light: true,
		Counts: teamcity.Counts{Total: 1, Failed: 1},
		Tree: []teamcity.Node{{ID: `Tests\CalcTest`, Kind: teamcity.KindClass, Status: teamcity.StatusFailed, Children: []*teamcity.Node{
			{ID: failID, Kind: teamcity.KindTest, Status: teamcity.StatusFailed, Comparison: true},
		}}}}
}

// A test run of another project (one that ended after a project switch)
// is not taken over (D381).
func TestTestsIgnoreRunOfOtherProject(t *testing.T) {
	f := projectFake()
	f.answer("test.run", api.TestRun{RunID: "r-other", State: run.StateRunning, Project: "/elsewhere"})
	c := started(t, f)
	tests := c.Tests
	for _, topic := range []string{api.TopicTestStarted, api.TopicTestUpdated, api.TopicTestFinished} {
		f.emit(topic, lightRun("r-old", "/elsewhere"))
	}
	c.Settle(time.Second)
	if tests.Run != nil || len(tests.Last) != 0 || tests.Started {
		t.Fatalf("run of another project taken over: %+v %v", tests.Run, tests.Last)
	}
	tests.RunTests(api.TestScope{}, false)
	c.Settle(time.Second)
	if tests.Run != nil {
		t.Fatalf("started run of another project shown: %+v", tests.Run)
	}
	// Runs of the open project (and runs without project) are shown.
	f.emit(api.TopicTestFinished, lightRun("r-1", root+"/"))
	c.Settle(time.Second)
	if tests.Run == nil || tests.Run.RunID != "r-1" || tests.StatusOf(failID) != teamcity.StatusFailed {
		t.Fatalf("own run %+v", tests.Run)
	}
	f.emit(api.TopicTestStarted, lightRun("r-2", ""))
	c.Settle(time.Second)
	if tests.Run.RunID != "r-2" {
		t.Fatalf("run without project %+v", tests.Run)
	}
}

// The selected node comes in full from test.node, again when the run
// changed (D380).
func TestTestsDetailOnDemand(t *testing.T) {
	f := projectFake()
	f.on("test.node", func(raw json.RawMessage) (any, error) {
		var p api.TestNodeParams
		_ = json.Unmarshal(raw, &p)
		if p.ID != failID {
			return nil, api.Errorf(api.CodeNotFound, "no test")
		}
		return teamcity.Node{ID: p.ID, Kind: teamcity.KindTest, Status: teamcity.StatusFailed, Comparison: true,
			Expected: "'foo'", Actual: "'bar'", Output: "printed " + p.RunID}, nil
	})
	c := started(t, f)
	tests := c.Tests
	if tests.Detail(failID) != nil {
		t.Fatal("detail without run")
	}
	f.emit(api.TopicTestFinished, lightRun("r-1", root))
	c.Settle(time.Second)
	if tests.Detail(failID) != nil {
		t.Fatal("detail before the answer")
	}
	c.Settle(time.Second)
	n := tests.Detail(failID)
	if n == nil || n.Expected != "'foo'" || n.Output != "printed r-1" || tests.DetailVersion == 0 {
		t.Fatalf("detail %+v", n)
	}
	// Unchanged run: no new request.
	tests.Detail(failID)
	c.Settle(time.Second)
	if got := f.called("test.node"); got != 1 {
		t.Fatalf("test.node called %d times", got)
	}
	// The run changed: fetched again (the old detail stays meanwhile).
	f.emit(api.TopicTestUpdated, lightRun("r-1", root))
	c.Settle(time.Second)
	if tests.Detail(failID) == nil {
		t.Fatal("detail dropped while reloading")
	}
	c.Settle(time.Second)
	if got := f.called("test.node"); got != 2 {
		t.Fatalf("test.node called %d times after an update", got)
	}
	// Another run: no stale detail.
	f.emit(api.TopicTestStarted, lightRun("r-2", root))
	c.Settle(time.Second)
	if tests.Detail(failID) != nil {
		t.Fatal("detail of the previous run")
	}
	c.Settle(time.Second)
	if n := tests.Detail(failID); n == nil || n.Output != "printed r-2" {
		t.Fatalf("detail r-2 %+v", n)
	}
	// Unknown node: nil, no request loop.
	if tests.Detail("nope") != nil {
		t.Fatal("unknown node")
	}
	c.Settle(time.Second)
	before := f.called("test.node")
	if tests.Detail("nope") != nil {
		t.Fatal("unknown node after answer")
	}
	c.Settle(time.Second)
	if f.called("test.node") != before {
		t.Fatal("unknown node fetched again")
	}
	tests.ProjectChanged()
}
