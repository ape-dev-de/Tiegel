// Package langphp is the lang-php module: it runs a PHP language server
// (Phpactor by default), keeps it in sync with the open documents and
// exposes completion, hover, signature help, definitions, implementations,
// semantic tokens and diagnostics through the lsp.* namespace.
//
// Document sync is driven by the bus, so edits from every client (API
// handlers, the in-process GUI editing the shared buffer) reach the server:
// doc.opened → didOpen, doc.edited → incremental didChange (the event
// carries the UTF-16 range), doc.closed → didClose, file.saved → didSave.
// Before every request the module makes sure the server has the current
// document version.
//
// The PHP version comes from the php.resolveVersion hook (php-runtime, an
// optional dependency); the server restarts on interpreter.changed and
// php.versionChanged.
package langphp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// ID is the module ID.
const ID = "lang-php"

// Settings keys (project settings, global without a project).
const (
	// SettingServer is the preferred server ID ("phpactor", "phpantom",
	// "intelephense").
	SettingServer = "lsp.server"
	// SettingCommand is a custom server executable (overrides detection).
	SettingCommand = "lsp.command"
	// SettingArgs are the arguments of SettingCommand ([]string).
	SettingArgs = "lsp.args"
)

// languageID is the LSP language of synced documents.
const languageID = "php"

// shutdownTimeout bounds the shutdown handshake.
const shutdownTimeout = 5 * time.Second

// Options configure the module.
type Options struct {
	// Connect starts and initializes a server; nil means lsp.Start (child
	// process). Tests connect to an in-process fake server.
	Connect func(ctx context.Context, cfg lsp.Config) (*lsp.Client, error)
}

// Module is the lang-php module.
type Module struct {
	opts Options
	app  *api.App

	// mu guards the server lifecycle and the state below.
	mu   sync.Mutex
	srv  *server
	gen  int
	last StartParams // last start request (for restarts)

	// diagMu guards diags; it is separate from mu because diagnostics
	// arrive on the client's goroutine while mu may wait for a shutdown.
	diagMu sync.Mutex
	diags  map[string]Diagnostics // by absolute path

	// syncMu serialises document sync (bus events vs. requests).
	syncMu sync.Mutex
}

// server is a running language server.
type server struct {
	client     *lsp.Client
	id         string
	name       string
	command    string
	args       []string
	phpVersion string
	gen        int
	closed     atomic.Bool
}

// New creates the module.
func New(opts Options) *Module { return &Module{opts: opts} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module. php-runtime is optional: the PHP
// version comes through the php.resolveVersion hook when it runs.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"lsp.*"} }

// Start registers the lsp namespace and the bus subscriptions.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("lang-php: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.mu.Unlock()
	m.clearDiags()
	p := api.NewProvider(ctx)
	api.Add(p, "lsp.servers", "Installed PHP language servers (no downloads)", m.lspServers)
	api.Add(p, "lsp.known", "Supported PHP language servers", m.lspKnown)
	api.Add(p, "lsp.start", "Start the language server (detected, from settings or a command)", m.lspStart)
	api.Add(p, "lsp.stop", "Stop the language server", m.lspStop)
	api.Add(p, "lsp.status", "Language server state", m.lspStatus)
	api.Add(p, "lsp.diagnostics", "Latest server diagnostics of a document (or of all)", m.lspDiagnostics)
	api.Add(p, "lsp.completion", "Completion items at a position", m.lspCompletion)
	api.Add(p, "lsp.hover", "Hover information at a position", m.lspHover)
	api.Add(p, "lsp.signature", "Signature help at a position", m.lspSignature)
	api.Add(p, "lsp.definition", "Definition locations of the symbol at a position", m.lspDefinition)
	api.Add(p, "lsp.implementation", "Implementations of the symbol at a position", m.lspImplementation)
	api.Add(p, "lsp.semanticTokens", "Semantic tokens of a document", m.lspSemanticTokens)
	if err := p.Err(); err != nil {
		return err
	}
	b := ctx.Bus()
	// One subscription for all doc.* events keeps them in publish order.
	ctx.OnStop(b.Subscribe("doc.*", m.onDoc))
	ctx.OnStop(b.Subscribe(api.TopicFileSaved, m.onSaved))
	ctx.OnStop(b.Subscribe(api.TopicFilesChanged, m.onFilesChanged))
	restart := func(bus.Event) { go m.restart() }
	ctx.OnStop(b.Subscribe(api.TopicInterpreterChanged, restart))
	ctx.OnStop(b.Subscribe(api.TopicPHPVersionChanged, restart))
	api.HookNavLSP.On(ctx.Hooks(), 0, m.navLSP, hooks.WithOwner(ID), hooks.WithTimeout(navTimeout))
	return nil
}

// Stop shuts the server down.
func (m *Module) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return nil
}

func (m *Module) current() *server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv
}

func (m *Module) store() *settings.Store { return m.app.SettingsStore() }

// StartParams are the params of "lsp.start". Without parameters the server
// comes from the settings (lsp.server, lsp.command, lsp.args) or detection
// (Phpactor preferred).
type StartParams struct {
	// Server is a server ID (phpactor, phpantom, intelephense).
	Server string `json:"server,omitempty"`
	// Command is a custom server executable; Args are its arguments.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// Status is the result of "lsp.start", "lsp.stop", "lsp.status" and the
// payload of the lsp.status event.
type Status struct {
	Running    bool     `json:"running"`
	Server     string   `json:"server,omitempty"`
	Name       string   `json:"name,omitempty"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	PHPVersion string   `json:"phpVersion,omitempty"`
	OpenDocs   int      `json:"openDocs"`
	// ServerInfo is the name/version the server reports.
	ServerInfo string `json:"serverInfo,omitempty"`
	// Generation counts server starts (restarts increase it).
	Generation int    `json:"generation"`
	Error      string `json:"error,omitempty"`
}

func (m *Module) statusLocked() Status {
	st := Status{Generation: m.gen}
	s := m.srv
	if s == nil {
		return st
	}
	st.Running, st.Server, st.Name, st.Command, st.Args, st.PHPVersion = true, s.id, s.name, s.command, s.args, s.phpVersion
	for _, d := range m.app.Documents() {
		if _, _, ok := s.client.Document(protocol.URIFromPath(d.File)); ok {
			st.OpenDocs++
		}
	}
	if si := s.client.ServerInfo(); si != nil {
		st.ServerInfo = strings.TrimSpace(si.Name + " " + si.Version)
	}
	return st
}

func (m *Module) lspStatus(context.Context, struct{}) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(), nil
}

// resolve finds the command to start for p.
func (m *Module) resolve(p StartParams) (id, name, command string, args []string, def servers.Definition, err error) {
	known := map[string]servers.Definition{}
	for _, d := range servers.Known() {
		known[d.ID] = d
	}
	def = known[servers.Phpactor]
	if p.Command == "" && p.Server == "" {
		p.Command = settings.Get(m.store(), SettingCommand, "")
		p.Args = settings.Get[[]string](m.store(), SettingArgs, nil)
		p.Server = settings.Get(m.store(), SettingServer, "")
	}
	if p.Command != "" {
		id = p.Server
		if id == "" {
			id = "custom"
		}
		if d, ok := known[p.Server]; ok {
			def = d
		}
		return id, id, p.Command, p.Args, def, nil
	}
	opts := m.app.Options().LSP
	if root := m.app.ProjectRoot(); root != "" {
		opts.ProjectDir = root
	}
	if p.Server != "" {
		if _, ok := known[p.Server]; !ok {
			return "", "", "", nil, def, api.Errorf(api.CodeInvalidParams, "unknown language server %q (see lsp.known)", p.Server)
		}
		opts.IncludeProprietary = true
		s, ok := servers.Find(p.Server, opts)
		if !ok {
			return "", "", "", nil, def, api.Errorf(api.CodeUnavailable, "language server %s is not installed (no downloads; install it or set lsp.command)", p.Server)
		}
		cmd, a := s.Command()
		return s.ID, s.Name, cmd, a, s.Definition, nil
	}
	found := servers.Detect(opts)
	if len(found) == 0 {
		return "", "", "", nil, def, api.Errorf(api.CodeUnavailable, "no PHP language server installed (phpactor, phpantom); pass command or set lsp.command")
	}
	cmd, a := found[0].Command()
	return found[0].ID, found[0].Name, cmd, a, found[0].Definition, nil
}

func (m *Module) lspStart(ctx context.Context, p StartParams) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv != nil {
		return m.statusLocked(), nil
	}
	if err := m.startLocked(ctx, p); err != nil {
		return Status{}, err
	}
	return m.statusLocked(), nil
}

func (m *Module) startLocked(ctx context.Context, p StartParams) error {
	id, name, command, args, def, err := m.resolve(p)
	if err != nil {
		return err
	}
	q, _ := api.HookPHPVersion.Apply(ctx, m.app.Hooks(), api.PHPVersionQuery{})
	short := q.Short
	if short == "" && q.Version != "" {
		short = q.Version
	}
	initOpts, cfgSettings := def.Options(short)
	if short == "" {
		initOpts, cfgSettings = map[string]any{}, map[string]any{}
	}
	s := &server{id: id, name: name, command: command, args: args, phpVersion: short}
	cfg := lsp.Config{
		Command: command, Args: args, RootDir: m.app.ProjectRoot(),
		InitializationOptions: initOpts, Settings: cfgSettings,
		ClientName: "Tiegel", ClientVersion: m.app.Options().AppVersion,
		Logger:        m.app.Logger().With("module", ID),
		OnDiagnostics: func(dp protocol.PublishDiagnosticsParams) { m.onDiagnostics(s, dp) },
	}
	connect := m.opts.Connect
	if connect == nil {
		connect = lsp.Start
	}
	c, err := connect(ctx, cfg)
	if err != nil {
		return api.Errorf(api.CodeUnavailable, "starting %s: %w", command, err)
	}
	m.gen++
	s.client, s.gen = c, m.gen
	m.srv, m.last = s, p
	go m.watch(s)
	m.syncMu.Lock()
	for _, d := range m.app.Documents() {
		if d.Language == languageID {
			m.openLocked(s, d)
		}
	}
	m.syncMu.Unlock()
	m.app.Bus().Publish(api.TopicLSPStatus, m.statusLocked())
	return nil
}

// watch notices a server that died on its own.
func (m *Module) watch(s *server) {
	<-s.client.Done()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv != s {
		return
	}
	m.srv = nil
	s.closed.Store(true)
	m.clearDiags()
	st := m.statusLocked()
	if err := s.client.Err(); err != nil {
		st.Error = err.Error()
	}
	m.app.Bus().Publish(api.TopicLSPStatus, st)
}

func (m *Module) stopLocked() {
	s := m.srv
	if s == nil {
		return
	}
	m.srv = nil
	s.closed.Store(true)
	m.clearDiags()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.client.Shutdown(ctx); err != nil {
		s.client.Close()
	}
	if m.app != nil {
		m.app.Bus().Publish(api.TopicLSPStatus, m.statusLocked())
	}
}

func (m *Module) lspStop(context.Context, struct{}) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return m.statusLocked(), nil
}

// restart restarts a running server with the same choice (after the
// interpreter or the PHP version changed).
func (m *Module) restart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return
	}
	p := m.last
	m.stopLocked()
	if err := m.startLocked(context.Background(), p); err != nil {
		m.app.Logger().Warn("lang-php: restart", "err", err)
		st := m.statusLocked()
		st.Error = err.Error()
		m.app.Bus().Publish(api.TopicLSPStatus, st)
	}
}

// lspErr maps client errors to API errors.
func lspErr(what string, err error) error {
	if errors.Is(err, lsp.ErrNotSupported) {
		return api.Errorf(api.CodeFailed, "%s: not supported by the language server", what)
	}
	return api.Errorf(api.CodeFailed, "%s: %w", what, err)
}

func notRunning() error {
	return api.Errorf(api.CodeUnavailable, "language server not running (use lsp.start)")
}

// LSPServersParams are the params of "lsp.servers".
type LSPServersParams struct {
	// All includes proprietary servers (Intelephense).
	All bool `json:"all,omitempty"`
}

// LSPServerInfo is a detected language server.
type LSPServerInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Args        []string `json:"args"`
	License     string   `json:"license"`
	Proprietary bool     `json:"proprietary,omitempty"`
}

func (m *Module) lspKnown(context.Context, struct{}) ([]LSPServerInfo, error) {
	out := []LSPServerInfo{}
	for _, d := range servers.Known() {
		out = append(out, LSPServerInfo{ID: d.ID, Name: d.Name, Args: d.Args, License: d.License, Proprietary: d.Proprietary})
	}
	return out, nil
}

func (m *Module) lspServers(_ context.Context, p LSPServersParams) ([]LSPServerInfo, error) {
	opts := m.app.Options().LSP
	if root := m.app.ProjectRoot(); root != "" {
		opts.ProjectDir = root
	}
	opts.IncludeProprietary = opts.IncludeProprietary || p.All
	out := []LSPServerInfo{}
	for _, s := range servers.Detect(opts) {
		args := s.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, LSPServerInfo{ID: s.ID, Name: s.Name, Path: s.Path, Args: args, License: s.License, Proprietary: s.Proprietary})
	}
	return out, nil
}
