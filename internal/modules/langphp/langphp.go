// Package langphp is the lang-php module: the client side of the PHP
// language server. Which server runs is chosen among the running lsp-*
// plugins (lsp-phpactor, lsp-phpantom, lsp-intelephense; decision D192)
// with the setting lang-php.server ("auto", "none" or a server ID; a
// project value overrides the global one). The chosen server runs
// natively under a supervisor (package lsp/supervisor) that checks the
// executable, restarts crashed or hung servers with backoff and stops
// after repeated crashes; its states are published as lsp.state events.
// With "none" (or without a server) no process runs and navigation falls
// back to the Tree-sitter index (php-index).
//
// The module keeps the server in sync with the open documents and exposes
// completion, hover, signature help, definitions, implementations,
// semantic tokens, diagnostics and refactorings through the lsp.*
// namespace.
//
// Document sync is driven by the bus, so edits from every client (API
// handlers, the in-process GUI editing the shared buffer) reach the server:
// doc.opened → didOpen, doc.edited → incremental didChange (the event
// carries the UTF-16 range), doc.closed → didClose, file.saved → didSave.
// Before every request the module makes sure the server has the current
// document version. After a restart all open documents are sent again.
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
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
)

// ID is the module ID.
const ID = "lang-php"

// Settings keys (project values override global ones).
const (
	// SettingServer selects the server: "auto" (first installed server of
	// the enabled plugins), "none" (language server off) or a server ID
	// ("phpactor", "phpantom", "intelephense").
	SettingServer = "lang-php.server"
	// SettingCommand is a custom server executable (overrides the
	// plugins); SettingArgs are its arguments ([]string).
	SettingCommand = "lang-php.command"
	SettingArgs    = "lang-php.args"
	// SettingRequestTimeout bounds a single request in seconds; a server
	// that keeps timing out is restarted (0: no limit).
	SettingRequestTimeout = "lang-php.requestTimeout"
)

// Former keys, still read when the new ones are missing.
const (
	legacyServer  = "lsp.server"
	legacyCommand = "lsp.command"
	legacyArgs    = "lsp.args"
)

// Selection values of SettingServer besides server IDs.
const (
	SelectAuto = "auto"
	SelectNone = "none"
)

// knownServerIDs are offered by the SettingServer enum (the IDs of the
// compiled-in lsp-* plugins).
var knownServerIDs = []string{"phpactor", "phpantom", "intelephense"}

// languageID is the LSP language of synced documents.
const languageID = "php"

// Options configure the module.
type Options struct {
	// Connect starts and initializes a server; nil means lsp.Start (child
	// process). Tests connect to an in-process fake server.
	Connect func(ctx context.Context, cfg lsp.Config) (*lsp.Client, error)
	// AutoStart starts the language server when a project is opened or
	// the first PHP document is opened (the GUI sets it; headless
	// commands start it explicitly with lsp.start).
	AutoStart bool
	// Supervisor adjusts the supervisor options (tests: short backoff).
	Supervisor func(*supervisor.Options)
}

// Module is the lang-php module.
type Module struct {
	opts Options
	app  *api.App
	sup  *supervisor.Supervisor

	// applyMu serialises server selection (apply).
	applyMu sync.Mutex

	// mu guards the fields below. It is never held while calling the
	// supervisor's lifecycle methods.
	mu         sync.Mutex
	srv        *server
	phpVersion string // of the target being started
	// active is set once the language server is wanted (lsp.start,
	// lsp.select, automatic start) and cleared by lsp.stop and project
	// close; while active, setting/plugin changes and installations
	// re-select the server.
	active bool
	// pinned holds explicit lsp.start parameters (server or command);
	// restarts reuse them until lsp.stop or lsp.select.
	pinned StartParams

	// diagMu guards diags; it is separate from mu because diagnostics
	// arrive on the client's goroutine.
	diagMu sync.Mutex
	diags  map[string]Diagnostics // by absolute path

	// syncMu serialises document sync (bus events vs. requests).
	syncMu sync.Mutex

	// actions caches the code actions of recent lsp.codeActions calls.
	actions actionCache
}

// server is a running (initialized) language server.
type server struct {
	client     *lsp.Client
	inst       *supervisor.Instance
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
// version comes through the php.resolveVersion hook when it runs. The
// lsp-* plugins are optional too: they contribute through lsp.servers.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"lsp.*"} }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingServer, Kind: settings.KindEnum, Default: SelectAuto, Scope: settings.ScopeProject,
			Options: append([]string{SelectAuto, SelectNone}, knownServerIDs...), Label: "Language server",
			Help:  "auto uses the first installed server of the enabled plugins; none switches the language server off (navigation then uses the index).",
			Group: "Language server"},
		{Key: SettingCommand, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Custom server command",
			Help: "Runs this executable instead of a plugin's server (advanced).", Group: "Language server"},
		{Key: SettingArgs, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Label: "Custom server arguments",
			Group: "Language server"},
		{Key: SettingRequestTimeout, Kind: settings.KindInt, Default: 30, Scope: settings.ScopeGlobal, Label: "Request timeout (seconds)",
			Help: "A server that does not answer in time repeatedly is restarted; 0 disables the limit.", Min: settings.IntPtr(0), Max: settings.IntPtr(600),
			Group: "Language server"},
	}
}

// Start registers the lsp namespace and the bus subscriptions.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("lang-php: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.srv = nil
	m.active = false
	m.pinned = StartParams{}
	m.mu.Unlock()
	m.clearDiags()
	m.sup = supervisor.New(m.supervisorOptions())
	p := api.NewProvider(ctx)
	api.Add(p, "lsp.servers", "Language servers of the enabled plugins: installed?, path, selected (no downloads)", m.lspServers)
	api.Add(p, "lsp.known", "Language servers of the enabled plugins (without detection)", m.lspKnown)
	api.Add(p, "lsp.start", "Start the selected language server (setting lang-php.server, a server ID or a command)", m.lspStart)
	api.Add(p, "lsp.stop", "Stop the language server", m.lspStop)
	api.Add(p, "lsp.status", "Language server supervisor state", m.lspStatus)
	api.Add(p, "lsp.select", "Choose the language server (auto, none or a server ID) for the project or globally and apply it", m.lspSelect)
	api.Add(p, "lsp.disable", "Switch the language server off (lang-php.server = none)", m.lspDisable)
	api.Add(p, "lsp.restart", "Restart the language server (also after a crash loop or an installation)", m.lspRestart)
	api.Add(p, "lsp.doctor", "Why the language server runs or not: selection, searched locations, per-server findings", m.lspDoctor)
	api.Add(p, "lsp.log", "Language server log: stderr, supervisor events, JSON-RPC trace (opt-in)", m.lspLog)
	api.Add(p, "lsp.trace", "Switch the JSON-RPC trace into the server log on or off", m.lspTrace)
	api.Add(p, "lsp.diagnostics", "Latest server diagnostics of a document (or of all)", m.lspDiagnostics)
	api.Add(p, "lsp.completion", "Completion items at a position", m.lspCompletion)
	api.Add(p, "lsp.hover", "Hover information at a position", m.lspHover)
	api.Add(p, "lsp.signature", "Signature help at a position", m.lspSignature)
	api.Add(p, "lsp.definition", "Definition locations of the symbol at a position", m.lspDefinition)
	api.Add(p, "lsp.implementation", "Implementations of the symbol at a position", m.lspImplementation)
	api.Add(p, "lsp.semanticTokens", "Semantic tokens of a document", m.lspSemanticTokens)
	m.registerRefactoring(p)
	if err := p.Err(); err != nil {
		m.sup.Close()
		return err
	}
	b := ctx.Bus()
	// One subscription for all doc.* events keeps them in publish order.
	ctx.OnStop(b.Subscribe("doc.*", m.onDoc))
	ctx.OnStop(b.Subscribe(api.TopicFileSaved, m.onSaved))
	ctx.OnStop(b.Subscribe(api.TopicFilesChanged, m.onFilesChanged))
	reload := func(bus.Event) { go m.reload() }
	ctx.OnStop(b.Subscribe(api.TopicInterpreterChanged, reload))
	ctx.OnStop(b.Subscribe(api.TopicPHPVersionChanged, reload))
	reapply := func(bus.Event) { go m.reapply() }
	ctx.OnStop(b.Subscribe(api.TopicModuleChanged, reapply))
	ctx.OnStop(b.Subscribe(api.TopicInstallDone, reapply))
	ctx.OnStop(b.Subscribe(settings.Changed.Name(), m.onSettings))
	ctx.OnStop(b.Subscribe(api.TopicProjectOpened, func(bus.Event) {
		if m.opts.AutoStart {
			go m.autoStart()
		}
	}))
	ctx.OnStop(b.Subscribe(api.TopicProjectClosed, func(bus.Event) {
		m.setActive(false)
		go m.stopSup("project closed")
	}))
	api.HookNavLSP.On(ctx.Hooks(), 0, m.navLSP, hooks.WithOwner(ID), hooks.WithTimeout(navTimeout))
	return nil
}

// Stop shuts the server down.
func (m *Module) Stop() error {
	if m.sup != nil {
		m.sup.Close()
	}
	m.mu.Lock()
	if m.srv != nil {
		m.srv.closed.Store(true)
		m.srv = nil
	}
	m.mu.Unlock()
	m.clearDiags()
	return nil
}

func (m *Module) current() *server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv
}

func (m *Module) setActive(on bool) {
	m.mu.Lock()
	m.active = on
	if !on {
		m.pinned = StartParams{}
	}
	m.mu.Unlock()
}

func (m *Module) pin(p StartParams) {
	m.mu.Lock()
	m.pinned = p
	m.mu.Unlock()
}

func (m *Module) pinnedParams() StartParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinned
}

func (m *Module) isActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *Module) store() *settings.Store { return m.app.SettingsStore() }

// StartParams are the params of "lsp.start". Without parameters the server
// comes from the settings (lang-php.server, lang-php.command) and the
// enabled lsp-* plugins.
type StartParams struct {
	// Server is a server ID (phpactor, phpantom, intelephense), "auto" or
	// "none".
	Server string `json:"server,omitempty"`
	// Command is a custom server executable; Args are its arguments.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// Status is the result of "lsp.start", "lsp.stop", "lsp.status" and the
// payload of the lsp.state event.
type Status struct {
	// State is the supervisor state: off, not-found, starting, running,
	// crashed or stopped.
	State   supervisor.State `json:"state"`
	Running bool             `json:"running"`
	// Selected is the effective lang-php.server value.
	Selected   string   `json:"selected,omitempty"`
	Server     string   `json:"server,omitempty"`
	Name       string   `json:"name,omitempty"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	PID        int      `json:"pid,omitempty"`
	PHPVersion string   `json:"phpVersion,omitempty"`
	OpenDocs   int      `json:"openDocs"`
	// ServerInfo is the name/version the server reports.
	ServerInfo string `json:"serverInfo,omitempty"`
	// Generation counts server starts (restarts increase it).
	Generation int `json:"generation"`
	// Reason explains not-found, crashed, stopped and off; Searched lists
	// the directories searched for the executable.
	Reason   string   `json:"reason,omitempty"`
	Searched []string `json:"searched,omitempty"`
	// Plugin is the lsp-* plugin of the server (for "Install…").
	Plugin   string `json:"plugin,omitempty"`
	Crashes  int    `json:"crashes"`
	Restarts int    `json:"restarts"`
	Trace    bool   `json:"trace"`
	// Error repeats Reason for failure states (former field).
	Error string `json:"error,omitempty"`
}

func (m *Module) status(st supervisor.Status) Status {
	out := Status{
		State: st.State, Running: st.State == supervisor.StateRunning, Server: st.Server, Name: st.Name,
		Command: st.Command, Args: st.Args, PID: st.PID, ServerInfo: st.ServerInfo, Generation: st.Generation,
		Reason: st.Reason, Searched: st.Searched, Crashes: st.Crashes, Restarts: st.Restarts, Trace: st.Trace,
	}
	if m.app != nil {
		out.Selected, _ = m.selection()
	}
	if st.Server != "" && st.Server != "custom" {
		out.Plugin = "lsp-" + st.Server
	}
	switch st.State {
	case supervisor.StateCrashed, supervisor.StateStopped, supervisor.StateNotFound:
		out.Error = st.Reason
	}
	if s := m.current(); s != nil && out.Running {
		out.PHPVersion = s.phpVersion
		for _, d := range m.app.Documents() {
			if _, _, ok := s.client.Document(protocol.URIFromPath(d.File)); ok {
				out.OpenDocs++
			}
		}
	}
	return out
}

func (m *Module) currentStatus() Status { return m.status(m.sup.Status()) }

func (m *Module) lspStatus(context.Context, struct{}) (Status, error) {
	return m.currentStatus(), nil
}

// supervisorOptions wires the supervisor to the module.
func (m *Module) supervisorOptions() supervisor.Options {
	timeout := time.Duration(-1) // off
	if secs := m.lookupInt(SettingRequestTimeout, 30); secs > 0 {
		timeout = secondsDuration(secs)
	}
	app := m.app
	o := supervisor.Options{
		Connect:        m.opts.Connect,
		RequestTimeout: timeout,
		OnState:        m.onState,
		OnReady:        m.onReady,
		OnDiagnostics:  m.onSupDiagnostics,
		Log:            supervisor.NewLog(2000, func(e supervisor.Entry) { app.Bus().Publish(api.TopicLSPLog, e) }),
		Logger:         app.Logger().With("module", ID),
	}
	if m.opts.Supervisor != nil {
		m.opts.Supervisor(&o)
	}
	return o
}

// onState follows the supervisor: a server that is not running any more
// is dropped (its diagnostics too) and the state is published.
func (m *Module) onState(st supervisor.Status) {
	if st.State != supervisor.StateRunning {
		m.mu.Lock()
		s := m.srv
		m.srv = nil
		m.mu.Unlock()
		if s != nil {
			s.closed.Store(true)
			m.clearDiags()
		}
	}
	m.app.Bus().Publish(api.TopicLSPState, m.status(st))
}

// onReady adopts a (re)started server and sends it all open documents.
func (m *Module) onReady(inst *supervisor.Instance) {
	t := inst.Target
	m.mu.Lock()
	s := &server{client: inst.Client(), inst: inst, id: t.ID, name: t.Name, command: t.Command, args: t.Args, phpVersion: m.phpVersion, gen: inst.Gen}
	old := m.srv
	m.srv = s
	m.mu.Unlock()
	if old != nil {
		old.closed.Store(true)
	}
	m.clearDiags()
	m.syncMu.Lock()
	for _, d := range m.app.Documents() {
		if d.Language == languageID {
			m.openLocked(s, d)
		}
	}
	m.syncMu.Unlock()
}

func (m *Module) onSupDiagnostics(inst *supervisor.Instance, p protocol.PublishDiagnosticsParams) {
	s := m.current()
	if s == nil || s.inst != inst {
		return
	}
	m.onDiagnostics(s, p)
}

func (m *Module) lspStart(ctx context.Context, p StartParams) (Status, error) {
	m.setActive(true)
	if p.Server == "" && p.Command == "" {
		// "Make sure a server runs": keep a running one (switching is
		// lsp.select's job).
		if st := m.sup.Status().State; st == supervisor.StateRunning || st == supervisor.StateStarting {
			return m.currentStatus(), nil
		}
	} else {
		m.pin(p)
	}
	return m.apply(ctx, p, applyExplicit)
}

func (m *Module) lspStop(context.Context, struct{}) (Status, error) {
	m.setActive(false)
	m.stopSup("stopped")
	return m.currentStatus(), nil
}

// stopSup switches the server off; serialised with apply so that a
// pending re-selection cannot start it again.
func (m *Module) stopSup(reason string) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.sup.Stop(supervisor.StateOff, reason, nil)
}

func (m *Module) lspRestart(ctx context.Context, _ struct{}) (Status, error) {
	m.setActive(true)
	return m.apply(ctx, StartParams{}, applyForce)
}

// reload restarts a wanted server after the interpreter or the PHP
// version changed (new initialization options).
func (m *Module) reload() {
	if !m.isActive() {
		return
	}
	mode := applyAuto
	if st := m.sup.Status().State; st == supervisor.StateRunning || st == supervisor.StateStarting {
		mode = applyForce
	}
	if _, err := m.apply(context.Background(), StartParams{}, mode); err != nil {
		m.app.Logger().Info("lang-php: reload", "err", err)
	}
}

// reapply re-selects the server after settings, plugins or installations
// changed (switching without restarting Tiegel).
func (m *Module) reapply() {
	if !m.isActive() {
		return
	}
	if _, err := m.apply(context.Background(), StartParams{}, applyAuto); err != nil {
		m.app.Logger().Info("lang-php: reapply", "err", err)
	}
}

// autoStart starts the server once (project opened, first PHP file).
func (m *Module) autoStart() {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return
	}
	m.active = true
	m.mu.Unlock()
	if _, err := m.apply(context.Background(), StartParams{}, applyAuto); err != nil {
		m.app.Logger().Info("lang-php: automatic start", "err", err)
	}
}

func (m *Module) onSettings(e bus.Event) {
	ch, ok := e.Payload.(settings.Change)
	if !ok {
		return
	}
	if strings.HasPrefix(ch.Key, ID+".") || strings.HasPrefix(ch.Key, "lsp-") || strings.HasPrefix(ch.Key, "lsp.") {
		go m.reapply()
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
	return api.Errorf(api.CodeUnavailable, "language server not running (use lsp.start; lsp.doctor explains why)")
}
