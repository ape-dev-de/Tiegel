package controller

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func errUnavailable() error { return api.Errorf(api.CodeUnavailable, "no language server") }

func TestLSPSupervisorUI(t *testing.T) {
	f := projectFake()
	notFound := LSPStatus{State: LSPNotFound, Server: "phpactor", Name: "Phpactor", Plugin: "lsp-phpactor", Selected: "auto",
		Reason: "no PHP language server found", Searched: []string{"/a", "/b"}}
	var mu sync.Mutex
	cur := notFound
	f.on("lsp.status", func(json.RawMessage) (any, error) { mu.Lock(); defer mu.Unlock(); return cur, nil })
	answer := func(method string, st LSPStatus) {
		f.on(method, func(json.RawMessage) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			st.Trace = cur.Trace
			cur = st
			return st, nil
		})
	}
	f.answer("lsp.servers", []LSPServer{{ID: "phpactor", Name: "Phpactor", Plugin: "lsp-phpactor"}, {ID: "phpantom", Name: "PHPantom", Plugin: "lsp-phpantom"}})
	var selected []string
	f.on("lsp.select", func(raw json.RawMessage) (any, error) {
		var p map[string]string
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		defer mu.Unlock()
		selected = append(selected, p["server"])
		cur = LSPStatus{State: LSPRunning, Running: true, Name: "PHPantom", Server: "phpantom", Selected: p["server"]}
		return cur, nil
	})
	answer("lsp.disable", LSPStatus{State: LSPOff, Reason: "switched off"})
	answer("lsp.restart", LSPStatus{State: LSPRunning, Running: true, Name: "Phpactor"})
	answer("lsp.setPath", LSPStatus{State: LSPRunning, Running: true, Name: "Phpactor"})
	f.on("lsp.trace", func(json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		cur.Trace = true
		return map[string]bool{"trace": true}, nil
	})
	f.answer("lsp.log", []LSPLogEntry{{Seq: 1, Kind: "stderr", Text: "hello"}, {Seq: 2, Kind: "event", Text: "running"}})
	f.answer("install.plan", InstallPlan{Plugin: "lsp-phpactor", Tool: "Phpactor", Available: true, Scope: "global", Display: "composer global require phpactor/phpactor"})
	f.answer("install.run", map[string]string{"runId": "install-1"})
	f.answer("install.cancel", map[string]any{})
	c := started(t, f)

	c.RefreshLSP()
	c.Settle(time.Second)
	if c.LSP.Status.State != LSPNotFound || len(c.LSP.Servers) != 2 || c.Status.LSP != "Phpactor not found" || !strings.Contains(c.Status.LSPHint, "Install") {
		t.Fatalf("not found %+v %+v", c.LSP.Status, c.Status)
	}
	// The start error does not hide the explained state.
	c.applyLSPStatus(LSPStatus{}, errUnavailable())
	if c.Status.LSP != "Phpactor not found" {
		t.Fatalf("error path %q", c.Status.LSP)
	}

	c.SelectLSP("phpantom")
	c.Settle(time.Second)
	if !c.Status.LSPRunning || c.Status.LSP != "PHPantom" {
		t.Fatalf("select %+v", c.Status)
	}
	c.DisableLSP()
	c.Settle(time.Second)
	if c.Status.LSPRunning || c.Status.LSP != "LSP off" {
		t.Fatalf("disable %+v", c.Status)
	}
	c.RestartLSP()
	c.Settle(time.Second)
	c.SetLSPPath("phpactor", " /x/phpactor ")
	c.SetLSPTrace(true)
	c.LoadLSPLog()
	c.Settle(time.Second)
	if !c.LSP.Status.Trace || len(c.LSP.Log) != 2 {
		t.Fatalf("trace/log %+v %d", c.LSP.Status, len(c.LSP.Log))
	}
	// Events: state, log (duplicates dropped), install output and done.
	f.emit("lsp.state", LSPStatus{State: LSPStarting, Name: "Phpactor"})
	f.emit("lsp.log", LSPLogEntry{Seq: 2, Kind: "event", Text: "dup"})
	f.emit("lsp.log", LSPLogEntry{Seq: 3, Kind: "stderr", Text: "new"})
	c.Settle(time.Second)
	if c.Status.LSP != "Phpactor: starting…" || len(c.LSP.Log) != 3 {
		t.Fatalf("events %+v %d", c.Status, len(c.LSP.Log))
	}
	for _, st := range []LSPStatus{{State: LSPCrashed, Name: "P", Reason: "x"}, {State: LSPStopped, Name: "P"}, {State: LSPNotFound, Reason: "none"}} {
		c.applyLSPState(st)
		if c.Status.LSP == "" || c.Status.LSPRunning {
			t.Fatalf("state %+v → %+v", st, c.Status)
		}
	}

	var plan InstallPlan
	c.PlanInstall("lsp-phpactor", "", func(p InstallPlan) { plan = p })
	c.Settle(time.Second)
	if plan.Display == "" {
		t.Fatal("no plan")
	}
	c.RunInstall(plan)
	c.Settle(time.Second)
	if !c.LSP.Install.Running() || f.called("install.run") != 1 {
		t.Fatalf("install %+v", c.LSP.Install)
	}
	f.emit("install.output", map[string]string{"runId": "install-1", "text": "Using version ^2"})
	f.emit("install.output", map[string]string{"runId": "other", "text": "ignored"})
	c.Settle(time.Second)
	c.CancelInstall()
	f.emit("install.done", InstallRun{RunID: "install-1", Tool: "Phpactor", State: "cancelled", Output: []string{"Using version ^2"}})
	c.Settle(time.Second)
	if c.LSP.Install.Running() || c.LSP.Install.State != "cancelled" || !strings.Contains(c.Status.Notice, "cancelled") || f.called("install.cancel") != 1 {
		t.Fatalf("done %+v %q", c.LSP.Install, c.Status.Notice)
	}
	c.CancelInstall() // finished: nothing to cancel
	if f.called("install.cancel") != 1 {
		t.Fatal("cancelled a finished run")
	}
	if !IsNeedsLSPNotice(NoticeNeedsLSP("Completion")) || IsNeedsLSPNotice("other") {
		t.Fatal("notice helpers")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(selected) != 1 {
		t.Fatalf("selected %v", selected)
	}
}
