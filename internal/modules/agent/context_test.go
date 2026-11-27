package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// blockTexts returns the text blocks of a prompt.
func blockTexts(bs []acp.ContentBlock) []string {
	var out []string
	for _, b := range bs {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

// recorder is a script that remembers the prompt blocks of every turn.
type recorder struct {
	mu    sync.Mutex
	turns [][]acp.ContentBlock
}

func (r *recorder) script(_ context.Context, t *acptest.Turn) string {
	r.mu.Lock()
	r.turns = append(r.turns, t.Blocks)
	r.mu.Unlock()
	t.Text("ok")
	return ""
}

// Without a provider the prompt is the rules preamble and the text only,
// and the transcript carries no context.
func TestPromptWithoutContextHook(t *testing.T) {
	rec := &recorder{}
	f := newFixture(t, &acptest.Agent{Script: rec.script}, nil)
	res := f.prompt("hello", "")
	if len(res.Context) != 0 {
		t.Fatalf("%+v", res)
	}
	got := blockTexts(rec.turns[0])
	if len(got) != 2 || !strings.HasPrefix(got[0], "Context from the IDE") || got[1] != "hello" {
		t.Fatalf("%q", got)
	}
	if es := f.transcript(); es[0].Kind != api.EntryUser || es[0].Context != nil || es[0].Instructions != nil {
		t.Fatalf("%+v", es[0])
	}
}

// Instructions go with the first prompt of a session only, blocks with
// every prompt; both land before the user's text and in the transcript.
func TestPromptContextHook(t *testing.T) {
	rec := &recorder{}
	f := newFixture(t, &acptest.Agent{Script: rec.script}, nil)
	var firsts []bool
	var mu sync.Mutex
	api.HookAgentPromptContext.On(f.app.Hooks(), 0, func(_ context.Context, pc api.AgentPromptContext) (api.AgentPromptContext, error) {
		mu.Lock()
		firsts = append(firsts, pc.First)
		mu.Unlock()
		out := pc
		if pc.First {
			out.Instructions = append(slices.Clone(pc.Instructions), "VERIFY THE KNOWLEDGE")
		}
		out.Blocks = append(slices.Clone(pc.Blocks), api.AgentContextBlock{Source: "kb", Title: "Context from project knowledge", Text: "HITS FOR " + pc.Prompt, Tokens: 3,
			Hits: []api.AgentContextHit{{Title: "Page", Heading: "H", Snippet: "s"}}},
			api.AgentContextBlock{Source: "empty", Text: "  "})
		return out, nil
	})
	res := f.prompt("first question", "")
	if len(res.Context) != 1 || res.Context[0].Text != "HITS FOR first question" {
		t.Fatalf("%+v", res)
	}
	f.prompt("second", "")
	got := blockTexts(rec.turns[0])
	if len(got) != 4 || !strings.HasPrefix(got[0], "Context from the IDE") || got[1] != "VERIFY THE KNOWLEDGE" || got[2] != "HITS FOR first question" || got[3] != "first question" {
		t.Fatalf("%q", got)
	}
	got = blockTexts(rec.turns[1])
	if len(got) != 2 || got[0] != "HITS FOR second" || got[1] != "second" {
		t.Fatalf("%q", got)
	}
	if !slices.Equal(firsts, []bool{true, false}) {
		t.Fatal(firsts)
	}
	es := f.transcript()
	if len(es[0].Context) != 1 || es[0].Context[0].Hits[0].Title != "Page" || len(es[0].Instructions) != 1 {
		t.Fatalf("%+v", es[0])
	}
	var second api.AgentEntry
	for _, e := range es {
		if e.Kind == api.EntryUser && e.Text == "second" {
			second = e
		}
	}
	if len(second.Context) != 1 || len(second.Instructions) != 0 {
		t.Fatalf("%+v", second)
	}
	// The events carry the context too.
	found := false
	for _, ev := range f.rec.Events(api.TopicAgentUpdate) {
		if u, ok := ev.Payload.(api.AgentUpdateEvent); ok && u.Entry.Kind == api.EntryUser && len(u.Entry.Context) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("no update event with context")
	}
	// A new session sends the instruction again.
	f.call("agent.newSession", nil, nil)
	f.prompt("third", "")
	if got := blockTexts(rec.turns[2]); len(got) != 4 || got[1] != "VERIFY THE KNOWLEDGE" {
		t.Fatalf("%q", got)
	}
}

// Tools of read-only MCP servers are allowed without asking; other
// tools still follow "Other tools".
func TestReadOnlyServerToolsAllowed(t *testing.T) {
	var answers []string
	var mu sync.Mutex
	script := func(ctx context.Context, tt *acptest.Turn) string {
		for _, tc := range []acp.ToolCall{
			{ToolCallID: "k1", Name: "tiegel-knowledge_knowledge_search", Title: "tiegel-knowledge_knowledge_search", RawInput: json.RawMessage(`{"query":"invoice"}`)},
			{ToolCallID: "k2", Title: "mcp__tiegel-knowledge__knowledge_read"},
			{ToolCallID: "o1", Name: "other-server_lookup", Title: "other-server_lookup"},
			{ToolCallID: "o2", Name: "tiegel-knowledge-evil_x", Title: "tiegel-knowledge-evil_x"},
		} {
			a := tt.Permission(ctx, tc)
			mu.Lock()
			answers = append(answers, a)
			mu.Unlock()
		}
		return ""
	}
	f := newFixture(t, &acptest.Agent{Script: script}, nil)
	api.HookAgentMCPServers.On(f.app.Hooks(), 0, func(_ context.Context, l []api.AgentMCPServer) ([]api.AgentMCPServer, error) {
		return append(slices.Clone(l), api.AgentMCPServer{Name: "tiegel-knowledge", Command: "/bin/tiegel", ReadOnly: true},
			api.AgentMCPServer{Name: "other-server", Command: "/bin/other"}), nil
	})
	f.prompt("search", api.PolicyDeny)
	if !slices.Equal(answers, []string{"once", "once", "reject", "reject"}) {
		t.Fatal(answers)
	}
	blocked := 0
	for _, e := range f.transcript() {
		if e.Kind == api.EntryPermission {
			t.Fatalf("asked: %+v", e.Permission)
		}
		if e.Kind == api.EntryNotice && e.Level == api.NoticeBlocked {
			blocked++
		}
	}
	if blocked != 2 {
		t.Fatal(blocked)
	}
	// "Other tools: allow" also allows the other server.
	f.set(SettingOther, api.PolicyAllow)
	answers = nil
	f.prompt("again", "")
	if !slices.Equal(answers, []string{"once", "once", "once", "once"}) {
		t.Fatal(answers)
	}
}

func TestServerTool(t *testing.T) {
	for _, c := range []struct {
		name, srv string
		want      bool
	}{
		{"tiegel-knowledge_knowledge_search", "tiegel-knowledge", true},
		{"mcp__tiegel-knowledge__knowledge_list", "tiegel-knowledge", true},
		{"tiegel-knowledge.knowledge_read", "tiegel-knowledge", true},
		{"my_kb_search", "my kb", true},
		{"tiegel-knowledge_", "tiegel-knowledge", false},
		{"tiegel-knowledgeX_search", "tiegel-knowledge", false},
		{"read", "tiegel-knowledge", false},
		{"", "tiegel-knowledge", false},
		{"x_y", "", false},
	} {
		if got := serverTool(c.name, c.srv); got != c.want {
			t.Errorf("serverTool(%q, %q) = %v", c.name, c.srv, got)
		}
	}
}

func TestReadOnlyCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"grep -rn invoice src":                    true,
		"rg -n Invoice src | head -20":            true,
		"cd src && ls -la":                        true,
		"find src -name '*.php'":                  true,
		"cat src/a.php 2>/dev/null":               true,
		"grep x src 2>&1 | wc -l":                 true,
		"sed -n 1,20p src/a.php":                  true,
		"git log --oneline -5":                    true,
		"git -C src show HEAD:a.php":              true,
		"":                                        false,
		"rm -rf src":                              false,
		"echo x > a.php":                          false,
		"cat a >> b":                              false,
		"sed -i s/a/b/ a.php":                     false,
		"sed --in-place s/a/b/ a.php":             false,
		"find . -name x -delete":                  false,
		"find . -exec rm {} \\;":                  false,
		"git commit -am x":                        false,
		"git checkout main":                       false,
		"git push":                                false,
		"ls && npm install":                       false,
		"cat $(echo a)":                           false,
		"echo `id`":                               false,
		"awk 'BEGIN{system(\"rm -rf x\")}'":       false,
		"php artisan migrate":                     false,
		"/usr/bin/grep -r x .":                    true,
		"git branch -D main":                      false,
		"cat a.php | tee b.php":                   false,
		"git status && git diff":                  true,
		"vendor/bin/phpunit --filter InvoiceTest": false,
	} {
		if got := readOnlyCommand(cmd); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// evalScript answers like a reading agent: a read, a grep, a glob, an
// allowed read-only shell command, a rejected writing command and edit,
// a knowledge tool when the session has the MCP server, and an answer
// citing a file:line.
func evalScript(withKB func(sessionID string) bool) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		t.Thought("Let me look.")
		t.Tool(false, acp.ToolCall{ToolCallID: "r", Name: "read", Title: "read src/a.php", Kind: acp.KindRead, Status: acp.StatusCompleted})
		t.Tool(false, acp.ToolCall{ToolCallID: "g", Name: "grep", Title: "grep invoice", Kind: acp.KindSearch, Status: acp.StatusCompleted})
		t.Tool(false, acp.ToolCall{ToolCallID: "l", Name: "glob", Title: "glob **/*.php", Kind: acp.KindSearch, Status: acp.StatusCompleted})
		sh := acp.ToolCall{ToolCallID: "s", Name: "bash", Title: "grep -rn x src", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"grep -rn x src"}`)}
		t.Tool(false, sh)
		if t.Permission(ctx, sh) == "once" {
			sh.Status = acp.StatusCompleted
		}
		t.Tool(true, sh)
		t.Permission(ctx, acp.ToolCall{ToolCallID: "w", Name: "bash", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"rm -rf src"}`)})
		t.Permission(ctx, acp.ToolCall{ToolCallID: "e", Name: "edit", Kind: acp.KindEdit, Title: "edit"})
		t.Permission(ctx, acp.ToolCall{ToolCallID: "c", Name: "bash", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"git log -1"}`)})
		if withKB(t.SessionID) {
			kb := acp.ToolCall{ToolCallID: "k", Name: "tiegel-knowledge_knowledge_search", Title: "tiegel-knowledge_knowledge_search"}
			t.Tool(false, kb)
			if t.Permission(ctx, kb) == "once" {
				kb.Status = acp.StatusCompleted
				t.Tool(true, kb)
			}
		}
		if strings.Contains(t.Prompt, "KB HITS") {
			t.Text("The invoice is saved in src/Billing/InvoiceService.php:12 ")
		}
		t.Text("and checked in src/a.php:3.")
		return ""
	}
}

func TestEval(t *testing.T) {
	var mu sync.Mutex
	fake := &acptest.Agent{}
	// The fake numbers its sessions ses_1, ses_2 …: a session has the
	// MCP server when its session/new carried one.
	fake.Script = evalScript(func(sid string) bool {
		n, err := strconv.Atoi(strings.TrimPrefix(sid, "ses_"))
		news := fake.NewSessions()
		return err == nil && n >= 1 && n <= len(news) && len(news[n-1].MCPServers) > 0
	})
	f := newFixture(t, fake, nil)
	var modes []string
	api.HookAgentMCPServers.On(f.app.Hooks(), 0, func(ctx context.Context, l []api.AgentMCPServer) ([]api.AgentMCPServer, error) {
		mode := api.AgentContextModeFrom(ctx)
		mu.Lock()
		modes = append(modes, mode)
		mu.Unlock()
		if mode == api.AgentContextMCP || mode == api.AgentContextBoth {
			return append(slices.Clone(l), api.AgentMCPServer{Name: "tiegel-knowledge", Command: "/bin/tiegel", ReadOnly: true}), nil
		}
		return l, nil
	})
	api.HookAgentPromptContext.On(f.app.Hooks(), 0, func(ctx context.Context, pc api.AgentPromptContext) (api.AgentPromptContext, error) {
		mode := api.AgentContextModeFrom(ctx)
		if mode != api.AgentContextRAG && mode != api.AgentContextBoth {
			return pc, nil
		}
		out := pc
		out.Blocks = append(slices.Clone(pc.Blocks), api.AgentContextBlock{Source: "kb", Text: "KB HITS", Tokens: 120})
		return out, nil
	})

	// Without confirm: the plan only, nothing runs.
	var plan api.AgentEvalResult
	f.call("agent.eval", api.AgentEvalParams{Questions: []string{" Where are invoices saved? ", "", "How is the cart cleared?"}, Runs: 2}, &plan)
	if plan.Ran || plan.Plan.Total != 16 || len(plan.Plan.Modes) != 4 || len(plan.Plan.Questions) != 2 || !strings.Contains(plan.Plan.Note, "billed") || len(fake.Prompts()) != 0 {
		t.Fatalf("%+v", plan)
	}
	for _, bad := range []api.AgentEvalParams{{}, {Questions: []string{"q"}, Modes: []string{"magic"}}, {Questions: []string{"q"}, Runs: 99}} {
		var r api.AgentEvalResult
		if err := f.app.CallLocal(context.Background(), "agent.eval", bad, &r); err == nil {
			t.Fatalf("no error for %+v", bad)
		}
	}

	f.call("agent.start", nil, nil)
	var res api.AgentEvalResult
	params := api.AgentEvalParams{Questions: []string{"Where are invoices saved?"}, Modes: []string{"off", "mcp", "rag", "both"}, Runs: 2, Confirm: true}
	f.call("agent.eval", params, &res)
	if !res.Ran || len(res.Runs) != 8 || res.Dir == "" || len(res.Summary) != 4 {
		t.Fatalf("%+v", res)
	}
	for _, r := range res.Runs {
		if r.Error != "" {
			t.Fatalf("%+v", r)
		}
		wantKB := r.Mode == "mcp" || r.Mode == "both"
		if r.Tools["read"] != 1 || r.Tools["grep"] != 1 || r.Tools["glob"] != 1 || r.Tools["shell"] != 1 || r.Shell != 1 {
			t.Fatalf("%s: %+v", r.Mode, r.Tools)
		}
		if wantKB != (r.Tools["knowledge_search"] == 1) {
			t.Fatalf("%s: %+v", r.Mode, r.Tools)
		}
		// rm, edit and git are rejected (git log reads, but git counts
		// as a read-only command: allowed).
		if r.Blocked != 2 {
			t.Fatalf("%s blocked %d", r.Mode, r.Blocked)
		}
		wantCtx := r.Mode == "rag" || r.Mode == "both"
		if wantCtx != (r.ContextTokens == 120) || wantCtx != (r.Citations == 2) || !r.Cites {
			t.Fatalf("%+v", r)
		}
		if r.Chars == 0 || r.Tokens != (r.Chars+3)/4 || !strings.Contains(r.Answer, "src/a.php:3") {
			t.Fatalf("%+v", r)
		}
		if filepath.Base(r.File) != r.Mode+"-"+strconv.Itoa(r.N)+".md" {
			t.Fatal(r.File)
		}
		b, err := os.ReadFile(r.File)
		if err != nil || !strings.Contains(string(b), "Where are invoices saved?") || !strings.Contains(string(b), "src/a.php:3") {
			t.Fatalf("%s: %v", b, err)
		}
	}
	if res.Runs[1].N != 2 || res.Runs[1].Run != 2 {
		t.Fatalf("%+v", res.Runs[1])
	}
	for _, s := range res.Summary {
		if s.Runs != 2 || s.CiteRate != 1 || s.AvgToolCalls < 4 {
			t.Fatalf("%+v", s)
		}
	}
	// Each run had its own session with the mode in the hooks; afterwards
	// a normal session (no mode) is left and the settings apply again.
	mu.Lock()
	got := slices.Clone(modes)
	mu.Unlock()
	if got[len(got)-1] != "" || !slices.Contains(got, "rag") {
		t.Fatal(got)
	}
	var st api.AgentStatus
	f.call("agent.status", nil, &st)
	if st.Policy.Summary != "Edits: allowed · Shell: ask · Commits: blocked · Push: blocked · Web: ask" || st.State != api.AgentReady {
		t.Fatalf("%+v", st)
	}
	if len(f.changes()) != 0 {
		t.Fatal("eval changed files")
	}
	if n := len(f.rec.Events(api.TopicAgentEval)); n != 16 {
		t.Fatalf("%d progress events", n)
	}
}

func TestToolKind(t *testing.T) {
	for _, c := range []struct {
		t    api.AgentToolCall
		want string
	}{
		{api.AgentToolCall{Name: "read"}, "read"},
		{api.AgentToolCall{Title: "view src/a.php"}, "read"},
		{api.AgentToolCall{Kind: "read", Name: "weird"}, "read"},
		{api.AgentToolCall{Name: "grep"}, "grep"},
		{api.AgentToolCall{Kind: "search"}, "grep"},
		{api.AgentToolCall{Name: "glob"}, "glob"},
		{api.AgentToolCall{Name: "list"}, "glob"},
		{api.AgentToolCall{Name: "bash", Command: "ls"}, "shell"},
		{api.AgentToolCall{Kind: "execute"}, "shell"},
		{api.AgentToolCall{Name: "tiegel-knowledge_knowledge_read"}, "knowledge_read"},
		{api.AgentToolCall{Title: "mcp__tiegel-knowledge__knowledge_list"}, "knowledge_list"},
		{api.AgentToolCall{Name: "todowrite"}, "other"},
	} {
		if got := toolKind(&c.t); got != c.want {
			t.Errorf("%+v: %s, want %s", c.t, got, c.want)
		}
	}
}
