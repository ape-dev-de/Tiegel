package api

import (
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// Project knowledge contract (D560–D563, D565): the knowledge module
// owns the namespace knowledge.*: knowledge.status, knowledge.plan,
// knowledge.build, knowledge.cancel, knowledge.list, knowledge.show,
// knowledge.read, knowledge.questions, knowledge.search,
// knowledge.embed, knowledge.modelStatus, knowledge.pullModel,
// knowledge.mcpConfig. The analysis runs top-down in levels (overview,
// entrypoints, processes, components, questions); reading (status,
// list, show, read, questions, search) works while a build runs.
// Building runs coding agents (paid tokens) and starts only on an
// explicit user action; nothing runs automatically.

// Topics of the knowledge namespace.
const (
	// TopicKnowledgeStatus: pages, run state or store changed (also after
	// every finished page of a running build) (KnowledgeStatus).
	TopicKnowledgeStatus = "knowledge.status"
	// TopicKnowledgeProgress: a page of the running build moved on
	// (KnowledgeProgress).
	TopicKnowledgeProgress = "knowledge.progress"
	// TopicKnowledgeActivity: live activity of a running page – agent
	// thoughts, message chunks, tool calls, steps (KnowledgeActivity;
	// coalesced, at most about ten per second and page).
	TopicKnowledgeActivity = "knowledge.activity"
)

// Kinds of KnowledgeActivity.
const (
	KnowledgeActivityThought = "thought"
	KnowledgeActivityMessage = "message"
	KnowledgeActivityTool    = "tool"
	KnowledgeActivityStep    = "step"
)

// KnowledgeActivity is a piece of live activity of a page of the
// running build (D566).
type KnowledgeActivity struct {
	// Item is the page ID, Level its level.
	Item  string `json:"item"`
	Level string `json:"level,omitempty"`
	// Kind is thought, message, tool or step; Text the (coalesced) text:
	// streamed chunks, the tool's title or the step name.
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Tool and Status describe a tool call (kind read/search/…, status
	// pending/in_progress/completed/failed).
	Tool   string `json:"tool,omitempty"`
	Status string `json:"status,omitempty"`
}

// KnowledgeTranscriptParams are the params of knowledge.transcript.
type KnowledgeTranscriptParams struct {
	Item string `json:"item"`
}

// KnowledgeTranscript is the read-only transcript of a page's agent
// sessions in the running or last build.
type KnowledgeTranscript struct {
	Item  string `json:"item"`
	Level string `json:"level,omitempty"`
	Title string `json:"title,omitempty"`
	// Run identifies the build; File is the log on disk
	// (<knowledge>/runs/<run>/<item>.log, "" when not written).
	Run  string `json:"run,omitempty"`
	File string `json:"file,omitempty"`
	// Lines are the last lines ("» step", "thought: …", "tool: …",
	// message text); Truncated counts dropped older lines.
	Lines     []string `json:"lines"`
	Truncated int      `json:"truncated,omitempty"`
	// Running is set while the page is being built.
	Running bool `json:"running,omitempty"`
}

// Knowledge levels (KnowledgeScopeParams.Level, KnowledgeUnit.Level).
const (
	KnowledgeLevelOverview    = "overview"
	KnowledgeLevelEntryPoints = "entrypoints"
	KnowledgeLevelProcesses   = "processes"
	KnowledgeLevelComponents  = "components"
	KnowledgeLevelQuestions   = "questions"
	KnowledgeLevelAll         = "all"
)

// AgentMCPServer is an MCP server a module offers to the coding agent's
// sessions (stdio: Command with Args and Env).
type AgentMCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Env are KEY=VALUE pairs.
	Env []string `json:"env,omitempty"`
	// ReadOnly marks a server whose tools only read local data: the agent
	// module allows its tool calls without asking, independent of the
	// "Other tools" setting (D571).
	ReadOnly bool `json:"readOnly,omitempty"`
}

// HookAgentMCPServers collects the MCP servers passed to the agent's
// session/new (the knowledge module adds its search server when the
// knowledge base exists and the setting allows it).
var HookAgentMCPServers = hooks.NewFilter[[]AgentMCPServer]("agent.mcpServers")

// KnowledgeUnit is a page of the knowledge base (planned or built).
type KnowledgeUnit struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Level is overview, entrypoints, processes or components.
	Level string `json:"level"`
	// Group is the entry point kind (entrypoints), the handler class or
	// file (processes) or the class (components).
	Group string `json:"group,omitempty"`
	// Dir is kept for old clients (the group of the page).
	Dir string `json:"dir,omitempty"`
	// Files counts the sources of the page.
	Files int `json:"files"`
	// Stale says why the page needs a build ("" = current; new, changed,
	// failed, missing).
	Stale string `json:"stale,omitempty"`
	// MD is the absolute path of the page ("" before the first build),
	// Path the page relative to the knowledge directory.
	MD   string `json:"md,omitempty"`
	Path string `json:"path,omitempty"`
	// Summary is the first sentences of the page; Mechanical marks a
	// page with Tiegel's facts only (the agent failed).
	Summary     string    `json:"summary,omitempty"`
	Mechanical  bool      `json:"mechanical,omitempty"`
	Confidence  float64   `json:"confidence,omitempty"`
	GeneratedAt time.Time `json:"generatedAt,omitzero"`
	Commit      string    `json:"commit,omitempty"`
	Error       string    `json:"error,omitempty"`
	Unverified  int       `json:"unverified,omitempty"`
}

// KnowledgeScopeParams restrict a plan or build.
type KnowledgeScopeParams struct {
	// Scope are project relative directories whose PHP files are
	// analysed (empty: the whole project).
	Scope []string `json:"scope,omitempty"`
	// All rebuilds every page, not only stale ones.
	All bool `json:"all,omitempty"`
	// Level is overview, entrypoints, processes, components, questions
	// or all ("" = all; a comma separated list works too).
	Level string `json:"level,omitempty"`
}

// KnowledgeLevelEstimate is the estimate of one level.
type KnowledgeLevelEstimate struct {
	Level string `json:"level"`
	Title string `json:"title"`
	// Pages counts the pages to build, Total all pages of the level.
	Pages int `json:"pages"`
	Total int `json:"total"`
	Calls int `json:"calls"`
	// InputTokens is the rough estimate of the level.
	InputTokens int `json:"inputTokens"`
	// Later is set when the level is planned only when the run reaches
	// it (components need the process pages); the numbers are an upper
	// bound then.
	Later bool `json:"later,omitempty"`
	// Skipped counts processes left out by knowledge.maxProcesses.
	Skipped int `json:"skipped,omitempty"`
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
	// Levels are the estimates per level (in run order).
	Levels []KnowledgeLevelEstimate `json:"levels,omitempty"`
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
	// Unit is the page ID.
	Unit       string  `json:"unit"`
	Level      string  `json:"level,omitempty"`
	Title      string  `json:"title"`
	Index      int     `json:"index"`
	Total      int     `json:"total"`
	Step       string  `json:"step,omitempty"`
	State      string  `json:"state"`
	Error      string  `json:"error,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Attempt    int     `json:"attempt,omitempty"`
	// StepIndex is the step within the page (1 describe, 2 validate, 3
	// write) of Steps; Started is when the page started.
	StepIndex int       `json:"stepIndex,omitempty"`
	Steps     int       `json:"steps,omitempty"`
	Started   time.Time `json:"started,omitzero"`
	// Tools counts the agent's tool calls, Reads those that read or
	// searched, Chars the streamed characters (about four per token).
	Tools int `json:"tools,omitempty"`
	Reads int `json:"reads,omitempty"`
	Chars int `json:"chars,omitempty"`
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
	// Level is the level parameter of the run; Answered counts the
	// questions answered in it.
	Level    string `json:"level,omitempty"`
	Answered int    `json:"answered,omitempty"`
	// ID identifies the run (transcripts in <knowledge>/runs/<id>/).
	ID string `json:"id,omitempty"`
}

// KnowledgeLevelStatus counts the pages of a level.
type KnowledgeLevelStatus struct {
	Level string `json:"level"`
	Title string `json:"title"`
	// Pages counts written pages, Planned the pages the level has now,
	// Stale those to build, Failed those whose last build failed.
	Pages   int `json:"pages"`
	Planned int `json:"planned"`
	Stale   int `json:"stale"`
	Failed  int `json:"failed"`
}

// KnowledgeQuestionCounts counts the questions of the knowledge base.
type KnowledgeQuestionCounts struct {
	Open     int `json:"open"`
	Answered int `json:"answered"`
	// Answerable are open questions the next "answer questions" run
	// tries.
	Answerable int `json:"answerable"`
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
	// GiveAgent tells whether the agent gets the knowledge (MCP or RAG);
	// AgentMode is the setting knowledge.agentMode (off, mcp, rag, both).
	GiveAgent bool         `json:"giveAgent"`
	AgentMode string       `json:"agentMode,omitempty"`
	Run       KnowledgeRun `json:"run"`
	// Levels count the pages per level; Questions the open and answered
	// questions.
	Levels    []KnowledgeLevelStatus  `json:"levels,omitempty"`
	Questions KnowledgeQuestionCounts `json:"questions"`
	// Legacy is set when the directory holds pages of the old unit
	// format (D560); they are replaced by the next build.
	Legacy bool `json:"legacy,omitempty"`
}

// KnowledgeQuestionsParams are the params of knowledge.questions.
type KnowledgeQuestionsParams struct {
	// Status filters: open, answered or "" (all).
	Status string `json:"status,omitempty"`
}

// KnowledgeQuestion is an open or answered question.
type KnowledgeQuestion struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level string `json:"level"`
	// Item is the page that asked it.
	Item   string   `json:"item"`
	Refs   []string `json:"refs,omitempty"`
	Status string   `json:"status"`
	Answer string   `json:"answer,omitempty"`
	// AnsweredBy is the page or question batch that answered it.
	AnsweredBy string   `json:"answeredBy,omitempty"`
	AnswerRefs []string `json:"answerRefs,omitempty"`
	Attempts   int      `json:"attempts,omitempty"`
}

// KnowledgeQuestionsResult is the result of knowledge.questions.
type KnowledgeQuestionsResult struct {
	Questions []KnowledgeQuestion     `json:"questions"`
	Counts    KnowledgeQuestionCounts `json:"counts"`
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
	// "overview.md", "processes/<id>.md", "questions.md") or a page ID.
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
	// Unit is the page ID, Level its level.
	Unit  string `json:"unit"`
	Level string `json:"level,omitempty"`
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
