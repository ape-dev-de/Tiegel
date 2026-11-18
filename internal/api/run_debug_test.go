package api

import (
	"context"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/run"
)

// run.start with debug makes the debugger listen (hook debug.listener)
// and sets the Xdebug trigger; reruns listen again (D311).
func TestRunDebug(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	spec := &run.Spec{Program: self, Args: []string{"x"}, Env: []string{runHelperEnv + "=echo"}}
	if err := callErr(a, "run.start", RunStartParams{Spec: spec, Debug: true}); ErrorCode(err) != CodeModuleDisabled {
		t.Fatalf("no debugger: %v", err)
	}
	answer := DebugListenerQuery{Err: "port in use"}
	calls := 0
	defer HookDebugListener.On(a.Hooks(), 0, func(_ context.Context, q DebugListenerQuery) (DebugListenerQuery, error) {
		calls++
		return answer, nil
	})()
	if err := callErr(a, "run.start", RunStartParams{Spec: spec, Debug: true}); ErrorCode(err) != CodeUnavailable {
		t.Fatalf("listener error: %v", err)
	}
	answer = DebugListenerQuery{Addr: "127.0.0.1:9100"}
	var info run.Info
	call(t, a, "run.start", RunStartParams{Spec: spec, Debug: true}, &info)
	if !info.Spec.Debug || !slices.Contains(info.Spec.Env, "XDEBUG_SESSION="+run.DebugSession) || !slices.Contains(info.Spec.Env, "XDEBUG_TRIGGER=1") {
		t.Fatalf("spec %+v", info.Spec)
	}
	waitRun(t, a, info.ID)
	call(t, a, "run.rerun", RunIDParams{RunID: info.ID}, &info)
	waitRun(t, a, info.ID)
	if calls != 3 {
		t.Fatalf("listener asked %d times", calls)
	}
	// PHP specs get the step debugging options in front.
	if got := run.DebugPHPArgs(9100); len(got) != 3 || got[2] != "-dxdebug.client_port=9100" || len(run.DebugPHPArgs(0)) != 2 {
		t.Fatal(got)
	}
}

func callErr(a *App, method string, params any) error {
	_, err := a.CallJSON(context.Background(), method, params)
	return err
}
