package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// Flush waits for the session's own queue even while other publishers
// keep the bus busy, and only for events published before the call.
func TestSessionFlushIgnoresBusyBus(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var got []string
	s := a.NewSession(func(ev Event) {
		mu.Lock()
		got = append(got, ev.Topic)
		mu.Unlock()
	})
	defer s.Close()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush without subscriptions: %v", err)
	}
	s.Subscribe("doc.saved")
	// A slow subscriber elsewhere keeps the bus from ever being idle.
	release := make(chan struct{})
	defer close(release)
	unSlow := a.eventBus().Subscribe("noise", func(bus.Event) { <-release })
	defer unSlow()
	a.eventBus().Publish("noise", nil)
	a.eventBus().Publish("doc.saved", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "doc.saved" {
		t.Fatalf("delivered %v", got)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer dcancel()
	if a.eventBus().Drain(dctx) == nil {
		t.Fatal("bus unexpectedly idle: the test does not exercise a busy bus")
	}
}
