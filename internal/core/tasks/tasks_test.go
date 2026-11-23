package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recorder collects published events.
type recorder struct {
	mu  sync.Mutex
	evs []ChangedEvent
}

func (r *recorder) publish(e ChangedEvent) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.evs)
}

func (r *recorder) last() ChangedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.evs) == 0 {
		return ChangedEvent{}
	}
	return r.evs[len(r.evs)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestLifecycle(t *testing.T) {
	rec := &recorder{}
	r := New(rec.publish, WithInterval(0))
	a := r.Begin(context.Background(), Options{Title: "Indexing", Module: "php-index", Text: "scan"})
	b := r.Begin(context.Background(), Options{Title: "Push", Module: "git", Cancellable: true})
	if a.ID() == "" || a.ID() == b.ID() {
		t.Fatalf("ids %q %q", a.ID(), b.ID())
	}
	l := r.List()
	if len(l) != 2 || l[0].Title != "Indexing" || l[1].Module != "git" || !l[1].Cancellable || l[0].Text != "scan" || l[0].StartedAt.IsZero() {
		t.Fatalf("list %+v", l)
	}
	a.Progress(5, 10, "a.php")
	if i := a.Info(); i.Done != 5 || i.Total != 10 || i.Text != "a.php" || i.Fraction() != 0.5 {
		t.Fatalf("progress %+v", i)
	}
	a.Progress(20, 10, "") // clamped, text kept
	if i, _ := r.Get(a.ID()); i.Done != 10 || i.Text != "a.php" {
		t.Fatalf("clamp %+v", i)
	}
	a.Progress(-1, -3, "")
	if i := a.Info(); i.Done != 0 || i.Total != 0 || i.Fraction() != -1 {
		t.Fatalf("negative %+v", i)
	}
	n := rec.count()
	a.Progress(0, 0, "") // unchanged: no event
	if rec.count() != n {
		t.Fatal("unchanged progress published")
	}
	a.SetTitle("Indexing PHP")
	a.SetTitle("Indexing PHP")
	if rec.last().Tasks[0].Title != "Indexing PHP" {
		t.Fatalf("title %+v", rec.last())
	}
	a.End(nil)
	a.End(errors.New("late")) // idempotent
	if a.Context().Err() == nil {
		t.Fatal("context not released by End")
	}
	if l := r.List(); len(l) != 1 || l[0].ID != b.ID() {
		t.Fatalf("after end %+v", l)
	}
	if _, ok := r.Get(a.ID()); ok {
		t.Fatal("ended task still found")
	}
	a.Progress(1, 2, "x") // after end: ignored
	a.SetTitle("y")
	if got := rec.last().Tasks; len(got) != 1 {
		t.Fatalf("last event %+v", got)
	}
}

func TestCancel(t *testing.T) {
	r := New(nil)
	c := r.Begin(context.Background(), Options{Title: "Fetch", Cancellable: true})
	n := r.Begin(context.Background(), Options{Title: "LSP"})
	if err := r.Cancel(n.ID()); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("not cancellable: %v", err)
	}
	if err := r.Cancel("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := r.Cancel(c.ID()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("context not cancelled")
	}
	if !c.Cancelled() || !c.Info().Cancelling {
		t.Fatalf("cancel state %+v", c.Info())
	}
	// A cancelled task that ends with an error is not kept as failed.
	c.End(errors.New("signal: killed"))
	if _, ok := r.Get(c.ID()); ok {
		t.Fatal("cancelled task lingers")
	}
	n.End(nil)
}

func TestParentContext(t *testing.T) {
	r := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	tk := r.Begin(ctx, Options{Title: "x"})
	cancel()
	<-tk.Context().Done()
	tk.End(tk.Context().Err()) // context.Canceled: removed at once
	if len(r.List()) != 0 {
		t.Fatal("cancelled-by-parent task lingers")
	}
}

func TestFailedLinger(t *testing.T) {
	rec := &recorder{}
	r := New(rec.publish, WithInterval(0), WithFailedLinger(30*time.Millisecond))
	tk := r.Begin(context.Background(), Options{Title: "Push", Cancellable: true})
	tk.End(errors.New("rejected"))
	l := r.List()
	if len(l) != 1 || l[0].Error != "rejected" {
		t.Fatalf("failed task %+v", l)
	}
	waitFor(t, "linger end", func() bool { return len(r.List()) == 0 })
	waitFor(t, "removal event", func() bool { return len(rec.last().Tasks) == 0 })

	// Cancel dismisses a lingering failed task.
	tk = r.Begin(context.Background(), Options{Title: "Pull"})
	tk.End(errors.New("conflict"))
	if err := r.Cancel(tk.ID()); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Fatal("dismiss failed")
	}

	// Without linger failed tasks vanish at once.
	r2 := New(nil, WithFailedLinger(0))
	r2.Begin(context.Background(), Options{}).End(errors.New("x"))
	if len(r2.List()) != 0 {
		t.Fatal("linger 0 kept task")
	}
}

func TestThrottle(t *testing.T) {
	rec := &recorder{}
	r := New(rec.publish, WithInterval(50*time.Millisecond))
	tk := r.Begin(context.Background(), Options{Title: "Index"})
	for i := 1; i <= 1000; i++ {
		tk.Progress(i, 1000, "")
	}
	if n := rec.count(); n > 2 {
		t.Fatalf("%d events within one interval", n)
	}
	waitFor(t, "trailing event", func() bool {
		ts := rec.last().Tasks
		return len(ts) == 1 && ts[0].Done == 1000
	})
	tk.End(nil)
	waitFor(t, "end event", func() bool { return rec.count() > 0 && len(rec.last().Tasks) == 0 })
}

func TestRun(t *testing.T) {
	r := New(nil)
	want := errors.New("boom")
	err := r.Run(context.Background(), Options{Title: "x"}, func(tk *Task) error {
		if len(r.List()) != 1 {
			t.Error("task not listed while running")
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestClose(t *testing.T) {
	rec := &recorder{}
	r := New(rec.publish, WithInterval(time.Hour))
	tk := r.Begin(context.Background(), Options{Title: "a"})
	tk.Progress(1, 2, "") // schedules a trailing event
	r.Close()
	<-tk.Context().Done()
	n := rec.count()
	late := r.Begin(context.Background(), Options{Title: "late"})
	if late.Context().Err() == nil || len(r.List()) != 0 {
		t.Fatal("task registered after Close")
	}
	tk.End(errors.New("x"))
	late.End(nil)
	if rec.count() != n {
		t.Fatal("published after Close")
	}
}

func TestNilSafety(t *testing.T) {
	var r *Registry
	tk := r.Begin(nil, Options{Title: "x"}) //nolint:staticcheck // nil context is accepted
	tk.Progress(1, 2, "a")
	tk.SetTitle("y")
	if tk.ID() != "" || tk.Info().Title != "y" || tk.Info().Done != 1 || tk.Cancelled() {
		t.Fatalf("detached task %+v", tk.Info())
	}
	tk.End(nil)
	if tk.Context().Err() == nil {
		t.Fatal("detached task context not released")
	}
	if len(r.List()) != 0 {
		t.Fatal("nil list")
	}
	if _, ok := r.Get("t1"); ok {
		t.Fatal("nil get")
	}
	if err := r.Cancel("t1"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r.Close()
	_ = r.Run(context.Background(), Options{}, func(*Task) error { return nil })

	var nt *Task
	nt.Progress(1, 1, "")
	nt.SetTitle("x")
	nt.End(nil)
	if nt.ID() != "" || nt.Context() == nil || nt.Cancelled() || nt.Info().ID != "" {
		t.Fatal("nil task")
	}
}

func TestConcurrent(t *testing.T) {
	rec := &recorder{}
	r := New(rec.publish, WithInterval(time.Millisecond))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := r.Begin(context.Background(), Options{Title: "w", Cancellable: true})
			for j := 0; j < 50; j++ {
				tk.Progress(j, 50, "")
			}
			_ = r.Cancel(tk.ID())
			tk.End(nil)
		}()
	}
	wg.Wait()
	waitFor(t, "empty", func() bool { return len(r.List()) == 0 && len(rec.last().Tasks) == 0 })
}
