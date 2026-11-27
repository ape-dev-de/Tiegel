package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// readOnlyConfig is the inline opencode configuration of the pipeline's
// agent: opencode itself denies edits, shell commands and the web (the
// handler below rejects them as well).
const readOnlyConfig = `{"permission":{"edit":"deny","bash":"deny","webfetch":"deny","websearch":"deny"}}`

// acpAgent runs the pipeline's prompts in sessions of one agent process.
// It is read-only: every permission request except reading and
// searching is rejected, fs/write_text_file fails.
type acpAgent struct {
	proc  *acp.Process
	root  string
	model string

	// act receives the live activity (nil: none).
	act *activityLog

	mu    sync.Mutex
	sinks map[string]*strings.Builder
	// steps maps a session to the page and step it works on.
	steps map[string]knowledge.StepInfo
	// blocked counts rejected tool calls.
	blocked int
	// modelErr says that the wanted model is not offered.
	modelErr string
}

// Notes returns remarks for the run's report (model not offered,
// rejected tool calls).
func (a *acpAgent) Notes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	if a.modelErr != "" {
		out = append(out, a.modelErr)
	}
	if a.blocked > 0 {
		out = append(out, fmt.Sprintf("%d tool calls that would change something were rejected (read-only build)", a.blocked))
	}
	return out
}

type acpHandler struct{ a *acpAgent }

// startAgent starts the agent process and initializes it.
func startAgent(ctx context.Context, start StartFunc, cmd acp.Command, root, model string) (*acpAgent, error) {
	a := &acpAgent{root: root, model: model, sinks: map[string]*strings.Builder{}, steps: map[string]knowledge.StepInfo{}}
	proc, err := start(ctx, cmd, acpHandler{a})
	if err != nil {
		return nil, err
	}
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := proc.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{FS: acp.FSCapabilities{ReadTextFile: true}},
		ClientInfo:         &acp.Implementation{Name: "tiegel-knowledge", Title: "Tiegel knowledge", Version: version.Version},
	}); err != nil {
		_ = proc.Close()
		msg := err.Error()
		if s := strings.TrimSpace(proc.Stderr()); s != "" {
			msg += ": " + lastLines(s, 3)
		}
		return nil, errors.New("agent initialize: " + msg)
	}
	a.proc = proc
	return a, nil
}

func lastLines(s string, n int) string {
	l := strings.Split(s, "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// Close ends the agent process.
func (a *acpAgent) Close() {
	if a.proc != nil {
		_ = a.proc.Close()
	}
}

// NewSession implements knowledge.Agent: a fresh session in the project,
// switched to the read-only "plan" mode and the pipeline's model when
// the agent offers them.
func (a *acpAgent) NewSession(ctx context.Context) (knowledge.Session, error) {
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := a.proc.NewSession(sctx, a.root)
	if err != nil {
		if acp.ErrorCode(err) == acp.CodeAuthRequired {
			return nil, errors.New("the agent needs a login (see the Agent window)")
		}
		return nil, err
	}
	for _, o := range res.ConfigOptions {
		want := ""
		switch {
		case o.ID == "mode" || o.Category == "mode":
			want = "plan"
		case (o.ID == "model" || o.Category == "model") && a.model != "":
			want = a.model
		}
		if want == "" || string(o.CurrentValue) == want || !hasChoice(o, want) {
			if want != "" && want == a.model && !hasChoice(o, want) {
				a.mu.Lock()
				a.modelErr = "model " + want + " is not offered by the agent; using " + string(o.CurrentValue)
				a.mu.Unlock()
			}
			continue
		}
		_, _ = a.proc.SetConfigOption(sctx, res.SessionID, o.ID, want)
	}
	a.mu.Lock()
	a.sinks[res.SessionID] = &strings.Builder{}
	if st, ok := knowledge.StepOf(ctx); ok {
		a.steps[res.SessionID] = st
	}
	a.mu.Unlock()
	return &acpSession{a: a, id: res.SessionID}, nil
}

func hasChoice(o acp.ConfigOption, v string) bool {
	for _, c := range o.Options {
		if string(c.Value) == v {
			return true
		}
	}
	return false
}

type acpSession struct {
	a  *acpAgent
	id string
}

// Prompt implements knowledge.Session.
func (s *acpSession) Prompt(ctx context.Context, text string) (string, error) {
	s.a.mu.Lock()
	sink := s.a.sinks[s.id]
	if sink != nil {
		sink.Reset()
	}
	s.a.mu.Unlock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.a.proc.Cancel(s.id)
		case <-done:
		}
	}()
	res, err := s.a.proc.Prompt(ctx, s.id, []acp.ContentBlock{acp.TextBlock(text)})
	close(done)
	if err != nil {
		return "", err
	}
	if res.StopReason == acp.StopCancelled {
		return "", context.Canceled
	}
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	if sink == nil {
		return "", nil
	}
	return sink.String(), nil
}

// Close implements knowledge.Session.
func (s *acpSession) Close() {
	s.a.mu.Lock()
	delete(s.a.sinks, s.id)
	delete(s.a.steps, s.id)
	s.a.mu.Unlock()
}

// SessionUpdate collects the agent's message text per session and
// passes thoughts, message chunks and tool calls to the activity log.
func (h acpHandler) SessionUpdate(n acp.SessionNotification) {
	h.a.mu.Lock()
	st, known := h.a.steps[n.SessionID]
	if n.Update.Kind == acp.UpdateAgentMessage {
		if c := n.Update.Chunk(); c.Type == "text" {
			if sink := h.a.sinks[n.SessionID]; sink != nil {
				sink.WriteString(c.Text)
			}
		}
	}
	h.a.mu.Unlock()
	act := h.a.act
	if act == nil || !known {
		return
	}
	switch n.Update.Kind {
	case acp.UpdateAgentMessage:
		if c := n.Update.Chunk(); c.Type == "text" {
			act.chunk(st, api.KnowledgeActivityMessage, c.Text)
		}
	case acp.UpdateAgentThought:
		if c := n.Update.Chunk(); c.Type == "text" {
			act.chunk(st, api.KnowledgeActivityThought, c.Text)
		}
	case acp.UpdateToolCall:
		act.tool(st, n.Update.ToolCall(), false)
	case acp.UpdateToolCallUpdate:
		act.tool(st, n.Update.ToolCall(), true)
	}
}

// RequestPermission allows reading and searching only.
func (h acpHandler) RequestPermission(_ context.Context, r acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if readOnlyTool(r.ToolCall) {
		for _, kind := range []string{acp.AllowOnce, acp.AllowAlways} {
			for _, o := range r.Options {
				if o.Kind == kind {
					return acp.Selected(o.OptionID), nil
				}
			}
		}
	}
	h.a.mu.Lock()
	h.a.blocked++
	h.a.mu.Unlock()
	for _, kind := range []string{acp.RejectOnce, acp.RejectAlways} {
		for _, o := range r.Options {
			if o.Kind == kind {
				return acp.Selected(o.OptionID), nil
			}
		}
	}
	return acp.Cancelled(), nil
}

// readOnlyTool tells whether a tool call only reads.
func readOnlyTool(tc acp.ToolCall) bool {
	for _, c := range tc.Content {
		if c.Type == "diff" {
			return false
		}
	}
	switch strings.ToLower(tc.Name) {
	case "read", "grep", "glob", "list", "ls", "find", "search":
		return true
	case "":
	default:
		return false
	}
	switch tc.Kind {
	case acp.KindRead, acp.KindSearch, acp.KindThink:
		return true
	}
	return false
}

// ReadTextFile serves files inside the project.
func (h acpHandler) ReadTextFile(_ context.Context, r acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	p := r.Path
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.a.root, p)
	}
	p = filepath.Clean(p)
	if rel, err := filepath.Rel(h.a.root, p); err != nil || strings.HasPrefix(rel, "..") {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeInvalidParams, Message: "outside the project: " + r.Path}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeInvalidParams, Message: err.Error()}
	}
	text := string(b)
	if r.Line != nil || r.Limit != nil {
		lines := strings.SplitAfter(text, "\n")
		from := 0
		if r.Line != nil && *r.Line > 1 {
			from = min(*r.Line-1, len(lines))
		}
		to := len(lines)
		if r.Limit != nil && *r.Limit >= 0 {
			to = min(from+*r.Limit, len(lines))
		}
		text = strings.Join(lines[from:to], "")
	}
	return acp.ReadTextFileResponse{Content: text}, nil
}

// WriteTextFile is refused: the pipeline never changes the project.
func (h acpHandler) WriteTextFile(context.Context, acp.WriteTextFileRequest) error {
	h.a.mu.Lock()
	h.a.blocked++
	h.a.mu.Unlock()
	return &acp.Error{Code: acp.CodeInvalidParams, Message: "writing is not allowed while building the knowledge base"}
}
