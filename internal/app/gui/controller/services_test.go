package controller

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func servicesFake() *fakeBackend {
	f := newFake()
	f.answer("docker.watchStart", map[string]any{"id": "watch-1", "intervalSeconds": 5})
	f.answer("docker.watchStop", map[string]any{"stopped": true})
	f.answer("docker.contexts", map[string]any{"effective": "orbstack", "contexts": []DockerContext{{Name: "orbstack", Provider: "orbstack"}, {Name: "default"}}})
	f.answer("docker.services", DockerSnapshot{Context: "orbstack", Projects: []DockerProject{{Name: "shop", Services: []DockerService{{Name: "web", State: "running"}}}}})
	f.answer("docker.logsStop", map[string]any{"stopped": true})
	return f
}

func TestServicesWatchFollowsVisibility(t *testing.T) {
	f := servicesFake()
	c := New(f)
	defer c.Close()
	s := c.Services
	// Events are ignored while hidden.
	f.emit("docker.servicesChanged", DockerSnapshot{Context: "x"})
	c.Settle(time.Second)
	if s.Loaded || f.called("docker.watchStart") != 0 {
		t.Fatal("engine contacted while hidden")
	}
	s.SetVisible(true)
	s.SetVisible(true)
	c.Settle(time.Second)
	if f.called("docker.watchStart") != 1 || s.watchID != "watch-1" || s.Effective != "orbstack" || len(s.Contexts) != 2 || !s.Visible() {
		t.Fatalf("visible: %d %q %+v", f.called("docker.watchStart"), s.watchID, s)
	}
	f.emit("docker.servicesChanged", DockerSnapshot{Context: "orbstack", Refresh: "events", Projects: []DockerProject{{Name: "shop"}}})
	c.Settle(time.Second)
	if !s.Loaded || s.Snapshot.Refresh != "events" || s.Snapshot.Projects[0].Name != "shop" {
		t.Fatalf("snapshot %+v", s.Snapshot)
	}
	s.SetStandalone(true)
	c.Settle(time.Second)
	if f.called("docker.watchStop") != 1 || f.called("docker.watchStart") != 2 || !strings.Contains(string(f.params["docker.watchStart"][1]), `"standalone":true`) {
		t.Fatalf("standalone restart %v", f.calls)
	}
	s.SetStandalone(true) // no change
	s.SetVisible(false)
	c.Settle(time.Second)
	if f.called("docker.watchStop") != 2 || s.watchID != "" {
		t.Fatal("watch not stopped when hidden")
	}
	// A watch answered after the window was hidden is ended at once.
	s.SetVisible(true)
	s.SetVisible(false)
	c.Settle(time.Second)
	if f.called("docker.watchStop") != 3 || s.watchID != "" {
		t.Fatalf("late watch %d %q", f.called("docker.watchStop"), s.watchID)
	}
	// Failing watch.
	f.on("docker.watchStart", func(json.RawMessage) (any, error) { return nil, errors.New("no docker") })
	s.SetVisible(true)
	c.Settle(time.Second)
	if s.Err != "no docker" {
		t.Fatalf("err %q", s.Err)
	}
}

func TestServicesActionsAndRefresh(t *testing.T) {
	f := servicesFake()
	f.answer("docker.serviceAction", map[string]any{"output": []string{"Container shop-web-1  Restarted"}})
	f.answer("docker.setContext", map[string]any{"selected": "default"})
	f.answer("docker.shell", map[string]any{"command": []string{"docker", "compose", "exec", "web", "sh"}})
	f.answer("docker.openURL", map[string]any{"launched": []string{"open"}})
	c := New(f)
	defer c.Close()
	s := c.Services
	s.Refresh()
	c.Settle(time.Second)
	if !s.Loaded || s.Snapshot.Projects[0].Services[0].Name != "web" {
		t.Fatalf("refresh %+v", s.Snapshot)
	}
	s.Action("restart", "web", "", false)
	if s.Busy != "restart web" {
		t.Fatalf("busy %q", s.Busy)
	}
	c.Settle(time.Second)
	if s.Busy != "" || len(s.Output) != 1 || f.called("docker.services") != 2 {
		t.Fatalf("after action %+v %d", s, f.called("docker.services"))
	}
	var p map[string]any
	_ = json.Unmarshal(f.params["docker.serviceAction"][0], &p)
	if p["action"] != "restart" || p["service"] != "web" || p["confirm"] != false {
		t.Fatalf("params %v", p)
	}
	f.on("docker.serviceAction", func(json.RawMessage) (any, error) { return nil, errors.New("boom") })
	s.Action("down", "", "", true)
	c.Settle(time.Second)
	if s.Err != "boom" {
		t.Fatalf("err %q", s.Err)
	}
	s.Action("stop", "", "adminer", false)
	c.Settle(time.Second)
	f.on("docker.services", func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	s.Refresh()
	c.Settle(time.Second)
	if s.Err != "down" {
		t.Fatalf("refresh err %q", s.Err)
	}

	s.SelectContext("default")
	c.Settle(time.Second)
	if s.Effective != "orbstack" || f.called("docker.contexts") != 1 { // reloaded from the fake
		t.Fatalf("context %q %d", s.Effective, f.called("docker.contexts"))
	}
	f.on("docker.setContext", func(json.RawMessage) (any, error) { return nil, errors.New("unknown") })
	s.SelectContext("nope")
	c.Settle(time.Second)
	if s.Err != "unknown" {
		t.Fatalf("context err %q", s.Err)
	}

	s.OpenShell("web", "")
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "exec web sh") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
	f.on("docker.shell", func(json.RawMessage) (any, error) { return nil, errors.New("no terminal") })
	s.OpenShell("web", "")
	c.Settle(time.Second)
	if s.Err != "no terminal" {
		t.Fatalf("shell err %q", s.Err)
	}
	s.OpenURL("http://localhost:8080")
	c.Settle(time.Second)
	if f.called("docker.openURL") != 1 {
		t.Fatal("url not opened")
	}
}

func TestServicesLogs(t *testing.T) {
	f := servicesFake()
	f.on("docker.logs", func(raw json.RawMessage) (any, error) {
		var p struct {
			Follow bool `json:"follow"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Follow {
			return map[string]any{"id": "logs-1", "lines": []DockerLogLine{}}, nil
		}
		return map[string]any{"lines": []DockerLogLine{{Container: "web-1", Text: "GET / 200"}, {Container: "web-1", Text: "PHP Warning"}}}, nil
	})
	c := New(f)
	defer c.Close()
	s := c.Services
	s.SetVisible(true)
	s.ShowLogs("web", "")
	c.Settle(time.Second)
	if s.Logs.ID != "logs-1" || !s.Logs.Open || s.Logs.Loading {
		t.Fatalf("logs %+v", s.Logs)
	}
	f.emit("docker.log", map[string]any{"id": "other", "lines": []DockerLogLine{{Text: "x"}}})
	f.emit("docker.log", map[string]any{"id": "logs-1", "lines": []DockerLogLine{{Container: "web-1", Text: "hello"}, {Container: "web-1", Text: "PHP Fatal"}}})
	c.Settle(time.Second)
	if len(s.Logs.Lines) != 2 {
		t.Fatalf("lines %+v", s.Logs.Lines)
	}
	s.SetLogFilter("php")
	if got := s.VisibleLogs(); len(got) != 1 || got[0].Text != "PHP Fatal" {
		t.Fatalf("filter %+v", got)
	}
	s.SetLogFilter("")
	if len(s.VisibleLogs()) != 2 {
		t.Fatal("filter not cleared")
	}
	// Turning follow off stops the stream and loads the tail once.
	s.SetFollow(false)
	c.Settle(time.Second)
	if f.called("docker.logsStop") != 1 || s.Logs.ID != "" || len(s.Logs.Lines) != 2 || s.Logs.Lines[1].Text != "PHP Warning" {
		t.Fatalf("no follow %+v", s.Logs)
	}
	s.ClearLogs()
	if len(s.Logs.Lines) != 0 {
		t.Fatal("not cleared")
	}
	s.SetFollow(true)
	c.Settle(time.Second)
	f.emit("docker.log", map[string]any{"id": "logs-1", "done": true, "error": "engine gone"})
	c.Settle(time.Second)
	if s.Logs.ID != "" || s.Logs.Err != "engine gone" {
		t.Fatalf("done %+v", s.Logs)
	}
	// Many lines are capped.
	big := make([]DockerLogLine, maxServiceLogLines+10)
	s.appendLogs(big)
	if len(s.Logs.Lines) != maxServiceLogLines {
		t.Fatalf("cap %d", len(s.Logs.Lines))
	}
	// Hiding stops a followed stream; a context change drops it.
	s.ShowLogs("", "adminer")
	c.Settle(time.Second)
	f.emit("docker.contextChanged", map[string]any{"context": "default"})
	c.Settle(time.Second)
	if s.Logs.ID != "" {
		t.Fatal("stream kept after context change")
	}
	s.ShowLogs("web", "")
	c.Settle(time.Second)
	s.SetVisible(false)
	c.Settle(time.Second)
	if s.Logs.ID != "" {
		t.Fatal("stream kept after hide")
	}
	// Errors are shown.
	f.on("docker.logs", func(json.RawMessage) (any, error) { return nil, errors.New("no project") })
	s.ShowLogs("web", "")
	c.Settle(time.Second)
	if s.Logs.Err != "no project" {
		t.Fatalf("err %+v", s.Logs)
	}
	// A stream answered for an old selection is stopped.
	f.answer("docker.logs", map[string]any{"id": "logs-9"})
	s.ShowLogs("web", "")
	s.Logs.Service = "app"
	c.Settle(time.Second)
	// shutdown stops the watch and the stream synchronously.
	s.watchID, s.Logs.ID = "watch-5", "logs-5"
	s.shutdown()
	if s.watchID != "" || s.Logs.ID != "" {
		t.Fatal("shutdown")
	}
}
