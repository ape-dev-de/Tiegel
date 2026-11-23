package controller

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// Blocking-call guard (D512): every backend call may block (exec, file
// IO, network), so it must never run on the UI goroutine. Calls are only
// allowed with a context marked by Background: Controller.Go marks the
// context it passes to the work, and the few deliberately blocking paths
// (closing a window, own goroutines) mark theirs explicitly. A call with
// an unmarked context is a violation. The marker alone decides – there is
// no timing or goroutine guess – so calls from correctly marked
// goroutines can never be reported by mistake.

// backgroundKey marks contexts of work that runs off the UI goroutine.
type backgroundKey struct{}

// Background returns ctx marked as allowed to make blocking backend
// calls: background goroutines, or a deliberately synchronous path such
// as saving on window close.
func Background(ctx context.Context) context.Context {
	if IsBackground(ctx) {
		return ctx
	}
	return context.WithValue(ctx, backgroundKey{}, true)
}

// IsBackground reports whether ctx carries the Background marker.
func IsBackground(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	b, _ := ctx.Value(backgroundKey{}).(bool)
	return b
}

// SyncCallHandler receives a backend call made with an unmarked context
// (method name). It may be called from any goroutine.
type SyncCallHandler func(method string)

// syncGuard records unmarked calls of one controller.
type syncGuard struct {
	mu      sync.Mutex
	calls   []string
	logged  map[string]bool
	handler SyncCallHandler
}

// defaultSyncHandler is the process-wide handler of controllers without
// their own (SetDefaultSyncCallHandler).
var defaultSyncHandler atomic.Pointer[SyncCallHandler]

// SetDefaultSyncCallHandler sets the handler of unmarked backend calls
// for every controller without its own handler (nil: one stderr line per
// method and controller). Test packages install it in TestMain to fail on
// blocking calls.
func SetDefaultSyncCallHandler(h SyncCallHandler) {
	if h == nil {
		defaultSyncHandler.Store(nil)
		return
	}
	defaultSyncHandler.Store(&h)
}

// record notes a violation: the controller's handler, else the default
// handler gets it; without either it is logged once per method on
// stderr.
func (g *syncGuard) record(method string) {
	g.mu.Lock()
	g.calls = append(g.calls, method)
	h := g.handler
	if h == nil {
		if p := defaultSyncHandler.Load(); p != nil {
			h = *p
		}
	}
	first := !g.logged[method]
	if g.logged == nil {
		g.logged = map[string]bool{}
	}
	g.logged[method] = true
	g.mu.Unlock()
	if h != nil {
		h(method)
		return
	}
	if first {
		fmt.Fprintf(os.Stderr, "tiegel: blocking backend call %s outside background work (use Controller.Go)\n", method)
	}
}

// SetSyncCallHandler replaces the default reaction to an unmarked
// backend call (one stderr line per method); nil restores it. Tests use
// it to fail on blocking calls from the UI goroutine.
func (c *Controller) SetSyncCallHandler(h SyncCallHandler) {
	c.guard.mu.Lock()
	c.guard.handler = h
	c.guard.mu.Unlock()
}

// SyncCalls returns the methods called with an unmarked context so far
// (in call order, with repetitions).
func (c *Controller) SyncCalls() []string {
	c.guard.mu.Lock()
	defer c.guard.mu.Unlock()
	return append([]string(nil), c.guard.calls...)
}

// guardedBackend checks every Call for the Background marker.
type guardedBackend struct {
	Backend
	g *syncGuard
}

// Call implements api.Caller.
func (b guardedBackend) Call(ctx context.Context, method string, params, result any) error {
	if !IsBackground(ctx) {
		b.g.record(method)
	}
	return b.Backend.Call(ctx, method, params, result)
}
