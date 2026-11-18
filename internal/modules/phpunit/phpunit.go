// Package phpunit is the PHPUnit plugin (AP15.2–15.4): it finds PHPUnit
// in the project (vendor/bin/phpunit, the XML configuration, ParaTest),
// discovers tests with Tree-sitter (classes with a TestCase ancestor,
// test* methods, #[Test], @test, data providers), runs them with
// --teamcity through the kernel's run infrastructure (so the project
// interpreter – local, Compose, Docker image, WSL – and its path mappings
// apply) and turns the streamed service messages into a result tree.
// Go to Test / Create Test use the PSR-4 maps of composer.json.
//
// API (namespace test.*, see api.TestRun and friends): test.status,
// test.discover, test.run, test.rerunFailed, test.results, test.runs,
// test.lastStatus, test.goto, test.create.
// Events: test.started, test.updated, test.finished.
// Hooks provided: run.configTypes (type "phpunit"), install.recipes
// (composer require --dev phpunit/phpunit in the project interpreter;
// only on user action, D194).
// Settings: phpunit.executable, phpunit.configuration,
// phpunit.interpreter, phpunit.parallel, phpunit.arguments.
//
// The plugin talks to the kernel only (run.*, hooks, bus); the PHP index
// (nav.typeHierarchy) is asked through the API when it runs.
package phpunit

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// ID is the module ID (also the run configuration type).
const ID = "phpunit"

// Framework is the display name.
const Framework = "PHPUnit"

// Settings keys (D190: namespace = module ID).
const (
	SettingExecutable = ID + ".executable"
	SettingConfig     = ID + ".configuration"
	SettingInterp     = ID + ".interpreter"
	SettingParallel   = ID + ".parallel"
	SettingArgs       = ID + ".arguments"
)

// Options configure the module.
type Options struct {
	// PublishInterval throttles test.updated (default 150 ms).
	PublishInterval time.Duration
	// MaxRuns is the number of test runs kept (default 20).
	MaxRuns int
}

// Module is the PHPUnit plugin.
type Module struct {
	opts Options

	mu       sync.Mutex
	app      *api.App
	sessions map[string]*session
	order    []string // run IDs, oldest first
	last     map[string]teamcity.Status
}

// New creates the module.
func New(opts Options) *Module {
	if opts.PublishInterval <= 0 {
		opts.PublishInterval = 150 * time.Millisecond
	}
	if opts.MaxRuns <= 0 {
		opts.MaxRuns = 20
	}
	return &Module{opts: opts, sessions: map[string]*session{}, last: map[string]teamcity.Status{}}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module: the interpreter comes through the
// run infrastructure, the index through the API (both optional).
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"test.*"} }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingExecutable, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "PHPUnit script",
			Help: "Path of the phpunit script (project relative or absolute); empty uses vendor/bin/phpunit. It runs with php of the interpreter."},
		{Key: SettingConfig, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Configuration file",
			Help: "phpunit.xml to use; empty finds phpunit.xml, phpunit.xml.dist or phpunit.dist.xml in the project root."},
		{Key: SettingInterp, Kind: settings.KindString, Default: run.InterpreterProject, Scope: settings.ScopeProject, Label: "Interpreter",
			Help: "project (the project's interpreter, e.g. a Compose service), host or an interpreter ID."},
		{Key: SettingParallel, Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Run suites with ParaTest",
			Help: "Run whole suites and directories with vendor/bin/paratest when it is installed (single classes and methods always use PHPUnit)."},
		{Key: SettingArgs, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Additional arguments",
			Help: "Extra PHPUnit options for every run, e.g. --stop-on-failure; quote with ' or \"."},
	}
}

// Recipe is the installation recipe: PHPUnit as dev dependency of the
// project, installed by Composer in the project interpreter.
func Recipe() install.Recipe {
	return install.Recipe{
		Plugin: ID, Tool: Framework, Manager: install.ManagerComposer, Package: "phpunit/phpunit",
		Scopes: []install.Scope{install.ScopeProject}, Interpreter: install.InterpreterProject,
		Hint: "Alternatively run composer require --dev phpunit/phpunit yourself.",
	}
}

// Start registers the API, the run configuration type, the install
// recipe and the run event subscription.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("phpunit: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.mu.Unlock()

	p := api.NewProvider(ctx)
	api.Add(p, "test.status", "Test framework of the project: runner script, configuration file, suites, version; hint and install recipe when missing", m.status)
	api.Add(p, "test.discover", "Test classes and methods of a file, a directory or the test directories (Tree-sitter), with the last result", m.discover)
	api.Add(p, "test.run", "Run tests (suite, path, class, method, data set, filter or a configuration) with streamed results; debug: under the debugger", m.runTests)
	api.Add(p, "test.rerunFailed", "Run the failed tests of a test run again (default: the latest)", m.rerunFailed)
	api.Add(p, "test.results", "Result tree of a test run (default: the latest)", m.results)
	api.Add(p, "test.runs", "Test runs of this session with counts (newest last)", m.runs)
	api.Add(p, "test.lastStatus", "Last result per test ID (class, method, data set)", m.lastStatus)
	api.Add(p, "test.configFor", "An unsaved run configuration of the test type for a scope (to edit and save with run.configs.save)", m.configFor)
	api.Add(p, "test.goto", "Go to Test: the tests of a class or the class under test (PSR-4 of composer.json, convention Tests\\…Test)", m.gotoTest)
	api.Add(p, "test.create", "Create Test: write a test skeleton for a class (dryRun: only the proposal)", m.createTest)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(api.HookRunConfigTypes.On(ctx.Hooks(), 100, func(_ context.Context, l []run.ConfigType) ([]run.ConfigType, error) {
		return append(slices.Clone(l), m.configType()), nil
	}, hooks.WithOwner(ID)))
	ctx.OnStop(api.HookInstallRecipes.On(ctx.Hooks(), 100, func(_ context.Context, l []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(l), Recipe()), nil
	}, hooks.WithOwner(ID)))
	ctx.OnStop(ctx.Bus().Subscribe("run.*", m.onRun))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectOpened, func(bus.Event) { m.reset() }))
	return nil
}

// Stop implements module.Module.
func (m *Module) Stop() error {
	m.mu.Lock()
	for _, s := range m.sessions {
		s.stopTimer()
	}
	m.mu.Unlock()
	return nil
}

// reset forgets the results of the previous project.
func (m *Module) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.stopTimer()
	}
	m.sessions, m.order, m.last = map[string]*session{}, nil, map[string]teamcity.Status{}
}

// setting reads a setting of the module (project before global).
func setting[T any](m *Module, key string, def T) T {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app == nil {
		return def
	}
	return api.Setting(app, key, def)
}
