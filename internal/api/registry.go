package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// HandlerFunc is the transport-level form of an API method: it receives the
// raw JSON parameters and returns a JSON-serialisable result.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Method is one entry of the method table.
type Method struct {
	// Name is the full method name, "<namespace>.<verb>", e.g. "doc.open".
	Name string
	// Summary is a one-line description shown by "api.methods" and the CLI.
	Summary string
	// Handler runs the method.
	Handler HandlerFunc
	// Module is the ID of the module that provides the method; empty for
	// the kernel namespaces. Module methods are only callable while their
	// module runs (see Provide).
	Module string

	// direct is the JSON-free path used by in-process callers (see
	// InProcess); nil for methods registered with a plain HandlerFunc.
	direct directFunc
}

// MethodInfo describes a registered method (without its handler).
type MethodInfo struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Module is the providing module ("" for kernel methods).
	Module string `json:"module,omitempty"`
	// Disabled is true while the providing module is not running; calls
	// fail with CodeModuleDisabled.
	Disabled bool `json:"disabled,omitempty"`
}

type methodEntry struct {
	Method
	disabled bool
}

// registry is the method table shared by all transports.
type registry struct {
	mu      sync.RWMutex
	methods map[string]*methodEntry
}

func newRegistry() *registry { return &registry{methods: make(map[string]*methodEntry)} }

func validMethod(m Method) error {
	if m.Name == "" || !strings.Contains(m.Name, ".") || m.Handler == nil {
		return fmt.Errorf("api: invalid method %q (need \"namespace.verb\" and a handler)", m.Name)
	}
	return nil
}

func (r *registry) add(m Method) error {
	if err := validMethod(m); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.methods[m.Name]; dup {
		return fmt.Errorf("api: method %q already registered", m.Name)
	}
	r.methods[m.Name] = &methodEntry{Method: m}
	return nil
}

// provide adds or re-activates a module method. A disabled entry of the same
// module is replaced (the module was restarted).
func (r *registry) provide(m Method) error {
	if err := validMethod(m); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.methods[m.Name]; ok && !(e.disabled && e.Module == m.Module) {
		return fmt.Errorf("api: method %q already registered", m.Name)
	}
	r.methods[m.Name] = &methodEntry{Method: m}
	return nil
}

// disable marks a module method as unavailable; the entry stays so that
// calls report CodeModuleDisabled instead of CodeMethodNotFound.
func (r *registry) disable(name, owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.methods[name]; ok && e.Module == owner {
		e.disabled = true
	}
}

func (r *registry) get(name string) (methodEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.methods[name]
	if !ok {
		return methodEntry{}, false
	}
	return *e, true
}

func (r *registry) list() []MethodInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]MethodInfo, 0, len(r.methods))
	for _, m := range r.methods {
		out = append(out, MethodInfo{Name: m.Name, Summary: m.Summary, Module: m.Module, Disabled: m.disabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Register adds a method to the table of a. It fails for duplicate or
// malformed names.
func (a *App) Register(m Method) error { return a.methods.add(m) }

// Handle registers a typed method: params are decoded from JSON into P
// (unknown fields are rejected, missing or null params give the zero P) and
// the result R is returned as is for the transport to encode.
//
//	api.Handle(a, "git.status", "Working tree status", a.gitStatus)
func Handle[P, R any](a *App, name, summary string, fn func(ctx context.Context, p P) (R, error)) error {
	return a.Register(Method{Name: name, Summary: summary, Handler: typed(fn), direct: direct(fn)})
}

// mustHandle is Handle for the built-in namespaces, whose names are fixed;
// a failure is a programming error that tests catch, so it is recorded and
// reported by New instead of panicking.
func mustHandle[P, R any](a *App, name, summary string, fn func(ctx context.Context, p P) (R, error)) {
	if err := Handle(a, name, summary, fn); err != nil {
		a.initErrs = append(a.initErrs, err)
	}
}

func typed[P, R any](fn func(ctx context.Context, p P) (R, error)) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p P
		if err := DecodeParams(raw, &p); err != nil {
			return nil, err
		}
		return fn(ctx, p)
	}
}

// DecodeParams decodes raw JSON parameters into v. Empty and null params
// leave v unchanged; unknown fields are an error (typos should not be
// silently ignored). Errors are *Error with CodeInvalidParams.
func DecodeParams(raw json.RawMessage, v any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Errorf(CodeInvalidParams, "invalid params: %v", err)
	}
	return nil
}

// Methods lists all registered methods sorted by name.
func (a *App) Methods() []MethodInfo { return a.methods.list() }

// Call invokes method with raw JSON params outside of any session (no event
// subscriptions). Use a Session for clients that want notifications.
//
// A panic in the handler (or in code it runs on the calling goroutine) does
// not end the process: it is logged with its stack and reported as an
// error with CodeInternal.
func (a *App) Call(ctx context.Context, method string, params json.RawMessage) (res any, err error) {
	if t := a.forwardTarget(method); t != nil {
		return t.Call(ctx, method, params)
	}
	m, err := a.lookup(method)
	if err != nil {
		return nil, err
	}
	defer a.recoverCall(method, &err)
	return m.Handler(ctx, params)
}

// recoverCall turns a panic of the handler of method into an internal
// error stored in *err. It must be deferred directly.
func (a *App) recoverCall(method string, err *error) {
	p := recover()
	if p == nil {
		return
	}
	a.log.Error("method panicked", "method", method, "panic", p, "stack", string(debug.Stack()))
	*err = Errorf(CodeInternal, "internal error in %s: %v", method, p)
}

// lookup returns the callable entry of method or the error a call reports.
func (a *App) lookup(method string) (methodEntry, error) {
	m, ok := a.methods.get(method)
	if !ok {
		if owner := a.claimant(method); owner != "" {
			return m, moduleDisabled(method, owner)
		}
		return m, Errorf(CodeMethodNotFound, "method not found: %s", method)
	}
	if m.disabled {
		return m, moduleDisabled(method, m.Module)
	}
	return m, nil
}

// CallJSON is Call with params given as any JSON-serialisable value.
func (a *App) CallJSON(ctx context.Context, method string, params any) (any, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, Errorf(CodeInvalidParams, "encoding params: %v", err)
	}
	return a.Call(ctx, method, raw)
}
