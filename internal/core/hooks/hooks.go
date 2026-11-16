// Package hooks implements Tiegel's extension points, modelled after
// WordPress actions and filters plus a third kind, guards.
//
//   - An Action notifies hooks about something that happened
//     ("project.opened", "editor.focusLost"). Every hook runs; errors are
//     collected but never stop the chain.
//   - A Filter lets hooks transform a value ("file.beforeSave" for a
//     formatter, "commit.message"). Each hook receives the output of the
//     previous one. A failing hook is skipped: its output is discarded and the
//     chain continues with the previous value.
//   - A Guard lets hooks veto an operation ("commit.before" when lint fails,
//     "run.before"). The first hook returning an error aborts the check.
//
// Hook points are declared once, typed by their payload:
//
//	var BeforeSave = hooks.NewFilter[SaveEvent]("file.beforeSave")
//
//	remove := BeforeSave.On(reg, 10, func(ctx context.Context, e SaveEvent) (SaveEvent, error) {
//		e.Content = format(e.Content)
//		return e, nil
//	})
//	e, err := BeforeSave.Apply(ctx, reg, e)
//
// Hooks run ordered by priority (lower first); hooks with equal priority run
// in registration order. Each hook runs with a time limit (default
// DefaultTimeout, adjustable per registry and per hook) so that a misbehaving
// plugin cannot block the editor: a hook that exceeds it is abandoned and
// treated as failed with ErrTimeout. Panics inside hooks are recovered and
// reported as *PanicError.
//
// Because a timed-out hook keeps running in the background, hooks must treat
// their input as read-only and return modified copies (filters) instead of
// mutating shared memory.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// DefaultTimeout is the per-hook time limit used when neither the registry
// nor the hook specify one.
const DefaultTimeout = 2 * time.Second

// Kind is the kind of a hook point.
type Kind int

// Hook kinds.
const (
	KindAction Kind = iota + 1
	KindFilter
	KindGuard
)

// String returns the lower-case name of the kind.
func (k Kind) String() string {
	switch k {
	case KindAction:
		return "action"
	case KindFilter:
		return "filter"
	case KindGuard:
		return "guard"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Sentinel errors.
var (
	// ErrTimeout marks a hook that exceeded its time limit.
	ErrTimeout = errors.New("hook timed out")
	// ErrPanic marks a hook that panicked; the concrete error is *PanicError.
	ErrPanic = errors.New("hook panicked")
	// ErrTypeMismatch marks a hook registered under the same name with a
	// different kind or payload type than the hook point being invoked.
	ErrTypeMismatch = errors.New("hook kind or payload type mismatch")
)

// PanicError carries the recovered value and stack of a panicking hook.
// errors.Is(err, ErrPanic) reports true for it.
type PanicError struct {
	Value any
	Stack []byte
}

// Error implements error.
func (e *PanicError) Error() string { return fmt.Sprintf("hook panicked: %v", e.Value) }

// Unwrap returns ErrPanic.
func (e *PanicError) Unwrap() error { return ErrPanic }

// HookError describes a failure of a single hook.
type HookError struct {
	Hook     string // hook point name, e.g. "file.beforeSave"
	Kind     Kind
	Owner    string // owner set with WithOwner, usually a module ID
	Priority int
	Err      error
}

// Error implements error.
func (e *HookError) Error() string {
	owner := e.Owner
	if owner == "" {
		owner = "anonymous"
	}
	return fmt.Sprintf("%s %q (owner %s, priority %d): %v", e.Kind, e.Hook, owner, e.Priority, e.Err)
}

// Unwrap returns the underlying error.
func (e *HookError) Unwrap() error { return e.Err }

// AbortError is returned by Guard.Check when a guard vetoes the operation.
type AbortError struct {
	HookError
}

// Error implements error.
func (e *AbortError) Error() string { return "aborted by " + e.HookError.Error() }

// Unwrap returns the guard's error.
func (e *AbortError) Unwrap() error { return e.Err }

// Option configures a Registry.
type Option func(*Registry)

// WithDefaultTimeout sets the time limit for hooks registered without
// WithTimeout. A value <= 0 disables the limit.
func WithDefaultTimeout(d time.Duration) Option {
	return func(r *Registry) { r.timeout = d }
}

// WithErrorHandler installs a callback receiving every hook failure (errors,
// timeouts, panics, type mismatches), e.g. for logging. It must be safe for
// concurrent use.
func WithErrorHandler(fn func(*HookError)) Option {
	return func(r *Registry) { r.onError = fn }
}

// HookOption configures a single hook registration.
type HookOption func(*entry)

// WithTimeout overrides the time limit for one hook. A value <= 0 disables
// the limit for this hook.
func WithTimeout(d time.Duration) HookOption {
	return func(e *entry) { e.timeout = d; e.hasTimeout = true }
}

// WithOwner tags a hook with its owner (usually a module ID), used in error
// reports and by Registry.RemoveOwner.
func WithOwner(owner string) HookOption {
	return func(e *entry) { e.owner = owner }
}

// Registry stores registered hooks. It is safe for concurrent use.
type Registry struct {
	timeout time.Duration
	onError func(*HookError)

	mu    sync.RWMutex
	seq   uint64
	hooks map[string][]*entry
}

type entry struct {
	id         uint64
	name       string
	kind       Kind
	priority   int
	owner      string
	timeout    time.Duration
	hasTimeout bool
	fn         any
}

// New creates an empty registry.
func New(opts ...Option) *Registry {
	r := &Registry{timeout: DefaultTimeout, hooks: map[string][]*entry{}}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *Registry) add(name string, kind Kind, priority int, fn any, opts []HookOption) func() {
	e := &entry{name: name, kind: kind, priority: priority, fn: fn}
	for _, o := range opts {
		o(e)
	}
	r.mu.Lock()
	r.seq++
	e.id = r.seq
	list := append(r.hooks[name][:len(r.hooks[name]):len(r.hooks[name])], e)
	sort.SliceStable(list, func(i, j int) bool { return list[i].priority < list[j].priority })
	r.hooks[name] = list
	r.mu.Unlock()

	var once sync.Once
	return func() { once.Do(func() { r.removeWhere(func(x *entry) bool { return x.id == e.id }) }) }
}

func (r *Registry) removeWhere(pred func(*entry) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for name, list := range r.hooks {
		kept := make([]*entry, 0, len(list))
		for _, e := range list {
			if pred(e) {
				n++
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(r.hooks, name)
		} else {
			r.hooks[name] = kept
		}
	}
	return n
}

// RemoveOwner removes every hook registered with WithOwner(owner) and
// returns how many were removed. The module registry calls it when a module
// stops.
func (r *Registry) RemoveOwner(owner string) int {
	return r.removeWhere(func(e *entry) bool { return e.owner == owner })
}

// Count returns the number of hooks registered under name.
func (r *Registry) Count(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.hooks[name])
}

// snapshot returns the hooks for name in execution order. The slice is never
// mutated after publication, so it can be used without the lock.
func (r *Registry) snapshot(name string) []*entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hooks[name]
}

func (r *Registry) fail(e *entry, err error) *HookError {
	he := &HookError{Hook: e.name, Kind: e.kind, Owner: e.owner, Priority: e.priority, Err: err}
	if r.onError != nil {
		r.onError(he)
	}
	return he
}

func (r *Registry) mismatch(e *entry, want Kind) *HookError {
	return r.fail(e, fmt.Errorf("%w: registered as %s %T, invoked as %s", ErrTypeMismatch, e.kind, e.fn, want))
}

type result[R any] struct {
	v   R
	err error
}

// run executes call with e's time limit and panic protection.
func run[R any](ctx context.Context, r *Registry, e *entry, call func(context.Context) (R, error)) (R, error) {
	timeout := r.timeout
	if e.hasTimeout {
		timeout = e.timeout
	}
	hctx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		hctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	done := make(chan result[R], 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- result[R]{err: &PanicError{Value: p, Stack: debug.Stack()}}
			}
		}()
		v, err := call(hctx)
		done <- result[R]{v: v, err: err}
	}()

	var zero R
	select {
	case res := <-done:
		// A hook that notices its own deadline and returns promptly has
		// still timed out.
		if res.err != nil && ctx.Err() == nil && errors.Is(hctx.Err(), context.DeadlineExceeded) {
			return zero, fmt.Errorf("%w after %v", ErrTimeout, timeout)
		}
		return res.v, res.err
	case <-hctx.Done():
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, fmt.Errorf("%w after %v", ErrTimeout, timeout)
	}
}

// Action is a typed notification hook point.
type Action[T any] struct{ name string }

// NewAction declares an action hook point.
func NewAction[T any](name string) Action[T] { return Action[T]{name: name} }

// Name returns the hook point name.
func (a Action[T]) Name() string { return a.name }

// On registers fn for this action and returns a function removing it.
func (a Action[T]) On(r *Registry, priority int, fn func(ctx context.Context, v T) error, opts ...HookOption) (remove func()) {
	return r.add(a.name, KindAction, priority, fn, opts)
}

// Do runs every hook registered for the action. Failing hooks do not stop
// the others; their errors are returned joined (each a *HookError). If ctx is
// cancelled, Do stops and returns ctx.Err().
func (a Action[T]) Do(ctx context.Context, r *Registry, v T) error {
	var errs []error
	for _, e := range r.snapshot(a.name) {
		if err := ctx.Err(); err != nil {
			return err
		}
		fn, ok := e.fn.(func(context.Context, T) error)
		if !ok || e.kind != KindAction {
			errs = append(errs, r.mismatch(e, KindAction))
			continue
		}
		_, err := run(ctx, r, e, func(c context.Context) (struct{}, error) { return struct{}{}, fn(c, v) })
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, r.fail(e, err))
		}
	}
	return errors.Join(errs...)
}

// Filter is a typed value-transforming hook point.
type Filter[T any] struct{ name string }

// NewFilter declares a filter hook point.
func NewFilter[T any](name string) Filter[T] { return Filter[T]{name: name} }

// Name returns the hook point name.
func (f Filter[T]) Name() string { return f.name }

// On registers fn for this filter and returns a function removing it. fn
// must not mutate memory reachable from its input; it returns a new value.
func (f Filter[T]) On(r *Registry, priority int, fn func(ctx context.Context, v T) (T, error), opts ...HookOption) (remove func()) {
	return r.add(f.name, KindFilter, priority, fn, opts)
}

// Apply passes v through all hooks of the filter and returns the result.
// A hook that fails (error, timeout, panic) is skipped and the chain
// continues with the previous value; the failures are returned joined
// alongside the (still usable) result. If ctx is cancelled, Apply returns
// the value so far and ctx.Err().
func (f Filter[T]) Apply(ctx context.Context, r *Registry, v T) (T, error) {
	var errs []error
	for _, e := range r.snapshot(f.name) {
		if err := ctx.Err(); err != nil {
			return v, err
		}
		fn, ok := e.fn.(func(context.Context, T) (T, error))
		if !ok || e.kind != KindFilter {
			errs = append(errs, r.mismatch(e, KindFilter))
			continue
		}
		in := v
		out, err := run(ctx, r, e, func(c context.Context) (T, error) { return fn(c, in) })
		if err != nil {
			if ctx.Err() != nil {
				return v, ctx.Err()
			}
			errs = append(errs, r.fail(e, err))
			continue
		}
		v = out
	}
	return v, errors.Join(errs...)
}

// Guard is a typed veto hook point.
type Guard[T any] struct{ name string }

// NewGuard declares a guard hook point.
func NewGuard[T any](name string) Guard[T] { return Guard[T]{name: name} }

// Name returns the hook point name.
func (g Guard[T]) Name() string { return g.name }

// On registers fn for this guard and returns a function removing it.
// Returning a non-nil error vetoes the operation.
func (g Guard[T]) On(r *Registry, priority int, fn func(ctx context.Context, v T) error, opts ...HookOption) (remove func()) {
	return r.add(g.name, KindGuard, priority, fn, opts)
}

// Check runs the guard's hooks in order. The first hook that returns an
// error aborts the check; Check then returns an *AbortError wrapping it.
//
// Guards fail open: a hook that times out, panics or is registered with the
// wrong type is reported to the registry's error handler and skipped, so a
// broken plugin cannot permanently block an operation. If ctx is cancelled,
// Check returns ctx.Err().
func (g Guard[T]) Check(ctx context.Context, r *Registry, v T) error {
	for _, e := range r.snapshot(g.name) {
		if err := ctx.Err(); err != nil {
			return err
		}
		fn, ok := e.fn.(func(context.Context, T) error)
		if !ok || e.kind != KindGuard {
			r.mismatch(e, KindGuard)
			continue
		}
		_, err := run(ctx, r, e, func(c context.Context) (struct{}, error) { return struct{}{}, fn(c, v) })
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrTimeout), errors.Is(err, ErrPanic):
			r.fail(e, err)
		default:
			return &AbortError{HookError: *r.fail(e, err)}
		}
	}
	return nil
}
