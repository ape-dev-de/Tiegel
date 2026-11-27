// Package agentchat is the AI agent sidebar: a header with the agent,
// model choice and "New Session", a one-line summary of the permission
// settings, the transcript (user messages, agent messages as Markdown,
// collapsed thoughts, tool calls as compact expandable rows, the plan,
// notices such as "Blocked: git commit"), permission cards with the
// agent's options (Allow once / Always / Reject), the list of files the
// agent changed with line counts, "Revert" per file and "Revert All"
// (confirmed), and a multi-line input (Enter sends, Shift+Enter starts a
// new line, Escape cancels the running turn).
//
// The organism holds no backend knowledge: the host fills the exported
// data fields every frame (or when they change) and turns the Events of
// Update into API calls (agent.*).
package agentchat

// Kind is the kind of a transcript entry.
type Kind int

// Entry kinds.
const (
	User Kind = iota
	Agent
	Thought
	Tool
	Plan
	Permission
	Notice
)

// Level is the level of a notice.
type Level int

// Notice levels.
const (
	Info Level = iota
	Blocked
	Error
)

// Entry is one transcript entry.
type Entry struct {
	// Key identifies the entry across updates (its transcript index).
	Key  int
	Kind Kind
	// Text is the message, thought or notice (Markdown for Agent).
	Text  string
	Level Level
	Tool  *ToolCall
	Plan  []PlanItem
	Perm  *Request
}

// ToolCall is a tool call of the agent.
type ToolCall struct {
	Title string
	// Status is pending, in_progress, completed or failed.
	Status  string
	Command string
	Paths   []string
	Output  string
	Diffs   []DiffStat
}

// DiffStat counts changed lines of a file.
type DiffStat struct {
	Path           string
	Added, Removed int
}

// PlanItem is a step of the agent's plan (Status pending, in_progress,
// completed).
type PlanItem struct {
	Text   string
	Status string
}

// Option is an answer of a permission request.
type Option struct {
	ID, Name string
	// Kind is allow_once, allow_always, reject_once or reject_always.
	Kind string
}

// Request is a permission request.
type Request struct {
	ID      string
	Summary string
	// Detail is shown in code style (the command).
	Detail  string
	Options []Option
	// Answered requests show their outcome instead of buttons.
	Answered, Allowed bool
}

// Change is a file changed by the agent.
type Change struct {
	Path string
	// Kind is modified, created or deleted.
	Kind           string
	Added, Removed int
	// Conflict: changed after the agent's last change.
	Conflict bool
}

// Choice is a model.
type Choice struct {
	Value, Name string
}

// NoticeBox is a banner above the transcript (not installed, login
// needed, failure) with an optional action button.
type NoticeBox struct {
	Error  bool
	Title  string
	Detail string
	// Action is the button text ("Install…"); empty: no button.
	Action string
}

// Event is reported by Chat.Update.
type Event interface{ isEvent() }

// SendEvent: the user sent a message (Plan: with plan mode on – the
// host starts a plan instead of prompting the agent).
type SendEvent struct {
	Text string
	Plan bool
}

// PlanModeEvent: the "Plan mode" checkbox was toggled.
type PlanModeEvent struct{ On bool }

// CancelEvent: cancel the running turn (Stop button, Escape).
type CancelEvent struct{}

// NewSessionEvent: start a new session.
type NewSessionEvent struct{}

// SettingsEvent: open the agent settings (policy summary, gear).
type SettingsEvent struct{}

// NoticeActionEvent: the banner's action button was clicked.
type NoticeActionEvent struct{}

// AnswerEvent: a permission request was answered.
type AnswerEvent struct{ RequestID, OptionID string }

// DiffEvent: show the diff of a changed file.
type DiffEvent struct{ Path string }

// OpenEvent: open a file (tool call path).
type OpenEvent struct{ Path string }

// RevertEvent: revert one file.
type RevertEvent struct{ Path string }

// RevertAllEvent: revert every change (after confirmation).
type RevertAllEvent struct{}

// KeepAllEvent: keep all changes and clear the list.
type KeepAllEvent struct{}

// ModelEvent: a model was chosen.
type ModelEvent struct{ Value string }

// LinkEvent: a link in an agent message was clicked.
type LinkEvent struct{ Dest string }

// CopyEvent: "Copy" of a code block in an agent message.
type CopyEvent struct{ Text string }

func (SendEvent) isEvent()         {}
func (PlanModeEvent) isEvent()     {}
func (CancelEvent) isEvent()       {}
func (NewSessionEvent) isEvent()   {}
func (SettingsEvent) isEvent()     {}
func (NoticeActionEvent) isEvent() {}
func (AnswerEvent) isEvent()       {}
func (DiffEvent) isEvent()         {}
func (OpenEvent) isEvent()         {}
func (RevertEvent) isEvent()       {}
func (RevertAllEvent) isEvent()    {}
func (KeepAllEvent) isEvent()      {}
func (ModelEvent) isEvent()        {}
func (LinkEvent) isEvent()         {}
func (CopyEvent) isEvent()         {}
