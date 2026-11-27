package acp_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
)

// The test binary doubles as agent process for TestStartProcess.
func TestMain(m *testing.M) {
	if os.Getenv("TIEGEL_FAKE_ACP") == "1" {
		(&acptest.Agent{Name: "Process Agent"}).Serve(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

// recorder is a Handler that records updates and answers permissions
// with a fixed option.
type recorder struct {
	mu      sync.Mutex
	updates []acp.SessionNotification
	perms   []acp.RequestPermissionRequest
	answer  string
	files   map[string]string
	block   chan struct{}
}

func (r *recorder) SessionUpdate(n acp.SessionNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, n)
}

func (r *recorder) RequestPermission(ctx context.Context, q acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	r.mu.Lock()
	r.perms = append(r.perms, q)
	block := r.block
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return acp.Cancelled(), nil
		}
	}
	if r.answer == "" {
		return acp.Cancelled(), nil
	}
	return acp.Selected(r.answer), nil
}

func (r *recorder) ReadTextFile(_ context.Context, q acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.files[q.Path]
	if !ok {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeResourceNotFound, Message: "not found"}
	}
	return acp.ReadTextFileResponse{Content: c}, nil
}

func (r *recorder) WriteTextFile(_ context.Context, q acp.WriteTextFileRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q.Path == "/fail" {
		return errors.New("disk full")
	}
	r.files[q.Path] = q.Content
	return nil
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, u := range r.updates {
		out = append(out, u.Update.Kind)
	}
	return out
}

func start(t *testing.T, a *acptest.Agent, h acp.Handler) *acp.Process {
	t.Helper()
	p, err := a.Start(context.Background(), acp.Command{}, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestSessionLifecycle(t *testing.T) {
	a := &acptest.Agent{Script: func(ctx context.Context, tr *acptest.Turn) string {
		tr.Thought("thinking")
		tr.Text("hello ")
		tr.Plan(acp.PlanEntry{Content: "step", Status: "pending"})
		tr.Tool(false, acp.ToolCall{ToolCallID: "t1", Title: "edit", Kind: acp.KindEdit, Status: acp.StatusPending})
		tr.Tool(true, acp.ToolCall{ToolCallID: "t1", Status: acp.StatusCompleted, Content: []acp.ToolCallContent{
			{Type: "diff", Path: "/a.txt", OldText: acptest.Str("a\n"), NewText: acptest.Str("b\n")},
			{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: "done"}},
		}, Locations: []acp.Location{{Path: "/a.txt"}}})
		tr.Update(acp.UpdateCommands, map[string]any{"availableCommands": []acp.SlashCommand{{Name: "init", Description: "setup"}}})
		tr.Update(acp.UpdateConfigOptions, map[string]any{"configOptions": []acp.ConfigOption{{ID: "model", Type: "select", CurrentValue: "x"}}})
		got, err := tr.ReadFile(ctx, "/a.txt")
		if err != nil || got != "content" {
			tr.Text("read failed")
		}
		if _, err := tr.ReadFile(ctx, "/missing"); acp.ErrorCode(err) != acp.CodeResourceNotFound {
			tr.Text("missing not reported")
		}
		if err := tr.WriteFile(ctx, "/b.txt", "new"); err != nil {
			tr.Text("write failed")
		}
		if err := tr.WriteFile(ctx, "/fail", "x"); err == nil {
			tr.Text("failing write succeeded")
		}
		return ""
	}}
	h := &recorder{files: map[string]string{"/a.txt": "content"}}
	p := start(t, a, h)
	init, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{FS: acp.FSCapabilities{ReadTextFile: true, WriteTextFile: true}},
		ClientInfo:         &acp.Implementation{Name: "tiegel"}})
	if err != nil {
		t.Fatal(err)
	}
	if init.AgentInfo.Name != "Fake Agent" || len(init.AuthMethods) != 1 || !init.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Fatalf("init %+v", init)
	}
	if got := a.Inits(); len(got) != 1 || !got[0].ClientCapabilities.FS.WriteTextFile {
		t.Fatalf("agent saw %+v", got)
	}
	s, err := p.NewSession(ctx(t), "/proj")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID == "" || len(s.ConfigOptions) != 2 || s.ConfigOptions[0].CurrentValue != "fake/small" {
		t.Fatalf("session %+v", s)
	}
	opts, err := p.SetConfigOption(ctx(t), s.SessionID, "model", "fake/large")
	if err != nil || opts[0].CurrentValue != "fake/large" || a.Model() != "fake/large" {
		t.Fatalf("set model %v %+v", err, opts)
	}
	if _, err := p.SetConfigOption(ctx(t), s.SessionID, "model", "nope"); acp.ErrorCode(err) != acp.CodeInvalidParams {
		t.Fatalf("unknown model: %v", err)
	}
	res, err := p.Prompt(ctx(t), s.SessionID, []acp.ContentBlock{acp.TextBlock("do it")})
	if err != nil || res.StopReason != acp.StopEndTurn {
		t.Fatalf("prompt %v %+v", err, res)
	}
	if h.files["/b.txt"] != "new" {
		t.Fatalf("write not served: %v", h.files)
	}
	want := []string{acp.UpdateAgentThought, acp.UpdateAgentMessage, acp.UpdatePlan, acp.UpdateToolCall, acp.UpdateToolCallUpdate, acp.UpdateCommands, acp.UpdateConfigOptions}
	if got := strings.Join(h.kinds(), ","); got != strings.Join(want, ",") {
		t.Fatalf("updates %s", got)
	}
	h.mu.Lock()
	u := h.updates
	h.mu.Unlock()
	if u[0].Update.Chunk().Text != "thinking" || u[1].Update.Chunk().Text != "hello " || u[2].Update.Plan()[0].Content != "step" {
		t.Fatal("chunk/plan accessors")
	}
	tc := u[4].Update.ToolCall()
	if tc.ToolCallID != "t1" || tc.Status != acp.StatusCompleted || len(tc.Content) != 2 || *tc.Content[0].OldText != "a\n" || tc.Content[1].Content.Text != "done" {
		t.Fatalf("tool call %+v", tc)
	}
	if u[5].Update.Commands()[0].Name != "init" || u[6].Update.ConfigOptions()[0].CurrentValue != "x" {
		t.Fatal("commands/config accessors")
	}
	for _, m := range a.Prompts() {
		if m != "do it" {
			t.Fatalf("prompt text %q", m)
		}
	}
	if b, err := u[0].Update.MarshalJSON(); err != nil || !strings.Contains(string(b), "thinking") {
		t.Fatalf("marshal %s %v", b, err)
	}
	if b, _ := (acp.SessionUpdate{Kind: "plan"}).MarshalJSON(); string(b) != `{"sessionUpdate":"plan"}` {
		t.Fatalf("empty marshal %s", b)
	}
}

func TestPermissionAndCancel(t *testing.T) {
	answered := make(chan string, 1)
	a := &acptest.Agent{Script: func(ctx context.Context, tr *acptest.Turn) string {
		answered <- tr.Permission(ctx, acp.ToolCall{ToolCallID: "t", Kind: acp.KindExecute, RawInput: []byte(`{"command":"ls"}`)})
		<-ctx.Done()
		return acp.StopCancelled
	}}
	h := &recorder{answer: "once", block: make(chan struct{})}
	p := start(t, a, h)
	if _, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: 1}); err != nil {
		t.Fatal(err)
	}
	s, err := p.NewSession(ctx(t), "/p")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan acp.PromptResponse, 1)
	go func() {
		r, _ := p.Prompt(context.Background(), s.SessionID, []acp.ContentBlock{acp.TextBlock("x")})
		done <- r
	}()
	close(h.block)
	if got := <-answered; got != "once" {
		t.Fatalf("answer %q", got)
	}
	if err := p.Cancel(s.SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.StopReason != acp.StopCancelled {
			t.Fatalf("stop %q", r.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel not honoured")
	}
	h.mu.Lock()
	if len(h.perms) != 1 || string(h.perms[0].ToolCall.RawInput) != `{"command":"ls"}` || len(h.perms[0].Options) != 3 {
		t.Fatalf("perms %+v", h.perms)
	}
	h.mu.Unlock()
}

func TestAuthRequired(t *testing.T) {
	p := start(t, &acptest.Agent{AuthRequired: true}, &recorder{})
	if _, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := p.NewSession(ctx(t), "/p")
	if acp.ErrorCode(err) != acp.CodeAuthRequired || !strings.Contains(err.Error(), "Authentication required") {
		t.Fatalf("err %v", err)
	}
	if err := p.Authenticate(ctx(t), "x"); acp.ErrorCode(err) != acp.CodeMethodNotFound {
		t.Fatalf("authenticate %v", err)
	}
}

func TestCloseFailsCalls(t *testing.T) {
	a := &acptest.Agent{Script: func(ctx context.Context, tr *acptest.Turn) string {
		<-ctx.Done()
		return acp.StopCancelled
	}}
	p := start(t, a, &recorder{})
	if _, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: 1}); err != nil {
		t.Fatal(err)
	}
	s, _ := p.NewSession(ctx(t), "/p")
	errc := make(chan error, 1)
	go func() {
		_, err := p.Prompt(context.Background(), s.SessionID, nil)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = p.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, acp.ErrClosed) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not fail after close")
	}
	<-p.Exited()
	if err := p.Cancel(s.SessionID); !errors.Is(err, acp.ErrClosed) {
		t.Fatalf("notify after close: %v", err)
	}
	if _, err := p.NewSession(ctx(t), "/p"); !errors.Is(err, acp.ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
	if p.ExitErr() != nil || p.Stderr() != "" {
		t.Fatal("in-process agent has no exit error or stderr")
	}
}

func TestContextCancelsCall(t *testing.T) {
	a := &acptest.Agent{Script: func(ctx context.Context, tr *acptest.Turn) string {
		<-ctx.Done()
		return acp.StopCancelled
	}}
	p := start(t, a, &recorder{})
	_, _ = p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: 1})
	s, _ := p.NewSession(ctx(t), "/p")
	c, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Prompt(c, s.SessionID, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	_ = p.Cancel(s.SessionID)
}

func TestStartProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TIEGEL_FAKE_ACP=1")
	p, err := acp.Start(ctx(t), acp.Command{Path: exe, Dir: t.TempDir(), Env: env}, &recorder{})
	if err != nil {
		t.Fatal(err)
	}
	init, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: 1})
	if err != nil || init.AgentInfo.Name != "Process Agent" {
		t.Fatalf("%v %+v", err, init)
	}
	s, err := p.NewSession(ctx(t), "/p")
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Prompt(ctx(t), s.SessionID, []acp.ContentBlock{acp.TextBlock("hi")})
	if err != nil || r.StopReason != acp.StopEndTurn {
		t.Fatalf("%v %+v", err, r)
	}
	_ = p.Close()
	select {
	case <-p.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	_ = p.ExitErr()
	if _, err := acp.Start(ctx(t), acp.Command{Path: "/nonexistent/agent"}, &recorder{}); err == nil {
		t.Fatal("missing binary started")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acp.Start(cancelled, acp.Command{Path: exe}, &recorder{}); err == nil {
		t.Fatal("cancelled start")
	}
}

func TestLoadSession(t *testing.T) {
	a := &acptest.Agent{LoadSession: true}
	h := &recorder{}
	p := start(t, a, h)
	init, err := p.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion})
	if err != nil || !init.AgentCapabilities.LoadSession {
		t.Fatalf("init %v %+v", err, init)
	}
	s, err := p.NewSession(ctx(t), "/proj")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prompt(ctx(t), s.SessionID, []acp.ContentBlock{acp.TextBlock("hi")}); err != nil {
		t.Fatal(err)
	}
	// A second process of the same agent restores the session; the
	// replay arrives before the answer.
	h2 := &recorder{}
	p2 := start(t, a, h2)
	if _, err := p2.Initialize(ctx(t), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	res, err := p2.LoadSession(ctx(t), s.SessionID, "/proj", []acp.MCPServerStdio{{Name: "k", Command: "x"}})
	if err != nil || len(res.ConfigOptions) != 2 {
		t.Fatalf("load %v %+v", err, res)
	}
	if got := strings.Join(h2.kinds(), ","); got != acp.UpdateUserMessage+","+acp.UpdateAgentMessage {
		t.Fatalf("replay %s", got)
	}
	if l := a.LoadSessions(); len(l) != 1 || l[0].SessionID != s.SessionID || l[0].Cwd != "/proj" || len(l[0].MCPServers) != 1 {
		t.Fatalf("loads %+v", l)
	}
	if _, err := p2.LoadSession(ctx(t), "ses_unknown", "/proj", nil); acp.ErrorCode(err) != acp.CodeInvalidParams {
		t.Fatalf("unknown session: %v", err)
	}
	// Without the capability the method is unknown.
	b := &acptest.Agent{}
	p3 := start(t, b, &recorder{})
	if _, err := p3.LoadSession(ctx(t), "ses_1", "/proj", nil); acp.ErrorCode(err) != acp.CodeMethodNotFound {
		t.Fatalf("unsupported: %v", err)
	}
}
