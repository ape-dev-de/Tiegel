// Package module implements Tiegel's module system: the Module interface
// from the concept, the Context a module receives on start, and the
// Registry that enables, disables, starts and stops modules in dependency
// order.
//
// Modules are compiled in (MVP) and switched on/off via settings. They never
// talk to each other directly; Context gives them the event bus and the hook
// registry for that. Later the same interface is meant to be served over
// JSON-RPC for out-of-process plugins.
//
// Lifecycle rules:
//
//   - A module can only be enabled when all its dependencies are registered
//     and enabled (EnableWithDependencies enables them transitively).
//   - A module cannot be disabled while an enabled module depends on it.
//   - Start starts enabled modules in topological order; Stop stops them in
//     reverse start order. Dependency cycles are detected and reported as
//     *CycleError; the modules involved are not started.
//   - A module whose dependency failed to start is skipped.
//   - After a module stops, its OnStop callbacks run and all hooks it
//     registered with hooks.WithOwner(ctx.ModuleID()) are removed.
//
// Module code must not call back into the Registry from Start or Stop.
package module

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Module is a switchable unit of functionality.
type Module interface {
	ID() string
	DependsOn() []string     // e.g. debug-xdebug → php-runtime
	Start(ctx Context) error // registers menus, panels, gutter icons, status bar items
	Stop() error
}

// Context is what a module gets to work with while running. It is an
// interface so that later layers (UI registrations such as menus, panels,
// gutter icons, status bar items) can provide an extended implementation via
// Env.Extend; modules then type-assert to the richer interface.
type Context interface {
	// ModuleID returns the ID of the module this context belongs to. Use it
	// with hooks.WithOwner so hooks are removed automatically on stop.
	ModuleID() string
	// Bus returns the shared event bus.
	Bus() *bus.Bus
	// Hooks returns the shared hook registry.
	Hooks() *hooks.Registry
	// Settings returns the global settings store (may be nil in tests).
	Settings() *settings.Store
	// Logger returns a logger tagged with the module ID.
	Logger() *slog.Logger
	// OnStop registers fn to run after the module's Stop, in reverse
	// registration order. Typical use: bus unsubscribe functions.
	OnStop(fn func())
}

// SettingsProvider is implemented by modules that declare settings
// (decision D190). Keys must use the module's namespace "<ID>.<name>".
// The registry collects them for the settings dialog, the settings.schema
// API, the headless CLI and the IDE import.
type SettingsProvider interface {
	SettingSpecs() []settings.Spec
}

// StateStore persists module on/off states. *settings.Store implements it.
type StateStore interface {
	ModuleEnabled(id string) (enabled, known bool)
	SetModuleEnabled(id string, enabled bool) error
}

// Env holds the shared services handed to modules.
type Env struct {
	Bus      *bus.Bus
	Hooks    *hooks.Registry
	Settings *settings.Store
	Logger   *slog.Logger
	// State persists enabled flags. If nil, Settings is used when set;
	// otherwise states are kept in memory only.
	State StateStore
	// Extend, if set, wraps the base context of every module, e.g. to add
	// UI registration facilities. It must return a Context whose OnStop and
	// ModuleID delegate to the base.
	Extend func(base Context) Context
}

// Errors returned by the Registry. Use errors.Is to test for them.
var (
	ErrInvalidID          = errors.New("module: invalid module ID")
	ErrDuplicate          = errors.New("module: already registered")
	ErrUnknown            = errors.New("module: not registered")
	ErrMissingDependency  = errors.New("module: dependency not registered")
	ErrDependencyDisabled = errors.New("module: dependency not enabled")
	ErrDependencyFailed   = errors.New("module: dependency failed to start")
	ErrRequired           = errors.New("module: required by enabled modules")
	ErrCycle              = errors.New("module: dependency cycle")
	ErrPanic              = errors.New("module: panicked")
)

// CycleError reports a dependency cycle. Path starts and ends with the same
// module ID. errors.Is(err, ErrCycle) is true.
type CycleError struct{ Path []string }

// Error implements error.
func (e *CycleError) Error() string {
	return fmt.Sprintf("%v: %s", ErrCycle, strings.Join(e.Path, " → "))
}

// Unwrap returns ErrCycle.
func (e *CycleError) Unwrap() error { return ErrCycle }

// RegisterOption configures a registration.
type RegisterOption func(*entry)

// DefaultEnabled marks a module as enabled when no stored state exists.
func DefaultEnabled() RegisterOption { return func(e *entry) { e.defaultOn = true } }

// DefaultDisabled marks a module as disabled when no stored state exists
// (the default without options; useful where an option list is required,
// e.g. for opt-in plugins such as proprietary tools).
func DefaultDisabled() RegisterOption { return func(e *entry) { e.defaultOn = false } }

// Info describes a registered module.
type Info struct {
	ID        string
	DependsOn []string
	Enabled   bool
	Running   bool
	// Err is the last start/stop error, if any.
	Err error
}

type entry struct {
	mod       Module
	deps      []string
	defaultOn bool
	enabled   bool
	running   bool
	ctx       *baseContext
	err       error
}

// Registry manages modules. It is safe for concurrent use; lifecycle
// operations are serialised.
type Registry struct {
	env Env

	mu      sync.Mutex
	mods    map[string]*entry
	order   []string // registration order
	active  bool     // Start called, Stop not yet
	started []string // start order of running modules
}

// NewRegistry creates a registry handing env to modules.
func NewRegistry(env Env) *Registry {
	if env.Logger == nil {
		env.Logger = slog.New(slog.DiscardHandler)
	}
	if env.State == nil && env.Settings != nil {
		env.State = env.Settings
	}
	return &Registry{env: env, mods: map[string]*entry{}}
}

// Register adds a module. Its enabled flag is taken from the state store,
// falling back to DefaultEnabled. If the registry is already started and
// the module is enabled, it is started immediately.
func (r *Registry) Register(m Module, opts ...RegisterOption) error {
	id := m.ID()
	if id == "" || strings.ContainsAny(id, " \t\n") {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.mods[id]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicate, id)
	}
	e := &entry{mod: m, deps: append([]string(nil), m.DependsOn()...)}
	for _, o := range opts {
		o(e)
	}
	e.enabled = e.defaultOn
	if r.env.State != nil {
		if on, known := r.env.State.ModuleEnabled(id); known {
			e.enabled = on
		}
	}
	r.mods[id] = e
	r.order = append(r.order, id)
	if r.active && e.enabled {
		return r.startOneLocked(id)
	}
	return nil
}

// Modules returns information about all modules in registration order.
func (r *Registry) Modules() []Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Info, 0, len(r.order))
	for _, id := range r.order {
		e := r.mods[id]
		out = append(out, Info{ID: id, DependsOn: append([]string(nil), e.deps...), Enabled: e.enabled, Running: e.running, Err: e.err})
	}
	return out
}

// IsEnabled reports whether id is registered and enabled.
func (r *Registry) IsEnabled(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.mods[id]
	return ok && e.enabled
}

// IsRunning reports whether id is currently started.
func (r *Registry) IsRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.mods[id]
	return ok && e.running
}

// Enable switches a module on and persists the state. All dependencies must
// be registered and enabled. If the registry is started, the module is
// started too; a start failure is returned but the module stays enabled.
func (r *Registry) Enable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enableLocked(id)
}

func (r *Registry) enableLocked(id string) error {
	e, ok := r.mods[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	if e.enabled {
		if r.active && !e.running {
			return r.startOneLocked(id) // retry a module that failed earlier
		}
		return nil
	}
	for _, d := range e.deps {
		de, ok := r.mods[d]
		switch {
		case d == id:
			return &CycleError{Path: []string{id, id}}
		case !ok:
			return fmt.Errorf("%w: %s needs %s", ErrMissingDependency, id, d)
		case !de.enabled:
			return fmt.Errorf("%w: %s needs %s", ErrDependencyDisabled, id, d)
		}
	}
	if err := r.persist(id, true); err != nil {
		return err
	}
	e.enabled = true
	if r.active {
		return r.startOneLocked(id)
	}
	return nil
}

// EnableWithDependencies enables id and, first, all its transitive
// dependencies in dependency order.
func (r *Registry) EnableWithDependencies(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.mods[id]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	order, err := r.topo([]string{id})
	if err != nil {
		return err
	}
	for _, m := range order {
		if err := r.enableLocked(m); err != nil {
			return err
		}
	}
	return nil
}

// Dependents returns the enabled modules that directly depend on id.
func (r *Registry) Dependents(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dependentsLocked(id)
}

func (r *Registry) dependentsLocked(id string) []string {
	var out []string
	for _, other := range r.order {
		e := r.mods[other]
		if !e.enabled || other == id {
			continue
		}
		for _, d := range e.deps {
			if d == id {
				out = append(out, other)
				break
			}
		}
	}
	return out
}

// Disable switches a module off, stopping it if running, and persists the
// state. It fails with ErrRequired while enabled modules depend on it.
func (r *Registry) Disable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.mods[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	if !e.enabled {
		return nil
	}
	if deps := r.dependentsLocked(id); len(deps) > 0 {
		return fmt.Errorf("%w: %s is needed by %s", ErrRequired, id, strings.Join(deps, ", "))
	}
	if err := r.persist(id, false); err != nil {
		return err
	}
	e.enabled = false
	if e.running {
		return r.stopOneLocked(id)
	}
	return nil
}

func (r *Registry) persist(id string, on bool) error {
	if r.env.State == nil {
		return nil
	}
	if err := r.env.State.SetModuleEnabled(id, on); err != nil {
		return fmt.Errorf("module: persist state of %s: %w", id, err)
	}
	return nil
}

// StartOrder returns the enabled modules in the order Start would start
// them, or a *CycleError / dependency error if the enabled set is
// inconsistent.
func (r *Registry) StartOrder() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.topo(r.enabledLocked())
}

func (r *Registry) enabledLocked() []string {
	var ids []string
	for _, id := range r.order {
		if r.mods[id].enabled {
			ids = append(ids, id)
		}
	}
	return ids
}

// topo returns roots and their transitive dependencies in dependency order
// (dependencies first), following registration and declaration order for
// determinism. It fails on cycles and unregistered dependencies.
func (r *Registry) topo(roots []string) ([]string, error) {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var order, stack []string
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			i := len(stack) - 1
			for stack[i] != id {
				i--
			}
			return &CycleError{Path: append(append([]string(nil), stack[i:]...), id)}
		}
		e, ok := r.mods[id]
		if !ok {
			return fmt.Errorf("%w: %s needed by %s", ErrMissingDependency, id, stack[len(stack)-1])
		}
		state[id] = visiting
		stack = append(stack, id)
		for _, d := range e.deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		order = append(order, id)
		return nil
	}
	for _, id := range roots {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Start starts all enabled modules in dependency order. Modules that cannot
// start (cycle, missing or disabled dependency, failed dependency, error or
// panic in Start) are skipped; all problems are returned joined. Calling
// Start on a started registry is a no-op.
func (r *Registry) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return nil
	}
	r.active = true
	for _, e := range r.mods {
		e.err = nil
	}
	var errs []error
	for _, id := range r.enabledLocked() {
		if r.mods[id].running || r.mods[id].err != nil {
			continue // started as a dependency, or already failed
		}
		if err := r.startOneLocked(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// startOneLocked starts id after (recursively) making sure its dependencies
// run. Already running modules are skipped.
func (r *Registry) startOneLocked(id string) error {
	order, err := r.topo([]string{id})
	if err != nil {
		r.mods[id].err = err
		return fmt.Errorf("module: cannot start %s: %w", id, err)
	}
	target := r.mods[id]
	for _, m := range order {
		e := r.mods[m]
		if e.running {
			continue
		}
		if m != id {
			// A dependency that is not running yet: it must be enabled and
			// must not have failed before (failed modules are retried
			// only when they are started themselves).
			var reason error
			switch {
			case !e.enabled:
				reason = fmt.Errorf("%w: %s", ErrDependencyDisabled, m)
			case e.err != nil && !errors.Is(e.err, ErrDependencyFailed) && !errors.Is(e.err, ErrDependencyDisabled):
				// Failed on its own; dependencies that were merely
				// blocked by others are attempted again.
				reason = fmt.Errorf("%w: %s", ErrDependencyFailed, m)
			}
			if reason != nil {
				target.err = reason
				return fmt.Errorf("module: cannot start %s: %w", id, reason)
			}
		}
		if err := r.launchLocked(m, e); err != nil {
			if m != id {
				target.err = fmt.Errorf("%w: %s", ErrDependencyFailed, m)
				return fmt.Errorf("module: cannot start %s: %w", id, errors.Join(target.err, err))
			}
			return err
		}
	}
	return nil
}

func (r *Registry) launchLocked(id string, e *entry) error {
	base := &baseContext{id: id, env: &r.env, logger: r.env.Logger.With("module", id)}
	var ctx Context = base
	if r.env.Extend != nil {
		ctx = r.env.Extend(base)
	}
	e.ctx = base
	if err := safeCall(func() error { return e.mod.Start(ctx) }); err != nil {
		base.cleanup(r.env.Hooks)
		e.ctx = nil
		e.err = err
		return fmt.Errorf("module: start %s: %w", id, err)
	}
	e.err = nil
	e.running = true
	r.started = append(r.started, id)
	return nil
}

// Stop stops all running modules in reverse start order. Errors are
// collected; every module is stopped regardless.
func (r *Registry) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = false
	var errs []error
	for i := len(r.started) - 1; i >= 0; i-- {
		if err := r.stopOneLocked(r.started[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Registry) stopOneLocked(id string) error {
	e := r.mods[id]
	if !e.running {
		return nil
	}
	err := safeCall(e.mod.Stop)
	e.ctx.cleanup(r.env.Hooks)
	e.ctx = nil
	e.running = false
	for i, s := range r.started {
		if s == id {
			r.started = append(r.started[:i:i], r.started[i+1:]...)
			break
		}
	}
	if err != nil {
		e.err = err
		return fmt.Errorf("module: stop %s: %w", id, err)
	}
	e.err = nil
	return nil
}

// safeCall runs fn, converting a panic into an error wrapping ErrPanic.
func safeCall(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w: %v\n%s", ErrPanic, p, debug.Stack())
		}
	}()
	return fn()
}

type baseContext struct {
	id     string
	env    *Env
	logger *slog.Logger

	mu       sync.Mutex
	cleanups []func()
}

func (c *baseContext) ModuleID() string          { return c.id }
func (c *baseContext) Bus() *bus.Bus             { return c.env.Bus }
func (c *baseContext) Hooks() *hooks.Registry    { return c.env.Hooks }
func (c *baseContext) Settings() *settings.Store { return c.env.Settings }
func (c *baseContext) Logger() *slog.Logger      { return c.logger }

func (c *baseContext) OnStop(fn func()) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.cleanups = append(c.cleanups, fn)
	c.mu.Unlock()
}

// cleanup runs OnStop callbacks in reverse order (panic-safe) and removes
// the module's hooks.
func (c *baseContext) cleanup(h *hooks.Registry) {
	c.mu.Lock()
	fns := c.cleanups
	c.cleanups = nil
	c.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		if err := safeCall(func() error { fns[i](); return nil }); err != nil {
			c.logger.Error("OnStop callback panicked", "err", err)
		}
	}
	if h != nil {
		h.RemoveOwner(c.id)
	}
}
