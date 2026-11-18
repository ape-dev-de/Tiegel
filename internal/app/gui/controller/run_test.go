package controller

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/run"
)

func TestRunsUI(t *testing.T) {
	f := projectFake()
	var mu sync.Mutex
	var starts []api.RunStartParams
	seq := 0
	f.on("run.start", func(raw json.RawMessage) (any, error) {
		var p api.RunStartParams
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, p)
		seq++
		return run.Info{ID: "run-" + string(rune('0'+seq)), Name: "x", State: run.StateRunning, Project: root}, nil
	})
	cfgs := []api.RunConfigInfo{
		{Config: run.Config{ID: "a", Name: "Script", Type: run.TypePHPScript}, Available: true},
		{Config: run.Config{ID: "b", Name: "Tests", Type: run.TypeCommand}, Available: true, Selected: true},
	}
	f.answer("run.configs.list", cfgs)
	f.answer("run.configTypes", []api.RunConfigTypeInfo{{ConfigType: run.ConfigType{ID: run.TypePHPScript, Label: "PHP Script"}}})
	f.answer("run.list", []run.Info{
		{ID: "old", Name: "old", State: run.StateExited, Project: root},
		{ID: "other", Name: "other", State: run.StateExited, Project: "/elsewhere"},
	})
	f.answer("run.output", run.Page{RunID: "old", Lines: []run.Line{{Seq: 1, Text: "kept"}}, Last: 1})
	f.answer("run.configs.select", api.RunConfigInfo{})
	f.answer("run.configs.save", run.Config{ID: "c", Name: "New"})
	f.answer("run.configs.delete", struct{}{})
	f.answer("run.stop", run.Info{})
	f.answer("run.remove", struct{}{})
	f.answer("run.stopAll", []string{})
	f.answer("run.keepAll", []string{})
	f.answer("run.rerun", run.Info{ID: "run-9", Name: "x", State: run.StateRunning, Project: root, Replaces: "run-1"})
	f.answer("run.closeCheck", api.RunCloseCheck{Policy: run.CloseAsk, Active: []run.Info{{ID: "run-9"}}})
	c := started(t, f)
	r := c.Run

	r.ProjectChanged()
	c.Settle(time.Second)
	if len(r.Configs) != 2 || len(r.Types) != 1 || r.SelectedConfig().ID != "b" {
		t.Fatalf("configs %+v", r.Configs)
	}
	if len(r.Tabs) != 1 || r.Tabs[0].Info.ID != "old" || r.Tabs[0].Lines[0].Text != "kept" {
		t.Fatalf("restored tabs %+v", r.Tabs)
	}

	// Ctrl+R runs the selected configuration.
	r.RunSelected("/proj/a.php")
	c.Settle(time.Second)
	if len(starts) != 1 || starts[0].Config != "b" || !r.Started || r.Selected != "run-1" {
		t.Fatalf("run selected %+v %+v", starts, r)
	}
	// Events: output and exit of the run.
	f.emit(api.TopicRunStarted, run.Info{ID: "run-1", State: run.StateRunning, Project: root})
	f.emit(api.TopicRunOutput, run.Output{RunID: "run-1", Lines: []run.Line{{Seq: 1, Text: "a"}, {Seq: 2, Text: "b"}}})
	f.emit(api.TopicRunOutput, run.Output{RunID: "run-1", Lines: []run.Line{{Seq: 2, Text: "b"}, {Seq: 3, Text: "c"}}})
	f.emit(api.TopicRunOutput, run.Output{RunID: "unknown", Lines: []run.Line{{Seq: 1}}})
	c.Settle(time.Second)
	tab := r.Tab("run-1")
	if tab == nil || len(tab.Lines) != 3 || !tab.Active() || r.ActiveCount() != 1 {
		t.Fatalf("tab %+v", tab)
	}
	f.emit(api.TopicRunExited, run.Info{ID: "run-1", State: run.StateExited, ExitCode: 2, Project: root})
	c.Settle(time.Second)
	if tab.Active() || RunState(tab.Info) != "exit 2" {
		t.Fatalf("exited %+v", tab.Info)
	}
	// Rerun replaces the tab.
	r.Rerun("run-1")
	c.Settle(time.Second)
	if r.Tab("run-1") != nil || r.Tab("run-9") == nil || r.Current().Info.ID != "run-9" {
		t.Fatalf("rerun tabs %+v", r.Tabs)
	}
	if RunState(r.Tab("run-9").Info) != "running" {
		t.Fatal("state text")
	}
	// Closing an active tab stops the run; the tab goes when it ended.
	r.CloseTab("run-9")
	c.Settle(time.Second)
	if f.called("run.stop") != 1 || r.Tab("run-9") == nil {
		t.Fatal("active tab closed without stop")
	}
	f.emit(api.TopicRunExited, run.Info{ID: "run-9", State: run.StateStopped, Project: root})
	c.Settle(time.Second)
	if r.Tab("run-9") != nil || f.called("run.remove") != 1 || RunState(run.Info{State: run.StateStopped}) != "stopped" {
		t.Fatalf("tab after stop %+v", r.Tabs)
	}
	r.CloseTab("nope")
	r.Select("old")
	if r.Selected != "old" {
		t.Fatal("select tab")
	}
	f.emit(api.TopicRunRemoved, api.RunIDParams{RunID: "old"})
	f.emit(api.TopicRunConfigsChanged, cfgs[:1])
	c.Settle(time.Second)
	if len(r.Tabs) != 0 || r.Current() != nil || len(r.Configs) != 1 || r.SelectedConfig() != nil {
		t.Fatalf("removed/configs %+v %+v", r.Tabs, r.Configs)
	}

	// Without a selected configuration Ctrl+R runs the file.
	r.RunSelected("/proj/a.php")
	r.RunSelected("")
	c.Settle(time.Second)
	if starts[len(starts)-1].File != "/proj/a.php" || !strings.Contains(c.Status.Notice, "No run configuration") {
		t.Fatalf("file run %+v %q", starts, c.Status.Notice)
	}
	r.RunFile("")
	if c.Status.Notice != "Run: no file" {
		t.Fatal(c.Status.Notice)
	}
	r.SelectConfig("a")
	r.SaveConfig(run.Config{Name: "New", Type: run.TypeCommand}, func(cfg run.Config) {
		if cfg.ID != "c" {
			t.Errorf("saved %+v", cfg)
		}
	})
	r.DeleteConfig("a")
	stopped, kept := false, false
	r.StopAll("/old", func() { stopped = true })
	c.Settle(time.Second)
	var sp api.RunStopAllParams
	lastParams(t, f, "run.stopAll", &sp)
	if !stopped || sp.Project != "/old" {
		t.Fatalf("stopAll %v %+v", stopped, sp)
	}
	r.KeepAll("/old", func() { kept = true })
	c.Settle(time.Second)
	lastParams(t, f, "run.keepAll", &sp)
	if !kept || sp.Project != "/old" {
		t.Fatalf("keepAll %v %+v", kept, sp)
	}
	var check api.RunCloseCheck
	r.CloseCheck(func(res api.RunCloseCheck) { check = res })
	c.Settle(time.Second)
	if check.Policy != run.CloseAsk || len(check.Active) != 1 || f.called("run.configs.select") != 1 || f.called("run.stopAll") != 1 {
		t.Fatalf("close check %+v", check)
	}
	// Errors become notices.
	f.on("run.start", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeUnavailable, "no php") })
	r.RunConfig("a")
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "no php") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
	f.on("run.rerun", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeFailed, "gone") })
	r.Rerun("x")
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "Rerun: gone") {
		t.Fatalf("rerun notice %q", c.Status.Notice)
	}
	f.on("run.closeCheck", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeFailed, "x") })
	r.CloseCheck(func(res api.RunCloseCheck) { check = res })
	f.on("run.configs.save", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeInvalidParams, "bad") })
	r.SaveConfig(run.Config{}, nil)
	c.Settle(time.Second)
	if check.Policy != run.CloseKeep || !strings.Contains(c.Status.Notice, "bad") {
		t.Fatalf("error paths %+v %q", check, c.Status.Notice)
	}

	// Another project: finished tabs of the old one go.
	f.emit(api.TopicRunStarted, run.Info{ID: "run-x", State: run.StateRunning, Project: root})
	c.Settle(time.Second)
	c.Project.Root = "/other"
	f.answer("run.list", []run.Info{})
	r.ProjectChanged()
	c.Settle(time.Second)
	for _, tab := range r.Tabs {
		if !tab.Active() {
			t.Fatalf("finished tab of the old project kept: %+v", tab.Info)
		}
	}
	if r.Tab("run-x") == nil {
		t.Fatalf("project change kept %+v", r.Tabs)
	}
	c.Project.Root = ""
	r.ProjectChanged()
	if r.Configs != nil {
		t.Fatal("configs without project")
	}
}

// TestRunReplyAfterExit: a start/rerun reply that arrives after
// run.exited does not make the finished run look active again.
func TestRunReplyAfterExit(t *testing.T) {
	f := projectFake()
	running := run.Info{ID: "run-1", Name: "x", State: run.StateRunning, Project: root}
	release := make(chan struct{})
	f.on("run.start", func(json.RawMessage) (any, error) { <-release; return running, nil })
	c := started(t, f)
	r := c.Run
	r.RunFile("/proj/a.php")
	f.emit(api.TopicRunStarted, running)
	exited := running
	exited.State, exited.ExitCode, exited.Finished = run.StateExited, 0, time.Now()
	f.emit(api.TopicRunExited, exited)
	c.Settle(time.Second)
	if tab := r.Tab("run-1"); tab == nil || tab.Active() {
		t.Fatalf("exit not applied: %+v", tab)
	}
	close(release)
	c.Settle(time.Second)
	if tab := r.Tab("run-1"); tab == nil || tab.Active() || tab.Info.Finished.IsZero() {
		t.Fatalf("reply after exit revived the run: %+v", tab.Info)
	}
}

func TestRunTabLineLimit(t *testing.T) {
	r := &Runs{}
	tab := &RunTab{}
	lines := make([]run.Line, maxRunLines+5)
	for i := range lines {
		lines[i].Seq = i + 1
	}
	r.appendLines(tab, lines)
	if len(tab.Lines) != maxRunLines || !tab.Dropped || tab.Lines[0].Seq != 6 {
		t.Fatalf("limit %d %v %d", len(tab.Lines), tab.Dropped, tab.Lines[0].Seq)
	}
}
