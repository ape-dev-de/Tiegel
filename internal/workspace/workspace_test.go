package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// fakeProject records its lifecycle calls.
type fakeProject struct {
	mu        sync.Mutex
	root      string
	running   bool // resources up
	busy      []string
	docs      []string
	resumed   []string
	unsaved   bool
	failClose error
	failRes   error
	calls     []string
}

func (p *fakeProject) record(s string) {
	p.mu.Lock()
	p.calls = append(p.calls, s)
	p.mu.Unlock()
}

func (p *fakeProject) Busy(context.Context) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.busy...)
}

func (p *fakeProject) Hibernate(_ context.Context, force bool) ([]string, error) {
	p.record(fmt.Sprintf("hibernate force=%v", force))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unsaved && !force {
		return nil, fmt.Errorf("a.php: %w", ErrUnsaved)
	}
	p.running = false
	return append([]string(nil), p.docs...), nil
}

func (p *fakeProject) Resume(_ context.Context, docs []string) error {
	p.record("resume")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failRes != nil {
		return p.failRes
	}
	p.running = true
	p.resumed = docs
	return nil
}

func (p *fakeProject) Close(_ context.Context, force bool) error {
	p.record(fmt.Sprintf("close force=%v", force))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failClose != nil {
		return p.failClose
	}
	p.running = false
	return nil
}

func (p *fakeProject) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// fakeClock fires timers synchronously in Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Time
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	keep := c.timers[:0]
	for _, t := range c.timers {
		switch {
		case t.stopped:
		case !t.at.After(c.now):
			t.stopped = true
			due = append(due, t)
		default:
			keep = append(keep, t)
		}
	}
	c.timers = keep
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

type env struct {
	m      *Manager
	clock  *fakeClock
	bus    *bus.Bus
	mu     sync.Mutex
	projs  map[string]*fakeProject
	events []string
	errs   []error
	change int
}

func newEnv(t *testing.T, auto time.Duration) *env {
	t.Helper()
	e := &env{clock: &fakeClock{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}, bus: bus.New(), projs: map[string]*fakeProject{}}
	e.bus.Subscribe("projects.*", func(ev bus.Event) {
		p := ev.Payload.(Event)
		e.mu.Lock()
		e.events = append(e.events, ev.Topic+" "+p.Name+" "+p.Reason)
		e.mu.Unlock()
	})
	m, err := New(Config{
		Open: func(_ context.Context, root string) (Project, error) {
			if filepath.Base(root) == "broken" {
				return nil, errors.New("cannot open")
			}
			p := &fakeProject{root: root, running: true}
			e.mu.Lock()
			e.projs[root] = p
			e.mu.Unlock()
			return p, nil
		},
		Bus: e.bus, Clock: e.clock, AutoHibernate: auto,
		OnChange: func() { e.mu.Lock(); e.change++; e.mu.Unlock() },
		OnError:  func(_ string, err error) { e.mu.Lock(); e.errs = append(e.errs, err); e.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	e.m = m
	t.Cleanup(func() { _ = e.bus.Close(context.Background()) })
	return e
}

func (e *env) proj(root string) *fakeProject {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.projs[root]
}

func (e *env) takeEvents(t *testing.T) []string {
	t.Helper()
	if err := e.bus.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.events
	e.events = nil
	return out
}

func root(name string) string { return filepath.Join(string(filepath.Separator), "src", name) }

func states(m *Manager) string {
	s := ""
	for _, i := range m.List() {
		s += i.Name + "=" + string(i.State) + " "
	}
	return s
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestOpenSwitchClose(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	a, err := e.m.Open(ctx, root("a"))
	if err != nil || a.State != StateActive || a.Name != "a" {
		t.Fatalf("open a: %+v %v", a, err)
	}
	if _, err := e.m.Open(ctx, root("b")); err != nil {
		t.Fatal(err)
	}
	if got := states(e.m); got != "a=background b=active " {
		t.Fatal(got)
	}
	eq(t, e.takeEvents(t), []string{"projects.opened a ", "projects.activated a ", "projects.opened b ", "projects.backgrounded a ", "projects.activated b "})
	// Opening an open project switches to it.
	if i, err := e.m.Open(ctx, root("a")); err != nil || i.State != StateActive {
		t.Fatalf("%+v %v", i, err)
	}
	if act, ok := e.m.Active(); !ok || act.Name != "a" {
		t.Fatal(act)
	}
	if _, err := e.m.Switch(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Switch(ctx, "b"); err != nil { // no-op
		t.Fatal(err)
	}
	e.takeEvents(t)
	if err := e.m.Close(ctx, "b", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.m.Active(); ok {
		t.Fatal("closed project still active")
	}
	if e.proj(root("b")).isRunning() {
		t.Fatal("b not closed")
	}
	eq(t, e.takeEvents(t), []string{"projects.closed b "})
	if got := states(e.m); got != "a=background " {
		t.Fatal(got)
	}
	if _, err := e.m.Get("b"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if e.change == 0 {
		t.Fatal("OnChange not called")
	}
}

func TestOpenErrors(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	if _, err := e.m.Open(ctx, "relative"); err == nil {
		t.Fatal("relative root accepted")
	}
	if _, err := e.m.Open(ctx, root("broken")); err == nil {
		t.Fatal("opener error lost")
	}
	if _, err := e.m.Add(ctx, root("x"), nil, true); err == nil {
		t.Fatal("nil project accepted")
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing opener accepted")
	}
	if _, err := e.m.Switch(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.m.Hibernate(ctx, "nope", HibernateOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.m.Resume(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := e.m.Close(ctx, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.m.Busy(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.m.Project("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestAmbiguousName(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	r1 := filepath.Join(root("x"), "app")
	r2 := filepath.Join(root("y"), "app")
	for _, r := range []string{r1, r2} {
		if _, err := e.m.Open(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.m.Switch(ctx, "app"); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
	if i, err := e.m.Switch(ctx, r1+string(filepath.Separator)); err != nil || i.Root != r1 {
		t.Fatalf("%+v %v", i, err)
	}
}

func TestHibernateResume(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	if _, err := e.m.Open(ctx, root("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Open(ctx, root("b")); err != nil {
		t.Fatal(err)
	}
	pa := e.proj(root("a"))
	pa.docs = []string{"/src/a/x.php"}
	e.takeEvents(t)
	client := json.RawMessage(`{"tabs":["x.php"]}`)
	i, err := e.m.Hibernate(ctx, "a", HibernateOptions{ClientState: client})
	if err != nil || i.State != StateHibernated || len(i.Docs) != 1 {
		t.Fatalf("%+v %v", i, err)
	}
	if pa.isRunning() {
		t.Fatal("resources still up")
	}
	// Hibernating again is a no-op (but may replace the client state).
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{ClientState: json.RawMessage(`{"tabs":[]}`)}); err != nil {
		t.Fatal(err)
	}
	eq(t, e.takeEvents(t), []string{"projects.hibernated a "})
	// Resume into the background (b stays active).
	i, err = e.m.Resume(ctx, "a")
	if err != nil || i.State != StateBackground || string(i.ClientState) != `{"tabs":[]}` {
		t.Fatalf("%+v %v", i, err)
	}
	eq(t, pa.resumed, []string{"/src/a/x.php"})
	if i, _ := e.m.Resume(ctx, "a"); i.State != StateBackground || i.ClientState != nil {
		t.Fatalf("resume of a running project: %+v", i)
	}
	eq(t, e.takeEvents(t), []string{"projects.resumed a "})
	// Hibernate the active project: none is active afterwards; resume
	// activates it again.
	if _, err := e.m.Hibernate(ctx, "b", HibernateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.m.Active(); ok {
		t.Fatal("hibernated project still active")
	}
	if i, err := e.m.Resume(ctx, "b"); err != nil || i.State != StateActive {
		t.Fatalf("%+v %v", i, err)
	}
	// Switch to a hibernated project resumes it and hands back the state.
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{ClientState: client}); err != nil {
		t.Fatal(err)
	}
	e.takeEvents(t)
	i, err = e.m.Switch(ctx, "a")
	if err != nil || i.State != StateActive || string(i.ClientState) != string(client) {
		t.Fatalf("%+v %v", i, err)
	}
	eq(t, e.takeEvents(t), []string{"projects.resumed a ", "projects.backgrounded b ", "projects.activated a "})
}

func TestBusyAndUnsaved(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	if _, err := e.m.Open(ctx, root("a")); err != nil {
		t.Fatal(err)
	}
	p := e.proj(root("a"))
	p.busy = []string{"run: phpunit"}
	if b, err := e.m.Busy(ctx, "a"); err != nil || len(b) != 1 {
		t.Fatal(b, err)
	}
	_, err := e.m.Hibernate(ctx, "a", HibernateOptions{})
	var be *BusyError
	if !errors.As(err, &be) || !errors.Is(err, ErrBusy) || be.Reasons[0] != "run: phpunit" || be.Error() == "" {
		t.Fatal(err)
	}
	if err := e.m.Close(ctx, "a", false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	p.busy = nil
	p.unsaved = true
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{}); !errors.Is(err, ErrUnsaved) {
		t.Fatal(err)
	}
	if got := states(e.m); got != "a=active " {
		t.Fatal(got)
	}
	p.busy = []string{"debug session 1"}
	e.takeEvents(t)
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	eq(t, e.takeEvents(t), []string{"projects.hibernated a forced"})
	// Closing a hibernated project ignores busy state.
	if err := e.m.Close(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Open(ctx, root("c")); err != nil {
		t.Fatal(err)
	}
	pc := e.proj(root("c"))
	pc.busy = []string{"run"}
	e.takeEvents(t)
	if err := e.m.Close(ctx, "c", true); err != nil {
		t.Fatal(err)
	}
	eq(t, e.takeEvents(t), []string{"projects.closed c forced"})
}

func TestResumeAndCloseFailures(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	if _, err := e.m.Open(ctx, root("a")); err != nil {
		t.Fatal(err)
	}
	p := e.proj(root("a"))
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{}); err != nil {
		t.Fatal(err)
	}
	p.failRes = errors.New("no app")
	if _, err := e.m.Resume(ctx, "a"); err == nil {
		t.Fatal("resume error lost")
	}
	if _, err := e.m.Switch(ctx, "a"); err == nil {
		t.Fatal("switch error lost")
	}
	if i, _ := e.m.Get("a"); i.State != StateHibernated {
		t.Fatal(i)
	}
	p.failClose = errors.New("stuck")
	if err := e.m.Close(ctx, "a", true); err == nil {
		t.Fatal("close error lost")
	}
	if err := e.m.CloseAll(ctx); err == nil {
		t.Fatal("CloseAll error lost")
	}
	if err := e.m.CloseAll(ctx); err != nil {
		t.Fatal("second CloseAll:", err)
	}
	if _, err := e.m.Open(ctx, root("z")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	for _, op := range []func() error{
		func() error { _, err := e.m.Switch(ctx, "a"); return err },
		func() error { _, err := e.m.Resume(ctx, "a"); return err },
		func() error { _, err := e.m.Hibernate(ctx, "a", HibernateOptions{}); return err },
		func() error { return e.m.Close(ctx, "a", true) },
	} {
		if err := op(); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
}

func TestAddAndCloseAll(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	host := &fakeProject{root: root("host"), running: true}
	if i, err := e.m.Add(ctx, root("host"), host, true); err != nil || i.State != StateActive {
		t.Fatalf("%+v %v", i, err)
	}
	other := &fakeProject{root: root("bg"), running: true}
	if i, err := e.m.Add(ctx, root("bg"), other, false); err != nil || i.State != StateBackground {
		t.Fatalf("%+v %v", i, err)
	}
	if i, err := e.m.Add(ctx, root("bg"), other, false); err != nil || i.State != StateBackground {
		t.Fatalf("re-add: %+v %v", i, err)
	}
	if p, err := e.m.Project("host"); err != nil || p != host {
		t.Fatal(p, err)
	}
	if err := e.m.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
	if host.isRunning() || other.isRunning() || len(e.m.List()) != 0 {
		t.Fatal("CloseAll left projects")
	}
	eq(t, other.calls, []string{"close force=true"})
}

func TestAutoHibernate(t *testing.T) {
	e := newEnv(t, 10*time.Minute)
	ctx := context.Background()
	if _, err := e.m.Open(ctx, root("a")); err != nil {
		t.Fatal(err)
	}
	if i, _ := e.m.Get("a"); i.AutoHibernateAt != nil {
		t.Fatal("active project has a timer")
	}
	if _, err := e.m.Open(ctx, root("b")); err != nil {
		t.Fatal(err)
	}
	i, _ := e.m.Get("a")
	if i.AutoHibernateAt == nil || !i.AutoHibernateAt.Equal(e.clock.Now().Add(10*time.Minute)) {
		t.Fatalf("timer: %+v", i)
	}
	e.clock.Advance(9 * time.Minute)
	if i, _ := e.m.Get("a"); i.State != StateBackground {
		t.Fatal("hibernated too early")
	}
	e.takeEvents(t)
	e.clock.Advance(time.Minute)
	if i, _ := e.m.Get("a"); i.State != StateHibernated {
		t.Fatalf("not hibernated: %+v", i)
	}
	eq(t, e.takeEvents(t), []string{"projects.hibernated a auto"})
	// The active project is never hibernated automatically.
	e.clock.Advance(time.Hour)
	if i, _ := e.m.Get("b"); i.State != StateActive {
		t.Fatal(i)
	}
	// Switching back cancels the timer of the old background project.
	if _, err := e.m.Switch(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Switch(ctx, "b"); err != nil { // a: background again
		t.Fatal(err)
	}
	e.clock.Advance(5 * time.Minute)
	if _, err := e.m.Switch(ctx, "a"); err != nil { // a active, b background
		t.Fatal(err)
	}
	e.clock.Advance(5 * time.Minute) // a's old timer would fire now
	if i, _ := e.m.Get("a"); i.State != StateActive {
		t.Fatal(i)
	}
	// Busy projects are retried after another interval.
	pb := e.proj(root("b"))
	pb.busy = []string{"run"}
	e.clock.Advance(5 * time.Minute)
	if i, _ := e.m.Get("b"); i.State != StateBackground || i.AutoHibernateAt == nil {
		t.Fatalf("busy project: %+v", i)
	}
	e.mu.Lock()
	nerr := len(e.errs)
	e.mu.Unlock()
	if nerr != 1 {
		t.Fatalf("errors: %d", nerr)
	}
	pb.busy = nil
	e.clock.Advance(10 * time.Minute)
	if i, _ := e.m.Get("b"); i.State != StateHibernated {
		t.Fatal(i)
	}
}

func TestSetAutoHibernate(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := e.m.Open(ctx, root(n)); err != nil {
			t.Fatal(err)
		}
	}
	e.clock.Advance(24 * time.Hour)
	if i, _ := e.m.Get("a"); i.State != StateBackground {
		t.Fatal("off but hibernated")
	}
	e.m.SetAutoHibernate(time.Minute)
	e.m.SetAutoHibernate(time.Minute) // unchanged
	if e.m.AutoHibernate() != time.Minute {
		t.Fatal(e.m.AutoHibernate())
	}
	e.m.SetAutoHibernate(2 * time.Minute)
	e.clock.Advance(time.Minute) // first timer was replaced
	if i, _ := e.m.Get("a"); i.State != StateBackground {
		t.Fatal("old timer fired")
	}
	e.clock.Advance(time.Minute)
	if i, _ := e.m.Get("a"); i.State != StateHibernated {
		t.Fatal(i)
	}
	e.m.SetAutoHibernate(-1)
	if e.m.AutoHibernate() != 0 {
		t.Fatal("negative delay kept")
	}
}

func TestConcurrentUse(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	e.m.clock = realClock{}
	ctx := context.Background()
	var wg sync.WaitGroup
	names := []string{"a", "b", "c", "d"}
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, _ = e.m.Open(ctx, root(n))
				_, _ = e.m.Hibernate(ctx, n, HibernateOptions{})
				_, _ = e.m.Switch(ctx, n)
				_ = e.m.List()
			}
		}()
	}
	wg.Wait()
	time.Sleep(5 * time.Millisecond)
	got := []string{}
	for _, i := range e.m.List() {
		got = append(got, i.Name)
	}
	sort.Strings(got)
	eq(t, got, names)
	active := 0
	for _, i := range e.m.List() {
		if i.State == StateActive {
			active++
		}
	}
	if active > 1 {
		t.Fatalf("%d active projects", active)
	}
	if err := e.m.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRealClock(t *testing.T) {
	c := realClock{}
	done := make(chan struct{})
	tm := c.AfterFunc(time.Millisecond, func() { close(done) })
	<-done
	tm.Stop()
	if c.Now().IsZero() {
		t.Fatal("zero time")
	}
	if NameOf(root("x")) != "x" {
		t.Fatal(NameOf(root("x")))
	}
}

// Review wave 9 B7: client state attached after an automatic hibernation
// never hibernates a project resumed meanwhile.
func TestAttachClientState(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := e.m.Open(ctx, root(n)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.m.Hibernate(ctx, "a", HibernateOptions{}); err != nil {
		t.Fatal(err)
	}
	cs := json.RawMessage(`{"tabs":["x.php"]}`)
	if ok, err := e.m.AttachClientState("a", cs); !ok || err != nil {
		t.Fatalf("attach to hibernated: %v %v", ok, err)
	}
	i, err := e.m.Resume(ctx, "a")
	if err != nil || string(i.ClientState) != string(cs) {
		t.Fatalf("%+v %v", i, err)
	}
	// Resumed first: the late state is dropped, the project keeps running.
	if ok, err := e.m.AttachClientState("a", cs); ok || err != nil {
		t.Fatalf("attach to running: %v %v", ok, err)
	}
	if i, _ := e.m.Get("a"); i.State != StateBackground {
		t.Fatalf("state %s", i.State)
	}
	if _, err := e.m.AttachClientState("nope", cs); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := e.m.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.AttachClientState("a", cs); err == nil {
		t.Fatal("attach after close")
	}
}
