package api

import (
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// Project knowledge contract (D560–D563): the knowledge module owns the
// namespace knowledge.*: knowledge.status, knowledge.plan,
// knowledge.build, knowledge.cancel, knowledge.list, knowledge.show,
// knowledge.read, knowledge.search, knowledge.embed,
// knowledge.modelStatus, knowledge.pullModel, knowledge.mcpConfig.
// Building runs coding agents (paid tokens) and starts only on an
// explicit user action; nothing runs automatically.

// Topics of the knowledge namespace.
const (
	// TopicKnowledgeStatus: units, run state or store changed
	// (KnowledgeStatus).
	TopicKnowledgeStatus = "knowledge.status"
	// TopicKnowledgeProgress: a unit of the running build moved on
	// (KnowledgeProgress).
	TopicKnowledgeProgress = "knowledge.progress"
)

// AgentMCPServer is an MCP server a module offers to the coding agent's
// sessions (stdio: Command with Args and Env).
type AgentMCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Env are KEY=VALUE pairs.
	Env []string `json:"env,omitempty"`
}

// HookAgentMCPServers collects the MCP servers passed to the agent's
// session/new (the knowledge module adds its search server when the
// knowledge base exists and the setting allows it).
var HookAgentMCPServers = hooks.NewFilter[[]AgentMCPServer]("agent.mcpServers")

// KnowledgeUnit is a unit of the knowledge base (planned or built).
type KnowledgeUnit struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Dir   string `json:"dir"`
	Files int    `json:"files"`
	// Stale says why the unit needs a build ("" = current; new, changed,
	// failed, missing).
	Stale string `json:"stale,omitempty"`
	// MD is the absolute path of the document ("" before the first
	// build).
	MD          string    `json:"md,omitempty"`
	Confidence  float64   `json:"confidence,omitempty"`
	GeneratedAt time.Time `json:"generatedAt,omitzero"`
	Commit      string    `json:"commit,omitempty"`
	Error       string    `json:"error,omitempty"`
	Unverified  int       `json:"unverified,omitempty"`
}

// KnowledgeScopeParams restrict a plan or build.
type KnowledgeScopeParams struct {
	// Scope are project relative directories (empty: the whole project).
	Scope []string `json:"scope,omitempty"`
	// All rebuilds every unit, not only stale ones.
	All bool `json:"all,omitempty"`
}

// KnowledgeEstimate tells the user what a build costs before it runs.
type KnowledgeEstimate struct {
	// Units and Files count the work; Calls the agent prompts (three per
	// unit, more when answers need repair).
	Units int `json:"units"`
	Files int `json:"files"`
	Calls int `json:"calls"`
	// InputTokens is a rough estimate of the source the agents read
	// (characters / 4, read about twice per unit).
	InputTokens int `json:"inputTokens"`
	// Agent and Model name who does the work (Model "" = the agent's
	// default).
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
	// Note says in words that the configured agent's tokens are used.
	Note string `json:"note"`
}

// KnowledgePlan is the result of knowledge.plan.
type KnowledgePlan struct {
	// Units are the units the build would run.
	Units    []KnowledgeUnit   `json:"units"`
	Estimate KnowledgeEstimate `json:"estimate"`
	// Total counts all units of the scope (also the current ones).
	Total int `json:"total"`
}

// KnowledgeBuildParams are the params of knowledge.build.
type KnowledgeBuildParams struct {
	KnowledgeScopeParams
	// Units restricts the build to these unit IDs.
	Units []string `json:"units,omitempty"`
	// Wait blocks until the build ended (headless).
	Wait bool `json:"wait,omitempty"`
	// NoEmbed skips embedding after the build.
	NoEmbed bool `json:"noEmbed,omitempty"`
}

// KnowledgeProgress is a progress event of a build.
type KnowledgeProgress struct {
	Unit       string  `json:"unit"`
	Title      string  `json:"title"`
	Index      int     `json:"index"`
	Total      int     `json:"total"`
	Step       string  `json:"step,omitempty"`
	State      string  `json:"state"`
	Error      string  `json:"error,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Attempt    int     `json:"attempt,omitempty"`
}

// KnowledgeRun is the state of the running (or last) build.
type KnowledgeRun struct {
	Running bool `json:"running"`
	// Phase is "agents" or "embedding" while running.
	Phase     string              `json:"phase,omitempty"`
	Units     []KnowledgeProgress `json:"units,omitempty"`
	Done      int                 `json:"done"`
	Failed    int                 `json:"failed"`
	Cancelled bool                `json:"cancelled,omitempty"`
	Errors    []string            `json:"errors,omitempty"`
	Started   time.Time           `json:"started,omitzero"`
	Ended     time.Time           `json:"ended,omitzero"`
	// Embedded and EmbedTotal report the embedding phase.
	Embedded   int    `json:"embedded,omitempty"`
	EmbedTotal int    `json:"embedTotal,omitempty"`
	EmbedError string `json:"embedError,omitempty"`
}

// KnowledgeEmbedding describes the embedding configuration and store.
type KnowledgeEmbedding struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	// Local is true for Ollama on this machine (contents stay local).
	Local bool `json:"local"`
	// Chunks, Embedded and Bytes describe the store; StoreModel is the
	// model of the stored vectors.
	Chunks     int    `json:"chunks"`
	Embedded   int    `json:"embedded"`
	Bytes      int64  `json:"bytes"`
	StoreModel string `json:"storeModel,omitempty"`
	Recovered  string `json:"recovered,omitempty"`
}

// KnowledgeStatus is the result of knowledge.status.
type KnowledgeStatus struct {
	// Dir is the knowledge directory (outside the project); Exists is
	// true once a document was written.
	Dir    string `json:"dir"`
	Exists bool   `json:"exists"`
	// Units counts built units, Stale the units that need a build
	// (including new ones), Failed units whose last build failed.
	Units   int       `json:"units"`
	Stale   int       `json:"stale"`
	Failed  int       `json:"failed"`
	LastRun time.Time `json:"lastRun,omitzero"`
	// Agent is the agent command, Model the pipeline's model.
	Agent     string             `json:"agent"`
	Model     string             `json:"model,omitempty"`
	Embedding KnowledgeEmbedding `json:"embedding"`
	// GiveAgent tells whether the agent gets the knowledge (MCP).
	GiveAgent bool         `json:"giveAgent"`
	Run       KnowledgeRun `json:"run"`
}

// KnowledgeUnitParams address a unit.
type KnowledgeUnitParams struct {
	Unit string `json:"unit"`
}

// KnowledgeDoc is the result of knowledge.show and knowledge.read.
type KnowledgeDoc struct {
	// Path is the document relative to the knowledge directory, File
	// the absolute path.
	Path    string `json:"path"`
	File    string `json:"file"`
	Content string `json:"content"`
}

// KnowledgeReadParams are the params of knowledge.read.
type KnowledgeReadParams struct {
	// Path is relative to the knowledge directory ("index.md",
	// "units/src-billing.md") or a unit ID.
	Path string `json:"path"`
}

// KnowledgeSearchParams are the params of knowledge.search.
type KnowledgeSearchParams struct {
	Query string `json:"query"`
	// K is the number of results (default 8, at most 50).
	K int `json:"k,omitempty"`
	// KeywordOnly skips the embedding of the query.
	KeywordOnly bool `json:"keywordOnly,omitempty"`
}

// KnowledgeHit is a search result.
type KnowledgeHit struct {
	Score   float64 `json:"score"`
	Vector  float64 `json:"vector,omitempty"`
	Keyword float64 `json:"keyword,omitempty"`
	Unit    string  `json:"unit"`
	// Path is the document relative to the knowledge directory, File
	// absolute; Heading the section path, Anchor its heading anchor.
	Path    string `json:"path"`
	File    string `json:"file"`
	Heading string `json:"heading"`
	Anchor  string `json:"anchor,omitempty"`
	Text    string `json:"text"`
	// Snippet is the plain text of the section (shortened) for lists.
	Snippet string   `json:"snippet"`
	Sources []string `json:"sources,omitempty"`
}

// KnowledgeSearchResult is the result of knowledge.search.
type KnowledgeSearchResult struct {
	Hits []KnowledgeHit `json:"hits"`
	// Mode is "hybrid" or "keyword"; Note explains a fallback.
	Mode string `json:"mode"`
	Note string `json:"note,omitempty"`
}

// KnowledgeEmbedParams are the params of knowledge.embed.
type KnowledgeEmbedParams struct {
	// Model overrides the configured embedding model once.
	Model string `json:"model,omitempty"`
}

// KnowledgeEmbedResult is the result of knowledge.embed.
type KnowledgeEmbedResult struct {
	Chunks   int    `json:"chunks"`
	Embedded int    `json:"embedded"`
	Deleted  int    `json:"deleted"`
	Pending  int    `json:"pending"`
	Model    string `json:"model"`
	Error    string `json:"error,omitempty"`
}

// KnowledgeModelStatus is the result of knowledge.modelStatus.
type KnowledgeModelStatus struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	// Reachable: the server answered; Available: the model is there.
	Reachable bool `json:"reachable"`
	Available bool `json:"available"`
	// PullCommand is shown before a pull ("ollama pull <model>"; Ollama
	// only, "" otherwise).
	PullCommand string `json:"pullCommand,omitempty"`
	Error       string `json:"error,omitempty"`
	// Pulling and PullStatus report a running pull.
	Pulling    bool   `json:"pulling,omitempty"`
	PullStatus string `json:"pullStatus,omitempty"`
}

// KnowledgePullParams are the params of knowledge.pullModel.
type KnowledgePullParams struct {
	// Wait blocks until the pull ended (headless).
	Wait bool `json:"wait,omitempty"`
}

// KnowledgeMCPConfig is the result of knowledge.mcpConfig: how to give
// the knowledge to an MCP client by hand.
type KnowledgeMCPConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Opencode and Claude are configuration snippets.
	Opencode string `json:"opencode"`
	Claude   string `json:"claude"`
}
