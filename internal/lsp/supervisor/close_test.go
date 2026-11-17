package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp"
)

// TestSetRequestTimeoutLive: a changed request limit reaches the running
// client and later starts.
func TestSetRequestTimeoutLive(t *testing.T) {
	f := &fakes{}
	s := newSup(t, f, newRecorder(), func(o *Options) { o.RequestTimeout = 7 * time.Second })
	if err := s.Start(context.Background(), target("a")); err != nil {
		t.Fatal(err)
	}
	c := s.Current().Client()
	if c.RequestTimeout() != 7*time.Second {
		t.Fatalf("initial %s", c.RequestTimeout())
	}
	s.SetRequestTimeout(2 * time.Second)
	if c.RequestTimeout() != 2*time.Second || s.RequestTimeout() != 2*time.Second {
		t.Fatalf("live %s", c.RequestTimeout())
	}
	s.SetRequestTimeout(0)
	if err := s.Reload(context.Background(), target("a")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got := f.cfgs[len(f.cfgs)-1].RequestTimeout
	f.mu.Unlock()
	if got != 0 || s.Current().Client().RequestTimeout() != 0 {
		t.Fatalf("after reload %s", got)
	}
}

// TestCallerCancelEndsStart: cancelling the context of a start that hangs
// in initialize ends it at once, without counting a crash.
func TestCallerCancelEndsStart(t *testing.T) {
	r := newRecorder()
	s := newSup(t, &fakes{}, r, func(o *Options) {
		o.InitTimeout = time.Minute
		o.Connect = func(ctx context.Context, _ lsp.Config) (*lsp.Client, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- s.Start(ctx, target("a")) }()
	r.wait(t, "starting", state(StateStarting))
	t0 := time.Now()
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if time.Since(t0) > 2*time.Second {
		t.Fatal("cancel did not end the start")
	}
	st := s.Status()
	if st.State != StateStopped || st.Crashes != 0 {
		t.Fatalf("status %+v", st)
	}
	// A start whose context is already cancelled does not launch.
	if err := s.Start(ctx, target("b")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
}

// TestCloseWinsOverPendingStart: a Start or Restart that passed the closed
// check and waits for opMu must not launch a server after Close (it would
// be orphaned).
func TestCloseWinsOverPendingStart(t *testing.T) {
	for _, op := range []string{"start", "restart"} {
		t.Run(op, func(t *testing.T) {
			f := &fakes{}
			s := newSup(t, f, newRecorder(), nil)
			if op == "restart" {
				if err := s.Start(context.Background(), target("a")); err != nil {
					t.Fatal(err)
				}
			}
			n := f.count()
			s.opMu.Lock() // an operation in progress
			s.mu.Lock()
			epoch := s.epoch
			s.mu.Unlock()
			errc := make(chan error, 1)
			go func() {
				if op == "restart" {
					errc <- s.Restart(context.Background())
					return
				}
				errc <- s.Start(context.Background(), target("b"))
			}()
			// The operation has passed the closed check once it cancelled
			// pending work (epoch bumped); it now waits for opMu.
			waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.epoch != epoch })
			closed := make(chan struct{})
			go func() { s.Close(); close(closed) }()
			time.Sleep(50 * time.Millisecond) // let Close reach opMu
			s.opMu.Unlock()
			<-closed
			if err := <-errc; !errors.Is(err, ErrClosed) {
				t.Fatalf("%s after Close: %v, want ErrClosed", op, err)
			}
			if f.count() != n {
				t.Fatalf("%s launched a server after Close", op)
			}
			if st := s.Status(); st.State != StateOff {
				t.Fatalf("state %+v", st)
			}
		})
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
