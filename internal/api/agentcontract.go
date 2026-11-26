package api

import "time"

// AI agent contract (D540): a coding agent speaking the Agent Client
// Protocol (opencode, …) runs as a child process of the agent module,
// which owns the namespace agent.*: agent.status, agent.start,
// agent.newSession, agent.prompt, agent.cancel, agent.stop,
// agent.transcript, agent.changes, agent.diff, agent.revert,
// agent.acceptChanges, agent.permission, agent.setOption, agent.setModel.
// The types below are what clients (GUI, headless, serve) program
// against.

// Topics of the agent namespace.
const (
	// TopicAgentStatus: state, session, options or permissions changed
	// (AgentStatus).
	TopicAgentStatus = "agent.status"
	// TopicAgentUpdate: a transcript entry was added or changed
	// (AgentUpdateEvent; streaming text arrives as many updates of one
	// entry).
	TopicAgentUpdate = "agent.update"
	// TopicAgentPermission: the agent waits for a decision
	// (AgentPermission; answer with agent.permission).
	TopicAgentPermission = "agent.permission"
	// TopicAgentChanges: the files changed by the agent changed
	// ([]AgentChange).
	TopicAgentChanges = "agent.changes"
	// TopicAgentTurnEnded: a prompt turn ended (AgentTurnResult).
	TopicAgentTurnEnded = "agent.turnEnded"
)

// Agent states (AgentStatus.State).
const (
	AgentStopped   = "stopped"
	AgentStarting  = "starting"
	AgentReady     = "ready"
	AgentPrompting = "prompting"
	AgentFailed    = "failed"
)

// Permission categories of tool calls (AgentPermission.Category).
const (
	AgentCatEdit      = "edit"
	AgentCatShell     = "shell"
	AgentCatGitCommit = "git-commit"
	AgentCatGitPush   = "git-push"
	AgentCatWeb       = "web"
	AgentCatOther     = "other"
)

// Permission policies (AgentPolicy values).
const (
	PolicyAllow = "allow"
	PolicyAsk   = "ask"
	PolicyDeny  = "deny"
)

// AgentPolicy is what the settings say for each category, in words for
// the user ("Commits: blocked · Shell: ask").
type AgentPolicy struct {
	Edit      string `json:"edit"`
	Shell     string `json:"shell"`
	GitCommit string `json:"gitCommit"`
	GitPush   string `json:"gitPush"`
	Web       string `json:"web"`
	Other     string `json:"other"`
	// Summary is a one-line description of the policy.
	Summary string `json:"summary"`
	// Rules is the free-text guidance sent with the first prompt.
	Rules string `json:"rules,omitempty"`
}

// AgentChoice is one value of a select option.
type AgentChoice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// AgentOption is a session option of the agent (model, effort, mode …).
type AgentOption struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Category string        `json:"category,omitempty"`
	Value    string        `json:"value"`
	Choices  []AgentChoice `json:"choices,omitempty"`
}

// AgentStatus is the result of agent.status (and the agent.status event).
type AgentStatus struct {
	// Command is the configured command line ("opencode acp").
	Command string `json:"command"`
	// Path is the resolved executable ("" = not found).
	Path string `json:"path,omitempty"`
	// Available is true when the executable was found.
	Available bool `json:"available"`
	// Install names the plugin whose install recipe adds the agent
	// (install.plan / install.run, only on user action); InstallHint
	// explains the manual way.
	Install     string `json:"install,omitempty"`
	InstallHint string `json:"installHint,omitempty"`
	// State is stopped, starting, ready, prompting or failed.
	State string `json:"state"`
	// Agent and Version name the running agent (from initialize).
	Agent   string `json:"agent,omitempty"`
	Version string `json:"version,omitempty"`
	// SessionID is the agent's session.
	SessionID string `json:"sessionId,omitempty"`
	// Options are the session options; Model is the value of the model
	// option.
	Options []AgentOption `json:"options,omitempty"`
	Model   string        `json:"model,omitempty"`
	// Commands are the agent's slash commands.
	Commands []string `json:"commands,omitempty"`
	// Policy are the permission settings.
	Policy AgentPolicy `json:"policy"`
	// Pending counts permission requests waiting for an answer.
	Pending int `json:"pending,omitempty"`
	// Changes counts files changed by the agent and not yet reverted or
	// accepted.
	Changes int `json:"changes,omitempty"`
	// AuthRequired is set when the agent needs a login; AuthHint says
	// how (e.g. "Run `opencode auth login` in the terminal").
	AuthRequired bool   `json:"authRequired,omitempty"`
	AuthHint     string `json:"authHint,omitempty"`
	// Error explains the failed state (with the agent's last stderr).
	Error string `json:"error,omitempty"`
}

// Transcript entry kinds (AgentEntry.Kind).
const (
	EntryUser       = "user"
	EntryAgent      = "agent"
	EntryThought    = "thought"
	EntryTool       = "tool"
	EntryPlan       = "plan"
	EntryPermission = "permission"
	EntryNotice     = "notice"
)

// Notice levels (AgentEntry.Level).
const (
	NoticeInfo    = "info"
	NoticeBlocked = "blocked"
	NoticeError   = "error"
)

// AgentToolCall is a tool call of the agent.
type AgentToolCall struct {
	ID string `json:"id"`
	// Name is the agent's tool name when known (edit, write, shell …).
	Name   string `json:"name,omitempty"`
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
	// Command is the shell command of an execute call.
	Command string `json:"command,omitempty"`
	// Paths are the files the call works on (project relative).
	Paths []string `json:"paths,omitempty"`
	// Input is the raw input (JSON, shortened), Output the text output
	// (shortened).
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	// Diffs summarise the file changes of the call.
	Diffs []AgentDiffStat `json:"diffs,omitempty"`
}

// AgentDiffStat counts the changed lines of a file.
type AgentDiffStat struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// AgentPlanEntry is a step of the agent's plan.
type AgentPlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status,omitempty"`
}

// AgentPermOption is a choice of a permission request.
type AgentPermOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is allow_once, allow_always, reject_once or reject_always.
	Kind string `json:"kind"`
}

// AgentPermission is a permission request of the agent.
type AgentPermission struct {
	// RequestID identifies the request for agent.permission.
	RequestID string `json:"requestId"`
	// Category is edit, shell, git-commit, git-push, web or other;
	// Summary says in words what the agent wants ("Run: npm test").
	Category string            `json:"category"`
	Summary  string            `json:"summary"`
	Tool     AgentToolCall     `json:"tool"`
	Options  []AgentPermOption `json:"options"`
	// Answer is the chosen option ID ("" while pending, "cancelled"),
	// By who decided ("user" or "settings"), Allowed the outcome.
	Answer  string `json:"answer,omitempty"`
	By      string `json:"by,omitempty"`
	Allowed bool   `json:"allowed,omitempty"`
}

// AgentEntry is one entry of the transcript.
type AgentEntry struct {
	// Index is the position in the transcript (stable).
	Index int `json:"index"`
	// Kind is user, agent, thought, tool, plan, permission or notice.
	Kind string `json:"kind"`
	// Text is the message, thought or notice text (Markdown for agent
	// messages).
	Text string `json:"text,omitempty"`
	// Level is the notice level (info, blocked, error).
	Level      string           `json:"level,omitempty"`
	Tool       *AgentToolCall   `json:"tool,omitempty"`
	Plan       []AgentPlanEntry `json:"plan,omitempty"`
	Permission *AgentPermission `json:"permission,omitempty"`
	Time       time.Time        `json:"time"`
}

// AgentUpdateEvent is the payload of agent.update.
type AgentUpdateEvent struct {
	Entry AgentEntry `json:"entry"`
	// Delta is the text appended by this update (streaming chunks).
	Delta string `json:"delta,omitempty"`
}

// Change kinds (AgentChange.Kind).
const (
	ChangeModified = "modified"
	ChangeCreated  = "created"
	ChangeDeleted  = "deleted"
)

// AgentChange is a file the agent changed in this session.
type AgentChange struct {
	Path string `json:"path"`
	File string `json:"file"`
	// Kind is modified, created or deleted (compared with the content
	// before the agent's first change).
	Kind    string `json:"kind"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// Conflict is set when the file changed after the agent's last
	// change (by the user or another program): revert needs force.
	Conflict bool `json:"conflict,omitempty"`
}

// AgentPathParams address a changed file.
type AgentPathParams struct {
	Path string `json:"path"`
}

// AgentDiff is the result of agent.diff: the file before the agent's
// first change and now.
type AgentDiff struct {
	AgentChange
	Original string `json:"original"`
	Current  string `json:"current"`
	// Unified is a unified diff of Original and Current.
	Unified string `json:"unified"`
}

// AgentRevertParams are the params of agent.revert.
type AgentRevertParams struct {
	// Path is one changed file; All reverts every change.
	Path string `json:"path,omitempty"`
	All  bool   `json:"all,omitempty"`
	// Force reverts files that changed after the agent's last change.
	Force bool `json:"force,omitempty"`
}

// AgentConflict is a file revert refused.
type AgentConflict struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// AgentRevertResult is the result of agent.revert.
type AgentRevertResult struct {
	Reverted  []string        `json:"reverted"`
	Conflicts []AgentConflict `json:"conflicts,omitempty"`
}

// AgentPromptParams are the params of agent.prompt.
type AgentPromptParams struct {
	Text string `json:"text"`
	// Files are attached as context (project relative or absolute;
	// open documents with their unsaved text).
	Files []string `json:"files,omitempty"`
	// Wait blocks until the turn ended (headless); otherwise the call
	// returns when the turn started.
	Wait bool `json:"wait,omitempty"`
	// Ask decides permission requests whose setting is "ask" when no
	// client answers: "" waits for agent.permission, "allow" or "deny"
	// answer at once (headless: deny, --yes: allow). Blocked categories
	// stay blocked.
	Ask string `json:"ask,omitempty"`
}

// AgentTurnResult is the result of agent.prompt (with wait) and the
// payload of agent.turnEnded.
type AgentTurnResult struct {
	// StopReason is end_turn, max_tokens, refusal or cancelled ("" while
	// running).
	StopReason string `json:"stopReason,omitempty"`
	// Started is true when the turn started (without wait).
	Started bool   `json:"started,omitempty"`
	Error   string `json:"error,omitempty"`
	// ByUser is set when the turn was cancelled by the client (not by
	// the agent, e.g. after a rejected permission).
	ByUser bool `json:"byUser,omitempty"`
}

// AgentPermissionParams answer a permission request.
type AgentPermissionParams struct {
	RequestID string `json:"requestId"`
	// OptionID is the chosen option; alternatively Allow/Deny pick the
	// first allow_once / reject_once option.
	OptionID string `json:"optionId,omitempty"`
	Allow    bool   `json:"allow,omitempty"`
	Deny     bool   `json:"deny,omitempty"`
}

// AgentOptionParams set a session option.
type AgentOptionParams struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// AgentModelParams set the model.
type AgentModelParams struct {
	Model string `json:"model"`
}
