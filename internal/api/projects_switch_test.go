package api

import (
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// keepBackground sets projects.onSwitch to background (tests of the
// background state).
func keepBackground(t *testing.T, a *App) {
	t.Helper()
	call(t, a, "settings.set", map[string]any{"key": KeyOnSwitch, "scope": "global", "value": settings.OnSwitchBackground}, nil)
}

func stateOf(t *testing.T, a *App, root string) workspace.State {
	t.Helper()
	var list ProjectsListResult
	call(t, a, "projects.list", nil, &list)
	for _, p := range list.Projects {
		if p.Root == root {
			return p.State
		}
	}
	return ""
}

// D547: switching hibernates the project left (default projects.onSwitch);
// its terminals keep running with their shell state and move to the App
// of the resume.
func TestProjectsSwitchHibernatesKeepsTerminals(t *testing.T) {
	needSh(t)
	a := newTestApp(t)
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	call(t, a, "projects.open", ProjectsSwitchParams{Path: dirA}, nil)
	var info terminal.Info
	call(t, a, "terminal.open", TerminalOpenParams{Shell: "/bin/sh", Env: []string{"PS1=$ "}}, &info)
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "export FOO=bar\r"}, nil)

	var ps ProjectSession
	call(t, a, "projects.open", ProjectsSwitchParams{Path: dirB}, &ps)
	if ps.Previous != dirA || ps.PreviousState != workspace.StateHibernated || stateOf(t, a, dirA) != workspace.StateHibernated {
		t.Fatalf("open beta %+v", ps)
	}
	// The hibernated project's App is gone, its terminal runs on.
	if app, _ := a.Workspace().App(dirA); app != nil {
		t.Fatal("hibernated project keeps its App")
	}
	var terms []terminal.Info
	call(t, a, "projects.terminals", ProjectsNameParams{Name: "alpha"}, &terms)
	if len(terms) != 1 || !terms[0].Running || terms[0].ID != info.ID {
		t.Fatalf("parked terminals %+v", terms)
	}
	// The active project (beta) does not see alpha's terminal.
	var list []terminal.Info
	call(t, a, "terminal.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("beta lists %+v", list)
	}

	call(t, a, "projects.switch", ProjectsSwitchParams{Name: "alpha"}, &ps)
	if ps.PreviousState != workspace.StateHibernated || stateOf(t, a, dirB) != workspace.StateHibernated {
		t.Fatalf("switch alpha %+v", ps)
	}
	call(t, a, "terminal.list", nil, &list)
	if len(list) != 1 || list[0].ID != info.ID || !list[0].Running {
		t.Fatalf("resumed terminals %+v", list)
	}
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "echo \"v=$FOO\"\r"}, nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var txt TerminalText
		call(t, a, "terminal.text", TerminalTextParams{ID: info.ID}, &txt)
		if strings.Contains(txt.Text, "v=bar") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("variable lost: %q", txt.Text)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Closing a hibernated project ends its terminals.
	call(t, a, "projects.switch", ProjectsSwitchParams{Name: "beta"}, nil)
	call(t, a, "projects.close", ProjectsCloseParams{Name: "alpha"}, nil)
	if stateOf(t, a, dirA) != "" {
		t.Fatal("alpha still open")
	}
	wantCode(t, a, "projects.switch", ProjectsSwitchParams{Name: "beta", Previous: "nope"}, CodeInvalidParams)
	wantCode(t, a, "projects.open", ProjectsSwitchParams{Path: dirA, Previous: "nope"}, CodeInvalidParams)
}

// Running configurations keep the project left in the background.
func TestProjectsSwitchBusyStaysBackground(t *testing.T) {
	a := newTestApp(t)
	dirA, dirB := projectDir(t, "alpha"), projectDir(t, "beta")
	call(t, a, "projects.open", ProjectsSwitchParams{Path: dirA}, nil)
	var ri run.Info
	call(t, a, "run.start", RunStartParams{Spec: &run.Spec{Name: "sleeper", Program: selfExe(t), Env: []string{runHelperEnv + "=sleep"}}}, &ri)
	var ps ProjectSession
	call(t, a, "projects.open", ProjectsSwitchParams{Path: dirB}, &ps)
	if ps.PreviousState != workspace.StateBackground || len(ps.PreviousBusy) != 1 || !strings.HasPrefix(ps.PreviousBusy[0], "run: ") {
		t.Fatalf("open beta %+v", ps)
	}
	if stateOf(t, a, dirA) != workspace.StateBackground {
		t.Fatal("busy project hibernated")
	}
	// Explicit background keeps beta running too.
	call(t, a, "projects.switch", ProjectsSwitchParams{Name: "alpha", Previous: settings.OnSwitchBackground}, &ps)
	if ps.Previous != "" || stateOf(t, a, dirB) != workspace.StateBackground {
		t.Fatalf("switch %+v", ps)
	}
	// Close stops the run (force).
	call(t, a, "projects.close", ProjectsCloseParams{Name: "alpha", Force: true}, nil)
}
