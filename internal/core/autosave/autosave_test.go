package autosave

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/history"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// fakeDocs is an in-memory editor.
type fakeDocs struct {
	mu      sync.Mutex
	buffers map[string]*buffer
	disk    map[string][]byte
	failOn  string
	saves   int
}

type buffer struct {
	content []byte
	version uint64
	saved   uint64
}

func newDocs() *fakeDocs {
	return &fakeDocs{buffers: map[string]*buffer{}, disk: map[string][]byte{}}
}

func (f *fakeDocs) edit(path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buffers[path]
	if b == nil {
		b = &buffer{}
		f.buffers[path] = b
	}
	b.content = []byte(content)
	b.version++
}

func (f *fakeDocs) Dirty() []Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Doc
	for p, b := range f.buffers {
		if b.version != b.saved {
			out = append(out, Doc{Path: p, Content: append([]byte(nil), b.content...), Version: b.version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (f *fakeDocs) Save(_ context.Context, d Doc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d.Path == f.failOn {
		return errors.New("disk full")
	}
	f.saves++
	f.disk[d.Path] = d.Content
	b := f.buffers[d.Path]
	if b != nil && b.version == d.Version {
		b.content = d.Content
		b.saved = d.Version
	}
	return nil
}

func (f *fakeDocs) onDisk(p string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.disk[p])
}

func (f *fakeDocs) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

// fakeClock fires timers only when advanced.
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
	fired   bool
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
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

// Advance moves time forward and synchronously runs due timers.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.at.After(c.now) {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

type errSink struct {
	mu   sync.Mutex
	errs []error
}

func (s *errSink) add(err error) {
	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()
}

func (s *errSink) all() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.errs...)
}

func TestNewRequiresDocs(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrNoDocs) {
		t.Fatal(err)
	}
	m, err := New(Config{Docs: newDocs()})
	if err != nil {
		t.Fatal(err)
	}
	if m.cfg.Clock == nil || m.cfg.BackgroundTimeout != DefaultBackgroundTimeout {
		t.Fatal("defaults not applied")
	}
	if RealClock().Now().IsZero() {
		t.Fatal("real clock")
	}
	timer := RealClock().AfterFunc(time.Hour, func() {})
	if !timer.Stop() {
		t.Fatal("real timer stop")
	}
}

func TestPipelineFilterHistoryAction(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	hist, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, _ := New(Config{Docs: docs, Hooks: reg, History: hist})

	BeforeSave.On(reg, 10, func(_ context.Context, e SaveEvent) (SaveEvent, error) {
		e.Content = bytes.TrimRight(e.Content, " ")
		e.Path = "ignored"
		return e, nil
	})
	var mu sync.Mutex
	var saved []SavedEvent
	Saved.On(reg, 0, func(_ context.Context, e SavedEvent) error {
		mu.Lock()
		saved = append(saved, e)
		mu.Unlock()
		return nil
	})

	p := filepath.Join(t.TempDir(), "a.php")
	docs.edit(p, "<?php echo 1;   ")
	if err := m.FocusLost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := docs.onDisk(p); got != "<?php echo 1;" {
		t.Fatalf("disk = %q", got)
	}
	if len(docs.Dirty()) != 0 {
		t.Fatal("doc still dirty")
	}
	revs, _ := hist.List(p)
	if len(revs) != 1 || revs[0].Label != "autosave: focusLost" {
		t.Fatalf("history = %+v", revs)
	}
	if len(saved) != 1 || saved[0].Path != p || saved[0].Trigger != TriggerFocusLost || saved[0].Revision.ID != 1 || saved[0].Size != 13 {
		t.Fatalf("saved = %+v", saved)
	}
	// nothing dirty: no-op
	if err := m.BeforeRun(context.Background()); err != nil || docs.saveCount() != 1 {
		t.Fatalf("second run saved again: %v", err)
	}
}

func TestTriggers(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	m, _ := New(Config{Docs: docs, Hooks: reg})
	var got []Trigger
	Saved.On(reg, 0, func(_ context.Context, e SavedEvent) error { got = append(got, e.Trigger); return nil })
	ctx := context.Background()
	steps := []struct {
		fn func(context.Context) error
		tr Trigger
	}{
		{m.FocusLost, TriggerFocusLost},
		{m.BeforeRun, TriggerBeforeRun},
		{m.BeforeDebug, TriggerBeforeDebug},
		{m.BeforeCommit, TriggerBeforeCommit},
	}
	for i, s := range steps {
		docs.edit("/f", strings.Repeat("x", i+1))
		if err := s.fn(ctx); err != nil {
			t.Fatal(err)
		}
		if got[len(got)-1] != s.tr {
			t.Fatalf("trigger = %s want %s", got[len(got)-1], s.tr)
		}
	}
	ev, err := m.SaveDoc(ctx, Doc{Path: "/manual", Content: []byte("m")}, TriggerManual)
	if err != nil || ev.Trigger != TriggerManual || docs.onDisk("/manual") != "m" {
		t.Fatalf("manual: %+v %v", ev, err)
	}
}

func TestErrorsAreIsolated(t *testing.T) {
	docs := newDocs()
	docs.failOn = "/bad"
	reg := hooks.New()
	sink := &errSink{}
	m, _ := New(Config{Docs: docs, Hooks: reg, History: failingHistory{}, OnError: sink.add})
	BeforeSave.On(reg, 0, func(context.Context, SaveEvent) (SaveEvent, error) {
		return SaveEvent{}, errors.New("formatter crashed")
	})
	Saved.On(reg, 0, func(context.Context, SavedEvent) error { return errors.New("listener failed") })

	docs.edit("/bad", "x")
	docs.edit("/good", "unformatted")
	err := m.SaveAll(context.Background(), TriggerFocusLost)
	if err == nil || !strings.Contains(err.Error(), "/bad") || strings.Contains(err.Error(), "/good") {
		t.Fatalf("err = %v", err)
	}
	if docs.onDisk("/good") != "unformatted" {
		t.Fatal("failing filter must not change content")
	}
	var msgs []string
	for _, e := range sink.all() {
		msgs = append(msgs, e.Error())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"formatter crashed", "local history", "listener failed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("OnError missing %q in:\n%s", want, joined)
		}
	}
}

type failingHistory struct{}

func (failingHistory) Save(string, []byte, string) (history.Revision, bool, error) {
	return history.Revision{}, false, errors.New("history broken")
}

func TestContextCancelled(t *testing.T) {
	docs := newDocs()
	m, _ := New(Config{Docs: docs, Hooks: hooks.New()})
	docs.edit("/a", "1")
	docs.edit("/b", "2")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.SaveAll(ctx, TriggerManual); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if docs.saveCount() != 0 {
		t.Fatal("saved despite cancelled context")
	}
	if _, err := m.SaveDoc(ctx, Doc{Path: "/c"}, TriggerManual); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveDoc err = %v", err)
	}
}

func TestIdleTimer(t *testing.T) {
	docs := newDocs()
	clk := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	sink := &errSink{}
	m, _ := New(Config{Docs: docs, Clock: clk, IdleDelay: 5 * time.Second, OnError: sink.add})
	defer m.Close()

	docs.edit("/a", "1")
	m.Activity()
	if !m.LastActivity().Equal(clk.Now()) {
		t.Fatal("last activity")
	}
	clk.Advance(3 * time.Second)
	m.Activity() // typing again resets the timer
	clk.Advance(3 * time.Second)
	if docs.saveCount() != 0 {
		t.Fatal("saved before idle delay elapsed")
	}
	clk.Advance(2 * time.Second)
	if docs.onDisk("/a") != "1" {
		t.Fatal("idle save did not happen")
	}
	if clk.pending() != 0 {
		t.Fatal("timer still pending after firing")
	}

	// Change delay while armed.
	docs.edit("/a", "2")
	m.Activity()
	m.SetIdleDelay(time.Second)
	if m.IdleDelay() != time.Second {
		t.Fatal("delay")
	}
	clk.Advance(time.Second)
	if docs.onDisk("/a") != "2" {
		t.Fatal("new delay not applied")
	}

	// Disable.
	m.SetIdleDelay(0)
	docs.edit("/a", "3")
	m.Activity()
	clk.Advance(time.Hour)
	if docs.onDisk("/a") != "2" || clk.pending() != 0 {
		t.Fatal("idle save with delay 0")
	}

	// Errors of idle saves go to OnError.
	m.SetIdleDelay(time.Second)
	docs.failOn = "/a"
	m.Activity()
	clk.Advance(time.Second)
	if len(sink.all()) != 1 {
		t.Fatalf("errors = %v", sink.all())
	}
}

func TestStaleTimerAndClose(t *testing.T) {
	docs := newDocs()
	clk := &fakeClock{}
	m, _ := New(Config{Docs: docs, Clock: clk, IdleDelay: time.Second})
	docs.edit("/a", "1")
	m.Activity()
	// Simulate a timer callback that races with newer activity.
	m.mu.Lock()
	stale := m.gen
	m.mu.Unlock()
	m.Activity()
	m.idleFired(stale)
	if docs.saveCount() != 0 {
		t.Fatal("stale timer saved")
	}
	m.Close()
	m.Activity()
	clk.Advance(time.Hour)
	if docs.saveCount() != 0 || clk.pending() != 0 {
		t.Fatal("timer active after Close")
	}
	m.mu.Lock()
	gen := m.gen
	m.mu.Unlock()
	m.idleFired(gen)
	if docs.saveCount() != 0 {
		t.Fatal("fired after close")
	}
}

func TestAttachToHooks(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	m, _ := New(Config{Docs: docs, Hooks: reg})

	type commitReq struct{ Message string }
	commitBefore := hooks.NewGuard[commitReq]("commit.before")
	focusLost := hooks.NewAction[struct{}]("editor.focusLost")

	var linted string
	commitBefore.On(reg, 0, func(context.Context, commitReq) error {
		linted = docs.onDisk("/a.php") // lint sees saved content
		return nil
	})
	rmGuard := SaveBeforeGuard(m, reg, commitBefore, TriggerBeforeCommit)
	rmAction := SaveOnAction(m, reg, focusLost, TriggerFocusLost)

	docs.edit("/a.php", "<?php // v1")
	if err := commitBefore.Check(context.Background(), reg, commitReq{"msg"}); err != nil {
		t.Fatal(err)
	}
	if linted != "<?php // v1" {
		t.Fatalf("guard ran before autosave: %q", linted)
	}

	docs.edit("/a.php", "<?php // v2")
	if err := focusLost.Do(context.Background(), reg, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if docs.onDisk("/a.php") != "<?php // v2" {
		t.Fatal("focus lost did not save")
	}

	docs.failOn = "/a.php"
	docs.edit("/a.php", "<?php // v3")
	err := commitBefore.Check(context.Background(), reg, commitReq{"msg"})
	var ab *hooks.AbortError
	if !errors.As(err, &ab) || ab.Owner != "autosave" {
		t.Fatalf("failed save must veto commit: %v", err)
	}
	rmGuard()
	rmAction()
	if reg.Count("commit.before") != 1 || reg.Count("editor.focusLost") != 0 {
		t.Fatal("remove")
	}
}

func TestConcurrentSaves(t *testing.T) {
	docs := newDocs()
	clk := &fakeClock{}
	m, _ := New(Config{Docs: docs, Hooks: hooks.New(), Clock: clk, IdleDelay: time.Millisecond})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				docs.edit("/f", strings.Repeat("x", i*100+j))
				m.Activity()
				_ = m.FocusLost(context.Background())
				clk.Advance(time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	m.Close()
	_ = m.SaveAll(context.Background(), TriggerManual)
	if len(docs.Dirty()) != 0 {
		t.Fatal("dirty docs left")
	}
}

type panicDocs struct{ *fakeDocs }

func (p panicDocs) Save(context.Context, Doc) error { panic("save crashed") }

// Review 2026-11-16 #6: the save-before-commit guard must veto the commit
// when saving panics (or times out) instead of letting it run on unsaved
// files.
func TestSaveBeforeGuardFailsClosed(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	m, _ := New(Config{Docs: panicDocs{docs}, Hooks: reg})
	commitBefore := hooks.NewGuard[string]("commit.before")
	SaveBeforeGuard(m, reg, commitBefore, TriggerBeforeCommit)
	docs.edit("/a.php", "<?php // unsaved")
	err := commitBefore.Check(context.Background(), reg, "msg")
	var ab *hooks.AbortError
	if !errors.As(err, &ab) || !errors.Is(err, hooks.ErrPanic) {
		t.Fatalf("panicking save must veto the commit: %v", err)
	}
}

// A save whose context ended while the filters ran must not write.
func TestCancelledSaveDoesNotWrite(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	m, _ := New(Config{Docs: docs, Hooks: reg})
	ctx, cancel := context.WithCancel(context.Background())
	BeforeSave.On(reg, 0, func(_ context.Context, ev SaveEvent) (SaveEvent, error) {
		cancel() // e.g. the guard timed out meanwhile
		return ev, nil
	})
	docs.edit("/a.php", "late")
	if _, err := m.SaveDoc(ctx, Doc{Path: "/a.php", Content: []byte("late"), Version: 1}, TriggerBeforeCommit); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if docs.saveCount() != 0 {
		t.Fatal("cancelled save was written")
	}
}

// Review 2026-11-16 #10: a filter returning an empty SaveEvent without an
// error must not empty file and buffer.
func TestEmptyFilterResultIsFilterError(t *testing.T) {
	docs := newDocs()
	reg := hooks.New()
	var sink errSink
	m, _ := New(Config{Docs: docs, Hooks: reg, OnError: sink.add})
	BeforeSave.On(reg, 0, func(context.Context, SaveEvent) (SaveEvent, error) {
		return SaveEvent{}, nil // broken formatter
	})
	docs.edit("/a.php", "<?php echo 1;")
	if err := m.SaveAll(context.Background(), TriggerManual); err != nil {
		t.Fatal(err)
	}
	if got := docs.onDisk("/a.php"); got != "<?php echo 1;" {
		t.Fatalf("disk = %q", got)
	}
	errs := sink.all()
	if len(errs) != 1 || !errors.Is(errs[0], ErrEmptyFilterResult) {
		t.Fatalf("errors = %v", errs)
	}
	// Empty input stays allowed.
	docs.edit("/b.php", "")
	if err := m.SaveAll(context.Background(), TriggerManual); err != nil {
		t.Fatal(err)
	}
	if len(sink.all()) != 1 {
		t.Fatal("empty input reported as error")
	}
}
