package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

// recorder collects the callbacks of a Manager.
type recorder struct {
	mu      sync.Mutex
	started []Info
	lines   map[string][]Line
	exited  map[string]Info
	order   []string
}

func newRecorder(m *Manager) *recorder {
	r := &recorder{lines: map[string][]Line{}, exited: map[string]Info{}}
	m.OnStart = func(i Info) { r.mu.Lock(); r.started = append(r.started, i); r.mu.Unlock() }
	m.OnOutput = func(o Output) {
		r.mu.Lock()
		r.lines[o.RunID] = append(r.lines[o.RunID], o.Lines...)
		r.order = append(r.order, "output "+o.RunID)
		r.mu.Unlock()
	}
	m.OnExit = func(i Info) {
		r.mu.Lock()
		r.exited[i.ID] = i
		r.order = append(r.order, "exit "+i.ID)
		r.mu.Unlock()
	}
	return r
}

func (r *recorder) texts(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.lines[id] {
		out = append(out, l.Stream+":"+l.Text)
	}
	return out
}

func wait(t *testing.T, m *Manager, id string) Info {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := m.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// waitLine waits until run id produced a line with text.
func waitLine(t *testing.T, m *Manager, id, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		pg, err := m.Output(id, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range pg.Lines {
			if l.Text == text {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no line %q", text)
}

func TestManagerRunToExit(t *testing.T) {
	m := &Manager{}
	rec := newRecorder(m)
	spec, tgt := helperSpec(t, "echo", "hello", "src/A.php:3")
	spec.Env = append(spec.Env, "FOO=bar", "TIEGEL_RUN_EXIT=3")
	if err := os.MkdirAll(filepath.Join(tgt.ProjectDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt.ProjectDir, "src", "A.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{Project: tgt.ProjectDir})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != StateRunning || info.ID != "run-1" || info.Name != "echo" || info.Project != tgt.ProjectDir || info.Where != InterpreterHost {
		t.Fatalf("start %+v", info)
	}
	done := wait(t, m, info.ID)
	if done.State != StateExited || done.ExitCode != 3 || done.Finished.IsZero() || done.DurationMs < 0 {
		t.Fatalf("done %+v", done)
	}
	got := rec.texts(info.ID)
	joined := strings.Join(got, "|")
	for _, want := range []string{"stdout:hello", "stdout:src/A.php:3", "stderr:err:bar", "system:Process finished with exit code 3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("lines %v miss %q", got, want)
		}
	}
	if !strings.HasPrefix(rec.order[len(rec.order)-1], "exit ") {
		t.Fatalf("exit not last: %v", rec.order)
	}
	pg, err := m.Output(info.ID, 0, 0)
	if err != nil || pg.Last != len(got) || len(pg.Lines) != len(got) || pg.State != StateExited {
		t.Fatalf("page %+v %v", pg, err)
	}
	var link *Link
	for _, l := range pg.Lines {
		if len(l.Links) > 0 {
			link = &l.Links[0]
		}
	}
	if link == nil || link.File != filepath.Join(tgt.ProjectDir, "src", "A.php") || link.Line != 3 {
		t.Fatalf("link %+v", link)
	}
	tail, _ := m.Output(info.ID, 0, 1)
	if len(tail.Lines) != 1 || !tail.Dropped || tail.Lines[0].Stream != StreamSystem {
		t.Fatalf("tail %+v", tail)
	}
	none, _ := m.Output(info.ID, pg.Last, 0)
	if len(none.Lines) != 0 || none.Dropped {
		t.Fatalf("since last %+v", none)
	}
	if len(rec.started) != 1 {
		t.Fatalf("started %v", rec.started)
	}
	if list := m.List(); len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("list %+v", list)
	}
	if len(m.Active()) != 0 {
		t.Fatal("active after exit")
	}
	if err := m.Remove(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(info.ID); !errors.Is(err, ErrUnknownRun) {
		t.Fatalf("get removed: %v", err)
	}
	for _, f := range []func() error{
		func() error { return m.Stop("nope") },
		func() error { return m.Remove("nope") },
		func() error { _, err := m.Output("nope", 0, 0); return err },
		func() error { _, err := m.Wait(context.Background(), "nope"); return err },
		func() error { _, err := m.Rerun(context.Background(), "nope"); return err },
	} {
		if err := f(); !errors.Is(err, ErrUnknownRun) {
			t.Fatalf("unknown run: %v", err)
		}
	}
}

func TestManagerStopTreeAndRerun(t *testing.T) {
	m := &Manager{}
	rec := newRecorder(m)
	spec, tgt := helperSpec(t, "tree")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, info.ID, "ready")
	if len(m.Active()) != 1 {
		t.Fatal("not active")
	}
	if err := m.Remove(info.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("remove running: %v", err)
	}
	start := time.Now()
	if err := m.Stop(info.ID); err != nil {
		t.Fatal(err)
	}
	done := wait(t, m, info.ID)
	if done.State != StateStopped || time.Since(start) > 15*time.Second {
		t.Fatalf("stop %+v after %v", done, time.Since(start))
	}
	if err := m.Stop(info.ID); err != nil {
		t.Fatalf("stop finished: %v", err)
	}
	if txt := rec.texts(info.ID); txt[len(txt)-1] != "system:Process stopped" {
		t.Fatalf("lines %v", txt)
	}

	// Rerun replaces the run.
	again, err := m.Rerun(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Replaces != info.ID || again.ID == info.ID {
		t.Fatalf("rerun %+v", again)
	}
	if _, err := m.Get(info.ID); err == nil {
		t.Fatal("old run kept")
	}
	waitLine(t, m, again.ID, "ready")
	// Rerunning an active run stops it first.
	third, err := m.Rerun(context.Background(), again.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, third.ID, "ready")
	if ids := m.StopAll(func(i Info) bool { return i.ID == third.ID }); len(ids) != 1 {
		t.Fatalf("stop all %v", ids)
	}
	if wait(t, m, third.ID).State != StateStopped {
		t.Fatal("not stopped")
	}
}

func TestManagerLinesAndLimits(t *testing.T) {
	m := &Manager{MaxLines: 3, MaxRuns: 1, Exists: func(string) bool { return true }}
	rec := newRecorder(m)
	spec, tgt := helperSpec(t, "long")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, m, info.ID)
	got := rec.texts(info.ID)
	total := 0
	for _, l := range got[:len(got)-2] {
		total += len(strings.TrimPrefix(l, "stdout:"))
		if len(l)-len("stdout:") > maxLineBytes {
			t.Fatalf("line of %d bytes", len(l))
		}
	}
	if total != 200000 || got[len(got)-2] != "stdout:partial" {
		t.Fatalf("long line split wrong: total %d, %d lines, tail %q", total, len(got), got[len(got)-2:])
	}
	pg, _ := m.Output(info.ID, 0, 0)
	if len(pg.Lines) != 3 || !pg.Dropped {
		t.Fatalf("buffer %d dropped %v", len(pg.Lines), pg.Dropped)
	}

	spec2, tgt2 := helperSpec(t, "special")
	info2, err := m.Start(context.Background(), spec2, launcherFor(spec2, tgt2), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, m, info2.ID)
	want := []string{"stdout:100%", "stdout:green", "stdout:", "stdout:end"}
	if got := rec.texts(info2.ID); strings.Join(got[:4], "|") != strings.Join(want, "|") {
		t.Fatalf("special lines %q", got)
	}
	// Starting another run prunes the oldest finished one (MaxRuns 1).
	if _, err := m.Get(info.ID); err == nil {
		t.Fatal("first run not pruned")
	}
}

// markerInterp is an Execer that runs the helper in marker mode and
// records the remote stop.
type markerInterp struct {
	self    string
	stopped chan string
}

func (markerInterp) ID() string                                    { return "fake:marker" }
func (markerInterp) Label() string                                 { return "Fake" }
func (markerInterp) Kind() interp.Kind                             { return "fake" }
func (markerInterp) Version(context.Context) (string, error)       { return "8.3.0", nil }
func (markerInterp) Modules(context.Context) ([]string, error)     { return nil, nil }
func (markerInterp) Ini(context.Context) (interp.IniValues, error) { return nil, nil }
func (markerInterp) Command(context.Context, ...string) (*exec.Cmd, error) {
	return nil, errors.New("no")
}

func (f markerInterp) ExecCommand(_ context.Context, x Exec) (*Launch, error) {
	cmd := exec.Command(f.self)
	cmd.Env = append(os.Environ(), helperEnv+"=marker")
	return &Launch{Cmd: cmd, Display: "fake", Where: "Fake", PIDMarker: pidMarker,
		StopRemote: func(_ context.Context, pid string) error { f.stopped <- pid; return nil }}, nil
}

func TestManagerRemoteStop(t *testing.T) {
	self, _ := os.Executable()
	fi := markerInterp{self: self, stopped: make(chan string, 1)}
	m := &Manager{}
	rec := newRecorder(m)
	spec := Spec{Name: "m", PHP: true}
	info, err := m.Start(context.Background(), spec, launcherFor(spec, Target{Interp: fi}), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, info.ID, "ready")
	_ = m.Stop(info.ID)
	select {
	case pid := <-fi.stopped:
		if pid != "4711" {
			t.Fatalf("pid %q", pid)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("remote stop not called")
	}
	wait(t, m, info.ID)
	for _, l := range rec.texts(info.ID) {
		if strings.Contains(l, pidMarker) {
			t.Fatalf("marker leaked: %v", l)
		}
	}
}

func TestManagerStartErrors(t *testing.T) {
	m := &Manager{}
	if _, err := m.Start(context.Background(), Spec{}, nil, StartOptions{}); err == nil {
		t.Fatal("nil launcher")
	}
	if _, err := m.Start(context.Background(), Spec{}, func(context.Context) (*Launch, error) { return nil, nil }, StartOptions{}); err == nil {
		t.Fatal("nil launch")
	}
	boom := errors.New("boom")
	if _, err := m.Start(context.Background(), Spec{}, func(context.Context) (*Launch, error) { return nil, boom }, StartOptions{}); !errors.Is(err, boom) {
		t.Fatalf("launcher error %v", err)
	}
	bad := func(context.Context) (*Launch, error) {
		return &Launch{Cmd: exec.Command(filepath.Join(t.TempDir(), "missing")), Display: "missing"}, nil
	}
	if _, err := m.Start(context.Background(), Spec{Program: "missing"}, bad, StartOptions{}); err == nil {
		t.Fatal("missing program started")
	}
	if len(m.List()) != 0 {
		t.Fatal("failed start left a run")
	}
	spec, tgt := helperSpec(t, "sleep")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Wait(ctx, info.ID); err == nil {
		t.Fatal("wait did not time out")
	}
	m.StopAll(nil)
	wait(t, m, info.ID)
}
