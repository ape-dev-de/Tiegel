// Package knowledge is the project knowledge plugin (D560–D563, D565):
// on an explicit user action it runs a read-only coding agent (opencode
// over ACP, the command of the agent plugin) top-down through the levels
// overview → entry points → processes → components → open questions;
// every page is described by one session and validated by another, and
// Tiegel renders the Markdown outside the repository in the project's
// Tiegel directory (<config>/projects/<hash>/knowledge). Reading works
// while a build runs: pages appear as they are written, search uses the
// last consistent vector store plus the new pages. The pages are chunked and
// embedded (Ollama by default, or an OpenAI compatible endpoint) into a
// brute-force vector store for hybrid search, which the coding agent
// reaches through Tiegel's MCP server (`tiegel --headless mcp knowledge`).
//
// Nothing runs on its own: builds cost tokens and start only from
// knowledge.build; models are pulled only from knowledge.pullModel.
//
// API (namespace knowledge.*): status, plan, build, cancel, list, show,
// read, questions, search, embed, modelStatus, pullModel, mcpConfig.
// Events: knowledge.status, knowledge.progress.
package knowledge

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// ID is the module ID.
const ID = "knowledge"

// Settings keys.
const (
	SettingGiveAgent  = ID + ".giveAgent"
	SettingModel      = ID + ".agentModel"
	SettingProvider   = ID + ".embedProvider"
	SettingEndpoint   = ID + ".embedEndpoint"
	SettingEmbedModel = ID + ".embedModel"
	SettingAPIKey     = ID + ".embedApiKey"
	SettingChunkSize  = ID + ".chunkSize"
	SettingExclude    = ID + ".exclude"
	// SettingMaxProcesses and SettingMaxComponents bound the pages of
	// levels 2 and 3.
	SettingMaxProcesses  = ID + ".maxProcesses"
	SettingMaxComponents = ID + ".maxComponents"
)

// APIKeyEnv is read when the API key setting is empty.
const APIKeyEnv = "TIEGEL_EMBED_API_KEY"

// Settings of the agent plugin read here (the same agent does the work).
const (
	agentCommandKey = "agent.command"
	agentModelKey   = "agent.model"
	defaultCommand  = "opencode acp"
)

// StartFunc starts an agent process and connects handler h.
type StartFunc func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error)

// Options configure the module.
type Options struct {
	// Start replaces acp.Start (tests connect a fake agent).
	Start StartFunc
	// LookPath finds the agent executable (nil: tool PATH and
	// ~/.opencode/bin).
	LookPath func(name string) (string, error)
	// HomeDir replaces the home directory.
	HomeDir string
	// Executable returns Tiegel's executable for the MCP server command
	// (nil: os.Executable).
	Executable func() (string, error)
	// HTTPClient is used for the embedding server (nil: default).
	HTTPClient *http.Client
}

// Module is the knowledge plugin.
type Module struct {
	opts Options
	app  atomic.Pointer[api.App]
	bus  atomic.Pointer[bus.Bus]

	mu  sync.Mutex
	run api.KnowledgeRun
	// act is the live activity of the running or last build.
	act    *activityLog
	cancel context.CancelFunc
	done   chan struct{}

	store    kn.Store
	storeDir string

	pulling    bool
	pullStatus string
	pullErr    string

	// planCache is the last plan of the whole project (cleared on file
	// changes).
	planCache *planned

	// live caches the chunks of the current pages for search (keyed by
	// the manifest's and questions.md's stamps).
	live liveChunks
}

// New creates the module.
func New(opts Options) *Module {
	if opts.Start == nil {
		opts.Start = acp.Start
	}
	if opts.Executable == nil {
		opts.Executable = os.Executable
	}
	return &Module{opts: opts}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"knowledge.*"} }

// SettingsPage implements module.PageProvider.
func (m *Module) SettingsPage() (section, title string) { return "AI", "Knowledge" }

func ptr(i int) *int { return &i }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	const build, embed = "Building", "Search (embeddings)"
	return []settings.Spec{
		{Key: SettingGiveAgent, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Give the agent the project knowledge", Group: "Agent",
			Help: "When the knowledge base exists, the coding agent gets Tiegel's MCP server with the tools knowledge_search, knowledge_read and knowledge_list (read-only). Applies to new agent sessions."},
		{Key: SettingModel, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model for building", Group: build,
			Help: "Model the agent uses to describe and validate the pages (e.g. a cheaper one). Empty uses the model of the Agent window. Building uses tokens of your agent's provider; Tiegel shows an estimate before every build."},
		{Key: SettingExclude, Kind: settings.KindList, Default: kn.DefaultExclude, Scope: settings.ScopeProject, Label: "Leave out folders", Group: build,
			Help: "Folder names skipped anywhere in the project (unless you build exactly that folder)."},
		{Key: SettingMaxProcesses, Kind: settings.KindInt, Default: kn.DefaultMaxProcesses, Min: ptr(1), Max: ptr(500), Scope: settings.ScopeProject, Label: "Processes", Group: build,
			Help: "At most this many process pages (level 2): entry points are grouped by their handler class, the biggest groups first."},
		{Key: SettingMaxComponents, Kind: settings.KindInt, Default: kn.DefaultMaxComponents, Min: ptr(0), Max: ptr(500), Scope: settings.ScopeProject, Label: "Components", Group: build,
			Help: "At most this many component pages (level 3): the classes the process pages cite most."},
		{Key: SettingProvider, Kind: settings.KindEnum, Options: []string{kn.ProviderOllama, kn.ProviderOpenAI}, Default: kn.ProviderOllama, Scope: settings.ScopeProject, Label: "Embedding provider", Group: embed,
			Help: "ollama: local, the documents stay on this machine. openai-compatible: any /v1/embeddings endpoint – the knowledge documents and your search queries are sent to that server."},
		{Key: SettingEndpoint, Kind: settings.KindString, Default: kn.DefaultEndpoint, Scope: settings.ScopeProject, Label: "Endpoint", Group: embed,
			Help: "Base URL of the embedding server, e.g. http://localhost:11434 (Ollama) or https://api.openai.com/v1."},
		{Key: SettingEmbedModel, Kind: settings.KindString, Default: kn.DefaultModel, Scope: settings.ScopeProject, Label: "Embedding model", Group: embed,
			Help: "Ollama: embeddinggemma-2:270m (text-only, 300–380 MB) by default; other text tags are e.g. embeddinggemma-2:270m-q8_0-text or embeddinggemma-2:570m. A missing model is pulled only when you click Pull. Changing the model re-embeds everything."},
		{Key: SettingAPIKey, Kind: settings.KindString, Default: "", Scope: settings.ScopeGlobal, Label: "API key", Group: embed,
			Help: "Only for openai-compatible endpoints. Stored in plain text in Tiegel's settings file; leave it empty to use the environment variable " + APIKeyEnv + " instead."},
		{Key: SettingChunkSize, Kind: settings.KindInt, Default: 500, Min: ptr(150), Max: ptr(2000), Scope: settings.ScopeProject, Label: "Chunk size (tokens)", Group: embed,
			Help: "Target size of the searchable sections; sections stay between 60 % and 160 % of it."},
	}
}

// Start registers the API and hooks.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("knowledge: needs an api.App context")
	}
	m.app.Store(app)
	m.bus.Store(ctx.Bus())
	p := api.NewProvider(ctx)
	api.Add(p, "knowledge.status", "Project knowledge base: directory, pages per level (stale, failed), open/answered questions, last run, agent and model, embedding store, running build", m.status)
	api.Add(p, "knowledge.plan", "Pages a build would run per level with an estimate (agent calls, tokens); level: overview|entrypoints|processes|components|questions|all, scope: folders, all: also current pages", m.plan)
	api.Add(p, "knowledge.build", "Build or update the knowledge base with the coding agent, top-down by level (uses tokens; explicit user action); wait: until done", m.build)
	api.Add(p, "knowledge.cancel", "Cancel the running build", m.cancelBuild)
	api.Add(p, "knowledge.list", "Pages of the knowledge base by level with state, confidence and file (also while a build runs)", m.list)
	api.Add(p, "knowledge.show", "The page of an ID or title (empty: index.md)", m.show)
	api.Add(p, "knowledge.read", "A file of the knowledge base (index.md, overview.md, processes/<id>.md, questions.md …) or a page ID", m.read)
	api.Add(p, "knowledge.questions", "Open and answered questions of the knowledge base; status: open|answered", m.questions)
	api.Add(p, "knowledge.transcript", "Live transcript of a page's agent sessions in the running or last build (thoughts, messages, tool calls)", m.transcript)
	api.Add(p, "knowledge.search", "Hybrid search (embeddings + keywords) over the knowledge base: sections with score, document and heading", m.search)
	api.Add(p, "knowledge.embed", "Chunk the documents and (re-)embed changed sections (model: override once)", m.embed)
	api.Add(p, "knowledge.modelStatus", "Is the embedding server reachable and the model there (with the pull command)", m.modelStatus)
	api.Add(p, "knowledge.pullModel", "Pull the embedding model on the Ollama server (ollama pull; explicit user action, downloads the model)", m.pullModel)
	api.Add(p, "knowledge.mcpConfig", "How to give the knowledge to an MCP client by hand (command and config snippets)", m.mcpConfig)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(api.HookAgentMCPServers.On(ctx.Hooks(), 100, func(_ context.Context, l []api.AgentMCPServer) ([]api.AgentMCPServer, error) {
		if s, ok := m.agentServer(); ok {
			l = append(l, s)
		}
		return l, nil
	}, hooks.WithOwner(ID)))
	// A running build keeps the project from hibernating.
	ctx.OnStop(api.HookProjectBusy.On(ctx.Hooks(), 100, func(_ context.Context, l []string) ([]string, error) {
		m.mu.Lock()
		running := m.run.Running
		m.mu.Unlock()
		if running {
			l = append(l, "knowledge build")
		}
		return l, nil
	}, hooks.WithOwner(ID)))
	invalidate := func(bus.Event) { m.invalidate() }
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicFilesChanged, invalidate))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicFileSaved, invalidate))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectOpened, func(bus.Event) { m.projectChanged() }))
	ctx.OnStop(ctx.Bus().Subscribe("settings.changed", func(bus.Event) { m.invalidate(); m.publishStatus() }))
	return nil
}

// Stop cancels a running build.
func (m *Module) Stop() error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	m.closeStore()
	return nil
}

func (m *Module) application() *api.App { return m.app.Load() }

func (m *Module) publish(topic string, v any) {
	if b := m.bus.Load(); b != nil {
		b.Publish(topic, v)
	}
}

func setting[T any](m *Module, key string, def T) T {
	app := m.application()
	if app == nil {
		return def
	}
	return api.Setting(app, key, def)
}

func (m *Module) invalidate() {
	m.mu.Lock()
	m.planCache = nil
	m.mu.Unlock()
}

func (m *Module) projectChanged() {
	m.invalidate()
	m.closeStore()
}

// dir is the knowledge directory of the open project.
func (m *Module) dir() (string, error) {
	app := m.application()
	if app == nil {
		return "", api.Errorf(api.CodeFailed, "knowledge: module not started")
	}
	if _, err := app.RequireProject(); err != nil {
		return "", err
	}
	ps := app.ProjectSettings()
	if ps == nil || ps.Dir() == "" {
		return "", api.Errorf(api.CodeFailed, "knowledge: the project has no settings directory")
	}
	return filepath.Join(ps.Dir(), "knowledge"), nil
}

// openStore returns the vector store of the project (opened once).
func (m *Module) openStore() (kn.Store, error) {
	dir, err := m.dir()
	if err != nil {
		return nil, err
	}
	vdir := filepath.Join(dir, "vectors")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil && m.storeDir == vdir {
		return m.store, nil
	}
	if m.store != nil {
		_ = m.store.Close()
	}
	s, err := kn.OpenFileStore(vdir)
	if err != nil {
		return nil, err
	}
	m.store, m.storeDir = s, vdir
	return s, nil
}

func (m *Module) closeStore() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		_ = m.store.Close()
		m.store, m.storeDir = nil, ""
	}
}

// embedConfig reads the embedding settings (model overrides once).
func (m *Module) embedConfig(model string) kn.EmbedConfig {
	cfg := kn.EmbedConfig{
		Provider: setting(m, SettingProvider, kn.ProviderOllama),
		Endpoint: strings.TrimSpace(setting(m, SettingEndpoint, kn.DefaultEndpoint)),
		Model:    strings.TrimSpace(setting(m, SettingEmbedModel, kn.DefaultModel)),
		APIKey:   setting(m, SettingAPIKey, ""),
		Client:   m.opts.HTTPClient,
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv(APIKeyEnv)
	}
	if model != "" {
		cfg.Model = model
	}
	if cfg.Provider != kn.ProviderOpenAI {
		cfg.Provider = kn.ProviderOllama
		cfg.APIKey = ""
	}
	return cfg
}

// localEndpoint tells whether the endpoint is this machine.
func localEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	switch h := u.Hostname(); h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// agentCommand returns the agent command line and argv (the agent
// plugin's setting).
func (m *Module) agentCommand() (string, []string) {
	line := strings.TrimSpace(setting(m, agentCommandKey, defaultCommand))
	if line == "" {
		line = defaultCommand
	}
	argv, err := run.SplitWords(line)
	if err != nil || len(argv) == 0 {
		argv = strings.Fields(line)
	}
	return line, argv
}

// agentModel is the pipeline's model ("" = the agent's default).
func (m *Module) agentModel() string {
	if v := strings.TrimSpace(setting(m, SettingModel, "")); v != "" {
		return v
	}
	return strings.TrimSpace(setting(m, agentModelKey, ""))
}

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

// gitHead returns the commit of the project ("" without git).
func gitHead(ctx context.Context, root string) string {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return ""
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, git, "-C", root, "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// agentServer is the MCP server given to the agent's sessions: Tiegel
// itself in headless mode, when the knowledge exists and the setting is
// on.
func (m *Module) agentServer() (api.AgentMCPServer, bool) {
	if !setting(m, SettingGiveAgent, true) {
		return api.AgentMCPServer{}, false
	}
	dir, err := m.dir()
	if err != nil {
		return api.AgentMCPServer{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err != nil {
		return api.AgentMCPServer{}, false
	}
	cmd, args, err := m.mcpCommand()
	if err != nil {
		return api.AgentMCPServer{}, false
	}
	return api.AgentMCPServer{Name: "tiegel-knowledge", Command: cmd, Args: args}, true
}

// mcpCommand is the command line of the knowledge MCP server.
func (m *Module) mcpCommand() (string, []string, error) {
	app := m.application()
	root, err := app.RequireProject()
	if err != nil {
		return "", nil, err
	}
	exe, err := m.opts.Executable()
	if err != nil {
		return "", nil, api.Errorf(api.CodeFailed, "tiegel executable: %w", err)
	}
	args := []string{"--headless", "mcp", "knowledge", "--project", root}
	if cd := app.ConfigDir(); cd != "" {
		args = append(args, "--config-dir", cd)
	}
	return exe, args, nil
}
