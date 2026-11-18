// Package qakit is the shared helper of the quality tool plugins
// (AP17.1): a plugin describes its tool with a Definition (program,
// install package, argument builders, output parser) and New turns it
// into a module that
//
//   - declares its settings in its own namespace (D190): path (default
//     vendor/bin/<program>), configuration file, extra arguments, run in
//     the project interpreter or on the host, timeout, severity and when
//     to run (Reformat Code, on save, on demand);
//   - contributes the tool to the kernel's qa service (api.HookQATools)
//     and its Composer recipe to the installer (api.HookInstallRecipes,
//     D194: installed only on the user's click);
//   - runs the tool through the run infrastructure in the project's
//     interpreter (local php, Compose service, image, WSL) with path
//     mapping, so it works inside containers;
//   - formats on save through the file.beforeSave filter (optional) and
//     inspects saved files in the background (optional);
//   - imports the tool's settings from the reference IDE's project
//     configuration (Definition.Import, hook import.ide.component).
//
// The package knows no tool by name; the definitions live in the
// plugin package.
package qakit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
)

// Severities of findings (the names Problems understands).
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
	SevHint    = "hint"
)

// SeverityAuto keeps the severity the tool reports.
const SeverityAuto = "auto"

// Definition describes a quality tool plugin.
type Definition struct {
	// ID is the module ID (namespace of the settings); Name the display
	// name of the tool.
	ID   string
	Name string
	// Order sorts the plugins (the first available formatter serves
	// Reformat Code).
	Order int
	// Program is the program name; the default path is vendor/bin/<Program>.
	Program string
	// FormatProgram is the formatter's program when it differs from
	// Program (a tool with separate checker and fixer); it gets its own
	// path setting.
	FormatProgram string
	// Package is the Composer package of the install recipe (dev
	// dependency of the project, run in the project interpreter).
	Package string
	// ConfigHelp explains the configuration file setting.
	ConfigHelp string
	// Format makes the plugin a formatter, Analyze an analyzer (or both).
	Format  *FormatSpec
	Analyze *AnalyzeSpec
	// Disabled makes the module opt-in (off without a stored state).
	Disabled bool
	// Import names the tool's components in the reference IDE's
	// configuration (settings import, M16); nil: none.
	Import *ImportSpec
}

// Call is the input of the argument builders. Paths are relative to the
// project root, slash separated (the tool runs there, also inside a
// container).
type Call struct {
	// File is the file to format (a temporary copy unless Stdin).
	File string
	// Paths are the files and directories to analyze ("." = the project).
	Paths []string
	// Config is the configuration file setting ("" = the tool's own
	// discovery), relative to the project when inside it.
	Config string
}

// FormatSpec describes the formatter.
type FormatSpec struct {
	// Stdin: the tool reads the content on stdin and writes the result to
	// stdout (File names the real file). Otherwise the tool formats a
	// temporary copy in place (same base name and relative path, D360).
	Stdin bool
	// Args builds the arguments (without the program).
	Args func(c Call) []string
	// OK reports whether an exit code means success (nil: 0 only).
	OK func(code int) bool
	// Unchanged reports a successful run that printed nothing because
	// nothing changed (Stdin tools; nil: never).
	Unchanged func(code int, stdout []byte) bool
}

// AnalyzeSpec describes the analyzer.
type AnalyzeSpec struct {
	// Args builds the arguments (without the program).
	Args func(c Call) []string
	// OK reports whether an exit code means a complete run (findings or
	// not); nil: 0 and 1.
	OK func(code int) bool
	// Parse reads the findings from stdout. Messages are tool level
	// remarks. Finding files may be relative to the project, absolute on
	// the host or absolute in the container.
	Parse func(stdout []byte) (findings []Finding, messages []string, err error)
	// DefaultPaths are analyzed without paths and without the paths
	// setting (nil: no paths, the tool's configuration decides). root is
	// the project root.
	DefaultPaths func(root string) []string
}

// Finding is one parsed finding. Line and Col are 1-based (0 = unknown).
type Finding struct {
	File     string
	Line     int
	Col      int
	EndLine  int
	EndCol   int
	Severity string
	Message  string
	Code     string
}

// Plugin is a quality tool module built from a Definition.
type Plugin struct {
	def Definition

	mu      sync.Mutex
	app     *api.App
	pending map[string]*time.Timer        // inspect on save: debounced per file
	running map[string]context.CancelFunc // inspect on save: running per file
	stopped bool
}

// New returns the module of def.
func New(def Definition) *Plugin { return &Plugin{def: def} }

// Definition returns the plugin's definition.
func (p *Plugin) Definition() Definition { return p.def }

// ID implements module.Module.
func (p *Plugin) ID() string { return p.def.ID }

// DependsOn implements module.Module: the run infrastructure is part of
// the kernel; without php-runtime tools run on the host.
func (p *Plugin) DependsOn() []string { return nil }

// RegisterOption returns the registration option of the plugin.
func (p *Plugin) RegisterOption() module.RegisterOption {
	if p.def.Disabled {
		return module.DefaultDisabled()
	}
	return module.DefaultEnabled()
}

// Recipe returns the installation recipe (composer require --dev in the
// project interpreter).
func (p *Plugin) Recipe() install.Recipe {
	return install.Recipe{
		Plugin: p.def.ID, Tool: p.def.Name, Manager: install.ManagerComposer, Package: p.def.Package,
		Scopes: []install.Scope{install.ScopeProject}, Interpreter: install.InterpreterProject,
		Hint: "Or install " + p.def.Name + " another way and set its path in the plugin settings.",
	}
}

// Setting keys.
func (p *Plugin) key(name string) string { return p.def.ID + "." + name }

// Setting names.
const (
	KeyPath           = "path"
	KeyFormatPath     = "formatPath"
	KeyConfig         = "config"
	KeyArgs           = "args"
	KeyUseInterpreter = "useInterpreter"
	KeyTimeout        = "timeout"
	KeySeverity       = "severity"
	KeyReformat       = "reformat"
	KeyFormatOnSave   = "formatOnSave"
	KeyInspectOnSave  = "inspectOnSave"
	KeyPaths          = "paths"
)

// DefaultTimeout is the default of the timeout setting (seconds),
// MaxTimeout its maximum.
const (
	DefaultTimeout = 60
	MaxTimeout     = 3600
)

// saveHookTimeout bounds the format-on-save filter. The hooks' default
// (2 s) would cut off formatters in containers; the tool run itself ends
// at the plugin's timeout setting (at most MaxTimeout), this only adds a
// margin for stopping it (D361).
const saveHookTimeout = MaxTimeout*time.Second + 30*time.Second

// SettingSpecs implements module.SettingsProvider.
func (p *Plugin) SettingSpecs() []settings.Spec {
	n := p.def.Name
	one, hour := 1, MaxTimeout
	specs := []settings.Spec{
		{Key: p.key(KeyPath), Kind: settings.KindPath, Default: "vendor/bin/" + p.def.Program, Scope: settings.ScopeProject, Label: n + " executable", Group: "Tool",
			Help: "Relative paths are taken from the project root (also inside the interpreter); a bare name is looked up in PATH."},
	}
	if p.def.FormatProgram != "" {
		specs = append(specs, settings.Spec{Key: p.key(KeyFormatPath), Kind: settings.KindPath, Default: "vendor/bin/" + p.def.FormatProgram,
			Scope: settings.ScopeProject, Label: "Fixer executable", Group: "Tool", Help: "The program that fixes files (Reformat Code, format on save)."})
	}
	specs = append(specs,
		settings.Spec{Key: p.key(KeyConfig), Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Configuration file", Group: "Tool",
			Help: orText(p.def.ConfigHelp, "Empty: the tool finds its configuration in the project.")},
		settings.Spec{Key: p.key(KeyArgs), Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Label: "Extra arguments", Group: "Tool",
			Help: "Added to every call of " + n + "."},
		settings.Spec{Key: p.key(KeyUseInterpreter), Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Run in the project interpreter", Group: "Tool",
			Help: "Run " + n + " with the project's PHP interpreter (local, Docker Compose, image, WSL); off runs it on the host."},
		settings.Spec{Key: p.key(KeyTimeout), Kind: settings.KindInt, Default: DefaultTimeout, Min: &one, Max: &hour, Scope: settings.ScopeProject, Label: "Timeout (seconds)", Group: "Tool",
			Help: "A run taking longer is stopped."},
	)
	if p.def.Format != nil {
		specs = append(specs,
			settings.Spec{Key: p.key(KeyReformat), Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Use for Reformat Code", Group: "Formatting",
				Help: "Reformat Code formats the whole file with " + n + " instead of the language server (the first available formatter plugin wins)."},
			settings.Spec{Key: p.key(KeyFormatOnSave), Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Format on save", Group: "Formatting",
				Help: "Format PHP files with " + n + " on explicit saves (not on automatic saves while typing)."},
		)
	}
	if p.def.Analyze != nil {
		specs = append(specs,
			settings.Spec{Key: p.key(KeyInspectOnSave), Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Inspect on save", Group: "Inspection",
				Help: "Analyze a PHP file with " + n + " after it was saved; otherwise only on demand (Inspect File / Inspect Project)."},
			settings.Spec{Key: p.key(KeySeverity), Kind: settings.KindEnum, Default: SeverityAuto, Options: []string{SeverityAuto, SevError, SevWarning, SevInfo, SevHint},
				Scope: settings.ScopeProject, Label: "Severity", Group: "Inspection", Help: "auto keeps the severity " + n + " reports; otherwise all findings get this one."},
			settings.Spec{Key: p.key(KeyPaths), Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Label: "Project paths", Group: "Inspection",
				Help: "What Inspect Project analyzes (relative to the project); empty leaves it to the tool's configuration."},
		)
	}
	return specs
}

// Start registers the plugin's hooks.
func (p *Plugin) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New(p.def.ID + ": needs an api.App context")
	}
	p.mu.Lock()
	p.app, p.stopped = app, false
	p.pending, p.running = map[string]*time.Timer{}, map[string]context.CancelFunc{}
	p.mu.Unlock()
	owner := hooks.WithOwner(p.def.ID)
	ctx.OnStop(api.HookQATools.On(ctx.Hooks(), p.def.Order, func(_ context.Context, list []api.QATool) ([]api.QATool, error) {
		return append(slices.Clone(list), p.tool()), nil
	}, owner))
	ctx.OnStop(api.HookInstallRecipes.On(ctx.Hooks(), p.def.Order, func(_ context.Context, list []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(list), p.Recipe()), nil
	}, owner))
	if p.def.Import != nil {
		ctx.OnStop(api.OnIDEComponent(ctx, p.importComponent))
	}
	if p.def.Format != nil {
		// Before the .editorconfig filter (priority 1000), which then has
		// the last word on whitespace.
		// The filter runs as long as the tool may (timeout setting), not
		// for the hooks' default limit.
		ctx.OnStop(autosave.BeforeSave.On(ctx.Hooks(), 500+p.def.Order, p.beforeSave, owner, hooks.WithTimeout(saveHookTimeout)))
	}
	if p.def.Analyze != nil {
		ctx.OnStop(ctx.Bus().Subscribe(api.TopicFileSaved, p.onSaved))
		ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectClosed, func(bus.Event) { p.cancelInspections() }))
	}
	return nil
}

// cancelInspections stops pending and running inspections on save (the
// project closed: their findings would belong to it).
func (p *Plugin) cancelInspections() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for f, t := range p.pending {
		t.Stop()
		delete(p.pending, f)
	}
	for _, c := range p.running {
		c()
	}
}

// Stop cancels pending inspections and clears the plugin's findings.
func (p *Plugin) Stop() error {
	p.mu.Lock()
	p.stopped = true
	for _, t := range p.pending {
		t.Stop()
	}
	for _, c := range p.running {
		c()
	}
	p.pending, p.running = nil, nil
	app := p.app
	p.mu.Unlock()
	if app != nil {
		app.ClearQA(p.def.ID, "")
	}
	return nil
}

// tool is the contribution to the qa service.
func (p *Plugin) tool() api.QATool {
	t := api.QATool{Plugin: p.def.ID, Name: p.def.Name, Order: p.def.Order, Status: p.Status}
	if p.def.Format != nil {
		t.Formatter, t.Format = true, p.format
	}
	if p.def.Analyze != nil {
		t.Analyzer, t.Analyze = true, p.analyze
	}
	return t
}

// Status reports whether the tool can run (see api.QAToolStatus).
func (p *Plugin) Status(_ context.Context) api.QAToolStatus {
	st := api.QAToolStatus{
		Reformat:      p.def.Format != nil && p.boolSetting(KeyReformat, true),
		FormatOnSave:  p.def.Format != nil && p.boolSetting(KeyFormatOnSave, false),
		InspectOnSave: p.def.Analyze != nil && p.boolSetting(KeyInspectOnSave, false),
	}
	if p.useInterpreter() {
		st.Where = "project interpreter"
	} else {
		st.Where = "host"
	}
	keys := []string{KeyPath}
	if p.def.FormatProgram != "" {
		keys = append(keys, KeyFormatPath)
	}
	for i, k := range keys {
		r := p.resolve(k)
		if i == 0 {
			st.Command = r.Display
		}
		if !r.Found {
			st.Command, st.Reason = r.Display, r.Reason
			return st
		}
	}
	st.Available = true
	return st
}

// resolved is a resolved program setting.
type resolved struct {
	// Program is what is run (relative to the project root, absolute, or
	// a bare name).
	Program string
	Display string
	Found   bool
	Reason  string
}

// resolve resolves the program setting key (KeyPath or KeyFormatPath).
func (p *Plugin) resolve(key string) resolved {
	prog := p.def.Program
	if key == KeyFormatPath && p.def.FormatProgram != "" {
		prog = p.def.FormatProgram
	}
	v := strings.TrimSpace(p.stringSetting(key, ""))
	if v == "" {
		v = "vendor/bin/" + prog
	}
	root := p.root()
	r := resolved{Program: v, Display: v}
	switch {
	case !strings.ContainsAny(v, `/\`):
		// A bare name: PATH of the host or of the interpreter.
		if p.useInterpreter() {
			r.Found = true
			return r
		}
		if found, err := lookPath(v, p.pathEnv()); err == nil {
			r.Program, r.Found = found, true
			return r
		}
		r.Reason = v + " not found in PATH"
		return r
	case filepath.IsAbs(v) || (len(v) > 0 && (v[0] == '/' || v[0] == '\\')):
		if root != "" && within(root, v) {
			rel, _ := filepath.Rel(root, v)
			if !p.useInterpreter() {
				r.Program = v
			} else {
				r.Program = filepath.ToSlash(rel)
			}
			r.Found = isFile(v)
			if !r.Found {
				r.Reason = v + " does not exist"
			}
			return r
		}
		if isFile(v) {
			r.Found = true
			return r
		}
		if p.useInterpreter() {
			// Possibly a path inside the container: not checkable here.
			r.Found = true
			return r
		}
		r.Reason = v + " does not exist"
		return r
	default:
		if root == "" {
			r.Reason = "no project open"
			return r
		}
		host := filepath.Join(root, filepath.FromSlash(v))
		r.Program = filepath.ToSlash(v)
		if !p.useInterpreter() {
			r.Program = host
		}
		r.Found = isFile(host)
		if !r.Found {
			r.Reason = v + " not found in the project (install " + p.def.Name + " or set its path)"
		}
		return r
	}
}

// root returns the project root ("" without project).
func (p *Plugin) root() string {
	app := p.getApp()
	if app == nil {
		return ""
	}
	return app.ProjectRoot()
}

func (p *Plugin) getApp() *api.App {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.app
}

func (p *Plugin) pathEnv() string {
	if app := p.getApp(); app != nil {
		if pe := app.InstallEnv().PathEnv; pe != "" {
			return pe
		}
	}
	return os.Getenv("PATH")
}

func (p *Plugin) useInterpreter() bool { return p.boolSetting(KeyUseInterpreter, true) }

func (p *Plugin) timeout() time.Duration {
	n := lookup(p.getApp(), p.key(KeyTimeout), float64(DefaultTimeout))
	if n < 1 {
		n = DefaultTimeout
	}
	return time.Duration(n * float64(time.Second))
}

func (p *Plugin) boolSetting(name string, def bool) bool {
	return lookup(p.getApp(), p.key(name), def)
}

func (p *Plugin) stringSetting(name, def string) string {
	return lookup(p.getApp(), p.key(name), def)
}

func (p *Plugin) listSetting(name string) []string {
	v, _ := lookupOK[[]string](p.getApp(), p.key(name))
	out := make([]string, 0, len(v))
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// config returns the configuration file for the tool: relative to the
// project when inside it.
func (p *Plugin) config() string {
	c := strings.TrimSpace(p.stringSetting(KeyConfig, ""))
	if c == "" {
		return ""
	}
	if root := p.root(); root != "" && filepath.IsAbs(c) && within(root, c) {
		rel, _ := filepath.Rel(root, c)
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(c)
}

func lookupOK[T any](a *api.App, key string) (T, bool) {
	var zero T
	if a == nil {
		return zero, false
	}
	if ps := a.ProjectSettings(); ps != nil {
		if v, ok, err := settings.Lookup[T](ps, key); ok && err == nil {
			return v, true
		}
	}
	if v, ok, err := settings.Lookup[T](a.Settings(), key); ok && err == nil {
		return v, true
	}
	return zero, false
}

func lookup[T any](a *api.App, key string, def T) T {
	if v, ok := lookupOK[T](a, key); ok {
		return v
	}
	return def
}

// beforeSave formats PHP files on explicit saves (format on save). A
// failing formatter never blocks the save: the content stays as it is and
// the error is reported as qa.finished.
func (p *Plugin) beforeSave(ctx context.Context, ev autosave.SaveEvent) (autosave.SaveEvent, error) {
	if !formatTrigger(ev.Trigger) || !IsPHP(ev.Path) || !p.boolSetting(KeyFormatOnSave, false) {
		return ev, nil
	}
	app := p.getApp()
	root := p.root()
	if app == nil || root == "" || !within(root, ev.Path) || len(ev.Content) == 0 {
		return ev, nil
	}
	if !p.Status(ctx).Available {
		return ev, nil
	}
	disp := []string{app.DisplayPath(ev.Path)}
	app.Bus().Publish(api.TopicQAStarted, api.QARunEvent{Tool: p.def.ID, Kind: api.QAKindFormat, Paths: disp, Trigger: "save"})
	start := time.Now()
	out, err := p.format(ctx, api.QAFormatRequest{File: ev.Path, Content: ev.Content})
	done := api.QARunEvent{Tool: p.def.ID, Kind: api.QAKindFormat, Paths: disp, Trigger: "save", Millis: time.Since(start).Milliseconds()}
	if err != nil {
		done.Error = err.Error()
		app.Logger().Warn(p.def.ID+": format on save", "path", ev.Path, "err", err)
	} else {
		done.Count = len(api.MinimalChanges(ev.Content, out))
		ev.Content = out
	}
	app.Bus().Publish(api.TopicQAFinished, done)
	return ev, nil
}

// formatTrigger reports whether a save with trigger t formats: explicit
// saves, before a commit and on close; automatic saves while the user may
// still type (idle, focus lost, before run/debug) do not.
func formatTrigger(t autosave.Trigger) bool {
	switch t {
	case autosave.TriggerManual, autosave.TriggerBeforeCommit, autosave.TriggerClose:
		return true
	}
	return false
}

// inspectDelay debounces inspections on save per file.
const inspectDelay = 300 * time.Millisecond

// onSaved schedules an inspection of a saved PHP file.
func (p *Plugin) onSaved(e bus.Event) {
	ev, ok := e.Payload.(api.FileSavedEvent)
	if !ok || !IsPHP(ev.File) || !p.boolSetting(KeyInspectOnSave, false) {
		return
	}
	root := p.root()
	if root == "" || !within(root, ev.File) {
		return
	}
	file := ev.File
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.pending == nil {
		return
	}
	if t := p.pending[file]; t != nil {
		t.Stop()
	}
	p.pending[file] = time.AfterFunc(inspectDelay, func() { p.inspectSaved(file) })
}

func (p *Plugin) inspectSaved(file string) {
	p.mu.Lock()
	if p.stopped || p.running == nil {
		p.mu.Unlock()
		return
	}
	delete(p.pending, file)
	if c := p.running[file]; c != nil {
		c()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.running[file] = cancel
	app := p.app
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		if p.running != nil {
			delete(p.running, file)
		}
		p.mu.Unlock()
	}()
	if app == nil || !p.Status(ctx).Available {
		return
	}
	if _, err := app.QARun(ctx, api.QARunParams{Tool: p.def.ID, Paths: []string{file}, Trigger: "save"}); err != nil && ctx.Err() == nil {
		app.Logger().Warn(p.def.ID+": inspect on save", "path", file, "err", err)
	}
}

// IsPHP reports whether path is a PHP source file.
func IsPHP(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".phtml", ".inc":
		return true
	}
	return false
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// lookPath finds name in the directories of pathEnv.
func lookPath(name, pathEnv string) (string, error) {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".bat", ".cmd", ""}
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" {
			continue
		}
		for _, e := range exts {
			c := filepath.Join(d, name+e)
			if st, err := os.Stat(c); err == nil && !st.IsDir() && (runtime.GOOS == "windows" || st.Mode()&0o111 != 0) {
				return c, nil
			}
		}
	}
	return "", errors.New(name + " not found in PATH")
}
