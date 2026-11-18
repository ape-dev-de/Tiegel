package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// TestTerminalOpenIn: a shell in a service or container opens with the
// kind a plugin lists in terminal.kinds (D372).
func TestTerminalOpenIn(t *testing.T) {
	f := newFake()
	f.answer("terminal.kinds", []api.TerminalKind{
		{Kind: "p.svc", Label: "Shell in Service", Target: api.TerminalTargetService},
		{Kind: "p.ctr", Label: "Shell in Container", Target: api.TerminalTargetContainer},
	})
	f.on("terminal.open", func(raw json.RawMessage) (any, error) {
		var p api.TerminalOpenParams
		_ = json.Unmarshal(raw, &p)
		return terminal.Info{ID: "t" + p.Kind, Kind: p.Kind, Target: p.Target, Running: true}, nil
	})
	f.answer("terminal.screen", terminal.Screen{})
	c := New(f)
	defer c.Close()
	tm := c.Terminals
	tm.OpenIn(api.TerminalTargetService, "app")
	c.Settle(time.Second)
	if x := tm.Current(); x == nil || x.Info.Kind != "p.svc" || x.Info.Target != "app" || !tm.Opened {
		t.Fatalf("service shell %+v", tm.Tabs)
	}
	tm.OpenIn(api.TerminalTargetContainer, "db")
	c.Settle(time.Second)
	if x := tm.Current(); x == nil || x.Info.Kind != "p.ctr" || x.Info.Target != "db" {
		t.Fatalf("container shell %+v", tm.Tabs)
	}
	// No plugin for the role: a notice, no call, no tab.
	tm.OpenIn("vm", "x")
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "no enabled plugin") || f.called("terminal.open") != 2 || len(tm.Tabs) != 2 {
		t.Fatalf("unknown role %q %d", c.Status.Notice, f.called("terminal.open"))
	}
}
