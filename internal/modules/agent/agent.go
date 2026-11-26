// Package agent is the AI coding agent plugin (D540): it runs a coding
// agent that speaks the Agent Client Protocol – by default opencode
// (`opencode acp`) – as a child process of the project, keeps the
// transcript of the session, enforces the permission settings (edits,
// shell commands, git commits and push, web access) by answering the
// agent's permission requests itself, and tracks every file the agent
// changed so that each change can be reviewed as a diff and reverted.
//
// The agent starts only on an explicit user action (agent.start or the
// first agent.prompt), never with the IDE. A missing agent is offered
// for installation through the install recipe (npm, only on click,
// D194).
//
// API (namespace agent.*, see api.AgentStatus and friends): agent.status,
// agent.start, agent.newSession, agent.prompt, agent.cancel, agent.stop,
// agent.transcript, agent.changes, agent.diff, agent.revert,
// agent.acceptChanges, agent.permission, agent.setOption, agent.setModel.
// Events: agent.status, agent.update, agent.permission, agent.changes,
// agent.turnEnded.
// Settings: agent.command, agent.model, agent.allowEdits,
// agent.allowShell, agent.allowCommits, agent.allowPush, agent.allowWeb,
// agent.allowOther, agent.rules.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// ID is the module ID.
const ID = "agent"

// Settings keys (D190: namespace = module ID).
const (
	SettingCommand = ID + ".command"
	SettingModel   = ID + ".model"
	SettingEdits   = ID + ".allowEdits"
	SettingShell   = ID + ".allowShell"
	SettingCommits = ID + ".allowCommits"
	SettingPush    = ID + ".allowPush"
	SettingWeb     = ID + ".allowWeb"
	SettingOther   = ID + ".allowOther"
	SettingRules   = ID + ".rules"
)

// DefaultCommand runs opencode as ACP agent.
const DefaultCommand = "opencode acp"

// StartFunc starts an agent process and connects handler h.
type StartFunc func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error)

// Options configure the module.
type Options struct {
	// Start replaces acp.Start (tests connect a fake agent).
	Start StartFunc
	// LookPath finds the agent executable; nil searches the tool PATH
	// (process, login shell) and ~/.opencode/bin.
	LookPath func(name string) (string, error)
	// HomeDir replaces the user's home directory (~/.opencode/bin).
	HomeDir string
}

// Module is the agent plugin.
type Module struct {
	opts Options

	// app and bus are set once by Start; atomic so that code holding mu
	// may read them.
	app atomic.Pointer[api.App]
	bus atomic.Pointer[bus.Bus]

	mu    sync.Mutex
	proc  *acp.Process
	state string
	err   string
	// gen counts agent processes; handlers of an old process are ignored.
	gen int

	agentName, agentVersion string
	authMethods             []acp.AuthMethod
	authRequired            bool

	sessionID string
	options   []acp.ConfigOption
	commands  []string
	entries   []*api.AgentEntry
	tools     map[string]int // tool call ID → entry index
	// open is the index of the message or thought entry chunks are
	// appended to (-1: none).
	open     int
	plan     int // index of the plan entry of the turn (-1: none)
	rulesOut bool
	always   map[string]bool

	perms   map[string]*pendingPerm
	permSeq int

	changes *tracker
	// changesFile is where changes are persisted ("" = not loaded).
	changesFile string
	toolPaths   map[string][]string // tool call ID → files snapshotted

	turnAsk    string
	userCancel bool
	// spawnConfig is the inline opencode configuration of the running
	// process ("" = none).
	spawnConfig string
	turnCancel  context.CancelFunc
	turnDone    chan struct{}
	lastStop    api.AgentTurnResult
}

// pendingPerm is a permission request waiting for the user.
type pendingPerm struct {
	entry int
	req   acp.RequestPermissionRequest
	cat   string
	ch    chan string
}

// New creates the module.
func New(opts Options) *Module {
	if opts.Start == nil {
		opts.Start = acp.Start
	}
	m := &Module{opts: opts, state: api.AgentStopped}
	m.reset()
	return m
}

// reset forgets the session state (not the tracked changes).
func (m *Module) reset() {
	m.sessionID, m.options, m.commands = "", nil, nil
	m.entries, m.tools, m.open, m.plan = nil, map[string]int{}, -1, -1
	m.rulesOut, m.always = false, map[string]bool{}
	if m.perms == nil {
		m.perms = map[string]*pendingPerm{}
	}
	if m.changes == nil {
		m.changes = newTracker()
	}
	m.toolPaths = map[string][]string{}
}

// syncChanges loads the persisted changes of the open project (when the
// project changed since the last load). Callers do not hold mu.
func (m *Module) syncChanges() {
	file := ""
	if app := m.application(); app != nil {
		if ps := app.ProjectSettings(); ps != nil {
			file = filepath.Join(ps.Dir(), "agent-changes.json")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if file == m.changesFile {
		return
	}
	m.changesFile = file
	if file == "" {
		m.changes = newTracker()
		return
	}
	m.changes = loadTracker(file)
}

// saveChanges persists the changes (callers hold mu).
func (m *Module) saveChangesLocked() {
	if m.changesFile == "" {
		return
	}
	if err := m.changes.save(m.changesFile); err != nil {
		if app := m.application(); app != nil {
			app.Logger().Warn("agent changes", "err", err)
		}
	}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"agent.*"} }

// SettingsPage implements module.PageProvider.
func (m *Module) SettingsPage() (section, title string) { return "AI", "Agent" }

var (
	askAllowDeny = []string{api.PolicyAsk, api.PolicyAllow, api.PolicyDeny}
	allowAsk     = []string{api.PolicyAllow, api.PolicyAsk}
)

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	const perms = "What the agent may do"
	return []settings.Spec{
		{Key: SettingCommand, Kind: settings.KindString, Default: DefaultCommand, Scope: settings.ScopeProject, Label: "Agent command", Executable: true, Group: "Agent",
			Help: "Command line of an agent that speaks the Agent Client Protocol, e.g. “opencode acp”. It starts only when you send the first message or click Start."},
		{Key: SettingModel, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model", Group: "Agent",
			Help: "Model to choose when a session starts (remembered from the Agent window); empty uses the agent's default."},
		{Key: SettingEdits, Kind: settings.KindEnum, Options: allowAsk, Default: api.PolicyAllow, Scope: settings.ScopeProject, Label: "Change files", Group: perms,
			Help: "allow: the agent changes files right away – every change is listed in the Agent window with its diff and can be reverted. ask: each change waits for your OK."},
		{Key: SettingShell, Kind: settings.KindEnum, Options: askAllowDeny, Default: api.PolicyAsk, Scope: settings.ScopeProject, Label: "Run shell commands", Group: perms,
			Help: "ask: you confirm each command (Always remembers the program for the session). deny: no commands at all."},
		{Key: SettingCommits, Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Allow git commits", Group: perms,
			Help: "Off: git commit and everything that rewrites history or throws work away (merge, rebase, reset, cherry-pick, revert, tag, stash, pull, --amend) is blocked by Tiegel. On: you confirm each one."},
		{Key: SettingPush, Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Allow git push", Group: perms,
			Help: "Off: git push is blocked by Tiegel. On: you confirm each push."},
		{Key: SettingWeb, Kind: settings.KindEnum, Options: askAllowDeny, Default: api.PolicyAsk, Scope: settings.ScopeProject, Label: "Access the web", Group: perms,
			Help: "Fetching web pages and web search by the agent."},
		{Key: SettingOther, Kind: settings.KindEnum, Options: askAllowDeny, Default: api.PolicyAsk, Scope: settings.ScopeProject, Label: "Other tools", Group: perms,
			Help: "Everything else the agent asks about, e.g. files outside the project or tools of MCP servers."},
		{Key: SettingRules, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Rules for the agent", Group: perms,
			Help: "Your own words, sent with the first message of every session, e.g. “Never commit. Don't touch vendor/.” The agent is asked to follow them; enforced are only the switches above."},
	}
}

// Recipe is the install recipe of opencode (npm, global, on click).
func Recipe() install.Recipe {
	return install.Recipe{
		Plugin: ID, Tool: "opencode", Manager: install.ManagerNPM, Package: "opencode-ai",
		Scopes: []install.Scope{install.ScopeGlobal}, Interpreter: install.InterpreterNative,
		Hint: "Or install opencode as described on opencode.ai; any agent speaking the Agent Client Protocol works (Settings › AI › Agent › Agent command).",
	}
}

// Start registers the API, the install recipe and the event subscriptions.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("agent: needs an api.App context")
	}
	m.app.Store(app)
	m.bus.Store(ctx.Bus())
	p := api.NewProvider(ctx)
	api.Add(p, "agent.status", "Coding agent: executable, state, session, model and options, permission policy, pending requests, changed files", m.status)
	api.Add(p, "agent.start", "Start the coding agent and a session in the project (explicit user action)", m.start)
	api.Add(p, "agent.newSession", "Start a new agent session (transcript cleared; changed files stay listed)", m.newSession)
	api.Add(p, "agent.prompt", "Send a message to the agent (starts it if needed); wait: until the turn ended; ask: answer for permissions set to ask", m.prompt)
	api.Add(p, "agent.cancel", "Cancel the running turn", m.cancel)
	api.Add(p, "agent.stop", "Stop the agent process", m.stop)
	api.Add(p, "agent.transcript", "Transcript of the session: messages, thoughts, tool calls, plan, permissions, notices", m.transcript)
	api.Add(p, "agent.changes", "Files the agent changed (with line counts and conflicts)", m.changesList)
	api.Add(p, "agent.diff", "A changed file before the agent's first change and now (with unified diff)", m.diff)
	api.Add(p, "agent.revert", "Revert the agent's changes of a file or all files (force: also files changed afterwards)", m.revert)
	api.Add(p, "agent.acceptChanges", "Keep all changes and clear the list", m.accept)
	api.Add(p, "agent.permission", "Answer a permission request of the agent (optionId, or allow/deny)", m.permission)
	api.Add(p, "agent.setOption", "Set a session option of the agent (model, effort, mode …)", m.setOption)
	api.Add(p, "agent.setModel", "Choose the model (remembered in agent.model)", m.setModel)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(api.HookInstallRecipes.On(ctx.Hooks(), 100, func(_ context.Context, l []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(l), Recipe()), nil
	}, hooks.WithOwner(ID)))
	ctx.OnStop(ctx.Bus().Subscribe("install.done", func(bus.Event) { m.publishStatus() }))
	ctx.OnStop(ctx.Bus().Subscribe("settings.changed", func(bus.Event) { m.settingsChanged() }))
	return nil
}

// Stop implements module.Module: the agent process ends with the module.
func (m *Module) Stop() error {
	m.shutdown("")
	return nil
}

func (m *Module) application() *api.App { return m.app.Load() }

func (m *Module) publish(topic string, v any) {
	if b := m.bus.Load(); b != nil {
		b.Publish(topic, v)
	}
}

// setting reads a setting (project before global).
func setting[T any](m *Module, key string, def T) T {
	app := m.application()
	if app == nil {
		return def
	}
	return api.Setting(app, key, def)
}

// policy returns the permission settings.
func (m *Module) policy() api.AgentPolicy {
	enum := func(key, def string, allowed []string) string {
		v := setting(m, key, def)
		if !slices.Contains(allowed, v) {
			return def
		}
		return v
	}
	boolPol := func(key string) string {
		if setting(m, key, false) {
			return api.PolicyAsk
		}
		return api.PolicyDeny
	}
	return summarize(api.AgentPolicy{
		Edit:      enum(SettingEdits, api.PolicyAllow, allowAsk),
		Shell:     enum(SettingShell, api.PolicyAsk, askAllowDeny),
		GitCommit: boolPol(SettingCommits),
		GitPush:   boolPol(SettingPush),
		Web:       enum(SettingWeb, api.PolicyAsk, askAllowDeny),
		Other:     enum(SettingOther, api.PolicyAsk, askAllowDeny),
		Rules:     setting(m, SettingRules, ""),
	})
}

// command returns the configured argv.
func (m *Module) command() (string, []string) {
	line := strings.TrimSpace(setting(m, SettingCommand, DefaultCommand))
	if line == "" {
		line = DefaultCommand
	}
	argv, err := run.SplitWords(line)
	if err != nil || len(argv) == 0 {
		argv = strings.Fields(line)
	}
	return line, argv
}

// searchPath is the PATH the agent is looked up in and runs with.
func (m *Module) searchPath() string {
	dirs := os.Getenv("PATH")
	if app := m.application(); app != nil {
		dirs = shellenv.Merge(app.InstallEnv().PathEnv, dirs)
	}
	home := m.opts.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		dirs = shellenv.Merge(dirs, filepath.Join(home, ".opencode", "bin"))
	}
	return dirs
}

// resolve finds the executable of argv[0].
func (m *Module) resolve(name string) (string, error) {
	if m.opts.LookPath != nil {
		return m.opts.LookPath(name)
	}
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			return name, nil
		}
		return "", os.ErrNotExist
	}
	return shellenv.LookPath(name, m.searchPath())
}

// isOpencode tells whether argv runs opencode.
func isOpencode(argv []string) bool {
	return len(argv) > 0 && strings.TrimSuffix(filepath.Base(argv[0]), ".exe") == "opencode"
}

// status builds agent.status.
func (m *Module) status(context.Context, struct{}) (api.AgentStatus, error) {
	return m.statusNow(), nil
}

func (m *Module) statusNow() api.AgentStatus {
	m.syncChanges()
	line, argv := m.command()
	st := api.AgentStatus{Command: line, Policy: m.policy()}
	if path, err := m.resolve(argv[0]); err == nil {
		st.Path, st.Available = path, true
	} else if isOpencode(argv) {
		st.Install = ID
		st.InstallHint = "opencode was not found (PATH, ~/.opencode/bin). Install it with npm, or as described on opencode.ai."
	} else {
		st.InstallHint = argv[0] + " was not found. Check Settings › AI › Agent › Agent command."
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st.State, st.Error = m.state, m.err
	st.Agent, st.Version, st.SessionID = m.agentName, m.agentVersion, m.sessionID
	st.Commands = append([]string(nil), m.commands...)
	for _, o := range m.options {
		ao := api.AgentOption{ID: o.ID, Name: o.Name, Category: o.Category, Value: string(o.CurrentValue)}
		for _, c := range o.Options {
			ao.Choices = append(ao.Choices, api.AgentChoice{Value: string(c.Value), Name: c.Name})
		}
		st.Options = append(st.Options, ao)
		if o.ID == "model" || o.Category == "model" {
			st.Model = ao.Value
		}
	}
	for _, p := range m.perms {
		if p != nil {
			st.Pending++
		}
	}
	st.Changes = len(m.changes.changed())
	if m.authRequired {
		st.AuthRequired = true
		var hints []string
		for _, a := range m.authMethods {
			if a.Description != "" {
				hints = append(hints, a.Description)
			} else {
				hints = append(hints, a.Name)
			}
		}
		st.AuthHint = strings.Join(hints, "; ")
		if st.AuthHint == "" {
			st.AuthHint = "The agent needs a login; run its login command in the terminal."
		}
	}
	return st
}

func (m *Module) publishStatus() { m.publish(api.TopicAgentStatus, m.statusNow()) }

func (m *Module) setState(state, errMsg string) {
	m.mu.Lock()
	m.state, m.err = state, errMsg
	m.mu.Unlock()
	m.publishStatus()
}

// start starts the agent process and a session (if not running).
func (m *Module) start(ctx context.Context, _ struct{}) (api.AgentStatus, error) {
	if err := m.ensure(ctx); err != nil {
		return m.statusNow(), err
	}
	return m.statusNow(), nil
}

// ensure makes sure an agent process with a session runs.
func (m *Module) ensure(ctx context.Context) error {
	m.mu.Lock()
	if m.proc != nil && m.sessionID != "" {
		m.mu.Unlock()
		return nil
	}
	if m.state == api.AgentStarting {
		m.mu.Unlock()
		return api.Errorf(api.CodeConflict, "the agent is starting")
	}
	proc := m.proc
	m.state, m.err = api.AgentStarting, ""
	m.mu.Unlock()
	m.publishStatus()
	if proc == nil {
		var err error
		if proc, err = m.spawn(ctx); err != nil {
			m.setState(api.AgentFailed, err.Error())
			return err
		}
	}
	if err := m.openSession(ctx, proc); err != nil {
		return err
	}
	m.setState(api.AgentReady, "")
	return nil
}

// spawn starts the process and initializes the connection.
func (m *Module) spawn(ctx context.Context) (*acp.Process, error) {
	app := m.application()
	if app == nil {
		return nil, api.Errorf(api.CodeFailed, "agent: module not started")
	}
	root, err := app.RequireProject()
	if err != nil {
		return nil, err
	}
	line, argv := m.command()
	path, err := m.resolve(argv[0])
	if err != nil {
		hint := ""
		if isOpencode(argv) {
			hint = " (install it from the Agent window or with: tiegel --headless install agent)"
		}
		return nil, api.Errorf(api.CodeNotFound, "agent %q not found%s", argv[0], hint)
	}
	env := append(os.Environ(), "PATH="+m.searchPath())
	cfg := ""
	if isOpencode(argv) {
		cfg = opencodeConfig(m.policy())
		env = append(env, "OPENCODE_CONFIG_CONTENT="+cfg)
	}
	m.mu.Lock()
	m.gen++
	gen := m.gen
	m.mu.Unlock()
	h := &handler{m: m, gen: gen}
	proc, err := m.opts.Start(ctx, acp.Command{Path: path, Args: argv[1:], Dir: root, Env: env}, h)
	if err != nil {
		return nil, api.Errorf(api.CodeFailed, "start %s: %w", line, err)
	}
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	init, err := proc.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{FS: acp.FSCapabilities{ReadTextFile: true, WriteTextFile: true}},
		ClientInfo:         &acp.Implementation{Name: "tiegel", Title: "Tiegel", Version: version.Version},
	})
	if err != nil {
		_ = proc.Close()
		return nil, api.Errorf(api.CodeFailed, "agent initialize: %w%s", err, stderrTail(proc))
	}
	m.mu.Lock()
	m.proc, m.spawnConfig = proc, cfg
	m.authMethods, m.authRequired = init.AuthMethods, false
	m.agentName, m.agentVersion = "", ""
	if init.AgentInfo != nil {
		m.agentName, m.agentVersion = init.AgentInfo.Name, init.AgentInfo.Version
		if init.AgentInfo.Title != "" {
			m.agentName = init.AgentInfo.Title
		}
	}
	m.mu.Unlock()
	go m.watch(proc, gen)
	return proc, nil
}

// settingsChanged republishes the status and tells the user when the
// running opencode still has the permission configuration of its start
// (Tiegel applies the new settings at once; opencode's own deny rules
// change with the next start).
func (m *Module) settingsChanged() {
	m.publishStatus()
	m.mu.Lock()
	running, old := m.proc != nil, m.spawnConfig
	m.mu.Unlock()
	if !running || old == "" {
		return
	}
	cfg := opencodeConfig(m.policy())
	if cfg == old {
		return
	}
	m.mu.Lock()
	m.spawnConfig = cfg
	m.mu.Unlock()
	m.notice(api.NoticeInfo, "Permission settings changed. Tiegel applies them right away; opencode's own copy of the blocked commands updates when the agent restarts (Stop, then send a message).")
}

// stderrTail returns the agent's last stderr lines for error messages.
func stderrTail(p *acp.Process) string {
	s := strings.TrimSpace(p.Stderr())
	if s == "" {
		return ""
	}
	if lines := strings.Split(s, "\n"); len(lines) > 5 {
		s = strings.Join(lines[len(lines)-5:], "\n")
	}
	return "\n" + s
}

// watch notices the end of the agent process.
func (m *Module) watch(p *acp.Process, gen int) {
	<-p.Exited()
	m.mu.Lock()
	if m.gen != gen {
		m.mu.Unlock()
		return
	}
	m.proc, m.sessionID = nil, ""
	pending := m.takePerms()
	expected := m.state == api.AgentStopped
	m.mu.Unlock()
	answerAll(pending, acp.OutcomeCancelled)
	if expected {
		return
	}
	msg := "The agent exited."
	if err := p.ExitErr(); err != nil {
		msg = "The agent exited: " + err.Error()
	}
	msg += stderrTail(p)
	m.notice(api.NoticeError, msg)
	m.setState(api.AgentFailed, msg)
}

// openSession creates a session and applies the remembered model.
func (m *Module) openSession(ctx context.Context, proc *acp.Process) error {
	app := m.application()
	root, err := app.RequireProject()
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := proc.NewSessionWith(sctx, root, m.mcpServers(sctx))
	if err != nil {
		if acp.ErrorCode(err) == acp.CodeAuthRequired {
			m.mu.Lock()
			m.authRequired = true
			m.mu.Unlock()
			st := m.statusNow()
			msg := "The agent needs a login: " + st.AuthHint
			m.notice(api.NoticeError, msg)
			m.setState(api.AgentFailed, msg)
			return api.Errorf(api.CodeFailed, "%s", msg)
		}
		msg := "agent session: " + err.Error() + stderrTail(proc)
		m.setState(api.AgentFailed, msg)
		return api.Errorf(api.CodeFailed, "%s", msg)
	}
	m.mu.Lock()
	m.sessionID, m.options, m.authRequired = res.SessionID, res.ConfigOptions, false
	m.mu.Unlock()
	if want := setting(m, SettingModel, ""); want != "" {
		if opt, ok := m.modelOption(); ok && string(opt.CurrentValue) != want && hasChoice(opt, want) {
			if opts, err := proc.SetConfigOption(sctx, res.SessionID, opt.ID, want); err == nil && len(opts) > 0 {
				m.mu.Lock()
				m.options = opts
				m.mu.Unlock()
			}
		}
	}
	return nil
}

// mcpServers collects the MCP servers other modules offer to the
// session (HookAgentMCPServers, e.g. the project knowledge, D563).
func (m *Module) mcpServers(ctx context.Context) []acp.MCPServerStdio {
	app := m.application()
	if app == nil {
		return nil
	}
	list, err := api.HookAgentMCPServers.Apply(ctx, app.Hooks(), nil)
	if err != nil {
		app.Logger().Warn("agent mcp servers", "err", err)
	}
	var out []acp.MCPServerStdio
	for _, s := range list {
		srv := acp.MCPServerStdio{Name: s.Name, Command: s.Command, Args: s.Args}
		for _, kv := range s.Env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				srv.Env = append(srv.Env, acp.EnvVariable{Name: k, Value: v})
			}
		}
		out = append(out, srv)
	}
	return out
}

// modelOption returns the model option of the session.
func (m *Module) modelOption() (acp.ConfigOption, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.options {
		if o.ID == "model" || o.Category == "model" {
			return o, true
		}
	}
	return acp.ConfigOption{}, false
}

func hasChoice(o acp.ConfigOption, v string) bool {
	for _, c := range o.Options {
		if string(c.Value) == v {
			return true
		}
	}
	return false
}

// newSession starts a new session in the running agent (or starts it).
func (m *Module) newSession(ctx context.Context, _ struct{}) (api.AgentStatus, error) {
	m.cancelTurn()
	m.mu.Lock()
	proc := m.proc
	pending := m.takePerms()
	m.reset()
	m.mu.Unlock()
	answerAll(pending, acp.OutcomeCancelled)
	m.publish(api.TopicAgentUpdate, api.AgentUpdateEvent{Entry: api.AgentEntry{Index: -1}})
	if proc == nil {
		return m.start(ctx, struct{}{})
	}
	if err := m.openSession(ctx, proc); err != nil {
		return m.statusNow(), err
	}
	m.setState(api.AgentReady, "")
	return m.statusNow(), nil
}

// stop ends the agent process (the transcript and changes stay).
func (m *Module) stop(context.Context, struct{}) (api.AgentStatus, error) {
	m.shutdown(api.AgentStopped)
	return m.statusNow(), nil
}

func (m *Module) shutdown(state string) {
	m.cancelTurn()
	m.mu.Lock()
	proc := m.proc
	m.proc, m.sessionID = nil, ""
	m.state, m.err = api.AgentStopped, ""
	pending := m.takePerms()
	m.mu.Unlock()
	answerAll(pending, acp.OutcomeCancelled)
	if proc != nil {
		_ = proc.Close()
	}
	if state != "" {
		m.publishStatus()
	}
}

// cancelTurn cancels a running turn and waits briefly for its end.
func (m *Module) cancelTurn() {
	m.mu.Lock()
	cancel, done, proc, sid := m.turnCancel, m.turnDone, m.proc, m.sessionID
	if cancel != nil {
		m.userCancel = true
	}
	pending := m.takePerms()
	m.mu.Unlock()
	answerAll(pending, acp.OutcomeCancelled)
	if cancel == nil {
		return
	}
	if proc != nil {
		_ = proc.Cancel(sid)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
	}
}

// cancel is agent.cancel.
func (m *Module) cancel(context.Context, struct{}) (api.AgentStatus, error) {
	m.mu.Lock()
	running := m.turnCancel != nil
	m.mu.Unlock()
	if running {
		m.cancelTurn()
		m.notice(api.NoticeInfo, "Cancelled.")
	}
	return m.statusNow(), nil
}

// takePerms removes all pending permission requests (callers hold mu).
func (m *Module) takePerms() []*pendingPerm {
	var out []*pendingPerm
	for id, p := range m.perms {
		out = append(out, p)
		delete(m.perms, id)
	}
	return out
}

func answerAll(ps []*pendingPerm, answer string) {
	for _, p := range ps {
		select {
		case p.ch <- answer:
		default:
		}
	}
}

// setOption is agent.setOption.
func (m *Module) setOption(ctx context.Context, p api.AgentOptionParams) (api.AgentStatus, error) {
	if p.ID == "" {
		return m.statusNow(), api.Errorf(api.CodeInvalidParams, "id is required")
	}
	if err := m.ensure(ctx); err != nil {
		return m.statusNow(), err
	}
	m.mu.Lock()
	proc, sid := m.proc, m.sessionID
	m.mu.Unlock()
	opts, err := proc.SetConfigOption(ctx, sid, p.ID, p.Value)
	if err != nil {
		return m.statusNow(), api.Errorf(api.CodeFailed, "set %s: %w", p.ID, err)
	}
	m.mu.Lock()
	if len(opts) > 0 {
		m.options = opts
	} else {
		for i := range m.options {
			if m.options[i].ID == p.ID {
				m.options[i].CurrentValue = acp.Value(p.Value)
			}
		}
	}
	m.mu.Unlock()
	if o, ok := m.modelOption(); ok && o.ID == p.ID {
		m.rememberModel(ctx, p.Value)
	}
	m.publishStatus()
	return m.statusNow(), nil
}

// setModel is agent.setModel.
func (m *Module) setModel(ctx context.Context, p api.AgentModelParams) (api.AgentStatus, error) {
	if p.Model == "" {
		return m.statusNow(), api.Errorf(api.CodeInvalidParams, "model is required")
	}
	if err := m.ensure(ctx); err != nil {
		return m.statusNow(), err
	}
	o, ok := m.modelOption()
	if !ok {
		return m.statusNow(), api.Errorf(api.CodeFailed, "the agent offers no model choice")
	}
	return m.setOption(ctx, api.AgentOptionParams{ID: o.ID, Value: p.Model})
}

// rememberModel stores the model for the next session (global, so it
// applies in every project unless a project overrides it).
func (m *Module) rememberModel(ctx context.Context, model string) {
	if app := m.application(); app != nil {
		raw, _ := json.Marshal(model)
		_ = app.CallLocal(ctx, "settings.set", api.SettingsSetParams{Key: SettingModel, Scope: "global", Value: raw}, nil)
	}
}
