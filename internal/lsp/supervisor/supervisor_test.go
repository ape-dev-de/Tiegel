package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/fakeserver"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// The test binary doubles as fake language server process.
func TestMain(m *testing.M) {
	switch os.Getenv("TIEGEL_SUPERVISOR_FAKE") {
	case "serve":
		fmt.Fprintln(os.Stderr, "fake server says hello")
		fakeserver.Main()
	case "crash":
		fmt.Fprintln(os.Stderr, "fatal: broken installation")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// fakes connects to in-process fake servers.
type fakes struct {
	mu   sync.Mutex
	srvs []*fakeserver.Server
	cfgs []lsp.Config
	fail error
}

func (f *fakes) connect(ctx context.Context, cfg lsp.Config) (*lsp.Client, error) {
	f.mu.Lock()
	if f.fail != nil {
		err := f.fail
		f.mu.Unlock()
		return nil, err
	}
	a, b := net.Pipe()
	f.srvs = append(f.srvs, fakeserver.Serve(b, fakeserver.Options{}))
	f.cfgs = append(f.cfgs, cfg)
	f.mu.Unlock()
	return lsp.Connect(ctx, a, cfg)
}

func (f *fakes) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func (f *fakes) last() *fakeserver.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.srvs[len(f.srvs)-1]
}

func (f *fakes) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.srvs)
}

// recorder collects states.
type recorder struct {
	mu     sync.Mutex
	states []Status
	ch     chan Status
}

func newRecorder() *recorder { return &recorder{ch: make(chan Status, 256)} }

func (r *recorder) on(st Status) {
	r.mu.Lock()
	r.states = append(r.states, st)
	r.mu.Unlock()
	r.ch <- st
}

func (r *recorder) wait(t *testing.T, what string, pred func(Status) bool) Status {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case st := <-r.ch:
			if pred(st) {
				return st
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func state(s State) func(Status) bool { return func(st Status) bool { return st.State == s } }

func newSup(t *testing.T, f *fakes, r *recorder, mod func(*Options)) *Supervisor {
	t.Helper()
	opts := Options{
		Connect:     f.connect,
		Backoff:     func(int) time.Duration { return 10 * time.Millisecond },
		MaxCrashes:  3,
		InitTimeout: 5 * time.Second,
		OnState:     r.on,
	}
	if mod != nil {
		mod(&opts)
	}
	s := New(opts)
	t.Cleanup(s.Close)
	return s
}

func target(id string) Target {
	return Target{ID: id, Name: strings.ToUpper(id), Command: id, Args: []string{"--stdio"}}
}

func TestStartCrashRestartAndLoopStop(t *testing.T) {
	f, r := &fakes{}, newRecorder()
	var ready []int
	var mu sync.Mutex
	s := newSup(t, f, r, func(o *Options) {
		o.OnReady = func(i *Instance) { mu.Lock(); ready = append(ready, i.Gen); mu.Unlock() }
	})
	if st := s.Status(); st.State != StateOff {
		t.Fatalf("initial %+v", st)
	}
	if err := s.Restart(context.Background()); err == nil {
		t.Fatal("restart without target")
	}
	if err := s.Start(context.Background(), target("a")); err != nil {
		t.Fatal(err)
	}
	r.wait(t, "starting", state(StateStarting))
	st := r.wait(t, "running", state(StateRunning))
	if st.Server != "a" || st.Name != "A" || st.Generation != 1 || st.ServerInfo != "fake 1.0" {
		t.Fatalf("running %+v", st)
	}
	if s.Current() == nil || s.Current().Client() == nil {
		t.Fatal("no current instance")
	}
	if tg, ok := s.Target(); !ok || tg.ID != "a" {
		t.Fatal("target")
	}
	// Same target again: no second server.
	if err := s.Start(context.Background(), target("a")); err != nil || f.count() != 1 {
		t.Fatalf("restarted same target: %v %d", err, f.count())
	}

	// Crash → restart with backoff, documents re-synced via OnReady.
	_ = f.last().Close()
	st = r.wait(t, "crashed", state(StateCrashed))
	if st.Crashes != 1 || st.RetryAt == nil || !strings.Contains(st.Reason, "exited") {
		t.Fatalf("crashed %+v", st)
	}
	st = r.wait(t, "running again", state(StateRunning))
	if st.Generation != 2 || st.Restarts != 1 {
		t.Fatalf("restarted %+v", st)
	}
	mu.Lock()
	if len(ready) != 2 {
		t.Fatalf("OnReady %v", ready)
	}
	mu.Unlock()

	// Crash loop: every restart fails → stopped after MaxCrashes.
	f.setFail(errors.New("boom"))
	_ = f.last().Close()
	st = r.wait(t, "stopped", state(StateStopped))
	if st.Crashes != 3 || !strings.Contains(st.Reason, "3 times in a row") || !strings.Contains(st.Reason, "boom") {
		t.Fatalf("loop stop %+v", st)
	}
	n := f.count()
	time.Sleep(50 * time.Millisecond)
	if f.count() != n || s.Status().State != StateStopped {
		t.Fatal("restarted after giving up")
	}
	// An explicit restart resets the counter.
	f.setFail(nil)
	if err := s.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.State != StateRunning || st.Crashes != 0 {
		t.Fatalf("after restart %+v", st)
	}
	// Log has the story.
	var events []string
	for _, e := range s.Log().Since(0, 0) {
		if e.Kind == KindEvent {
			events = append(events, e.Text)
		}
	}
	all := strings.Join(events, "\n")
	for _, w := range []string{"starting", "crashed", "giving up", "running"} {
		if !strings.Contains(all, w) {
			t.Errorf("log lacks %q:\n%s", w, all)
		}
	}
}

func TestSwitchStopAndNotFound(t *testing.T) {
	f, r := &fakes{}, newRecorder()
	s := newSup(t, f, r, nil)
	if err := s.Start(context.Background(), target("a")); err != nil {
		t.Fatal(err)
	}
	first := f.last()
	// Switching shuts the first server down cleanly and starts the second.
	if err := s.Start(context.Background(), target("b")); err != nil {
		t.Fatal(err)
	}
	if first.Snapshot().ShutdownCalls != 1 {
		t.Fatal("old server not shut down")
	}
	if st := s.Status(); st.State != StateRunning || st.Server != "b" || st.Generation != 2 {
		t.Fatalf("switched %+v", st)
	}
	if !s.Same(target("b")) || s.Same(target("a")) {
		t.Fatal("Same")
	}
	// Reload restarts even the same target.
	if err := s.Reload(context.Background(), target("b")); err != nil || s.Status().Generation != 3 {
		t.Fatalf("reload %v %+v", err, s.Status())
	}
	// Disable: no process, state off.
	s.Stop(StateOff, "disabled", nil)
	if st := s.Status(); st.State != StateOff || st.Server != "" || st.Reason != "disabled" {
		t.Fatalf("off %+v", st)
	}
	if s.Current() != nil {
		t.Fatal("instance survived")
	}
	if _, ok := s.Target(); ok {
		t.Fatal("target kept after off")
	}
	// Not found.
	tg := target("c")
	tg.Searched = []string{"/x", "/y"}
	s.NotFound(tg, "c is not installed")
	st := s.Status()
	if st.State != StateNotFound || st.Server != "c" || len(st.Searched) != 2 || st.Reason != "c is not installed" {
		t.Fatalf("not found %+v", st)
	}
	// Stopped keeps the target for a later restart.
	if err := s.Start(context.Background(), target("d")); err != nil {
		t.Fatal(err)
	}
	s.Stop(StateStopped, "stopped by user", nil)
	if tg, ok := s.Target(); !ok || tg.ID != "d" || s.Status().Server != "d" {
		t.Fatal("stopped lost target")
	}
	if err := s.Restart(context.Background()); err != nil || s.Status().State != StateRunning {
		t.Fatalf("restart after stop %v", err)
	}
	s.Close()
	if err := s.Start(context.Background(), target("a")); !errors.Is(err, ErrClosed) {
		t.Fatalf("start after close %v", err)
	}
	if err := s.Restart(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("restart after close %v", err)
	}
}

func TestDisableMidRequestAndHung(t *testing.T) {
	f, r := &fakes{}, newRecorder()
	s := newSup(t, f, r, func(o *Options) {
		o.RequestTimeout = 100 * time.Millisecond
		o.HungLimit = 2
	})
	if err := s.Start(context.Background(), target("a")); err != nil {
		t.Fatal(err)
	}
	c := s.Current().Client()
	uri := protocol.URIFromPath(filepath.Join(t.TempDir(), "a.php"))
	text := strings.Repeat("\n", fakeserver.BlockLine) + "x"
	if err := c.DidOpen(context.Background(), uri, "php", 1, text); err != nil {
		t.Fatal(err)
	}
	block := protocol.Position{Line: fakeserver.BlockLine, Character: 0}
	// Hung: two timed out requests in a row → killed and restarted.
	for i := 0; i < 2; i++ {
		if _, err := c.Hover(context.Background(), uri, block); err == nil || !strings.Contains(err.Error(), "no answer") {
			t.Fatalf("hover %d: %v", i, err)
		}
	}
	st := r.wait(t, "crashed (hung)", state(StateCrashed))
	if !strings.Contains(st.Reason, "not responding") {
		t.Fatalf("hung reason %+v", st)
	}
	r.wait(t, "running after hang", state(StateRunning))

	// Disable while a request is pending: the request fails promptly.
	s2 := newSup(t, &fakes{}, newRecorder(), func(o *Options) { o.RequestTimeout = -1 })
	if err := s2.Start(context.Background(), target("b")); err != nil {
		t.Fatal(err)
	}
	c2 := s2.Current().Client()
	if err := c2.DidOpen(context.Background(), uri, "php", 1, text); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := c2.Hover(context.Background(), uri, block)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	s2.Stop(StateOff, "disabled", nil)
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("pending request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request hangs after disable")
	}
	if s2.Status().State != StateOff {
		t.Fatal("not off")
	}
}

func TestAbortStartAndInitFailure(t *testing.T) {
	r := newRecorder()
	block := make(chan struct{})
	s := New(Options{
		Connect: func(ctx context.Context, _ lsp.Config) (*lsp.Client, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-block:
				return nil, errors.New("never")
			}
		},
		InitTimeout: 5 * time.Second, MaxCrashes: 1, OnState: r.on,
	})
	defer s.Close()
	errc := make(chan error, 1)
	go func() { errc <- s.Start(context.Background(), target("slow")) }()
	r.wait(t, "starting", state(StateStarting))
	// Starting the same target again is a no-op while it starts.
	if err := s.Start(context.Background(), target("slow")); err != nil {
		t.Fatal(err)
	}
	s.Stop(StateOff, "disabled while starting", nil)
	if err := <-errc; err == nil {
		t.Fatal("aborted start reported success")
	}
	if st := s.Status(); st.State != StateOff {
		t.Fatalf("after abort %+v", st)
	}

	// Initialize timeout counts as crash; MaxCrashes 1 stops at once.
	s3 := New(Options{
		Connect: func(ctx context.Context, _ lsp.Config) (*lsp.Client, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		InitTimeout: 50 * time.Millisecond, MaxCrashes: 1,
	})
	defer s3.Close()
	if err := s3.Start(context.Background(), target("mute")); err == nil {
		t.Fatal("init timeout not reported")
	}
	if st := s3.Status(); st.State != StateStopped || !strings.Contains(st.Reason, "no answer to initialize") {
		t.Fatalf("init timeout %+v", st)
	}
}

func TestCheckFailsAndStableRuns(t *testing.T) {
	f, r := &fakes{}, newRecorder()
	s := newSup(t, f, r, func(o *Options) {
		o.Check = func(cmd string, searched []string) (string, error) {
			if cmd == "missing" {
				return "", fmt.Errorf("%s not found in %d directories", cmd, len(searched))
			}
			return "/abs/" + cmd, nil
		}
		o.StableAfter = time.Nanosecond
	})
	tg := target("missing")
	tg.Searched = []string{"/a"}
	if err := s.Start(context.Background(), tg); err == nil {
		t.Fatal("missing executable started")
	}
	if st := s.Status(); st.State != StateNotFound || len(st.Searched) != 1 || f.count() != 0 {
		t.Fatalf("check %+v", st)
	}
	if err := s.Start(context.Background(), target("ok")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	cmd := f.cfgs[0].Command
	f.mu.Unlock()
	if cmd != "/abs/ok" {
		t.Fatalf("checked command %q", cmd)
	}
	// With StableAfter ~0 every crash starts a new series: never gives up.
	for i := 0; i < 4; i++ {
		_ = f.last().Close()
		st := r.wait(t, "crashed", state(StateCrashed))
		if st.Crashes != 1 {
			t.Fatalf("crash %d counted %d", i, st.Crashes)
		}
		r.wait(t, "running", state(StateRunning))
	}
}

func TestRealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process test uses the test binary with env (fine on Windows too, but slow)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := newRecorder()
	s := New(Options{Backoff: func(int) time.Duration { return 10 * time.Millisecond }, MaxCrashes: 2, OnState: r.on})
	defer s.Close()
	s.SetTrace(true)
	tg := Target{ID: "proc", Name: "Proc", Command: exe, Config: lsp.Config{Env: []string{"TIEGEL_SUPERVISOR_FAKE=serve"}}}
	if err := s.Start(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.State != StateRunning || st.PID == 0 || !st.Trace {
		t.Fatalf("process %+v", st)
	}
	waitLog := func(kind, text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for _, e := range s.Log().Since(0, 0) {
				if e.Kind == kind && strings.Contains(e.Text, text) {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("log lacks %s %q: %+v", kind, text, s.Log().Since(0, 0))
	}
	waitLog(KindStderr, "fake server says hello")
	waitLog(KindSend, `"method":"initialize"`)
	waitLog(KindRecv, `"serverInfo"`)
	s.SetTrace(false)
	s.Stop(StateOff, "done", nil)

	// A server that exits at once: crash loop with stderr in the log.
	tg.Config.Env = []string{"TIEGEL_SUPERVISOR_FAKE=crash"}
	tg.ID = "crash"
	if err := s.Start(context.Background(), tg); err == nil {
		t.Fatal("crashing server started")
	}
	r.wait(t, "stopped", state(StateStopped))
	waitLog(KindStderr, "broken installation")

	// Missing and non-executable commands: not found.
	if err := s.Start(context.Background(), Target{ID: "x", Command: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("missing command started")
	}
	if st := s.Status(); st.State != StateNotFound || !strings.Contains(st.Reason, "not found") {
		t.Fatalf("missing %+v", st)
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background(), Target{ID: "y", Command: plain}); err == nil || !strings.Contains(s.Status().Reason, "not executable") {
		t.Fatalf("not executable: %v %+v", err, s.Status())
	}
}

func TestLogRingAndBackoff(t *testing.T) {
	var got []Entry
	l := NewLog(1, func(e Entry) { got = append(got, e) })
	for i := 0; i < 20; i++ {
		l.Append(KindStderr, "s", fmt.Sprint(i))
	}
	all := l.Since(0, 0)
	if len(all) != 16 || all[0].Text != "4" || all[15].Seq != 20 || len(got) != 20 {
		t.Fatalf("ring %d %+v", len(all), all[0])
	}
	if tail := l.Since(18, 0); len(tail) != 2 {
		t.Fatalf("since %+v", tail)
	}
	if lim := l.Since(0, 3); len(lim) != 3 || lim[2].Seq != 20 {
		t.Fatalf("limit %+v", lim)
	}
	l.Append(KindSend, "s", strings.Repeat("x", maxTraceText+10))
	if e := l.Since(20, 0)[0]; len(e.Text) > maxTraceText+4 {
		t.Fatal("trace not truncated")
	}
	l.Clear()
	if len(l.Since(0, 0)) != 0 {
		t.Fatal("clear")
	}
	if DefaultBackoff(1) != 500*time.Millisecond || DefaultBackoff(2) != time.Second || DefaultBackoff(20) != 30*time.Second {
		t.Fatal("backoff")
	}
}
