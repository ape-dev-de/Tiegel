package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestManagerExitRacingStop: a stop that arrives after the process ended
// by itself (cmd.Wait returned) does not turn the run into "stopped".
func TestManagerExitRacingStop(t *testing.T) {
	m := &Manager{}
	m.afterWait = func(p *proc) {
		m.stop(p)
		time.Sleep(50 * time.Millisecond) // let the stop goroutine act
	}
	spec, tgt := helperSpec(t, "echo", "hi")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := wait(t, m, info.ID)
	if done.State != StateExited || done.ExitCode != 0 {
		t.Fatalf("exited run reported as %+v", done)
	}
	pg, _ := m.Output(info.ID, 0, 0)
	if last := pg.Lines[len(pg.Lines)-1].Text; last != "Process finished with exit code 0" {
		t.Fatalf("last line %q", last)
	}
}

// TestManagerLinksOutsideLock: checking link targets on disk does not
// block state queries of the run.
func TestManagerLinksOutsideLock(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	m := &Manager{Exists: func(string) bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return true
	}}
	spec, tgt := helperSpec(t, "echo", "src/A.php:3")
	started := make(chan Info, 1)
	go func() {
		info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
		if err != nil {
			t.Error(err)
		}
		started <- info
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("no link check")
	}
	listed := make(chan []Info, 1)
	go func() { listed <- m.List() }()
	select {
	case l := <-listed:
		if len(l) != 1 || !l[0].Active() {
			t.Fatalf("list %+v", l)
		}
	case <-time.After(2 * time.Second):
		unblock()
		t.Fatal("List blocked by a link check")
	}
	unblock()
	info := <-started
	wait(t, m, info.ID)
}

// TestManagerConcurrentRerun: two Reruns of the same run (double click)
// start one process and return the same run.
func TestManagerConcurrentRerun(t *testing.T) {
	m := &Manager{}
	spec, tgt := helperSpec(t, "sleep")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, info.ID, "ready")
	var (
		wg   sync.WaitGroup
		got  [2]Info
		errs [2]error
	)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = m.Rerun(context.Background(), info.ID)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors %v %v", errs[0], errs[1])
	}
	if got[0].ID != got[1].ID || got[0].Replaces != info.ID {
		t.Fatalf("two reruns %s %s", got[0].ID, got[1].ID)
	}
	if a := m.Active(); len(a) != 1 {
		t.Fatalf("active %+v", a)
	}
	// A later rerun of the new run starts again.
	third, err := m.Rerun(context.Background(), got[0].ID)
	if err != nil || third.ID == got[0].ID {
		t.Fatalf("third %+v %v", third, err)
	}
	m.StopAll(nil)
	wait(t, m, third.ID)

	// A failed rerun can be tried again.
	var mu sync.Mutex
	n := 0
	flaky := func(ctx context.Context) (*Launch, error) {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		if k > 1 {
			return nil, errors.New("gone")
		}
		return Prepare(ctx, spec, tgt)
	}
	r, err := m.Start(context.Background(), spec, flaky, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m.StopAll(nil)
	wait(t, m, r.ID)
	for range 2 {
		if _, err := m.Rerun(context.Background(), r.ID); err == nil || err.Error() != "gone" {
			t.Fatalf("failed rerun %v", err)
		}
	}
	if n != 3 {
		t.Fatalf("launcher calls %d", n)
	}
}

// TestManagerLinksFromWorkDir: relative paths resolve against the
// program's working directory (Launch.Dir), not where the client runs.
func TestManagerLinksFromWorkDir(t *testing.T) {
	spec, tgt := helperSpec(t, "echo", "a.php:3")
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher := func(ctx context.Context) (*Launch, error) {
		l, err := Prepare(ctx, spec, tgt)
		if err != nil {
			return nil, err
		}
		l.Dir = work // l.Cmd.Dir stays the project dir (like the compose dir)
		return l, nil
	}
	m := &Manager{}
	info, err := m.Start(context.Background(), spec, launcher, StartOptions{LinkDirs: []string{tgt.ProjectDir}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Dir != work {
		t.Fatalf("dir %q", info.Dir)
	}
	wait(t, m, info.ID)
	pg, _ := m.Output(info.ID, 0, 0)
	for _, l := range pg.Lines {
		if l.Text == "a.php:3" {
			if len(l.Links) != 1 || l.Links[0].File != filepath.Join(work, "a.php") {
				t.Fatalf("links %+v", l.Links)
			}
			return
		}
	}
	t.Fatalf("no line in %+v", pg.Lines)
}

// TestManagerRemoteStopError: a failing stop inside the container is
// reported in the output; the local client is killed anyway.
func TestManagerRemoteStopError(t *testing.T) {
	self, _ := os.Executable()
	launcher := func(context.Context) (*Launch, error) {
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), helperEnv+"=marker")
		return &Launch{Cmd: cmd, Display: "fake", PIDMarker: pidMarker,
			StopRemote: func(context.Context, string) error { return errors.New("kill: not found") }}, nil
	}
	m := &Manager{}
	info, err := m.Start(context.Background(), Spec{Name: "m", Program: "x"}, launcher, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitLine(t, m, info.ID, "ready")
	_ = m.Stop(info.ID)
	if done := wait(t, m, info.ID); done.State != StateStopped {
		t.Fatalf("state %+v", done)
	}
	waitLine(t, m, info.ID, "Stopping the process in the container failed: kill: not found")
}

func TestManagerKeep(t *testing.T) {
	m := &Manager{}
	spec, tgt := helperSpec(t, "sleep")
	info, err := m.Start(context.Background(), spec, launcherFor(spec, tgt), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Keep(info.ID); err != nil {
		t.Fatal(err)
	}
	if i, _ := m.Get(info.ID); !i.Keep {
		t.Fatalf("not kept %+v", i)
	}
	if err := m.Keep("nope"); !errors.Is(err, ErrUnknownRun) {
		t.Fatal(err)
	}
	m.StopAll(nil)
	wait(t, m, info.ID)
}
