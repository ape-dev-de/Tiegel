// Package acptest is a scriptable fake ACP agent for tests: it speaks
// the agent side of the protocol (initialize, session/new,
// session/prompt, session/cancel, session/set_config_option) and runs a
// Script for every prompt, which streams updates and calls back into the
// client (permissions, file access) like a real coding agent.
package acptest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/acp"
)

// Script runs one prompt turn. It returns the stop reason ("" =
// end_turn); a cancelled turn should return acp.StopCancelled.
type Script func(ctx context.Context, t *Turn) string

// Agent is the fake agent. Configure it before Start/Serve.
type Agent struct {
	// Name is reported in initialize (default "Fake Agent").
	Name string
	// AuthRequired makes session/new fail with the auth error.
	AuthRequired bool
	// Models are the choices of the "model" config option (default two).
	Models []string
	// Script runs for each prompt (default: echo the prompt).
	Script Script

	mu       sync.Mutex
	model    string
	mode     string
	sessions int
	prompts  []string
	cancels  map[string]context.CancelFunc
	inits    []acp.InitializeRequest
}

// Prompts returns the texts of all prompts received so far.
func (a *Agent) Prompts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.prompts...)
}

// Inits returns the initialize requests received.
func (a *Agent) Inits() []acp.InitializeRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]acp.InitializeRequest(nil), a.inits...)
}

// Model returns the current model.
func (a *Agent) Model() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

func (a *Agent) models() []string {
	if len(a.Models) > 0 {
		return a.Models
	}
	return []string{"fake/small", "fake/large"}
}

// Start connects a client with handler h to a new in-process run of the
// agent; it has the signature of the agent module's start hook.
func (a *Agent) Start(_ context.Context, _ acp.Command, h acp.Handler) (*acp.Process, error) {
	cr, aw := io.Pipe() // agent → client
	ar, cw := io.Pipe() // client → agent
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Serve(ar, aw)
		_ = aw.Close()
	}()
	return acp.Attach(cr, cw, h, done), nil
}

// Serve speaks the agent side on r/w until r ends.
func (a *Agent) Serve(r io.Reader, w io.Writer) {
	var conn *acp.Conn
	ready := make(chan struct{})
	conn = acp.NewConn(r, w, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		<-ready
		return a.handle(ctx, conn, method, params)
	}, func(method string, params json.RawMessage) {
		if method != acp.MethodSessionCancel {
			return
		}
		var c acp.CancelNotification
		_ = json.Unmarshal(params, &c)
		a.mu.Lock()
		cancel := a.cancels[c.SessionID]
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	close(ready)
	<-conn.Done()
}

func (a *Agent) options() []acp.ConfigOption {
	var vals []acp.ConfigOptionValue
	for _, m := range a.models() {
		vals = append(vals, acp.ConfigOptionValue{Value: acp.Value(m), Name: strings.ToUpper(m[:1]) + m[1:]})
	}
	a.mu.Lock()
	cur, mode := a.model, a.mode
	a.mu.Unlock()
	if mode == "" {
		mode = "build"
	}
	return []acp.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", Type: "select", CurrentValue: acp.Value(cur), Options: vals},
		{ID: "mode", Name: "Session Mode", Category: "mode", Type: "select", CurrentValue: acp.Value(mode),
			Options: []acp.ConfigOptionValue{{Value: "build", Name: "build"}, {Value: "plan", Name: "plan"}}},
	}
}

func (a *Agent) handle(ctx context.Context, conn *acp.Conn, method string, params json.RawMessage) (any, error) {
	switch method {
	case acp.MethodInitialize:
		var r acp.InitializeRequest
		_ = json.Unmarshal(params, &r)
		a.mu.Lock()
		a.inits = append(a.inits, r)
		if a.model == "" {
			a.model = a.models()[0]
		}
		a.mu.Unlock()
		name := a.Name
		if name == "" {
			name = "Fake Agent"
		}
		return acp.InitializeResponse{ProtocolVersion: 1, AgentInfo: &acp.Implementation{Name: name, Version: "1.0"},
			AgentCapabilities: acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: true}},
			AuthMethods:       []acp.AuthMethod{{ID: "fake-login", Name: "Login", Description: "Run `fake auth login` in the terminal"}}}, nil
	case acp.MethodSessionNew:
		if a.AuthRequired {
			return nil, &acp.Error{Code: acp.CodeAuthRequired, Message: "Authentication required"}
		}
		a.mu.Lock()
		a.sessions++
		id := fmt.Sprintf("ses_%d", a.sessions)
		a.mu.Unlock()
		return acp.NewSessionResponse{SessionID: id, ConfigOptions: a.options()}, nil
	case acp.MethodSetConfigOption:
		var r acp.SetConfigOptionRequest
		_ = json.Unmarshal(params, &r)
		if r.ConfigID == "model" {
			ok := false
			for _, m := range a.models() {
				ok = ok || m == r.Value
			}
			if !ok {
				return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: "unknown model " + r.Value}
			}
			a.mu.Lock()
			a.model = r.Value
			a.mu.Unlock()
		}
		if r.ConfigID == "mode" {
			a.mu.Lock()
			a.mode = r.Value
			a.mu.Unlock()
		}
		return acp.SetConfigOptionResponse{ConfigOptions: a.options()}, nil
	case acp.MethodSessionPrompt:
		var r acp.PromptRequest
		_ = json.Unmarshal(params, &r)
		var text strings.Builder
		for _, b := range r.Prompt {
			switch {
			case b.Type == "text":
				text.WriteString(b.Text)
			case b.Resource != nil:
				text.WriteString(b.Resource.Text)
			}
		}
		pctx, cancel := context.WithCancel(ctx)
		a.mu.Lock()
		a.prompts = append(a.prompts, text.String())
		if a.cancels == nil {
			a.cancels = map[string]context.CancelFunc{}
		}
		a.cancels[r.SessionID] = cancel
		a.mu.Unlock()
		defer cancel()
		t := &Turn{conn: conn, SessionID: r.SessionID, Prompt: text.String(), Blocks: r.Prompt}
		script := a.Script
		if script == nil {
			script = Echo
		}
		stop := script(pctx, t)
		if stop == "" {
			stop = acp.StopEndTurn
		}
		return acp.PromptResponse{StopReason: stop}, nil
	}
	return nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: "unknown method " + method}
}

// Echo answers with the prompt text in two chunks.
func Echo(_ context.Context, t *Turn) string {
	t.Text("You said: ")
	t.Text(t.Prompt)
	return ""
}

// Turn is the running prompt as seen by a Script.
type Turn struct {
	conn      *acp.Conn
	SessionID string
	// Prompt is the text of all text and resource blocks.
	Prompt string
	Blocks []acp.ContentBlock
}

// Update sends a raw session update (fields of the update object).
func (t *Turn) Update(kind string, fields map[string]any) {
	u := map[string]any{"sessionUpdate": kind}
	for k, v := range fields {
		u[k] = v
	}
	_ = t.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": t.SessionID, "update": u})
}

// Text streams an agent message chunk.
func (t *Turn) Text(s string) {
	t.Update(acp.UpdateAgentMessage, map[string]any{"content": acp.TextBlock(s)})
}

// Thought streams a thought chunk.
func (t *Turn) Thought(s string) {
	t.Update(acp.UpdateAgentThought, map[string]any{"content": acp.TextBlock(s)})
}

// Plan sends the plan.
func (t *Turn) Plan(entries ...acp.PlanEntry) {
	t.Update(acp.UpdatePlan, map[string]any{"entries": entries})
}

// Tool sends a tool_call (first report) or tool_call_update.
func (t *Turn) Tool(update bool, tc acp.ToolCall) {
	b, _ := json.Marshal(tc)
	var fields map[string]any
	_ = json.Unmarshal(b, &fields)
	kind := acp.UpdateToolCall
	if update {
		kind = acp.UpdateToolCallUpdate
	}
	t.Update(kind, fields)
}

// Permission asks the client and returns the chosen option ID ("" when
// cancelled or on error).
func (t *Turn) Permission(ctx context.Context, tc acp.ToolCall) string {
	var res acp.RequestPermissionResponse
	err := t.conn.Call(ctx, acp.MethodRequestPermission, acp.RequestPermissionRequest{SessionID: t.SessionID, ToolCall: tc, Options: []acp.PermissionOption{
		{OptionID: "once", Name: "Allow once", Kind: acp.AllowOnce},
		{OptionID: "always", Name: "Always allow", Kind: acp.AllowAlways},
		{OptionID: "reject", Name: "Reject", Kind: acp.RejectOnce},
	}}, &res)
	if err != nil || res.Outcome.Outcome != acp.OutcomeSelected {
		return ""
	}
	return res.Outcome.OptionID
}

// ReadFile calls fs/read_text_file.
func (t *Turn) ReadFile(ctx context.Context, path string) (string, error) {
	var res acp.ReadTextFileResponse
	err := t.conn.Call(ctx, acp.MethodReadTextFile, acp.ReadTextFileRequest{SessionID: t.SessionID, Path: path}, &res)
	return res.Content, err
}

// WriteFile calls fs/write_text_file.
func (t *Turn) WriteFile(ctx context.Context, path, content string) error {
	return t.conn.Call(ctx, acp.MethodWriteTextFile, acp.WriteTextFileRequest{SessionID: t.SessionID, Path: path, Content: content}, nil)
}

// Str returns a pointer to s (diff texts).
func Str(s string) *string { return &s }

// Conn returns the agent side of the connection (raw calls).
func (t *Turn) Conn() *acp.Conn { return t.conn }
