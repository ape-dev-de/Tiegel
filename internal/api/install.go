package api

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Kernel service "install" (decision D194): plugins declare recipes
// (HookInstallRecipes); install.plan shows the exact command, target and
// side effects; install.run executes it only with confirm=true, streams
// the output (install.output) and reports the end (install.done); it can
// be cancelled. Nothing is installed automatically or in the background,
// and a missing package manager is only reported.

// InstallPlanParams are the params of "install.plan".
type InstallPlanParams struct {
	// Plugin is the module ID declaring the recipe ("lsp-phpactor").
	Plugin string `json:"plugin"`
	// Scope is "global" or "project" (empty: the recipe's default).
	Scope string `json:"scope,omitempty"`
}

// InstallRunParams are the params of "install.run".
type InstallRunParams struct {
	Plugin string `json:"plugin"`
	Scope  string `json:"scope,omitempty"`
	// Confirm must be true: the user has seen the plan and agreed.
	Confirm bool `json:"confirm"`
}

// InstallRunResult is the result of "install.run".
type InstallRunResult struct {
	RunID string       `json:"runId"`
	Plan  install.Plan `json:"plan"`
}

// InstallRunIDParams address a run.
type InstallRunIDParams struct {
	RunID string `json:"runId"`
}

// InstallWaitParams are the params of "install.wait".
type InstallWaitParams struct {
	RunID string `json:"runId"`
}

func (a *App) registerInstall() {
	a.installs.OnLine = func(l install.Line) { a.bus.Publish(TopicInstallOutput, l) }
	a.installs.OnDone = func(r install.Result) { a.bus.Publish(TopicInstallDone, r) }
	mustHandle(a, "install.recipes", "Installation recipes of the running plugins (package manager, package, targets)", a.installRecipes)
	mustHandle(a, "install.plan", "Exact command, target and side effects of installing a plugin's tool (runs nothing)", a.installPlan)
	mustHandle(a, "install.run", "Install a plugin's tool after explicit confirmation (confirm: true); output as install.output events", a.installRun)
	mustHandle(a, "install.cancel", "Cancel a running installation", a.installCancel)
	mustHandle(a, "install.status", "State and output of an installation", a.installStatus)
	mustHandle(a, "install.wait", "Wait until an installation finished and return its result", a.installWait)
}

func (a *App) recipes(ctx context.Context) []install.Recipe {
	list, err := HookInstallRecipes.Apply(ctx, a.hooks, nil)
	if err != nil {
		a.log.Warn("install recipes", "err", err)
	}
	list = slices.Clone(list)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Plugin < list[j].Plugin })
	return list
}

func (a *App) installRecipes(ctx context.Context, _ struct{}) ([]install.Recipe, error) {
	out := []install.Recipe{}
	return append(out, a.recipes(ctx)...), nil
}

// InstallEnv returns the environment plans are made for: the open project
// and the PATH of the language server search (process PATH, login shell
// PATH, package manager directories).
func (a *App) InstallEnv() install.Env {
	opts := a.opts.LSP
	opts.ProjectDir = ""
	dirs := servers.SearchDirs(opts)
	return install.Env{ProjectDir: a.ProjectRoot(), PathEnv: strings.Join(dirs, string(os.PathListSeparator))}
}

func (a *App) makePlan(ctx context.Context, plugin, scope string) (install.Plan, error) {
	if plugin == "" {
		return install.Plan{}, Errorf(CodeInvalidParams, "plugin is required")
	}
	var names []string
	for _, r := range a.recipes(ctx) {
		if r.Plugin != plugin {
			names = append(names, r.Plugin)
			continue
		}
		p, err := install.MakePlan(r, install.Scope(scope), a.InstallEnv())
		if errors.Is(err, install.ErrUnknownScope) {
			return install.Plan{}, Errorf(CodeInvalidParams, "%w", err)
		}
		if err != nil {
			return install.Plan{}, Errorf(CodeFailed, "%w", err)
		}
		return p, nil
	}
	return install.Plan{}, Errorf(CodeNotFound, "no installation recipe for %q (plugin disabled or without recipe; recipes: %s)", plugin, strings.Join(names, ", "))
}

func (a *App) installPlan(ctx context.Context, p InstallPlanParams) (install.Plan, error) {
	return a.makePlan(ctx, p.Plugin, p.Scope)
}

func (a *App) installRun(ctx context.Context, p InstallRunParams) (InstallRunResult, error) {
	plan, err := a.makePlan(ctx, p.Plugin, p.Scope)
	if err != nil {
		return InstallRunResult{}, err
	}
	if !p.Confirm {
		return InstallRunResult{Plan: plan}, Errorf(CodeInvalidParams, "install.run needs confirm: true after showing the plan (%s)", plan.Display)
	}
	if !plan.Available {
		return InstallRunResult{Plan: plan}, Errorf(CodeUnavailable, "cannot install %s: %s", plan.Tool, plan.Hint)
	}
	id, err := a.installs.Start(ctx, plan)
	if err != nil {
		return InstallRunResult{Plan: plan}, Errorf(CodeConflict, "%w", err)
	}
	a.log.Info("install started", "plugin", plan.Plugin, "command", plan.Display)
	return InstallRunResult{RunID: id, Plan: plan}, nil
}

func (a *App) installCancel(_ context.Context, p InstallRunIDParams) (install.Result, error) {
	if err := a.installs.Cancel(p.RunID); err != nil {
		return install.Result{}, Errorf(CodeNotFound, "%w", err)
	}
	return a.installs.Status(p.RunID)
}

func (a *App) installStatus(_ context.Context, p InstallRunIDParams) (install.Result, error) {
	r, err := a.installs.Status(p.RunID)
	if err != nil {
		return install.Result{}, Errorf(CodeNotFound, "%w", err)
	}
	return r, nil
}

func (a *App) installWait(ctx context.Context, p InstallWaitParams) (install.Result, error) {
	r, err := a.installs.Wait(ctx, p.RunID)
	if errors.Is(err, install.ErrUnknownRun) {
		return install.Result{}, Errorf(CodeNotFound, "%w", err)
	}
	if err != nil {
		return r, Errorf(CodeFailed, "%w", err)
	}
	return r, nil
}
