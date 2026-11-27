package acp

import "encoding/json"

// Protocol methods.
const (
	MethodInitialize        = "initialize"
	MethodAuthenticate      = "authenticate"
	MethodSessionNew        = "session/new"
	MethodSessionLoad       = "session/load"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionCancel     = "session/cancel"
	MethodSessionUpdate     = "session/update"
	MethodSetConfigOption   = "session/set_config_option"
	MethodRequestPermission = "session/request_permission"
	MethodReadTextFile      = "fs/read_text_file"
	MethodWriteTextFile     = "fs/write_text_file"
)

// ProtocolVersion is the ACP version this client speaks.
const ProtocolVersion = 1

// Implementation names a client or agent.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// FSCapabilities tell the agent which file methods the client serves.
type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// ClientCapabilities are sent with initialize.
type ClientCapabilities struct {
	FS       FSCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

// InitializeRequest are the params of initialize.
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// PromptCapabilities tell which content the agent accepts in prompts.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// AgentCapabilities are returned by initialize.
type AgentCapabilities struct {
	LoadSession        bool               `json:"loadSession,omitempty"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
}

// AuthMethod is a way to log in, e.g. "Run `opencode auth login`".
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// InitializeResponse is the result of initialize.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
}

// NewSessionRequest are the params of session/new.
type NewSessionRequest struct {
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

// EnvVariable is an environment variable of an MCP server.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServerStdio is an MCP server the agent starts as child process and
// talks to over stdio (supported by every ACP agent).
type MCPServerStdio struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

// Value is a config option value: agents send strings, other scalars are
// kept in their JSON text form.
type Value string

// UnmarshalJSON accepts any JSON scalar.
func (v *Value) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*v = Value(s)
		return nil
	}
	if string(b) == "null" {
		*v = ""
		return nil
	}
	*v = Value(b)
	return nil
}

// ConfigOptionValue is one choice of a select option.
type ConfigOptionValue struct {
	Value       Value  `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ConfigOption is a session setting the agent offers (model, effort,
// mode …); Category "model" marks the model choice.
type ConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"`
	CurrentValue Value               `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options,omitempty"`
}

// NewSessionResponse is the result of session/new.
type NewSessionResponse struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// LoadSessionRequest are the params of session/load (agents with
// AgentCapabilities.LoadSession): the agent restores the session and
// replays its conversation as session/update notifications before it
// answers.
type LoadSessionRequest struct {
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

// LoadSessionResponse is the result of session/load.
type LoadSessionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// SetConfigOptionRequest are the params of session/set_config_option.
type SetConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// SetConfigOptionResponse is its result: all options with new values.
type SetConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// AuthenticateRequest are the params of authenticate.
type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

// EmbeddedResource is the resource of a "resource" content block.
type EmbeddedResource struct {
	URI      string `json:"uri"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ContentBlock is prompt or message content: "text", "resource" (an
// embedded file), "resource_link", "image".
type ContentBlock struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Resource *EmbeddedResource `json:"resource,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Name     string            `json:"name,omitempty"`
	MimeType string            `json:"mimeType,omitempty"`
	Data     string            `json:"data,omitempty"`
}

// TextBlock returns a text content block.
func TextBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// PromptRequest are the params of session/prompt.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// Stop reasons of a prompt turn.
const (
	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
	StopCancelled = "cancelled"
)

// PromptResponse is the result of session/prompt (the end of the turn).
type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

// CancelNotification are the params of session/cancel.
type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// Session update kinds (SessionUpdate.Kind).
const (
	UpdateUserMessage     = "user_message_chunk"
	UpdateAgentMessage    = "agent_message_chunk"
	UpdateAgentThought    = "agent_thought_chunk"
	UpdateToolCall        = "tool_call"
	UpdateToolCallUpdate  = "tool_call_update"
	UpdatePlan            = "plan"
	UpdateCommands        = "available_commands_update"
	UpdateConfigOptions   = "config_option_update"
	UpdateCurrentModeName = "current_mode_update"
)

// Tool call statuses.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Tool kinds.
const (
	KindRead    = "read"
	KindEdit    = "edit"
	KindDelete  = "delete"
	KindMove    = "move"
	KindSearch  = "search"
	KindExecute = "execute"
	KindThink   = "think"
	KindFetch   = "fetch"
	KindOther   = "other"
)

// Location is a file a tool call works on.
type Location struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

// ToolCallContent is produced by a tool call: "content" (a content
// block), "diff" (a file change; OldText nil for a new file) or
// "terminal".
type ToolCallContent struct {
	Type       string        `json:"type"`
	Content    *ContentBlock `json:"content,omitempty"`
	Path       string        `json:"path,omitempty"`
	OldText    *string       `json:"oldText,omitempty"`
	NewText    *string       `json:"newText,omitempty"`
	TerminalID string        `json:"terminalId,omitempty"`
}

// ToolCall is a tool_call update, a tool_call_update (fields left empty
// were not sent and keep their value) or the tool call of a permission
// request.
type ToolCall struct {
	ToolCallID string `json:"toolCallId"`
	// Name is the agent's tool name (an opencode extension: "edit",
	// "write", "shell", …).
	Name      string            `json:"name,omitempty"`
	Title     string            `json:"title,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Status    string            `json:"status,omitempty"`
	Content   []ToolCallContent `json:"content,omitempty"`
	Locations []Location        `json:"locations,omitempty"`
	RawInput  json.RawMessage   `json:"rawInput,omitempty"`
	RawOutput json.RawMessage   `json:"rawOutput,omitempty"`
}

// PlanEntry is one step of the agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// SlashCommand is a slash command the agent offers.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionUpdate is the update of a session/update notification. Kind
// tells which of the accessors applies.
type SessionUpdate struct {
	Kind string
	Raw  json.RawMessage
}

// UnmarshalJSON keeps the raw update for the typed accessors.
func (u *SessionUpdate) UnmarshalJSON(b []byte) error {
	var k struct {
		Kind string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return err
	}
	u.Kind, u.Raw = k.Kind, append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON writes the raw update.
func (u SessionUpdate) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return json.Marshal(map[string]string{"sessionUpdate": u.Kind})
	}
	return u.Raw, nil
}

// Chunk returns the content of a message or thought chunk.
func (u SessionUpdate) Chunk() ContentBlock {
	var v struct {
		Content ContentBlock `json:"content"`
	}
	_ = json.Unmarshal(u.Raw, &v)
	return v.Content
}

// ToolCall returns the tool call of a tool_call or tool_call_update.
func (u SessionUpdate) ToolCall() ToolCall {
	var t ToolCall
	_ = json.Unmarshal(u.Raw, &t)
	return t
}

// Plan returns the entries of a plan update.
func (u SessionUpdate) Plan() []PlanEntry {
	var v struct {
		Entries []PlanEntry `json:"entries"`
	}
	_ = json.Unmarshal(u.Raw, &v)
	return v.Entries
}

// Commands returns the commands of an available_commands_update.
func (u SessionUpdate) Commands() []SlashCommand {
	var v struct {
		Commands []SlashCommand `json:"availableCommands"`
	}
	_ = json.Unmarshal(u.Raw, &v)
	return v.Commands
}

// ConfigOptions returns the options of a config_option_update.
func (u SessionUpdate) ConfigOptions() []ConfigOption {
	var v struct {
		Options []ConfigOption `json:"configOptions"`
	}
	_ = json.Unmarshal(u.Raw, &v)
	return v.Options
}

// SessionNotification are the params of session/update.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// Permission option kinds.
const (
	AllowOnce    = "allow_once"
	AllowAlways  = "allow_always"
	RejectOnce   = "reject_once"
	RejectAlways = "reject_always"
)

// PermissionOption is a choice of a permission request.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// RequestPermissionRequest are the params of session/request_permission.
type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// Permission outcomes.
const (
	OutcomeSelected  = "selected"
	OutcomeCancelled = "cancelled"
)

// PermissionOutcome is the user's (or the rules') decision.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// RequestPermissionResponse is the result of session/request_permission.
type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// Selected returns a response choosing option id.
func Selected(id string) RequestPermissionResponse {
	return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: OutcomeSelected, OptionID: id}}
}

// Cancelled returns the response for a cancelled turn.
func Cancelled() RequestPermissionResponse {
	return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: OutcomeCancelled}}
}

// ReadTextFileRequest are the params of fs/read_text_file (Line is
// 1-based; Limit counts lines).
type ReadTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      *int   `json:"line,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
}

// ReadTextFileResponse is the result of fs/read_text_file.
type ReadTextFileResponse struct {
	Content string `json:"content"`
}

// WriteTextFileRequest are the params of fs/write_text_file.
type WriteTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}
