package api

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

func needSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
}

// waitScreen polls terminal.text until it contains want.
func waitScreen(t *testing.T, a *App, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var txt TerminalText
	for time.Now().Before(deadline) {
		call(t, a, "terminal.text", TerminalTextParams{ID: id}, &txt)
		if strings.Contains(txt.Text, want) {
			return txt.Text
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal %s: no %q in %q", id, want, txt.Text)
	return ""
}

func TestTerminalService(t *testing.T) {
	needSh(t)
	a := newTestApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, a, "project.open", map[string]string{"path": dir}, nil)
	ev := &runEvents{}
	unsub := a.Bus().Subscribe("terminal.*", func(e bus.Event) { ev.add(e.Topic) })
	defer unsub()
	// The shell comes from the setting (project value wins).
	if err := settings.Set(a.ProjectSettings(), terminal.KeyShell, "/bin/sh"); err != nil {
		t.Fatal(err)
	}
	var info terminal.Info
	call(t, a, "terminal.open", TerminalOpenParams{Cols: 60, Rows: 8, Env: []string{"PS1=$ "}}, &info)
	if !info.Running || info.Command[0] != "/bin/sh" || info.Dir != a.ProjectRoot() || info.Cols != 60 || info.Project != a.ProjectRoot() {
		t.Fatalf("opened %+v", info)
	}
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "echo \"x.php:3 $TERM\"; printf '\\033]0;T1\\007'"}, nil)
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Key: &vt.Key{Name: "Enter"}}, nil)
	waitScreen(t, a, info.ID, "x.php:3 xterm-256color")

	var sc terminal.Screen
	deadline := time.Now().Add(5 * time.Second)
	for {
		call(t, a, "terminal.screen", TerminalScreenParams{ID: info.ID, Links: true}, &sc)
		found := false
		for _, l := range sc.Lines {
			if len(l.Links) == 1 && l.Links[0].File == filepath.Join(a.ProjectRoot(), "x.php") && l.Links[0].Line == 3 {
				found = true
			}
		}
		if found && sc.Title == "T1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no link or title in %+v", sc)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sc.Cols != 60 || sc.Rows != 8 || len(sc.Lines) != 8 || !sc.Running {
		t.Fatalf("screen %+v", sc)
	}
	top := 0
	call(t, a, "terminal.screen", TerminalScreenParams{ID: info.ID, Top: &top, Count: 2, NoSpans: true}, &sc)
	if len(sc.Lines) != 2 || sc.Top != 0 {
		t.Fatalf("range %+v", sc)
	}
	var txt TerminalText
	call(t, a, "terminal.text", TerminalTextParams{ID: info.ID, Screen: true}, &txt)
	if !strings.Contains(txt.Text, "xterm-256color") {
		t.Fatalf("screen text %q", txt.Text)
	}
	call(t, a, "terminal.text", TerminalTextParams{ID: info.ID, From: &TerminalPos{}, To: &TerminalPos{Line: 0, Col: 1}}, &txt)
	wantCode(t, a, "terminal.text", TerminalTextParams{ID: info.ID, From: &TerminalPos{}}, CodeInvalidParams)

	call(t, a, "terminal.resize", TerminalResizeParams{ID: info.ID, Cols: 70, Rows: 9}, &info)
	if info.Cols != 70 || info.Rows != 9 {
		t.Fatalf("resized %+v", info)
	}
	wantCode(t, a, "terminal.resize", TerminalResizeParams{ID: info.ID}, CodeInvalidParams)
	call(t, a, "terminal.rename", TerminalRenameParams{ID: info.ID, Name: "Build"}, &info)
	if info.Name != "Build" {
		t.Fatalf("renamed %+v", info)
	}
	wantCode(t, a, "terminal.rename", TerminalRenameParams{ID: info.ID}, CodeInvalidParams)
	wantCode(t, a, "terminal.input", TerminalInputParams{ID: info.ID}, CodeInvalidParams)
	wantCode(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "x", Paste: "y"}, CodeInvalidParams)
	wantCode(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Key: &vt.Key{Name: "Nope"}}, CodeInvalidParams)
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Paste: "exit 5\n"}, nil)
	var done terminal.Info
	call(t, a, "terminal.wait", TerminalWaitParams{ID: info.ID, TimeoutMs: 10000}, &done)
	if done.Running || done.ExitCode != 5 {
		t.Fatalf("exited %+v", done)
	}
	wantCode(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "x"}, CodeConflict)
	var list []terminal.Info
	call(t, a, "terminal.list", nil, &list)
	if len(list) != 1 || list[0].Name != "Build" {
		t.Fatalf("list %+v", list)
	}
	call(t, a, "terminal.close", TerminalIDParams{ID: info.ID}, nil)
	id := info.ID
	for m, p := range map[string]any{
		"terminal.close": TerminalIDParams{ID: id}, "terminal.screen": TerminalScreenParams{ID: id},
		"terminal.input": TerminalInputParams{ID: id, Data: "x"}, "terminal.resize": TerminalResizeParams{ID: id, Cols: 1, Rows: 1},
		"terminal.rename": TerminalRenameParams{ID: id, Name: "n"}, "terminal.text": TerminalTextParams{ID: id},
		"terminal.wait": TerminalWaitParams{ID: id},
	} {
		wantCode(t, a, m, p, CodeNotFound)
	}
	deadline = time.Now().Add(5 * time.Second)
	for !(ev.has("terminal.opened") && ev.has("terminal.output") && ev.has("terminal.changed") && ev.has("terminal.exited") && ev.has("terminal.closed")) {
		if time.Now().After(deadline) {
			t.Fatalf("events %v", ev.list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalOpenErrors(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "terminal.open", TerminalOpenParams{Cols: -1}, CodeInvalidParams)
	wantCode(t, a, "terminal.open", TerminalOpenParams{Dir: filepath.Join(t.TempDir(), "missing")}, CodeInvalidParams)
	wantCode(t, a, "terminal.open", TerminalOpenParams{Kind: "docker.service"}, CodeInvalidParams)
	// No plugin for the kind.
	wantCode(t, a, "terminal.open", TerminalOpenParams{Kind: "docker.service", Target: "app"}, CodeUnavailable)
	wantCode(t, a, "terminal.open", TerminalOpenParams{Shell: filepath.Join(t.TempDir(), "no-shell"), Dir: t.TempDir()}, CodeUnavailable)
	wantCode(t, a, "terminal.wait", TerminalWaitParams{ID: "nope"}, CodeNotFound)
}

// TestTerminalLaunchHook: plugin kinds get their program from the
// terminal.launch filter.
func TestTerminalLaunchHook(t *testing.T) {
	needSh(t)
	a := newTestApp(t)
	HookTerminalLaunch.On(a.Hooks(), 0, func(_ context.Context, q TerminalLaunch) (TerminalLaunch, error) {
		switch {
		case q.Kind != "test.kind":
			return q, nil
		case q.Target == "bad":
			q.Err = "service bad is not running"
			return q, nil
		}
		q.Program, q.Args, q.Env, q.Name = "/bin/sh", []string{"-c", "echo in-$TARGET; sleep 30"}, []string{"TARGET=" + q.Target}, "svc "+q.Target
		return q, nil
	}, hooks.WithOwner("test"))
	var info terminal.Info
	call(t, a, "terminal.open", TerminalOpenParams{Kind: "test.kind", Target: "app", Dir: t.TempDir()}, &info)
	if info.Kind != "test.kind" || info.Target != "app" || info.Name != "svc app" {
		t.Fatalf("info %+v", info)
	}
	waitScreen(t, a, info.ID, "in-app")
	wantCode(t, a, "terminal.wait", TerminalWaitParams{ID: info.ID, TimeoutMs: 20}, CodeConflict)
	wantCode(t, a, "terminal.open", TerminalOpenParams{Kind: "test.kind", Target: "bad"}, CodeUnavailable)
	// App.Close ends the running session (the cleanup closes the App).
}
