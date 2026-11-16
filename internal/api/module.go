package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/module"
)

// ModuleContext is the module.Context the App hands to modules. Besides the
// kernel services (bus, hooks, settings) it gives access to the App, so a
// module can register its API namespace with Provide and read documents and
// project data. Modules still never call each other: shared data travels
// over the bus (events) and hooks (see contract.go).
type ModuleContext struct {
	module.Context
	app *App
}

// App returns the App the module runs in.
func (c *ModuleContext) App() *App { return c.app }

// AppFrom returns the App behind a module context created by an App.
func AppFrom(ctx module.Context) (*App, bool) {
	h, ok := ctx.(interface{ App() *App })
	if !ok {
		return nil, false
	}
	return h.App(), true
}

// Claimer is implemented by modules that provide API methods. Claims lists
// the method names (or "namespace.*" patterns) the module provides, so that
// calls report CodeModuleDisabled – instead of "method not found" – while
// the module is disabled, even if it never ran.
type Claimer interface {
	Claims() []string
}

// Provide registers an API method of the module that owns ctx; call it from
// Module.Start. The method is available while the module runs: when the
// module stops it is disabled (calls fail with CodeModuleDisabled) and a
// later Start re-activates it. ctx must come from an App.
func Provide[P, R any](ctx module.Context, name, summary string, fn func(ctx context.Context, p P) (R, error)) error {
	a, ok := AppFrom(ctx)
	if !ok {
		return fmt.Errorf("api: Provide %s: module context has no App", name)
	}
	owner := ctx.ModuleID()
	if err := a.methods.provide(Method{Name: name, Summary: summary, Handler: typed(fn), direct: direct(fn), Module: owner}); err != nil {
		return err
	}
	ctx.OnStop(func() { a.methods.disable(name, owner) })
	return nil
}

// Provider collects Provide calls and reports the first error; it keeps
// module Start functions short.
type Provider struct {
	ctx module.Context
	err error
}

// NewProvider returns a Provider for ctx.
func NewProvider(ctx module.Context) *Provider { return &Provider{ctx: ctx} }

// Err returns the first registration error.
func (p *Provider) Err() error { return p.err }

// Add registers a method like Provide unless an earlier call failed.
func Add[P, R any](p *Provider, name, summary string, fn func(ctx context.Context, p P) (R, error)) {
	if p.err != nil {
		return
	}
	p.err = Provide(p.ctx, name, summary, fn)
}

// AddModule registers a compiled-in module with the kernel registry
// (enabled by default unless its state was stored otherwise). Claims of a
// Claimer are recorded. If the registry is running, an enabled module starts
// immediately.
func (a *App) AddModule(m module.Module, opts ...module.RegisterOption) error {
	if c, ok := m.(Claimer); ok {
		a.mu.Lock()
		if a.claims == nil {
			a.claims = map[string][]string{}
		}
		a.claims[m.ID()] = append([]string(nil), c.Claims()...)
		a.mu.Unlock()
	}
	if len(opts) == 0 {
		opts = []module.RegisterOption{module.DefaultEnabled()}
	}
	if err := a.modules.Register(m, opts...); err != nil {
		if errors.Is(err, module.ErrDuplicate) || errors.Is(err, module.ErrInvalidID) {
			return err
		}
		a.log.Warn("module start", "module", m.ID(), "err", err)
	}
	return nil
}

// claimant returns the module claiming method, or "".
func (a *App) claimant(method string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, patterns := range a.claims {
		for _, p := range patterns {
			if p == method || (strings.HasSuffix(p, ".*") && strings.HasPrefix(method, strings.TrimSuffix(p, "*"))) {
				return id
			}
		}
	}
	return ""
}

func moduleDisabled(method, owner string) error {
	return Errorf(CodeModuleDisabled, "module disabled: %s is provided by module %q (enable it with modules.enable)", method, owner).
		WithData(map[string]string{"module": owner, "method": method})
}

// ModuleInfo describes a module for "modules.list".
type ModuleInfo struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"dependsOn"`
	Enabled   bool     `json:"enabled"`
	Running   bool     `json:"running"`
	Error     string   `json:"error,omitempty"`
}

// ModuleParams address a module.
type ModuleParams struct {
	ID string `json:"id"`
	// WithDependencies enables the transitive dependencies first.
	WithDependencies bool `json:"withDependencies,omitempty"`
}

func (a *App) registerModules() {
	mustHandle(a, "modules.list", "Registered modules with state", a.modulesList)
	mustHandle(a, "modules.enable", "Enable a module (persisted, starts it immediately)", a.modulesEnable)
	mustHandle(a, "modules.disable", "Disable a module (persisted, stops it immediately)", a.modulesDisable)
}

func (a *App) modulesList(context.Context, struct{}) ([]ModuleInfo, error) {
	out := []ModuleInfo{}
	for _, m := range a.modules.Modules() {
		mi := ModuleInfo{ID: m.ID, DependsOn: m.DependsOn, Enabled: m.Enabled, Running: m.Running}
		if mi.DependsOn == nil {
			mi.DependsOn = []string{}
		}
		if m.Err != nil {
			mi.Error = m.Err.Error()
		}
		out = append(out, mi)
	}
	return out, nil
}

func (a *App) moduleResult(id string) (ModuleInfo, error) {
	list, _ := a.modulesList(context.Background(), struct{}{})
	for _, m := range list {
		if m.ID == id {
			a.bus.Publish(TopicModuleChanged, m)
			return m, nil
		}
	}
	return ModuleInfo{}, Errorf(CodeNotFound, "module %q not registered", id)
}

func moduleErr(err error) error {
	if errors.Is(err, module.ErrUnknown) {
		return Errorf(CodeNotFound, "%w", err)
	}
	return Errorf(CodeFailed, "%w", err)
}

func (a *App) modulesEnable(_ context.Context, p ModuleParams) (ModuleInfo, error) {
	var err error
	if p.WithDependencies {
		err = a.modules.EnableWithDependencies(p.ID)
	} else {
		err = a.modules.Enable(p.ID)
	}
	if err != nil {
		return ModuleInfo{}, moduleErr(err)
	}
	return a.moduleResult(p.ID)
}

func (a *App) modulesDisable(_ context.Context, p ModuleParams) (ModuleInfo, error) {
	if err := a.modules.Disable(p.ID); err != nil {
		return ModuleInfo{}, moduleErr(err)
	}
	return a.moduleResult(p.ID)
}
