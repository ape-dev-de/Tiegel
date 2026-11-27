package orchestrate

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
	"github.com/ape-dev-de/tiegel/internal/agentpolicy"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// StartFunc starts an agent process and connects handler h (acp.Start;
// tests connect acptest fakes).
type StartFunc func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error)

// ACPOptions configure an ACPAgent.
type ACPOptions struct {
	Start   StartFunc
	Command acp.Command
	// Policy is the agent plugin's permission policy (shell, web, other).
	Policy api.AgentPolicy
	// AllowAsk answers "ask" policies with allow (headless --yes);
	// otherwise they are rejected – nobody watches plan sessions.
	AllowAsk bool
	// ProjectRoot may be read in addition to a session's directory.
	ProjectRoot string
	// MCPServers returns the MCP servers of a session (knowledge, the
	// plan document tools).
	MCPServers func(spec SessionSpec) []acp.MCPServerStdio
	// OnActivity receives transcript updates of every session.
	OnActivity func(role string, e api.AgentEntry, delta string)
	// OnEdit runs before an allowed edit of abs (in-place snapshots).
	OnEdit func(abs string)
}

// ACPAgent runs role sessions in one coding agent process that speaks
// the Agent Client Protocol. Tiegel enforces the permissions of each
// session's mode itself (D567): it answers every permission request.
type ACPAgent struct {
	opts ACPOptions
	proc *acp.Process

	mu       sync.Mutex
	sessions map[string]*acpSession
	blocked  int
}

// StartACP starts the agent process and initializes the connection.
func StartACP(ctx context.Context, opts ACPOptions) (*ACPAgent, error) {
	if opts.Start == nil {
		opts.Start = acp.Start
	}
	a := &ACPAgent{opts: opts, sessions: map[string]*acpSession{}}
	proc, err := opts.Start(ctx, opts.Command, acpHandler{a})
	if err != nil {
		return nil, fmt.Errorf("orchestrate: start agent: %w", err)
	}
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := proc.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{FS: acp.FSCapabilities{ReadTextFile: true, WriteTextFile: true}},
		ClientInfo:         &acp.Implementation{Name: "tiegel-plan", Title: "Tiegel plan mode", Version: version.Version},
	}); err != nil {
		_ = proc.Close()
		msg := err.Error()
		if s := strings.TrimSpace(proc.Stderr()); s != "" {
			msg += ": " + lastLines(s, 3)
		}
		return nil, errors.New("orchestrate: agent initialize: " + msg)
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
func (a *ACPAgent) Close() error {
	if a.proc != nil {
		return a.proc.Close()
	}
	return nil
}

// Blocked implements Agent.
func (a *ACPAgent) Blocked() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.blocked
}

// Transcript implements Agent.
func (a *ACPAgent) Transcript(id string) []api.AgentEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	if s == nil {
		return nil
	}
	out := make([]api.AgentEntry, len(s.entries))
	for i, e := range s.entries {
		out[i] = copyEntry(*e)
	}
	return out
}

func copyEntry(e api.AgentEntry) api.AgentEntry {
	if e.Tool != nil {
		t := *e.Tool
		e.Tool = &t
	}
	return e
}

// NewSession implements Agent: a session in spec.Dir with the session
// mode (plan for read-only, build otherwise) and model when offered.
func (a *ACPAgent) NewSession(ctx context.Context, spec SessionSpec) (Session, error) {
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var servers []acp.MCPServerStdio
	if a.opts.MCPServers != nil {
		servers = a.opts.MCPServers(spec)
	}
	res, err := a.proc.NewSessionWith(sctx, spec.Dir, servers)
	if err != nil {
		if acp.ErrorCode(err) == acp.CodeAuthRequired {
			return nil, errors.New("the agent needs a login (see the Agent window)")
		}
		return nil, fmt.Errorf("orchestrate: new session: %w", err)
	}
	s := &acpSession{a: a, id: res.SessionID, role: spec.Role, mode: spec.Mode, dir: filepath.Clean(spec.Dir), open: -1, tools: map[string]int{}}
	a.mu.Lock()
	a.sessions[res.SessionID] = s
	a.mu.Unlock()
	s.applyOptions(sctx, res.ConfigOptions, spec)
	return s, nil
}

type acpSession struct {
	a  *ACPAgent
	id string

	// guarded by a.mu
	role    string
	mode    Mode
	dir     string
	entries []*api.AgentEntry
	tools   map[string]int
	open    int
	sink    *strings.Builder
}

func (s *acpSession) ID() string { return s.id }

// applyOptions chooses the session mode and model.
func (s *acpSession) applyOptions(ctx context.Context, opts []acp.ConfigOption, spec SessionSpec) {
	for _, o := range opts {
		want := ""
		switch {
		case o.ID == "mode" || o.Category == "mode":
			want = "build"
			if spec.Mode == ReadOnly {
				want = "plan"
			}
		case (o.ID == "model" || o.Category == "model") && spec.Model != "":
			want = spec.Model
			if !hasChoice(o, want) {
				s.notice(api.NoticeInfo, "model "+want+" is not offered by the agent; using "+string(o.CurrentValue))
				continue
			}
		}
		if want == "" || string(o.CurrentValue) == want || !hasChoice(o, want) {
			continue
		}
		_, _ = s.a.proc.SetConfigOption(ctx, s.id, o.ID, want)
	}
}

func hasChoice(o acp.ConfigOption, v string) bool {
	for _, c := range o.Options {
		if string(c.Value) == v {
			return true
		}
	}
	return false
}

// SetRole implements Session.
func (s *acpSession) SetRole(role string, mode Mode) {
	s.a.mu.Lock()
	s.role, s.mode = role, mode
	s.a.mu.Unlock()
}

// Close implements Session (the transcript stays available).
func (s *acpSession) Close() {}

// add appends an entry (callers hold a.mu).
func (s *acpSession) add(e api.AgentEntry) int {
	e.Index = len(s.entries)
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.entries = append(s.entries, &e)
	return e.Index
}

func (s *acpSession) emit(i int, delta string) {
	s.a.mu.Lock()
	e := copyEntry(*s.entries[i])
	role := s.role
	s.a.mu.Unlock()
	if s.a.opts.OnActivity != nil {
		s.a.opts.OnActivity(role, e, delta)
	}
}

func (s *acpSession) notice(level, text string) {
	s.a.mu.Lock()
	i := s.add(api.AgentEntry{Kind: api.EntryNotice, Level: level, Text: text})
	s.open = -1
	s.a.mu.Unlock()
	s.emit(i, "")
}

// Prompt implements Session.
func (s *acpSession) Prompt(ctx context.Context, text string) (string, error) {
	sink := &strings.Builder{}
	s.a.mu.Lock()
	s.sink = sink
	i := s.add(api.AgentEntry{Kind: api.EntryUser, Text: text})
	s.open = -1
	s.a.mu.Unlock()
	s.emit(i, "")
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
	s.a.mu.Lock()
	s.sink = nil
	s.a.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("orchestrate: prompt: %w", err)
	}
	if res.StopReason == acp.StopCancelled {
		return "", context.Canceled
	}
	return sink.String(), nil
}

type acpHandler struct{ a *ACPAgent }

func (h acpHandler) session(id string) *acpSession {
	h.a.mu.Lock()
	defer h.a.mu.Unlock()
	return h.a.sessions[id]
}

// SessionUpdate records messages, thoughts and tool calls per session.
func (h acpHandler) SessionUpdate(n acp.SessionNotification) {
	s := h.session(n.SessionID)
	if s == nil {
		return
	}
	u := n.Update
	switch u.Kind {
	case acp.UpdateAgentMessage, acp.UpdateAgentThought:
		c := u.Chunk()
		if c.Type != "text" || c.Text == "" {
			return
		}
		kind := api.EntryAgent
		if u.Kind == acp.UpdateAgentThought {
			kind = api.EntryThought
		}
		h.a.mu.Lock()
		if s.open < 0 || s.entries[s.open].Kind != kind {
			s.open = s.add(api.AgentEntry{Kind: kind})
		}
		s.entries[s.open].Text += c.Text
		if kind == api.EntryAgent && s.sink != nil {
			s.sink.WriteString(c.Text)
		}
		i := s.open
		h.a.mu.Unlock()
		s.emit(i, c.Text)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		tc := u.ToolCall()
		if tc.ToolCallID == "" {
			return
		}
		h.a.mu.Lock()
		i, ok := s.tools[tc.ToolCallID]
		if !ok {
			i = s.add(api.AgentEntry{Kind: api.EntryTool, Tool: &api.AgentToolCall{ID: tc.ToolCallID}})
			s.tools[tc.ToolCallID] = i
			s.open = -1
		}
		mergeTool(s.entries[i].Tool, tc)
		h.a.mu.Unlock()
		s.emit(i, "")
	}
}

// mergeTool applies the reported fields of tc to t.
func mergeTool(t *api.AgentToolCall, tc acp.ToolCall) {
	if tc.Name != "" {
		t.Name = tc.Name
	}
	if tc.Title != "" {
		t.Title = tc.Title
	}
	if tc.Kind != "" {
		t.Kind = tc.Kind
	}
	if tc.Status != "" {
		t.Status = tc.Status
	}
	if c := agentpolicy.CommandOf(tc); c != "" {
		t.Command = c
	}
	for _, l := range tc.Locations {
		t.Paths = appendUnique(t.Paths, l.Path)
	}
	for _, c := range tc.Content {
		if c.Type == "content" && c.Content != nil && c.Content.Text != "" {
			t.Output = shorten(c.Content.Text, 2000)
		}
	}
	if t.Title == "" {
		t.Title = t.Name
	}
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + " …"
}

// planTool tells whether a tool call is one of Tiegel's own read-only
// MCP tools (plan document, project knowledge).
func planTool(tc acp.ToolCall) bool {
	n := strings.ToLower(tc.Name + " " + tc.Title)
	for _, p := range []string{"plan_read", "plan_update", "plan_add_finding", "plan_comment", "knowledge_search", "knowledge_read", "knowledge_list"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// inside reports whether p lies in dir.
func inside(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func (s *acpSession) abs(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.dir, p)
	}
	return filepath.Clean(p)
}

// decide returns whether the session may run tc and why not.
func (s *acpSession) decide(tc acp.ToolCall) (bool, string) {
	s.a.mu.Lock()
	mode, dir := s.mode, s.dir
	if i, ok := s.tools[tc.ToolCallID]; ok && tc.Name == "" {
		tc.Name = s.entries[i].Tool.Name
	}
	s.a.mu.Unlock()
	if planTool(tc) {
		return true, ""
	}
	cat, verb := agentpolicy.Category(tc)
	if mode == ReadOnly {
		if agentpolicy.IsReadOnly(tc) {
			return true, ""
		}
		what := verb
		if what == "" {
			what = cat
		}
		return false, "read-only phase: " + what
	}
	pol := s.a.opts.Policy
	switch cat {
	case api.AgentCatGitCommit, api.AgentCatGitPush:
		return false, verb + " is never allowed in plan mode (Tiegel handles version control)"
	case api.AgentCatEdit:
		paths := agentpolicy.EditPaths(tc)
		for _, p := range paths {
			if !inside(dir, s.abs(p)) {
				return false, "edit outside the plan workspace: " + p
			}
		}
		if s.a.opts.OnEdit != nil {
			for _, p := range paths {
				s.a.opts.OnEdit(s.abs(p))
			}
		}
		return true, ""
	}
	if agentpolicy.IsReadOnly(tc) && cat == api.AgentCatOther {
		return true, ""
	}
	switch agentpolicy.Decide(pol, cat) {
	case api.PolicyAllow:
		return true, ""
	case api.PolicyAsk:
		if s.a.opts.AllowAsk {
			return true, ""
		}
		return false, cat + " needs confirmation in the agent settings; plan mode rejects it (set it to allow, or run headless with --yes)"
	}
	return false, cat + " is disabled in the agent settings"
}

// RequestPermission enforces the session's mode.
func (h acpHandler) RequestPermission(_ context.Context, r acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	s := h.session(r.SessionID)
	if s == nil {
		return acp.Cancelled(), nil
	}
	ok, why := s.decide(r.ToolCall)
	if ok {
		if id := agentpolicy.AllowOption(r.Options); id != "" {
			return acp.Selected(id), nil
		}
		return acp.Cancelled(), nil
	}
	h.a.mu.Lock()
	h.a.blocked++
	h.a.mu.Unlock()
	title := r.ToolCall.Title
	if title == "" {
		title = r.ToolCall.Name
	}
	s.notice(api.NoticeBlocked, "Blocked: "+why+" ("+title+")")
	if id := agentpolicy.RejectOption(r.Options); id != "" {
		return acp.Selected(id), nil
	}
	return acp.Cancelled(), nil
}

// ReadTextFile serves files of the session directory and the project.
func (h acpHandler) ReadTextFile(_ context.Context, r acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	s := h.session(r.SessionID)
	if s == nil {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeInvalidParams, Message: "unknown session"}
	}
	p := s.abs(r.Path)
	if !inside(s.dir, p) && (h.a.opts.ProjectRoot == "" || !inside(h.a.opts.ProjectRoot, p)) {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeInvalidParams, Message: "outside the project: " + r.Path}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeResourceNotFound, Message: err.Error()}
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

// WriteTextFile writes inside the session directory in Execute mode.
func (h acpHandler) WriteTextFile(_ context.Context, r acp.WriteTextFileRequest) error {
	s := h.session(r.SessionID)
	if s == nil {
		return &acp.Error{Code: acp.CodeInvalidParams, Message: "unknown session"}
	}
	s.a.mu.Lock()
	mode := s.mode
	s.a.mu.Unlock()
	p := s.abs(r.Path)
	if mode == ReadOnly || !inside(s.dir, p) {
		h.a.mu.Lock()
		h.a.blocked++
		h.a.mu.Unlock()
		s.notice(api.NoticeBlocked, "Blocked: write "+r.Path+" (read-only phase or outside the plan workspace)")
		return &acp.Error{Code: acp.CodeInvalidParams, Message: "writing is not allowed here (read-only phase or outside the plan workspace)"}
	}
	if h.a.opts.OnEdit != nil {
		h.a.opts.OnEdit(p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return &acp.Error{Code: acp.CodeInternalError, Message: err.Error()}
	}
	if err := os.WriteFile(p, []byte(r.Content), 0o644); err != nil {
		return &acp.Error{Code: acp.CodeInternalError, Message: err.Error()}
	}
	return nil
}
