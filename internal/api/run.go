package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Kernel service "run" (AP15.1): start, stop, rerun and observe processes
// in the project's interpreter; run configurations as project settings
// (types from HookRunConfigTypes); output with file links mapped to the
// host. Every start passes the run.before guard (autosave).

// RunStartParams are the params of "run.start". Exactly one of Config,
// File and Spec says what to run.
type RunStartParams struct {
	// Config is the ID or name of a run configuration.
	Config string `json:"config,omitempty"`
	// File runs a PHP file with the project interpreter (absolute or
	// project relative), with Args.
	File string   `json:"file,omitempty"`
	Args []string `json:"args,omitempty"`
	// Spec is an ad-hoc process.
	Spec *run.Spec `json:"spec,omitempty"`
	// Interpreter overrides the interpreter of File and Spec runs.
	Interpreter string `json:"interpreter,omitempty"`
	// Debug runs under the debugger: the listener is started (hook
	// debug.listener, D311) and the Xdebug trigger is set.
	Debug bool `json:"debug,omitempty"`
	// Context is the editor context (current file, caret, selection)
	// for the macros of a configuration, e.g. an external tool's
	// $FilePath$ (D413).
	Context *run.MacroContext `json:"context,omitempty"`
}

// RunIDParams address a run.
type RunIDParams struct {
	RunID string `json:"runId"`
}

// RunListParams are the params of "run.list".
type RunListParams struct {
	// Active lists only runs that have not finished.
	Active bool `json:"active,omitempty"`
}

// RunOutputParams are the params of "run.output".
type RunOutputParams struct {
	RunID string `json:"runId"`
	// Since returns only lines with a higher sequence number.
	Since int `json:"since,omitempty"`
	// Limit returns at most this many of the newest lines (0: all
	// buffered).
	Limit int `json:"limit,omitempty"`
}

// RunConfigInfo is a run configuration with its state.
type RunConfigInfo struct {
	run.Config
	TypeLabel string `json:"typeLabel,omitempty"`
	// Available is false when no running plugin provides the type.
	Available bool `json:"available"`
	// Display previews the command; Error explains a configuration that
	// cannot be built.
	Display  string `json:"display,omitempty"`
	Error    string `json:"error,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

// RunConfigIDParams address a configuration (ID or name).
type RunConfigIDParams struct {
	ID string `json:"id"`
}

// RunConfigTypeInfo is a configuration type with the suggestions of its
// Suggest fields for the open project.
type RunConfigTypeInfo struct {
	run.ConfigType
	Suggestions map[string][]string `json:"suggestions,omitempty"`
}

// RunCloseCheck is the result of "run.closeCheck".
type RunCloseCheck struct {
	// Policy is the setting run.confirmClose (ask, terminate, keep).
	Policy string `json:"policy"`
	// Active are the runs of the open project that have not finished.
	Active []run.Info `json:"active"`
	// Project is the project root the check was made for ("" without a
	// project: all runs). Pass it to run.stopAll / run.keepAll so that
	// the decision applies to these runs even if the project changed
	// meanwhile.
	Project string `json:"project,omitempty"`
}

// RunStopAllParams are the params of "run.stopAll" and "run.keepAll".
type RunStopAllParams struct {
	// Project is the project root whose runs are meant ("": the open
	// project; without an open project all runs).
	Project string `json:"project,omitempty"`
	// Wait (run.stopAll) returns only when the stopped runs ended (or
	// after stopAllWait).
	Wait bool `json:"wait,omitempty"`
}

// stopAllWait bounds run.stopAll with wait; closeWait the stop on App
// close (covers the grace period of a stop inside a container).
const (
	stopAllWait = 15 * time.Second
	closeWait   = 8 * time.Second
)

// runConfigsMu serialises the read-modify-write of the run
// configurations (API calls run concurrently).
var runConfigsMu sync.Mutex

func (a *App) registerRun() {
	a.runs.OnStart = func(i run.Info) { a.bus.Publish(TopicRunStarted, i) }
	a.runs.OnOutput = func(o run.Output) { a.bus.Publish(TopicRunOutput, o) }
	a.runs.OnExit = func(i run.Info) {
		a.bus.Publish(TopicRunExited, i)
		if i.Spec.SyncFiles {
			go a.syncAfterRun(i)
		}
	}
	HookRunConfigTypes.On(a.hooks, -1000, func(_ context.Context, l []run.ConfigType) ([]run.ConfigType, error) {
		return append(run.CoreTypes(), l...), nil
	}, hooks.WithOwner("kernel"))
	a.registerExternalTools()
	mustHandle(a, "run.start", "Run a configuration, a PHP file or an ad-hoc command in the interpreter (passes run.before; output as run.output events)", a.runStart)
	mustHandle(a, "run.stop", "Stop a run (process tree, and the process inside the container)", a.runStop)
	mustHandle(a, "run.rerun", "Run the same process again (stops it first if active)", a.runRerun)
	mustHandle(a, "run.list", "Runs with state, exit code and duration", a.runList)
	mustHandle(a, "run.output", "Buffered output lines of a run (with file links)", a.runOutput)
	mustHandle(a, "run.wait", "Wait until a run finished and return its state", a.runWait)
	mustHandle(a, "run.remove", "Forget a finished run", a.runRemove)
	mustHandle(a, "run.closeCheck", "Active runs of the project and the close policy (run.confirmClose)", a.runCloseCheck)
	mustHandle(a, "run.stopAll", "Stop all active runs of a project (default: the open one; wait: until they ended)", a.runStopAll)
	mustHandle(a, "run.keepAll", "Keep the active runs of a project running when Tiegel closes (close confirmation: Keep Running)", a.runKeepAll)
	mustHandle(a, "run.configs.list", "Run configurations of the project", a.runConfigsList)
	mustHandle(a, "run.configs.save", "Create or update a run configuration (validated against its type)", a.runConfigsSave)
	mustHandle(a, "run.configs.delete", "Delete a run configuration", a.runConfigsDelete)
	mustHandle(a, "run.configs.select", "Select the run configuration of Run (Ctrl+R)", a.runConfigsSelect)
	mustHandle(a, "run.configTypes", "Run configuration types (core and plugins) with their fields", a.runConfigTypes)
	a.bus.Subscribe(TopicProjectClosed, func(e bus.Event) {
		ev, ok := e.Payload.(ProjectEvent)
		if !ok || a.runClosePolicy() != run.CloseTerminate {
			return
		}
		a.runs.StopAll(func(i run.Info) bool { return i.Project == ev.Root })
	})
}

// runClosePolicy returns run.confirmClose (global).
func (a *App) runClosePolicy() string {
	v := settings.Get(a.global, run.KeyConfirmClose, run.CloseAsk)
	switch v {
	case run.CloseAsk, run.CloseTerminate, run.CloseKeep:
		return v
	}
	return run.CloseAsk
}

// closeRuns ends the active runs when the App closes, unless the policy
// keeps them or the user chose to keep them (run.keepAll); it waits
// briefly so containers are stopped too.
func (a *App) closeRuns() {
	if a.runClosePolicy() == run.CloseKeep {
		return
	}
	ids := a.runs.StopAll(func(i run.Info) bool { return !i.Keep })
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	a.waitRuns(ctx, ids)
}

// waitRuns waits until the runs ids ended or ctx ends.
func (a *App) waitRuns(ctx context.Context, ids []string) {
	for _, id := range ids {
		_, _ = a.runs.Wait(ctx, id)
	}
}

// runsOf returns a match for the runs of project root p ("": the open
// project; with none, all runs).
func (a *App) runsOf(p string) (string, func(run.Info) bool) {
	if p == "" {
		p = a.ProjectRoot()
	}
	return p, func(i run.Info) bool { return p == "" || i.Project == p }
}

// RunTarget resolves where a spec with interpreter choice id runs (see
// run.Spec.Interpreter). Without a provider of HookRunInterpreter (module
// php-runtime disabled) runs go to the host.
func (a *App) RunTarget(ctx context.Context, id string) (run.Target, error) {
	t := run.Target{ProjectDir: a.ProjectRoot(), PathEnv: a.InstallEnv().PathEnv}
	if id == "" || id == run.InterpreterHost {
		return t, nil
	}
	qid := id
	if id == run.InterpreterProject {
		qid = ""
	}
	q, err := HookRunInterpreter.Apply(ctx, a.hooks, RunInterpreterQuery{ID: qid})
	if err != nil {
		return t, Errorf(CodeFailed, "interpreter: %w", err)
	}
	if q.Interpreter == nil {
		if q.Err != "" {
			return t, Errorf(CodeUnavailable, "interpreter %s: %s", orText(qid, "of the project"), q.Err)
		}
		if qid != "" {
			return t, Errorf(CodeUnavailable, "interpreter %s is not available (module php-runtime disabled?)", qid)
		}
		return t, nil // no provider: the host's php
	}
	t.Interp, t.Mappings = q.Interpreter, runMappings(q.Mappings)
	return t, nil
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// launcher returns the launcher of spec: it resolves the target anew on
// every start and rerun.
func (a *App) launcher(spec run.Spec) run.Launcher {
	return func(ctx context.Context) (*run.Launch, error) {
		t, err := a.RunTarget(ctx, spec.EffectiveInterpreter())
		if err != nil {
			return nil, err
		}
		l, err := run.Prepare(ctx, spec, t)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return nil, Errorf(CodeUnavailable, "%w", err)
			}
			return nil, Errorf(CodeFailed, "%w", err)
		}
		return l, nil
	}
}

// runWrap returns install.Env.Wrap: the argv of a command in the
// project's interpreter (without the stop helper, so the plan shows
// exactly what runs).
func (a *App) runWrap(ctx context.Context) func(argv []string, dir string) ([]string, error) {
	return func(argv []string, dir string) ([]string, error) {
		if len(argv) == 0 {
			return nil, errors.New("run: empty command")
		}
		spec := run.Spec{Program: argv[0], Args: argv[1:], Dir: dir, Interpreter: run.InterpreterProject}
		t, err := a.RunTarget(ctx, run.InterpreterProject)
		if err != nil {
			return nil, err
		}
		t.Plain = true
		l, err := run.Prepare(ctx, spec, t)
		if err != nil {
			return nil, err
		}
		return append([]string{l.Cmd.Path}, l.Cmd.Args[1:]...), nil
	}
}

// specFor turns start params into a spec.
func (a *App) specFor(ctx context.Context, p RunStartParams) (run.Spec, error) {
	n := 0
	for _, set := range []bool{p.Config != "", p.File != "", p.Spec != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return run.Spec{}, Errorf(CodeInvalidParams, "give exactly one of config, file and spec")
	}
	root := a.ProjectRoot()
	switch {
	case p.Config != "":
		if root == "" {
			return run.Spec{}, Errorf(CodeNoProject, "run configurations need an open project")
		}
		cfg, err := a.findRunConfig(p.Config)
		if err != nil {
			return run.Spec{}, err
		}
		spec, err := a.buildRunConfig(ctx, cfg, p.Context)
		if err != nil {
			return run.Spec{}, err
		}
		// Tools (External Tools) run on demand and keep the selection:
		// Run must not start them later without editor context.
		if t, ok := a.configType(ctx, cfg.Type); ok && !t.Tool {
			if err := settings.Set(a.settingsStore(), run.KeySelected, cfg.ID); err != nil {
				a.log.Warn("run: select", "err", err)
			}
		}
		return spec, nil
	case p.File != "":
		file := p.File
		if !filepath.IsAbs(file) {
			if root == "" {
				return run.Spec{}, Errorf(CodeNoProject, "relative file %s needs an open project", file)
			}
			file = filepath.Join(root, filepath.FromSlash(file))
		}
		dir := root
		if dir == "" {
			dir = filepath.Dir(file)
		}
		spec := run.FileSpec(file, dir, p.Args)
		spec.Dir = dir
		if p.Interpreter != "" {
			spec.Interpreter = p.Interpreter
		}
		return spec, nil
	}
	spec := *p.Spec
	if p.Interpreter != "" {
		spec.Interpreter = p.Interpreter
	}
	if err := spec.Validate(); err != nil {
		return run.Spec{}, Errorf(CodeInvalidParams, "%w", err)
	}
	if spec.Name == "" {
		spec.Name = spec.Display()
	}
	return spec, nil
}

// guardRun runs the run.before guard.
func (a *App) guardRun(ctx context.Context, spec run.Spec, rerun string) error {
	req := RunRequest{Name: spec.Name, Config: spec.Config, Type: spec.Type, Display: spec.Display(), Rerun: rerun}
	if err := HookRunBefore.Check(ctx, a.hooks, req); err != nil {
		return Errorf(CodeFailed, "run aborted: %w", err)
	}
	return nil
}

func (a *App) runStart(ctx context.Context, p RunStartParams) (run.Info, error) {
	spec, err := a.specFor(ctx, p)
	if err != nil {
		return run.Info{}, err
	}
	if err := a.guardRun(ctx, spec, ""); err != nil {
		return run.Info{}, err
	}
	if p.Debug || spec.Debug {
		if spec, err = a.debugSpec(ctx, spec); err != nil {
			return run.Info{}, err
		}
	}
	root := a.ProjectRoot()
	info, err := a.runs.Start(ctx, spec, a.launcher(spec), run.StartOptions{Project: root, LinkDirs: []string{root}})
	if err != nil {
		return run.Info{}, startError(err)
	}
	a.log.Info("run started", "id", info.ID, "command", info.Display, "where", info.Where)
	return info, nil
}

// debugListener makes the debugger listen (hook debug.listener) and
// returns its port (0 if unknown).
func (a *App) debugListener(ctx context.Context) (int, error) {
	q, err := HookDebugListener.Apply(ctx, a.hooks, DebugListenerQuery{})
	if err != nil {
		return 0, Errorf(CodeFailed, "debugger: %w", err)
	}
	if q.Err != "" {
		return 0, Errorf(CodeUnavailable, "debugger: %s", q.Err)
	}
	if q.Addr == "" {
		return 0, Errorf(CodeModuleDisabled, "debugging needs a debugger plugin (debug-xdebug is disabled)")
	}
	_, port, _ := net.SplitHostPort(q.Addr)
	n, _ := strconv.Atoi(port)
	return n, nil
}

// debugSpec starts the listener and returns spec with the Xdebug trigger
// (environment, and php options for PHP specs).
func (a *App) debugSpec(ctx context.Context, spec run.Spec) (run.Spec, error) {
	port, err := a.debugListener(ctx)
	if err != nil {
		return spec, err
	}
	spec.Debug = true
	spec.Env = append(slices.Clone(spec.Env), run.DebugEnv()...)
	if spec.PHP {
		spec.Args = append(run.DebugPHPArgs(port), spec.Args...)
	}
	return spec, nil
}

// startError keeps API errors and wraps others.
func startError(err error) error {
	var ae *Error
	if errors.As(err, &ae) {
		return err
	}
	if errors.Is(err, exec.ErrNotFound) {
		return Errorf(CodeUnavailable, "%w", err)
	}
	if errors.Is(err, run.ErrBatchArgument) {
		return Errorf(CodeInvalidParams, "%w", err)
	}
	return Errorf(CodeFailed, "%w", err)
}

func (a *App) runError(err error) error {
	if errors.Is(err, run.ErrUnknownRun) {
		return Errorf(CodeNotFound, "%w", err)
	}
	if errors.Is(err, run.ErrRunning) {
		return Errorf(CodeConflict, "%w", err)
	}
	return Errorf(CodeFailed, "%w", err)
}

func (a *App) runStop(_ context.Context, p RunIDParams) (run.Info, error) {
	if err := a.runs.Stop(p.RunID); err != nil {
		return run.Info{}, a.runError(err)
	}
	return a.runs.Get(p.RunID)
}

func (a *App) runRerun(ctx context.Context, p RunIDParams) (run.Info, error) {
	old, err := a.runs.Get(p.RunID)
	if err != nil {
		return run.Info{}, a.runError(err)
	}
	if err := a.guardRun(ctx, old.Spec, p.RunID); err != nil {
		return run.Info{}, err
	}
	if old.Spec.Debug {
		if _, err := a.debugListener(ctx); err != nil {
			return run.Info{}, err
		}
	}
	info, err := a.runs.Rerun(ctx, p.RunID)
	if err != nil {
		if errors.Is(err, run.ErrUnknownRun) {
			return run.Info{}, a.runError(err)
		}
		return run.Info{}, startError(err)
	}
	a.bus.Publish(TopicRunRemoved, RunIDParams{RunID: p.RunID})
	return info, nil
}

func (a *App) runList(_ context.Context, p RunListParams) ([]run.Info, error) {
	out := []run.Info{}
	for _, i := range a.runs.List() {
		if !p.Active || i.Active() {
			out = append(out, i)
		}
	}
	return out, nil
}

func (a *App) runOutput(_ context.Context, p RunOutputParams) (run.Page, error) {
	pg, err := a.runs.Output(p.RunID, p.Since, p.Limit)
	if err != nil {
		return run.Page{}, a.runError(err)
	}
	return pg, nil
}

func (a *App) runWait(ctx context.Context, p RunIDParams) (run.Info, error) {
	i, err := a.runs.Wait(ctx, p.RunID)
	if errors.Is(err, run.ErrUnknownRun) {
		return run.Info{}, a.runError(err)
	}
	if err != nil {
		return i, Errorf(CodeFailed, "%w", err)
	}
	return i, nil
}

func (a *App) runRemove(_ context.Context, p RunIDParams) (struct{}, error) {
	if err := a.runs.Remove(p.RunID); err != nil {
		return struct{}{}, a.runError(err)
	}
	a.bus.Publish(TopicRunRemoved, p)
	return struct{}{}, nil
}

func (a *App) runCloseCheck(_ context.Context, _ struct{}) (RunCloseCheck, error) {
	root, match := a.runsOf("")
	res := RunCloseCheck{Policy: a.runClosePolicy(), Active: []run.Info{}, Project: root}
	for _, i := range a.runs.Active() {
		if match(i) {
			res.Active = append(res.Active, i)
		}
	}
	return res, nil
}

func (a *App) runStopAll(ctx context.Context, p RunStopAllParams) ([]string, error) {
	_, match := a.runsOf(p.Project)
	ids := a.runs.StopAll(match)
	if ids == nil {
		ids = []string{}
	}
	if p.Wait {
		wctx, cancel := context.WithTimeout(ctx, stopAllWait)
		defer cancel()
		a.waitRuns(wctx, ids)
	}
	return ids, nil
}

func (a *App) runKeepAll(_ context.Context, p RunStopAllParams) ([]string, error) {
	_, match := a.runsOf(p.Project)
	ids := []string{}
	for _, i := range a.runs.Active() {
		if match(i) && a.runs.Keep(i.ID) == nil {
			ids = append(ids, i.ID)
		}
	}
	return ids, nil
}

// ---- configurations -------------------------------------------------------------

// runConfigTypes collects the types (first of an ID wins).
func (a *App) configTypes(ctx context.Context) []run.ConfigType {
	list, err := HookRunConfigTypes.Apply(ctx, a.hooks, nil)
	if err != nil {
		a.log.Warn("run config types", "err", err)
	}
	seen := map[string]bool{}
	var out []run.ConfigType
	for _, t := range list {
		if seen[t.ID] {
			continue
		}
		if err := t.Validate(); err != nil {
			a.log.Warn("run config type", "err", err)
			continue
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out
}

func (a *App) configType(ctx context.Context, id string) (run.ConfigType, bool) {
	for _, t := range a.configTypes(ctx) {
		if t.ID == id {
			return t, true
		}
	}
	return run.ConfigType{}, false
}

func (a *App) buildEnv(mc *run.MacroContext) run.BuildEnv {
	return run.BuildEnv{ProjectDir: a.ProjectRoot(), Context: mc}
}

func (a *App) runConfigTypes(ctx context.Context, _ struct{}) ([]RunConfigTypeInfo, error) {
	env := a.buildEnv(nil)
	out := []RunConfigTypeInfo{}
	for _, t := range a.configTypes(ctx) {
		info := RunConfigTypeInfo{ConfigType: t}
		if t.Suggest != nil {
			for _, f := range t.Fields {
				if f.Suggest {
					if s := t.Suggest(ctx, env, f.Key); len(s) > 0 {
						if info.Suggestions == nil {
							info.Suggestions = map[string][]string{}
						}
						info.Suggestions[f.Key] = s
					}
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// storedRunConfigs returns the configurations of the project.
func (a *App) storedRunConfigs() ([]run.Config, error) {
	pr, err := a.requireProject()
	if err != nil {
		return nil, err
	}
	list, _, err := settings.Lookup[[]run.Config](pr.settings, run.KeyConfigurations)
	if err != nil {
		return nil, Errorf(CodeFailed, "run configurations: %w", err)
	}
	return list, nil
}

func (a *App) findRunConfig(idOrName string) (run.Config, error) {
	list, err := a.storedRunConfigs()
	if err != nil {
		return run.Config{}, err
	}
	for _, c := range list {
		if c.ID == idOrName {
			return c, nil
		}
	}
	for _, c := range list {
		if strings.EqualFold(c.Name, idOrName) {
			return c, nil
		}
	}
	return run.Config{}, Errorf(CodeNotFound, "no run configuration %q (see run.configs.list)", idOrName)
}

// buildRunConfig validates cfg against its type and builds the spec with
// the editor context mc (nil: none).
func (a *App) buildRunConfig(ctx context.Context, cfg run.Config, mc *run.MacroContext) (run.Spec, error) {
	t, ok := a.configType(ctx, cfg.Type)
	if !ok {
		return run.Spec{}, Errorf(CodeModuleDisabled, "run configuration %q has type %q, which no running plugin provides", cfg.Name, cfg.Type)
	}
	checked, err := t.Check(cfg)
	if err != nil {
		return run.Spec{}, Errorf(CodeInvalidParams, "%w", err)
	}
	spec, err := t.Build(ctx, t.WithDefaults(checked), a.buildEnv(mc))
	if err != nil {
		return run.Spec{}, Errorf(CodeInvalidParams, "%w", err)
	}
	spec.Name, spec.Config, spec.Type = orText(spec.Name, cfg.Name), cfg.ID, cfg.Type
	return spec, nil
}

func (a *App) runConfigsList(ctx context.Context, _ struct{}) ([]RunConfigInfo, error) {
	list, err := a.storedRunConfigs()
	if err != nil {
		return nil, err
	}
	selected := settings.Get(a.settingsStore(), run.KeySelected, "")
	out := []RunConfigInfo{}
	for _, c := range list {
		info := RunConfigInfo{Config: c, Selected: c.ID == selected}
		if t, ok := a.configType(ctx, c.Type); ok {
			info.TypeLabel, info.Available = t.Label, true
			if spec, err := a.buildRunConfig(ctx, c, nil); errors.Is(err, run.ErrMacroNeedsFile) {
				// Fine: the macros are filled in when it runs.
				info.Display = toolTemplate(c)
			} else if err != nil {
				info.Error = err.Error()
			} else {
				info.Display = spec.Display()
			}
		} else {
			info.Error = fmt.Sprintf("type %q is not available (plugin disabled?)", c.Type)
		}
		out = append(out, info)
	}
	return out, nil
}

func (a *App) publishRunConfigs(ctx context.Context) {
	if list, err := a.runConfigsList(ctx, struct{}{}); err == nil {
		a.bus.Publish(TopicRunConfigsChanged, list)
	}
}

func (a *App) runConfigsSave(ctx context.Context, c run.Config) (run.Config, error) {
	t, ok := a.configType(ctx, c.Type)
	if !ok {
		return run.Config{}, Errorf(CodeInvalidParams, "unknown run configuration type %q (see run.configTypes)", c.Type)
	}
	checked, err := t.Check(c)
	if err != nil {
		return run.Config{}, Errorf(CodeInvalidParams, "%w", err)
	}
	if checked, err = a.storeRunConfig(checked); err != nil {
		return run.Config{}, err
	}
	a.publishRunConfigs(ctx)
	return checked, nil
}

// storeRunConfig adds or replaces checked in the stored list (under
// runConfigsMu, so concurrent changes are not lost). The first stored
// configuration becomes the selected one unless its type is a tool
// (External Tools never become the selection).
func (a *App) storeRunConfig(checked run.Config) (run.Config, error) {
	t, _ := a.configType(context.Background(), checked.Type)
	runConfigsMu.Lock()
	defer runConfigsMu.Unlock()
	list, err := a.storedRunConfigs()
	if err != nil {
		return run.Config{}, err
	}
	for _, o := range list {
		if o.ID != checked.ID && strings.EqualFold(o.Name, checked.Name) {
			return run.Config{}, Errorf(CodeConflict, "a run configuration named %q exists", checked.Name)
		}
	}
	if checked.ID == "" {
		checked.ID = run.NewID()
	}
	i := slices.IndexFunc(list, func(o run.Config) bool { return o.ID == checked.ID })
	if i >= 0 {
		list[i] = checked
	} else {
		list = append(list, checked)
	}
	if err := settings.Set(a.ProjectSettings(), run.KeyConfigurations, list); err != nil {
		return run.Config{}, Errorf(CodeFailed, "run configurations: %w", err)
	}
	if !t.Tool && settings.Get(a.ProjectSettings(), run.KeySelected, "") == "" {
		_ = settings.Set(a.ProjectSettings(), run.KeySelected, checked.ID)
	}
	return checked, nil
}

func (a *App) runConfigsDelete(ctx context.Context, p RunConfigIDParams) (struct{}, error) {
	if err := a.deleteRunConfig(p.ID); err != nil {
		return struct{}{}, err
	}
	a.publishRunConfigs(ctx)
	return struct{}{}, nil
}

// deleteRunConfig removes configuration idOrName (under runConfigsMu).
func (a *App) deleteRunConfig(idOrName string) error {
	runConfigsMu.Lock()
	defer runConfigsMu.Unlock()
	cfg, err := a.findRunConfig(idOrName)
	if err != nil {
		return err
	}
	list, err := a.storedRunConfigs()
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(o run.Config) bool { return o.ID == cfg.ID })
	if err := settings.Set(a.ProjectSettings(), run.KeyConfigurations, list); err != nil {
		return Errorf(CodeFailed, "run configurations: %w", err)
	}
	if settings.Get(a.ProjectSettings(), run.KeySelected, "") == cfg.ID {
		_ = a.ProjectSettings().Delete(run.KeySelected)
	}
	return nil
}

func (a *App) runConfigsSelect(ctx context.Context, p RunConfigIDParams) (RunConfigInfo, error) {
	cfg, err := a.selectRunConfig(p.ID)
	if err != nil {
		return RunConfigInfo{}, err
	}
	a.publishRunConfigs(ctx)
	list, _ := a.runConfigsList(ctx, struct{}{})
	for _, c := range list {
		if c.ID == cfg.ID {
			return c, nil
		}
	}
	return RunConfigInfo{Config: cfg, Selected: true}, nil
}

// selectRunConfig selects configuration idOrName (under runConfigsMu, so
// that it cannot select one a concurrent delete just removed).
func (a *App) selectRunConfig(idOrName string) (run.Config, error) {
	runConfigsMu.Lock()
	defer runConfigsMu.Unlock()
	cfg, err := a.findRunConfig(idOrName)
	if err != nil {
		return run.Config{}, err
	}
	if err := settings.Set(a.ProjectSettings(), run.KeySelected, cfg.ID); err != nil {
		return run.Config{}, Errorf(CodeFailed, "run configurations: %w", err)
	}
	return cfg, nil
}

// runMappings converts api mappings for the run package.
func runMappings(ms []MappingInfo) compose.Mappings {
	var out compose.Mappings
	for _, m := range ms {
		out = out.Set(m.Host, m.Remote)
	}
	return out
}
