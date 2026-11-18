package debugxdebug

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

func newApp(t *testing.T, configDir string) *api.App {
	t.Helper()
	return modtest.NewApp(t, []module.Module{phpruntime.New(phpruntime.Options{}), New(Options{})}, func(o *api.Options) {
		if configDir != "" {
			o.ConfigDir = configDir
		}
	})
}

func ptr(b bool) *bool { return &b }

func TestBreakpointsWithoutSession(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	a := newApp(t, cfg)
	root := modtest.Project(t, a, map[string]string{"index.php": "<?php\n"})
	rec := modtest.Record(t, a, api.TopicDebugBreakpoints)

	var res BreakpointsResult
	modtest.Call(t, a, "debug.breakpoints.list", nil, &res)
	if len(res.Breakpoints) != 0 {
		t.Fatalf("list %+v", res)
	}
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php", Line: 5, Condition: " $a > 1 "}, &res)
	if res.Changed == nil || res.Changed.ID != "index.php:5" || res.Changed.Condition != "$a > 1" || !res.Changed.Enabled {
		t.Fatalf("set %+v", res.Changed)
	}
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: filepath.Join(root, "index.php"), Line: 2, Enabled: ptr(false)}, &res)
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php", Line: 5}, &res) // upsert clears condition
	if len(res.Breakpoints) != 2 || res.Breakpoints[0].Line != 2 || res.Breakpoints[0].Enabled || res.Breakpoints[1].Condition != "" {
		t.Fatalf("upsert %+v", res.Breakpoints)
	}
	modtest.Call(t, a, "debug.breakpoints.enable", BreakpointParams{ID: "index.php:2", Enabled: ptr(true)}, &res)
	if !res.Changed.Enabled {
		t.Fatal("not enabled")
	}
	modtest.WantCode(t, a, "debug.breakpoints.enable", BreakpointParams{ID: "index.php:2"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "debug.breakpoints.enable", BreakpointParams{ID: "x:1", Enabled: ptr(true)}, api.CodeNotFound)
	modtest.WantCode(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "debug.breakpoints.remove", BreakpointParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "debug.breakpoints.remove", BreakpointParams{File: "index.php", Line: 99}, api.CodeNotFound)
	rec.Wait(t, api.TopicDebugBreakpoints, nil)

	// Watches without a session have no values.
	var exprs []string
	modtest.Call(t, a, "debug.watches.set", WatchesSetParams{Expressions: []string{"$count + 1", "", "$x"}}, &exprs)
	if len(exprs) != 2 {
		t.Fatalf("watches %v", exprs)
	}
	var ws []Watch
	modtest.Call(t, a, "debug.watches", nil, &ws)
	if len(ws) != 2 || ws[0].Value != nil {
		t.Fatalf("watches %+v", ws)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// Persisted per project.
	b := newApp(t, cfg)
	if _, err := b.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, b, "debug.breakpoints.list", nil, &res)
	if len(res.Breakpoints) != 2 || res.Breakpoints[1].ID != "index.php:5" {
		t.Fatalf("persisted %+v", res.Breakpoints)
	}
	modtest.Call(t, b, "debug.breakpoints.remove", BreakpointParams{File: "index.php", Line: 5}, &res)
	if len(res.Breakpoints) != 1 {
		t.Fatalf("remove %+v", res.Breakpoints)
	}
}

func waitSessions(t *testing.T, a *api.App, n int) []DebugSessionInfo {
	t.Helper()
	var ss []DebugSessionInfo
	modtest.Eventually(t, "sessions", func() bool {
		modtest.Call(t, a, "debug.sessions", nil, &ss)
		return len(ss) == n
	})
	return ss
}

func TestSession(t *testing.T) {
	a := newApp(t, "")
	root := modtest.Project(t, a, map[string]string{"index.php": "<?php\n// line 2\n$count = 3;\n"})
	rec := modtest.Record(t, a, "debug.*")
	host := filepath.Join(root, "index.php")

	modtest.WantCode(t, a, "debug.run", nil, api.CodeUnavailable)
	modtest.Call(t, a, "runtime.mappings.set", map[string]any{"mappings": []api.MappingInfo{{Host: root, Remote: "/app"}}}, nil)
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php", Line: 3, Condition: "$count > 1"}, nil)
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php", Line: 5, Enabled: ptr(false)}, nil)

	// The debug.before guard saves modified documents.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "index.php"}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "index.php", At: api.Pos{Line: 4}, Text: "// saved\n"}, nil)

	var lr DebugListenResult
	modtest.Call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0"}, &lr)
	if !lr.Listening || len(lr.Mappings) != 1 || lr.Mappings[0].Remote != "/app" {
		t.Fatalf("listen %+v", lr)
	}
	if data, _ := os.ReadFile(host); !strings.Contains(string(data), "// saved") {
		t.Fatal("document not saved by debug.before")
	}
	var again DebugListenResult
	modtest.Call(t, a, "debug.listen", DebugListenParams{}, &again)
	if again.Addr != lr.Addr {
		t.Fatal("second listen should report the running listener")
	}
	modtest.WantCode(t, a, "debug.run", nil, api.CodeInvalidParams) // no session

	f := dialEngine(t, lr.Addr, "file:///app/index.php")
	started := rec.Wait(t, api.TopicDebugSessionStarted, nil).Payload.(DebugSessionInfo)
	if started.Breakpoints != 1 || started.File != host {
		t.Fatalf("started %+v", started)
	}
	if bps := f.breakpoints(); len(bps) != 1 || bps[0] != "file:///app/index.php:3:enabled:$count > 1" {
		t.Fatalf("engine breakpoints %v", bps)
	}
	id := waitSessions(t, a, 1)[0].ID

	var st DebugStatusEvent
	modtest.Call(t, a, "debug.run", DebugSessionParams{Session: id}, &st)
	if st.Status != "break" || st.Line != 3 || st.File != host {
		t.Fatalf("run %+v", st)
	}
	brk := rec.Wait(t, api.TopicDebugBreak, nil).Payload.(api.DebugBreakEvent)
	if brk.Path != "index.php" || brk.File != host || brk.Line != 3 || brk.RemoteURI != "file:///app/index.php" {
		t.Fatalf("break %+v", brk)
	}
	hit := rec.Wait(t, api.TopicDebugBreakpointHit, nil).Payload.(api.BreakpointHitEvent)
	if hit.Breakpoint != "index.php:3" || hit.Condition != "$count > 1" {
		t.Fatalf("hit %+v", hit)
	}
	rec.Wait(t, TopicDebugOutput, nil)

	// Live changes during the session.
	modtest.Call(t, a, "debug.breakpoints.set", BreakpointParams{File: "index.php", Line: 7}, nil)
	modtest.Call(t, a, "debug.breakpoints.enable", BreakpointParams{File: "index.php", Line: 5, Enabled: ptr(true)}, nil)
	modtest.Call(t, a, "debug.breakpoints.enable", BreakpointParams{File: "index.php", Line: 3, Enabled: ptr(false)}, nil)
	modtest.Call(t, a, "debug.breakpoints.remove", BreakpointParams{ID: "index.php:7"}, nil)
	want := []string{"file:///app/index.php:3:disabled:$count > 1", "file:///app/index.php:5:enabled:"}
	if bps := f.breakpoints(); strings.Join(bps, "|") != strings.Join(want, "|") {
		t.Fatalf("live breakpoints %v", bps)
	}
	modtest.Call(t, a, "debug.continue", DebugContinueParams{Action: "run"}, &st)
	if st.Line != 5 {
		t.Fatalf("second run %+v", st)
	}

	// Stepping.
	for _, step := range []struct {
		action string
		line   int
	}{{"stepInto", 20}, {"stepOver", 21}, {"stepOut", 9}} {
		modtest.Call(t, a, "debug."+step.action, nil, &st)
		if st.Status != "break" || st.Line != step.line {
			t.Fatalf("%s: %+v", step.action, st)
		}
	}
	modtest.WantCode(t, a, "debug.continue", DebugContinueParams{Action: "jump"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "debug.run", DebugSessionParams{Session: "zzz"}, api.CodeNotFound)

	var frames []dbgp.StackFrame
	modtest.Call(t, a, "debug.stack", nil, &frames)
	if len(frames) != 1 || frames[0].File != host || frames[0].Line != 9 {
		t.Fatalf("stack %+v", frames)
	}
	var prop dbgp.Property
	modtest.Call(t, a, "debug.eval", DebugEvalParams{Expr: "$count + 1"}, &prop)
	if prop.Value != "4" {
		t.Fatalf("eval %+v", prop)
	}
	modtest.WantCode(t, a, "debug.eval", DebugEvalParams{Expr: "1+"}, api.CodeFailed)

	// Variables, children, paging.
	var vars VariablesResult
	modtest.Call(t, a, "debug.variables", nil, &vars)
	if len(vars.Contexts) != 2 || vars.Contexts[1].Name != "Superglobals" || len(vars.Variables) != 2 ||
		vars.Variables[0].Value != "3" || vars.Variables[1].NumChildren != 3 || len(vars.Variables[1].Children) != 2 {
		t.Fatalf("variables %+v", vars)
	}
	modtest.Call(t, a, "debug.variables", VariablesParams{FullName: "$list"}, &vars)
	if !vars.HasMore || len(vars.Variables) != 2 || vars.Parent.FullName != "$list" {
		t.Fatalf("children %+v", vars)
	}
	modtest.Call(t, a, "debug.variables", VariablesParams{FullName: "$list", Page: 1}, &vars)
	if vars.HasMore || len(vars.Variables) != 1 || vars.Variables[0].FullName != "$list[2]" {
		t.Fatalf("page 1 %+v", vars)
	}
	modtest.WantCode(t, a, "debug.variables", VariablesParams{FullName: "$nope"}, api.CodeFailed)

	// Watches.
	modtest.Call(t, a, "debug.watches.set", WatchesSetParams{Expressions: []string{"$count + 1", "bad("}}, nil)
	var ws []Watch
	modtest.Call(t, a, "debug.watches", nil, &ws)
	if len(ws) != 2 || ws[0].Value == nil || ws[0].Value.Value != "4" || ws[1].Error == "" {
		t.Fatalf("watches %+v", ws)
	}
	modtest.WantCode(t, a, "debug.watches", WatchesParams{Session: "zzz"}, api.CodeNotFound)

	// Session-only breakpoint.
	var bp dbgp.Breakpoint
	modtest.Call(t, a, "debug.setBreakpoint", DebugBreakpointParams{File: "index.php", Line: 11}, &bp)
	if bp.ID == "" {
		t.Fatalf("setBreakpoint %+v", bp)
	}
	modtest.WantCode(t, a, "debug.setBreakpoint", DebugBreakpointParams{File: "index.php"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "debug.setBreakpoint", DebugBreakpointParams{Line: 1}, api.CodeInvalidParams)

	modtest.Call(t, a, "debug.stopSession", nil, &st)
	if st.Status != "stopped" {
		t.Fatalf("stopSession %+v", st)
	}
	rec.Wait(t, api.TopicDebugSessionEnded, nil)
	modtest.Call(t, a, "debug.stop", nil, nil)
	rec.Wait(t, TopicDebugStopped, nil)
	modtest.WantCode(t, a, "debug.stack", nil, api.CodeUnavailable)

	// Mappings change while listening: new sessions use them.
	modtest.Call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0"}, &lr)
	modtest.Call(t, a, "runtime.mappings.set", map[string]any{"mappings": []api.MappingInfo{{Host: root, Remote: "/srv"}}}, nil)
	modtest.Eventually(t, "mapper update", func() bool {
		f2 := dialEngine(t, lr.Addr, "file:///srv/index.php")
		ss := waitSessions(t, a, 1)
		modtest.Call(t, a, "debug.detach", DebugSessionParams{Session: ss[0].ID}, nil)
		waitSessions(t, a, 0)
		bps := f2.breakpoints()
		return len(bps) == 1 && strings.HasPrefix(bps[0], "file:///srv/index.php:5")
	})

	// Explicit mappings win over php-runtime and stopping via the API.
	modtest.Call(t, a, "debug.stop", nil, nil)
	modtest.Call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0", Mappings: []api.MappingInfo{{Host: root, Remote: "/x"}}}, &lr)
	modtest.Call(t, a, "runtime.mappings.set", map[string]any{"reset": true}, nil)
	f3 := dialEngine(t, lr.Addr, "file:///x/index.php")
	waitSessions(t, a, 1)
	modtest.Eventually(t, "explicit mapping", func() bool {
		bps := f3.breakpoints()
		return len(bps) == 1 && strings.HasPrefix(bps[0], "file:///x/index.php:5")
	})
	modtest.Call(t, a, "debug.stop", nil, nil)
	_ = f3.commands("stop")
}

func TestGuardAndModuleDisable(t *testing.T) {
	a := newApp(t, "")
	modtest.Project(t, a, map[string]string{"index.php": "<?php\n"})
	un := api.HookDebugBefore.On(a.Hooks(), 0, func(context.Context, api.DebugRequest) error { return errors.New("not now") })
	modtest.WantCode(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0"}, api.CodeFailed)
	un()

	var lr DebugListenResult
	modtest.Call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0"}, &lr)
	// A busy port is reported as unavailable.
	b := newApp(t, "")
	modtest.WantCode(t, b, "debug.listen", DebugListenParams{Addr: lr.Addr}, api.CodeUnavailable)

	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "debug.sessions", nil, api.CodeModuleDisabled)
	modtest.WantCode(t, a, "debug.breakpoints.list", nil, api.CodeModuleDisabled)
	if c, err := net.DialTimeout("tcp", lr.Addr, time.Second); err == nil {
		_ = c.Close()
		// The port may be reused by the OS only after the listener closed;
		// a successful dial means the listener is still running.
		t.Fatal("listener still running after disable")
	}
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: ID}, nil)
	var ss []DebugSessionInfo
	modtest.Call(t, a, "debug.sessions", nil, &ss)

	if err := debugErr(errors.New("x")); api.ErrorCode(err) != api.CodeFailed {
		t.Fatal(err)
	}
	if err := debugErr(dbgp.ErrSessionClosed); api.ErrorCode(err) != api.CodeNotFound {
		t.Fatal(err)
	}
	// Events with unexpected payloads are ignored.
	m := New(Options{})
	m.onMappingsChanged(bus.Event{Payload: 1})
	if err := (&Module{}).Start(nil); err == nil {
		t.Fatal("start without app context")
	}
}

// run.start with debug asks for a listener through the debug.listener
// hook (D311); a running listener is reused.
func TestDebugListenerHook(t *testing.T) {
	a := newApp(t, "")
	modtest.Project(t, a, map[string]string{"index.php": "<?php\n"})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingPort, Scope: "project", Value: []byte(port)}, nil)
	q, err := api.HookDebugListener.Apply(context.Background(), a.Hooks(), api.DebugListenerQuery{})
	if err != nil || q.Err != "" || !strings.HasSuffix(q.Addr, ":"+port) {
		t.Fatalf("listener %+v %v", q, err)
	}
	var s DebugListenResult
	modtest.Call(t, a, "debug.listen", DebugListenParams{}, &s)
	if !s.Listening || s.Addr != q.Addr {
		t.Fatalf("listen %+v", s)
	}
	// An answer of another provider is kept.
	if q2, _ := api.HookDebugListener.Apply(context.Background(), a.Hooks(), api.DebugListenerQuery{Addr: "x:1"}); q2.Addr != "x:1" {
		t.Fatal(q2)
	}
	modtest.Call(t, a, "debug.stop", nil, nil)
}
