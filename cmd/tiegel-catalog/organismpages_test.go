package main

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/debug"
)

func TestDebugHostAppliesEvents(t *testing.T) {
	h := newDebugHost()
	h.apply([]debug.Event{
		debug.ActionEvent{Action: debug.ActionToggleListen},
		debug.ActionEvent{Action: debug.ActionMuteBreakpoints},
		debug.ActionEvent{Action: debug.ActionResume},
	})
	if h.listening || !h.muted || h.state != debug.StateRunning {
		t.Fatalf("actions: %+v", h)
	}
	h.apply([]debug.Event{debug.ActionEvent{Action: debug.ActionStepOver}})
	if h.state != debug.StateBreak {
		t.Fatal("step")
	}
	h.apply([]debug.Event{debug.ExpandEvent{FullName: "$queue"}, debug.ExpandEvent{FullName: "$queue", Page: 1}, debug.ExpandEvent{FullName: "$this"}})
	if q := debug.Find(h.vars, "$queue"); len(q.Children) != 10 || q.Page != 1 {
		t.Fatalf("paging: %d children", len(q.Children))
	}
	if len(debug.Find(h.vars, "$this").Children) != 2 {
		t.Fatal("object children")
	}
	h.apply([]debug.Event{
		debug.WatchAddEvent{Expression: "$x"}, debug.WatchEditEvent{Index: 0, Expression: "$y"}, debug.WatchRemoveEvent{Index: 1},
		debug.BreakpointToggleEvent{ID: "3", Enabled: true}, debug.BreakpointConditionEvent{ID: "1", Condition: "$a"},
		debug.BreakpointRemoveEvent{ID: "2"}, debug.SessionStopEvent{ID: "s2"},
		debug.ActionEvent{Action: debug.ActionStop},
	})
	if len(h.watches) != 3 || h.watches[0].Expression != "$y" || h.watches[2].Expression != "$x" {
		t.Fatalf("watches %+v", h.watches)
	}
	if len(h.bps) != 2 || h.bps[0].Condition != "$a" || !h.bps[1].Enabled {
		t.Fatalf("breakpoints %+v", h.bps)
	}
	if h.sessions[1].State != debug.StateStopped || h.state != debug.StateNone {
		t.Fatal("stop")
	}
	if h.status() == "" {
		t.Fatal("status")
	}
	p := debug.NewDebugPanel()
	h.fill(p)
	if p.Sessions.Selected != "s1" || len(p.Breakpoints.Items) != 2 {
		t.Fatal("fill")
	}
}
