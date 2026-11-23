package controller

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"testing"
	"time"
)

// syncViolations collects unmarked backend calls of all controllers of
// the package's tests (TestMain fails the run on any).
var syncViolations struct {
	sync.Mutex
	byMethod map[string]string // method -> first stack
}

func TestMain(m *testing.M) {
	SetDefaultSyncCallHandler(func(method string) {
		syncViolations.Lock()
		defer syncViolations.Unlock()
		if syncViolations.byMethod == nil {
			syncViolations.byMethod = map[string]string{}
		}
		if _, ok := syncViolations.byMethod[method]; !ok {
			syncViolations.byMethod[method] = string(debug.Stack())
		}
	})
	code := m.Run()
	syncViolations.Lock()
	methods := make([]string, 0, len(syncViolations.byMethod))
	for k := range syncViolations.byMethod {
		methods = append(methods, k)
	}
	sort.Strings(methods)
	for _, k := range methods {
		fmt.Fprintf(os.Stderr, "blocking backend call %s without Background context:\n%s\n", k, syncViolations.byMethod[k])
	}
	syncViolations.Unlock()
	if len(methods) > 0 && code == 0 {
		code = 1
	}
	os.Exit(code)
}

func TestBackgroundMarker(t *testing.T) {
	ctx := context.Background()
	if IsBackground(ctx) || IsBackground(nil) {
		t.Fatal("unmarked context reported as background")
	}
	bg := Background(ctx)
	if !IsBackground(bg) || Background(bg) != bg {
		t.Fatal("marker missing or not idempotent")
	}
	sub, cancel := context.WithTimeout(bg, time.Second)
	defer cancel()
	if !IsBackground(sub) {
		t.Fatal("derived context lost the marker")
	}
}

func TestSyncCallGuard(t *testing.T) {
	f := newFake()
	f.answer("x.ping", map[string]any{})
	c := New(f)
	defer c.Close()
	var mu sync.Mutex
	var got []string
	c.SetSyncCallHandler(func(m string) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	})

	// Background work is allowed.
	c.Go(func(ctx context.Context) func() {
		_ = c.call(ctx, "x.ping", nil, nil)
		return nil
	})
	c.Settle(time.Second)
	if calls := c.SyncCalls(); len(calls) != 0 {
		t.Fatalf("background call reported: %v", calls)
	}

	// A blocking call on the caller's goroutine is reported, even when it
	// is made from another goroutine: only the marker counts.
	_ = c.call(context.Background(), "x.ping", nil, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.call(c.Context(), "x.ping", nil, nil)
	}()
	<-done
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 2 || len(c.SyncCalls()) != 2 || c.SyncCalls()[0] != "x.ping" {
		t.Fatalf("handler %v, calls %v", got, c.SyncCalls())
	}

	// The unguarded Backend is for callers off the UI goroutine.
	if err := c.Backend().Call(context.Background(), "x.ping", nil, nil); err != nil || len(c.SyncCalls()) != 2 {
		t.Fatalf("raw backend: %v %v", err, c.SyncCalls())
	}
}

func TestSyncCallDefaultLog(t *testing.T) {
	// Without any handler the guard logs once per method (stderr) and
	// still performs the call.
	prev := defaultSyncHandler.Load()
	SetDefaultSyncCallHandler(nil)
	defer defaultSyncHandler.Store(prev)
	f := newFake()
	f.answer("x.ping", map[string]any{})
	c := New(f)
	defer c.Close()
	for range 2 {
		if err := c.call(context.Background(), "x.ping", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.SyncCalls()) != 2 || !c.guard.logged["x.ping"] {
		t.Fatalf("calls %v", c.SyncCalls())
	}
}
