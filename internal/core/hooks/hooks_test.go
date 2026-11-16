package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type saveEvent struct {
	Path    string
	Content string
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{KindAction: "action", KindFilter: "filter", KindGuard: "guard", Kind(9): "kind(9)"} {
		if k.String() != want {
			t.Errorf("%d: %q", k, k.String())
		}
	}
}

func TestActionPriorityAndStableOrder(t *testing.T) {
	r := New()
	a := NewAction[string]("project.opened")
	if a.Name() != "project.opened" {
		t.Fatal(a.Name())
	}
	var mu sync.Mutex
	var order []string
	rec := func(tag string) func(context.Context, string) error {
		return func(context.Context, string) error {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return nil
		}
	}
	a.On(r, 10, rec("b1"))
	a.On(r, 5, rec("a"))
	a.On(r, 10, rec("b2"))
	a.On(r, 20, rec("c"))
	a.On(r, 10, rec("b3"))
	if err := a.Do(context.Background(), r, "x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "a,b1,b2,b3,c" {
		t.Fatalf("order = %s", got)
	}
}

func TestActionCollectsErrorsAndContinues(t *testing.T) {
	var reported atomic.Int32
	r := New(WithErrorHandler(func(*HookError) { reported.Add(1) }))
	a := NewAction[int]("x")
	boom := errors.New("boom")
	var ran atomic.Int32
	a.On(r, 1, func(context.Context, int) error { return boom }, WithOwner("mod-a"))
	a.On(r, 2, func(context.Context, int) error { panic("kaputt") })
	a.On(r, 3, func(context.Context, int) error { ran.Add(1); return nil })
	err := a.Do(context.Background(), r, 1)
	if !errors.Is(err, boom) || !errors.Is(err, ErrPanic) {
		t.Fatalf("err = %v", err)
	}
	var he *HookError
	if !errors.As(err, &he) || he.Owner != "mod-a" || he.Hook != "x" {
		t.Fatalf("hook error = %+v", he)
	}
	if !strings.Contains(err.Error(), "mod-a") || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("message lacks owners: %v", err)
	}
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "kaputt" || len(pe.Stack) == 0 || pe.Error() == "" {
		t.Fatalf("panic error = %+v", pe)
	}
	if ran.Load() != 1 || reported.Load() != 2 {
		t.Fatalf("ran=%d reported=%d", ran.Load(), reported.Load())
	}
}

func TestTimeout(t *testing.T) {
	r := New(WithDefaultTimeout(20 * time.Millisecond))
	a := NewAction[int]("slow")
	release := make(chan struct{})
	defer close(release)
	var after atomic.Bool
	a.On(r, 1, func(context.Context, int) error { <-release; return nil })
	a.On(r, 2, func(context.Context, int) error { after.Store(true); return nil })
	start := time.Now()
	err := a.Do(context.Background(), r, 0)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not enforced")
	}
	if !after.Load() {
		t.Fatal("hook after timed-out hook did not run")
	}
}

func TestHookNoticingDeadlineCountsAsTimeout(t *testing.T) {
	r := New()
	g := NewGuard[int]("g")
	g.On(r, 0, func(ctx context.Context, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	}, WithTimeout(10*time.Millisecond))
	// fail-open: timeouts do not abort guards
	if err := g.Check(context.Background(), r, 0); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestPerHookTimeoutOverride(t *testing.T) {
	r := New(WithDefaultTimeout(10 * time.Millisecond))
	a := NewAction[int]("x")
	a.On(r, 0, func(context.Context, int) error { time.Sleep(50 * time.Millisecond); return nil }, WithTimeout(0))
	if err := a.Do(context.Background(), r, 0); err != nil {
		t.Fatalf("unlimited hook failed: %v", err)
	}
	r2 := New(WithDefaultTimeout(0))
	a.On(r2, 0, func(context.Context, int) error { time.Sleep(30 * time.Millisecond); return nil })
	if err := a.Do(context.Background(), r2, 0); err != nil {
		t.Fatalf("registry without limit: %v", err)
	}
}

func TestFilterChainsAndSkipsFailures(t *testing.T) {
	r := New(WithDefaultTimeout(20 * time.Millisecond))
	f := NewFilter[saveEvent]("file.beforeSave")
	if f.Name() != "file.beforeSave" {
		t.Fatal(f.Name())
	}
	f.On(r, 10, func(_ context.Context, e saveEvent) (saveEvent, error) {
		e.Content = strings.TrimSpace(e.Content)
		return e, nil
	})
	f.On(r, 20, func(_ context.Context, e saveEvent) (saveEvent, error) {
		e.Content = "garbage"
		return e, errors.New("formatter failed")
	})
	f.On(r, 30, func(ctx context.Context, e saveEvent) (saveEvent, error) {
		<-ctx.Done()
		time.Sleep(5 * time.Millisecond)
		e.Content = "late"
		return e, nil
	})
	f.On(r, 40, func(_ context.Context, e saveEvent) (saveEvent, error) {
		e.Content += "\n"
		return e, nil
	})
	out, err := f.Apply(context.Background(), r, saveEvent{Path: "a.php", Content: "  <?php echo 1;  "})
	if out.Content != "<?php echo 1;\n" {
		t.Fatalf("content = %q", out.Content)
	}
	if err == nil || !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "formatter failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestGuardAbortsOnError(t *testing.T) {
	r := New(WithDefaultTimeout(20 * time.Millisecond))
	g := NewGuard[string]("commit.before")
	if g.Name() != "commit.before" {
		t.Fatal(g.Name())
	}
	var later atomic.Bool
	lint := errors.New("lint failed")
	g.On(r, 0, func(context.Context, string) error { panic("broken plugin") })
	g.On(r, 1, func(ctx context.Context, _ string) error { <-ctx.Done(); time.Sleep(time.Millisecond); return nil })
	g.On(r, 5, func(context.Context, string) error { return lint }, WithOwner("phpcs"))
	g.On(r, 9, func(context.Context, string) error { later.Store(true); return nil })
	err := g.Check(context.Background(), r, "msg")
	var ab *AbortError
	if !errors.As(err, &ab) || !errors.Is(err, lint) || ab.Owner != "phpcs" {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "aborted by guard") {
		t.Fatalf("message = %q", err.Error())
	}
	if later.Load() {
		t.Fatal("guard after abort ran")
	}
}

func TestContextCancellation(t *testing.T) {
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	a := NewAction[int]("a")
	f := NewFilter[int]("f")
	g := NewGuard[int]("g")
	a.On(r, 0, func(ctx context.Context, _ int) error { cancel(); <-ctx.Done(); return ctx.Err() })
	a.On(r, 1, func(context.Context, int) error { t.Error("ran after cancel"); return nil })
	if err := a.Do(ctx, r, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("action: %v", err)
	}
	f.On(r, 0, func(context.Context, int) (int, error) { return 1, nil })
	if v, err := f.Apply(ctx, r, 7); v != 7 || !errors.Is(err, context.Canceled) {
		t.Fatalf("filter: %v %v", v, err)
	}
	g.On(r, 0, func(context.Context, int) error { return nil })
	if err := g.Check(ctx, r, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("guard: %v", err)
	}

	// cancellation while a hook is running
	r2 := New()
	ctx2, cancel2 := context.WithCancel(context.Background())
	block := make(chan struct{})
	defer close(block)
	f.On(r2, 0, func(context.Context, int) (int, error) { cancel2(); <-block; return 0, nil })
	if _, err := f.Apply(ctx2, r2, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("filter running: %v", err)
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	g.On(r2, 0, func(context.Context, int) error { cancel3(); <-block; return nil })
	if err := g.Check(ctx3, r2, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("guard running: %v", err)
	}
}

func TestTypeMismatch(t *testing.T) {
	var reported atomic.Int32
	r := New(WithErrorHandler(func(*HookError) { reported.Add(1) }))
	NewAction[string]("same").On(r, 0, func(context.Context, string) error { return nil })
	if err := NewAction[int]("same").Do(context.Background(), r, 1); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("action: %v", err)
	}
	if _, err := NewFilter[string]("same").Apply(context.Background(), r, "x"); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("filter: %v", err)
	}
	if err := NewGuard[string]("same").Check(context.Background(), r, "x"); err != nil {
		t.Fatalf("guard should fail open: %v", err)
	}
	if reported.Load() != 3 {
		t.Fatalf("reported %d", reported.Load())
	}
}

func TestRemoveAndRemoveOwner(t *testing.T) {
	r := New()
	a := NewAction[int]("a")
	var n atomic.Int32
	inc := func(context.Context, int) error { n.Add(1); return nil }
	rm := a.On(r, 0, inc)
	a.On(r, 0, inc, WithOwner("git"))
	NewGuard[int]("g").On(r, 0, func(context.Context, int) error { return nil }, WithOwner("git"))
	if r.Count("a") != 2 || r.Count("g") != 1 {
		t.Fatal("count")
	}
	rm()
	rm()
	if r.Count("a") != 1 {
		t.Fatalf("after remove: %d", r.Count("a"))
	}
	if got := r.RemoveOwner("git"); got != 2 {
		t.Fatalf("RemoveOwner = %d", got)
	}
	if r.Count("a") != 0 || r.Count("g") != 0 {
		t.Fatal("owner hooks left")
	}
	_ = a.Do(context.Background(), r, 0)
	if n.Load() != 0 {
		t.Fatal("removed hook ran")
	}
}

func TestConcurrentRegistrationAndInvocation(t *testing.T) {
	r := New()
	f := NewFilter[int]("inc")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rm := f.On(r, i, func(_ context.Context, v int) (int, error) { return v + 1, nil })
				if _, err := f.Apply(context.Background(), r, 0); err != nil {
					t.Error(err)
				}
				rm()
			}
		}(i)
	}
	wg.Wait()
	if r.Count("inc") != 0 {
		t.Fatal("hooks leaked")
	}
}
