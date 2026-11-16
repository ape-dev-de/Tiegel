package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Version is the API version reported by "app.info". It changes when methods
// change incompatibly.
const Version = "0.1"

// Options configures an App. The zero value is usable; tests set ConfigDir
// and the discovery options so they never touch the real user directory or
// the machine's PATH.
type Options struct {
	// ConfigDir is the base directory for settings and per-project data.
	// Empty means settings.DefaultBaseDir() (<UserConfigDir>/Tiegel).
	ConfigDir string
	// Logger receives diagnostics; nil discards them.
	Logger *slog.Logger
	// Interp controls local interpreter discovery for "interp.list".
	Interp interp.DiscoverOptions
	// LSP controls language server detection for "lsp.servers".
	// ProjectDir is filled in from the open project.
	LSP servers.DetectOptions
	// ComposeEnv takes precedence over the project's .env when loading
	// compose files (like the shell environment does for docker compose).
	ComposeEnv map[string]string
	// GOOS is the host OS for Xdebug hints; empty means runtime.GOOS.
	GOOS string
	// AppVersion is the Tiegel build version reported by "app.info".
	AppVersion string
	// Extensions register additional namespaces after the built-in ones
	// (see the package documentation).
	Extensions []func(a *App) error
}

// App is the headless core of Tiegel: it owns the kernel services (bus,
// hooks, modules, settings, history) and serves the API methods. It is safe
// for concurrent use.
type App struct {
	opts    Options
	log     *slog.Logger
	bus     *bus.Bus
	hooks   *hooks.Registry
	global  *settings.Store
	modules *module.Registry
	saver   *autosave.Manager
	methods *registry

	initErrs []error

	mu      sync.Mutex
	project *project
	docs    map[string]*document
	interps map[string]interp.Interpreter
	debug   *debugState
}

// New creates an App with the built-in namespaces and the extensions from
// opts registered.
func New(opts Options) (*App, error) {
	if opts.ConfigDir == "" {
		dir, err := settings.DefaultBaseDir()
		if err != nil {
			return nil, fmt.Errorf("api: config dir: %w", err)
		}
		opts.ConfigDir = dir
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	b := bus.New()
	a := &App{
		opts:    opts,
		log:     log,
		bus:     b,
		methods: newRegistry(),
		docs:    make(map[string]*document),
		interps: make(map[string]interp.Interpreter),
	}
	a.hooks = hooks.New(hooks.WithErrorHandler(func(e *hooks.HookError) {
		log.Warn("hook failed", "hook", e.Hook, "owner", e.Owner, "err", e.Err)
	}))
	global, err := settings.OpenGlobal(opts.ConfigDir, b)
	if err != nil {
		return nil, fmt.Errorf("api: global settings: %w", err)
	}
	a.global = global
	a.modules = module.NewRegistry(module.Env{Bus: b, Hooks: a.hooks, Settings: global, Logger: log})
	saver, err := autosave.New(autosave.Config{
		Docs:    docSet{a},
		Hooks:   a.hooks,
		History: historySink{a},
		OnError: func(err error) { log.Warn("save", "err", err) },
	})
	if err != nil {
		return nil, fmt.Errorf("api: autosave: %w", err)
	}
	a.saver = saver

	a.registerApp()
	a.registerProject()
	a.registerDocs()
	a.registerSearch()
	a.registerInterp()
	a.registerCompose()
	a.registerLSP()
	a.registerDebug()
	a.registerModules()
	for _, ext := range opts.Extensions {
		if err := ext(a); err != nil {
			a.initErrs = append(a.initErrs, err)
		}
	}
	if err := errors.Join(a.initErrs...); err != nil {
		return nil, err
	}
	return a, nil
}

// Close stops the debugger listener and module registry, closes the project
// and shuts the bus down. Unsaved documents are discarded.
func (a *App) Close() error {
	var errs []error
	a.saver.Close()
	if err := a.stopDebug(); err != nil {
		errs = append(errs, err)
	}
	if err := a.modules.Stop(); err != nil {
		errs = append(errs, err)
	}
	a.mu.Lock()
	a.project = nil
	a.docs = make(map[string]*document)
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.bus.Close(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Bus returns the event bus. Events published on it are forwarded to
// subscribed clients.
func (a *App) Bus() *bus.Bus { return a.bus }

// Hooks returns the hook registry.
func (a *App) Hooks() *hooks.Registry { return a.hooks }

// Settings returns the global settings store.
func (a *App) Settings() *settings.Store { return a.global }

// Modules returns the module registry; compiled-in modules are registered
// here by the application before Start.
func (a *App) Modules() *module.Registry { return a.modules }

// Logger returns the App's logger.
func (a *App) Logger() *slog.Logger { return a.log }

// ConfigDir returns the base directory for settings and project data.
func (a *App) ConfigDir() string { return a.opts.ConfigDir }

// ProjectRoot returns the absolute root of the open project, or "".
func (a *App) ProjectRoot() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.project == nil {
		return ""
	}
	return a.project.root
}

// ProjectSettings returns the settings store of the open project, or nil.
func (a *App) ProjectSettings() *settings.Store {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.project == nil {
		return nil
	}
	return a.project.settings
}

// AppInfo is the result of "app.info".
type AppInfo struct {
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
	ConfigDir  string `json:"configDir"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	Project    string `json:"project,omitempty"`
	Methods    int    `json:"methods"`
	PID        int    `json:"pid"`
}

func (a *App) registerApp() {
	mustHandle(a, "app.info", "Version, config directory and open project", func(context.Context, struct{}) (AppInfo, error) {
		return AppInfo{
			Version:    a.opts.AppVersion,
			APIVersion: Version,
			ConfigDir:  a.opts.ConfigDir,
			GOOS:       a.opts.GOOS,
			GOARCH:     runtime.GOARCH,
			Project:    a.ProjectRoot(),
			Methods:    len(a.methods.list()),
			PID:        os.Getpid(),
		}, nil
	})
	mustHandle(a, "app.methods", "List all API methods", func(context.Context, struct{}) ([]MethodInfo, error) {
		return a.Methods(), nil
	})
	mustHandle(a, "events.subscribe", "Forward bus topics (patterns like \"doc.*\" or \"*\") as notifications", a.eventsSubscribe)
	mustHandle(a, "events.unsubscribe", "Stop forwarding topics (none given: all)", a.eventsUnsubscribe)
	mustHandle(a, "events.publish", "Publish an event on the bus (for scripting and tests)", a.eventsPublish)
}
