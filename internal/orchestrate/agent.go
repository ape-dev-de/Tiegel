package orchestrate

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Mode is the permission mode of a session.
type Mode int

const (
	// ReadOnly allows reading and searching only (planning, critic,
	// mediator): edits, shell commands, git and the web are rejected,
	// file writes refused.
	ReadOnly Mode = iota
	// Execute allows edits inside the session's directory and shell
	// commands as the agent's policy says (ask counts as deny unless
	// AllowAsk); git commits and push are always rejected.
	Execute
)

// SessionSpec describes a role session.
type SessionSpec struct {
	// Role labels the transcript and activity.
	Role string
	// Dir is the working directory (the project or the plan worktree).
	Dir  string
	Mode Mode
	// Model is the model to choose ("" = the agent's default).
	Model string
	// PlanID and MCP enable the plan document tools for the session.
	PlanID string
	MCP    bool
}

// Agent creates role sessions. Implementations: ACPAgent (a coding agent
// over ACP), fakes in tests.
type Agent interface {
	NewSession(ctx context.Context, spec SessionSpec) (Session, error)
	// Transcript returns the entries of a session.
	Transcript(sessionID string) []api.AgentEntry
	// Blocked counts rejected tool calls.
	Blocked() int
	Close() error
}

// Session is one conversation with the agent.
type Session interface {
	ID() string
	// Prompt sends a turn and returns the agent's message text.
	Prompt(ctx context.Context, text string) (string, error)
	// SetRole relabels the session (one shared session for all roles).
	SetRole(role string, mode Mode)
	Close()
}
