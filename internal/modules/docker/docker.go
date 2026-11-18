// Package docker is the docker module (plugin, D190): it finds the docker
// CLI, lists Docker contexts (OrbStack, Docker Desktop, Colima, …), keeps
// the chosen context (global default, per-project override) and checks
// whether an engine is reachable – the latter only when asked.
//
// API: docker.contexts, docker.status, docker.setContext, docker.setPath;
// Services window: docker.services, docker.serviceAction, docker.logs,
// docker.logsStop, docker.watchStart, docker.watchStop, docker.shell,
// docker.openURL.
// Events: docker.contextChanged, docker.log, docker.servicesChanged.
// Hooks provided: docker.endpoint (binary, context and PATH for running
// docker), used by php-runtime for compose and image interpreters.
// Settings: docker.path, docker.context, docker.timeoutSeconds,
// docker.servicesRefreshSeconds, docker.terminal.
package docker

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
)

// ID is the module ID.
const ID = "docker"

// Settings keys (D190: namespace = module ID).
const (
	// SettingPath is the docker binary (global).
	SettingPath = "docker.path"
	// SettingContext is the Docker context; a project value overrides the
	// global default, "" means docker's current context.
	SettingContext = "docker.context"
	// SettingTimeout bounds the reachability check in seconds (global).
	SettingTimeout = "docker.timeoutSeconds"
)

// Options configure the module.
type Options struct {
	// Binary is used when docker.path is not set, instead of searching
	// (tests pass a fake).
	Binary string
	// LoginPath returns the login shell PATH; nil means
	// shellenv.Default().LoginPath unless NoLoginShell is set.
	LoginPath func(ctx context.Context) (string, error)
	// NoLoginShell never asks the login shell (tests, CI).
	NoLoginShell bool
	// CommonDirs replaces the well-known install directories (nil:
	// docker.CommonDirs).
	CommonDirs []string
	// Env are extra KEY=VALUE entries for every docker run (tests).
	Env []string
	// Getenv reads DOCKER_HOST/DOCKER_CONTEXT; nil means os.Getenv.
	Getenv func(string) string
	// GOOS overrides the platform for terminals and browsers (tests).
	GOOS string
	// Launch starts external programs (terminal, browser) without
	// waiting; nil starts them for real (tests record instead).
	Launch func(argv, env []string) error
	// LookPath finds terminal programs; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// TempDir receives the macOS terminal launcher scripts; empty means
	// os.TempDir.
	TempDir string
}

// Module is the docker module.
type Module struct {
	opts Options

	mu  sync.Mutex
	app *api.App
	// Binary cache, valid while the docker.path value equals binKey.
	binKey   string
	binValid bool
	bin      dk.Binary
	binErr   error
	login    string
	last     string // last effective context, for docker.contextChanged

	sv *servicesState // Services window (services.go)
}

// New creates the module.
func New(opts Options) *Module { return &Module{opts: opts, sv: newServicesState()} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"docker.*"} }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return append([]settings.Spec{
		{Key: SettingPath, Kind: settings.KindPath, Scope: settings.ScopeGlobal, Label: "Docker binary",
			Help: "Path to the docker CLI; empty searches PATH, the login shell PATH and the usual install directories (Homebrew, OrbStack, Docker Desktop)."},
		{Key: SettingContext, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Docker context",
			Help: "Context for compose and image interpreters (e.g. orbstack, desktop-linux); empty uses docker's current context."},
		{Key: SettingTimeout, Kind: settings.KindInt, Default: 5, Scope: settings.ScopeGlobal, Label: "Connection check timeout (seconds)",
			Min: settings.IntPtr(1), Max: settings.IntPtr(120)},
	}, serviceSpecs()...)
}

// Start registers the API and the docker.endpoint hook.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("docker: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.binValid = false
	m.mu.Unlock()
	m.sv.mu.Lock()
	m.sv.closing, m.sv.stopped = false, false
	m.sv.mu.Unlock()
	cur, _ := m.selectedContext()
	m.mu.Lock()
	m.last = cur
	m.mu.Unlock()

	p := api.NewProvider(ctx)
	api.Add(p, "docker.contexts", "Docker contexts with endpoint and engine (OrbStack, Docker Desktop, …); local only", m.contexts)
	api.Add(p, "docker.status", "Docker binary, effective context and whether its engine answers (contacts the engine)", m.status)
	api.Add(p, "docker.setContext", "Choose the Docker context for the project (or globally); clear restores the default", m.setContext)
	api.Add(p, "docker.setPath", "Set or clear the docker binary path (docker.path)", m.setPath)
	m.registerServices(p)
	if err := p.Err(); err != nil {
		return err
	}
	api.HookDockerEndpoint.On(ctx.Hooks(), 0, func(c context.Context, q api.DockerEndpointQuery) (api.DockerEndpointQuery, error) {
		if q.Binary != "" {
			return q, nil
		}
		return m.endpoint(c), nil
	}, hooks.WithOwner(ID), hooks.WithTimeout(10*time.Second))
	api.HookTerminalLaunch.On(ctx.Hooks(), 0, func(c context.Context, q api.TerminalLaunch) (api.TerminalLaunch, error) {
		if q.Program != "" {
			return q, nil
		}
		return m.terminalLaunch(c, q), nil
	}, hooks.WithOwner(ID), hooks.WithTimeout(10*time.Second))

	ctx.OnStop(settings.Changed.Subscribe(ctx.Bus(), func(ch settings.Change) {
		if !strings.HasPrefix(ch.Key, "docker.") {
			return
		}
		m.mu.Lock()
		m.binValid = false
		m.mu.Unlock()
		if ch.Key == SettingContext || ch.Key == SettingPath {
			m.announce(string(ch.Scope))
		}
		if ch.Key == SettingContext || ch.Key == SettingPath || ch.Key == SettingRefresh {
			// Followed logs belong to the old engine; a watch restarts
			// with the new context or interval.
			if ch.Key != SettingRefresh {
				m.stopStaleLogs()
			}
			go m.restartWatch()
		}
	}))
	reset := func(bus.Event) {
		cur, _ := m.selectedContext()
		m.mu.Lock()
		m.last = cur
		m.mu.Unlock()
		m.stopStaleLogs()
		go m.restartWatch()
	}
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectOpened, reset))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectClosed, reset))
	return nil
}

// Stop implements module.Module: it ends followed logs and watches.
func (m *Module) Stop() error {
	m.shutdownLogs()
	m.stopWatch()
	return nil
}

// announce publishes docker.contextChanged with the effective context.
func (m *Module) announce(scope string) {
	cur, _ := m.selectedContext()
	m.mu.Lock()
	prev := m.last
	m.last = cur
	app := m.app
	m.mu.Unlock()
	if app != nil {
		app.Bus().Publish(api.TopicDockerContextChanged, api.DockerContextChangedEvent{Context: cur, Scope: scope, Previous: prev})
	}
}

func (m *Module) getenv(k string) string {
	if m.opts.Getenv != nil {
		return m.opts.Getenv(k)
	}
	return os.Getenv(k)
}

func (m *Module) globalStore() *settings.Store { return m.app.Settings() }

// selectedContext returns the configured context and where it comes from:
// "project", "global" or "docker" (nothing configured: docker's current
// context, DOCKER_CONTEXT or DOCKER_HOST).
func (m *Module) selectedContext() (string, string) {
	if m.app == nil {
		return "", "docker"
	}
	if ps := m.app.ProjectSettings(); ps != nil {
		if v, ok, err := settings.Lookup[string](ps, SettingContext); ok && err == nil {
			if v != "" {
				return v, "project"
			}
		}
	}
	if g := m.globalStore(); g != nil {
		if v := settings.Get(g, SettingContext, ""); v != "" {
			return v, "global"
		}
	}
	return "", "docker"
}

func (m *Module) timeout() time.Duration {
	secs := 5
	if g := m.globalStore(); g != nil {
		secs = settings.Get(g, SettingTimeout, 5)
	}
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

// binary finds (and caches) the docker binary.
func (m *Module) binary(ctx context.Context) (dk.Binary, error) {
	configured := ""
	if g := m.globalStore(); g != nil {
		configured = settings.Get(g, SettingPath, "")
	}
	m.mu.Lock()
	if m.binValid && m.binKey == configured {
		b, err := m.bin, m.binErr
		m.mu.Unlock()
		return b, err
	}
	m.mu.Unlock()

	o := dk.FindOptions{Configured: configured, CommonDirs: m.opts.CommonDirs}
	if configured == "" && m.opts.Binary != "" {
		o.Configured = m.opts.Binary
	}
	var login string
	if !m.opts.NoLoginShell {
		lp := m.opts.LoginPath
		if lp == nil {
			lp = shellenv.Default().LoginPath
		}
		o.LoginPath = func(c context.Context) (string, error) {
			p, err := lp(c)
			login = p
			return p, err
		}
	}
	b, err := dk.Find(ctx, o)
	if err == nil && configured == "" && m.opts.Binary != "" {
		b.Source = "option"
	}
	m.mu.Lock()
	m.binKey, m.binValid, m.bin, m.binErr = configured, true, b, err
	if login != "" {
		m.login = login
	}
	m.mu.Unlock()
	return b, err
}

// client returns the docker client for the effective context (name ""
// means: use the configured one).
func (m *Module) client(ctx context.Context, name string) (dk.Client, dk.Binary, error) {
	b, err := m.binary(ctx)
	if err != nil {
		return dk.Client{}, b, err
	}
	if name == "" {
		name, _ = m.selectedContext()
	}
	m.mu.Lock()
	login := m.login
	m.mu.Unlock()
	env := append([]string{dk.EnvPath(b.Path, os.Getenv("PATH"), login)}, m.opts.Env...)
	return dk.Client{Binary: b.Path, Context: name, Env: env}, b, nil
}

// endpoint answers the docker.endpoint hook. Without a binary it returns
// only the context (callers then use "docker" from PATH).
func (m *Module) endpoint(ctx context.Context) api.DockerEndpointQuery {
	c, _, err := m.client(ctx, "")
	if err != nil {
		name, _ := m.selectedContext()
		return api.DockerEndpointQuery{Context: name}
	}
	return api.DockerEndpointQuery{Binary: c.Binary, Context: c.Context, Env: c.Env}
}
