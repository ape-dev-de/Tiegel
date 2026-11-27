package api

import (
	"context"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// Prompt context of the coding agent (D571): before a prompt is sent,
// the agent module asks other modules for context through
// HookAgentPromptContext – the knowledge module adds the hits of a
// knowledge search (RAG) and, with the first prompt of a session, the
// instruction how to treat the knowledge. The agent module sends the
// instructions and blocks before the user's text and records the blocks
// on the user's transcript entry (AgentEntry.Context) for transparency.
// Without a provider nothing is added.

// Context modes of the project knowledge (setting knowledge.agentMode;
// agent.eval compares them). The agent module passes the mode of an
// evaluation run through the context (WithAgentContextMode) without
// interpreting it.
const (
	// AgentContextOff: the agent gets no project knowledge.
	AgentContextOff = "off"
	// AgentContextMCP: the knowledge MCP server is given to the session.
	AgentContextMCP = "mcp"
	// AgentContextRAG: every prompt gets the hits of a knowledge search.
	AgentContextRAG = "rag"
	// AgentContextBoth: search hits and the MCP server.
	AgentContextBoth = "both"
)

// AgentContextModes are the valid context modes.
var AgentContextModes = []string{AgentContextOff, AgentContextMCP, AgentContextRAG, AgentContextBoth}

// AgentContextHit is one search hit in a context block.
type AgentContextHit struct {
	// Title is the page title, Heading the section path inside it.
	Title   string `json:"title"`
	Heading string `json:"heading,omitempty"`
	// Unit is the page ID; Path the page relative to the knowledge
	// directory, File absolute, Anchor the heading anchor.
	Unit   string `json:"unit,omitempty"`
	Path   string `json:"path,omitempty"`
	File   string `json:"file,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	// Sources are the cited code locations (file:line, or files).
	Sources     []string  `json:"sources,omitempty"`
	GeneratedAt time.Time `json:"generatedAt,omitzero"`
	// Outdated is set when a source file of the page changed since it was
	// generated; Changed lists those files.
	Outdated bool     `json:"outdated,omitempty"`
	Changed  []string `json:"changed,omitempty"`
	Snippet  string   `json:"snippet"`
	Score    float64  `json:"score,omitempty"`
}

// AgentContextBlock is context a module added to a prompt.
type AgentContextBlock struct {
	// Source names the provider ("knowledge"); Title is the header shown
	// to the user ("Context from project knowledge").
	Source string `json:"source"`
	Title  string `json:"title"`
	// Text is exactly what was sent to the agent.
	Text string            `json:"text"`
	Hits []AgentContextHit `json:"hits,omitempty"`
	// Tokens estimates the size of Text (four characters per token).
	Tokens int `json:"tokens"`
	// Mode is the search mode (hybrid, keyword); Note explains a
	// fallback.
	Mode string `json:"mode,omitempty"`
	Note string `json:"note,omitempty"`
}

// AgentPromptContext is the value of HookAgentPromptContext: the prompt
// (read-only for hooks) and what hooks add.
type AgentPromptContext struct {
	Prompt string `json:"prompt"`
	// First is set for the first prompt of a session.
	First bool `json:"first,omitempty"`
	// Instructions are sent before the blocks (providers add them with
	// the first prompt only).
	Instructions []string            `json:"instructions,omitempty"`
	Blocks       []AgentContextBlock `json:"blocks,omitempty"`
}

// HookAgentPromptContext collects context for an agent prompt (filter;
// hooks append to Instructions and Blocks and must not change Prompt).
var HookAgentPromptContext = hooks.NewFilter[AgentPromptContext]("agent.promptContext")

type agentContextModeKey struct{}

// WithAgentContextMode returns ctx carrying a context mode that
// overrides the setting (agent.eval runs one mode per session).
func WithAgentContextMode(ctx context.Context, mode string) context.Context {
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, agentContextModeKey{}, mode)
}

// AgentContextModeFrom returns the context mode carried by ctx ("" =
// use the setting).
func AgentContextModeFrom(ctx context.Context) string {
	s, _ := ctx.Value(agentContextModeKey{}).(string)
	return s
}

// ---- evaluation -----------------------------------------------------------

// TopicAgentEval: an evaluation run started or ended (AgentEvalProgress).
const TopicAgentEval = "agent.eval"

// AgentEvalParams are the params of agent.eval.
type AgentEvalParams struct {
	// Questions are asked in every mode; Modes are context modes (default
	// off, mcp, rag, both); Runs repeats each question (default 1).
	Questions []string `json:"questions"`
	Modes     []string `json:"modes,omitempty"`
	Runs      int      `json:"runs,omitempty"`
	// Confirm runs the evaluation (it costs tokens); without it only the
	// plan is returned.
	Confirm bool `json:"confirm,omitempty"`
	// TimeoutSec bounds one run (default 600).
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

// AgentEvalPlan says what an evaluation would run.
type AgentEvalPlan struct {
	Modes     []string `json:"modes"`
	Questions []string `json:"questions"`
	Runs      int      `json:"runs"`
	// Total is modes × questions × runs agent turns.
	Total int `json:"total"`
	// Agent is the agent command, Model the configured model.
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
	// Note says that the runs are billed by the agent's provider.
	Note string `json:"note"`
}

// AgentEvalRun is the measurement of one run.
type AgentEvalRun struct {
	Mode     string `json:"mode"`
	Question int    `json:"question"`
	Run      int    `json:"run"`
	// N numbers the runs of a mode (question-major), the answer file is
	// <mode>-<n>.md.
	N          int    `json:"n"`
	DurationMS int64  `json:"durationMs"`
	StopReason string `json:"stopReason,omitempty"`
	Error      string `json:"error,omitempty"`
	// Tools counts the tool calls by kind (read, grep, glob, shell,
	// knowledge_search …, other); ToolCalls is their sum, Shell the shell
	// calls, Blocked the rejected requests.
	Tools     map[string]int `json:"tools"`
	ToolCalls int            `json:"toolCalls"`
	Shell     int            `json:"shell"`
	Blocked   int            `json:"blocked,omitempty"`
	// Chars counts the streamed message and thought characters, Tokens
	// estimates them (Chars/4).
	Chars  int `json:"chars"`
	Tokens int `json:"tokens"`
	// ContextTokens estimates the injected context (RAG) of the prompt.
	ContextTokens int `json:"contextTokens,omitempty"`
	// Cites: the answer names code locations (file:line); Citations
	// counts them.
	Cites     bool   `json:"cites"`
	Citations int    `json:"citations"`
	Answer    string `json:"answer"`
	File      string `json:"file,omitempty"`
}

// AgentEvalSummary aggregates the runs of a mode.
type AgentEvalSummary struct {
	Mode          string  `json:"mode"`
	Runs          int     `json:"runs"`
	AvgDurationMS int64   `json:"avgDurationMs"`
	AvgToolCalls  float64 `json:"avgToolCalls"`
	AvgShell      float64 `json:"avgShell"`
	AvgKnowledge  float64 `json:"avgKnowledge"`
	AvgTokens     float64 `json:"avgTokens"`
	AvgContext    float64 `json:"avgContext"`
	// CiteRate is the share of runs whose answer cites file:line.
	CiteRate float64 `json:"citeRate"`
	Errors   int     `json:"errors,omitempty"`
}

// AgentEvalResult is the result of agent.eval.
type AgentEvalResult struct {
	Plan AgentEvalPlan `json:"plan"`
	// Ran is false when only the plan was returned (no confirm).
	Ran     bool               `json:"ran"`
	Runs    []AgentEvalRun     `json:"runs,omitempty"`
	Summary []AgentEvalSummary `json:"summary,omitempty"`
	// Dir holds the answers (<config>/projects/<hash>/eval/<timestamp>).
	Dir string `json:"dir,omitempty"`
}

// AgentEvalProgress is the payload of agent.eval.
type AgentEvalProgress struct {
	Index int           `json:"index"`
	Total int           `json:"total"`
	Mode  string        `json:"mode"`
	Done  bool          `json:"done,omitempty"`
	Run   *AgentEvalRun `json:"run,omitempty"`
}
