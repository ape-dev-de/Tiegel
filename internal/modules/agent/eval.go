package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Evaluation of the context modes (agent.eval, D571): every question is
// asked in every mode in a fresh session, with a read-only policy (no
// edits, no writing shell commands, no git, no web), and the turn is
// measured – duration, tool calls by kind, streamed characters, whether
// the answer cites code locations. The answers are saved for reading side
// by side. Runs cost tokens: without Confirm only the plan is returned.

// Limits of agent.eval.
const (
	maxEvalRuns    = 20
	maxEvalTotal   = 200
	defaultEvalSec = 600
)

// citeRE matches code locations like src/Billing/InvoiceService.php:12.
var citeRE = regexp.MustCompile(`[A-Za-z0-9_][\w./\\-]*\.[A-Za-z0-9]{1,8}:\d+`)

// knowledgeToolRE extracts the knowledge tool of a tool name (the last
// "knowledge_<tool>": "tiegel-knowledge_knowledge_read" → knowledge_read).
var knowledgeToolRE = regexp.MustCompile(`knowledge_([a-z]+)`)

// knowledgeTool returns "knowledge_<tool>" of a tool name, or "".
func knowledgeTool(s string) string {
	i := strings.LastIndex(s, "knowledge_")
	if i < 0 {
		return ""
	}
	if m := knowledgeToolRE.FindStringSubmatch(s[i:]); m != nil {
		return "knowledge_" + m[1]
	}
	return ""
}

// evalPlan validates the params and returns the plan.
func (m *Module) evalPlan(p api.AgentEvalParams) (api.AgentEvalPlan, error) {
	var qs []string
	for _, q := range p.Questions {
		if q = strings.TrimSpace(q); q != "" {
			qs = append(qs, q)
		}
	}
	if len(qs) == 0 {
		return api.AgentEvalPlan{}, api.Errorf(api.CodeInvalidParams, "at least one question is required")
	}
	modes := p.Modes
	if len(modes) == 0 {
		modes = api.AgentContextModes
	}
	var ms []string
	for _, mode := range modes {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !slices.Contains(api.AgentContextModes, mode) {
			return api.AgentEvalPlan{}, api.Errorf(api.CodeInvalidParams, "unknown mode %q (off, mcp, rag, both)", mode)
		}
		if !slices.Contains(ms, mode) {
			ms = append(ms, mode)
		}
	}
	runs := p.Runs
	if runs <= 0 {
		runs = 1
	}
	if runs > maxEvalRuns {
		return api.AgentEvalPlan{}, api.Errorf(api.CodeInvalidParams, "at most %d runs", maxEvalRuns)
	}
	total := len(ms) * len(qs) * runs
	if total > maxEvalTotal {
		return api.AgentEvalPlan{}, api.Errorf(api.CodeInvalidParams, "%d runs are too many (at most %d)", total, maxEvalTotal)
	}
	line, _ := m.command()
	model := setting(m, SettingModel, "")
	if model == "" {
		if o, ok := m.modelOption(); ok {
			model = string(o.CurrentValue)
		}
	}
	return api.AgentEvalPlan{Modes: ms, Questions: qs, Runs: runs, Total: total, Agent: line, Model: model,
		Note: fmt.Sprintf("Runs %d agent turns, each in a new read-only session. They are billed by your agent's provider; the current agent session is replaced.", total)}, nil
}

// eval is agent.eval.
func (m *Module) eval(ctx context.Context, p api.AgentEvalParams) (api.AgentEvalResult, error) {
	plan, err := m.evalPlan(p)
	if err != nil {
		return api.AgentEvalResult{}, err
	}
	res := api.AgentEvalResult{Plan: plan}
	if !p.Confirm {
		return res, nil
	}
	app := m.application()
	if app == nil {
		return res, api.Errorf(api.CodeFailed, "agent: module not started")
	}
	if _, err := app.RequireProject(); err != nil {
		return res, err
	}
	if !m.evalMu.TryLock() {
		return res, api.Errorf(api.CodeConflict, "an evaluation is running")
	}
	defer m.evalMu.Unlock()
	m.mu.Lock()
	busy := m.turnCancel != nil
	m.mu.Unlock()
	if busy {
		return res, api.Errorf(api.CodeConflict, "the agent is working on a message (agent.cancel ends it)")
	}
	if ps := app.ProjectSettings(); ps != nil && ps.Dir() != "" {
		dir, err := evalDir(filepath.Join(ps.Dir(), "eval"), time.Now())
		if err != nil {
			return res, api.Errorf(api.CodeFailed, "eval directory: %w", err)
		}
		res.Dir = dir
	}
	pol := evalPolicy(setting(m, SettingRules, ""))
	m.evalPol.Store(&pol)
	defer func() {
		m.evalPol.Store(nil)
		m.setCtxMode("")
		// Leave a normal session behind (the mode of the settings).
		m.mu.Lock()
		running := m.proc != nil
		m.mu.Unlock()
		if running {
			_, _ = m.newSession(context.WithoutCancel(ctx), struct{}{})
		}
		m.publishStatus()
	}()
	timeout := time.Duration(p.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultEvalSec * time.Second
	}
	index := 0
	for _, mode := range plan.Modes {
		for qi, q := range plan.Questions {
			for r := 0; r < plan.Runs; r++ {
				if err := ctx.Err(); err != nil {
					return m.summarizeEval(res), err
				}
				index++
				m.publish(api.TopicAgentEval, api.AgentEvalProgress{Index: index, Total: plan.Total, Mode: mode})
				run := m.evalRun(ctx, mode, q, timeout)
				run.Question, run.Run, run.N = qi+1, r+1, qi*plan.Runs+r+1
				if res.Dir != "" {
					run.File = filepath.Join(res.Dir, fmt.Sprintf("%s-%d.md", mode, run.N))
					if err := os.WriteFile(run.File, []byte(evalMarkdown(run, q)), 0o644); err != nil {
						run.File = ""
					}
				}
				res.Runs = append(res.Runs, run)
				rc := run
				m.publish(api.TopicAgentEval, api.AgentEvalProgress{Index: index, Total: plan.Total, Mode: mode, Done: true, Run: &rc})
			}
		}
	}
	res.Ran = true
	return m.summarizeEval(res), nil
}

// evalDir creates a new directory for the answers of an evaluation
// (<base>/<timestamp>, with a suffix when it exists).
func evalDir(base string, now time.Time) (string, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	stamp := now.Format("20060102-150405")
	for i := 1; ; i++ {
		dir := filepath.Join(base, stamp)
		if i > 1 {
			dir = fmt.Sprintf("%s-%d", dir, i)
		}
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) || i > 100 {
			return "", err
		}
	}
}

func (m *Module) setCtxMode(mode string) {
	m.mu.Lock()
	m.ctxMode = mode
	m.mu.Unlock()
}

// evalRun asks q in a new session of mode and measures the turn.
func (m *Module) evalRun(ctx context.Context, mode, q string, timeout time.Duration) api.AgentEvalRun {
	run := api.AgentEvalRun{Mode: mode, Tools: map[string]int{}}
	m.setCtxMode(mode)
	if _, err := m.newSession(ctx, struct{}{}); err != nil {
		run.Error = err.Error()
		return run
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	tr, err := m.prompt(rctx, api.AgentPromptParams{Text: q, Wait: true, Ask: api.PolicyDeny})
	run.DurationMS = time.Since(start).Milliseconds()
	run.StopReason = tr.StopReason
	switch {
	case err != nil:
		run.Error = err.Error()
	case tr.Error != "":
		run.Error = tr.Error
	case rctx.Err() != nil && ctx.Err() == nil:
		run.Error = "timed out after " + timeout.String()
	}
	es, _ := m.transcript(ctx, struct{}{})
	measure(&run, es)
	return run
}

// measure fills the counters of run from the transcript of its session.
func measure(run *api.AgentEvalRun, es []api.AgentEntry) {
	var answer []string
	for _, e := range es {
		switch e.Kind {
		case api.EntryUser:
			for _, b := range e.Context {
				run.ContextTokens += b.Tokens
			}
		case api.EntryAgent:
			run.Chars += len(e.Text)
			answer = append(answer, strings.TrimSpace(e.Text))
		case api.EntryThought:
			run.Chars += len(e.Text)
		case api.EntryTool:
			if e.Tool == nil {
				continue
			}
			k := toolKind(e.Tool)
			run.Tools[k]++
			run.ToolCalls++
			if k == "shell" {
				run.Shell++
			}
		case api.EntryNotice:
			if e.Level == api.NoticeBlocked {
				run.Blocked++
			}
		case api.EntryPermission:
			if e.Permission != nil && e.Permission.Answer != "" && !e.Permission.Allowed {
				run.Blocked++
			}
		}
	}
	run.Answer = strings.Join(answer, "\n\n")
	run.Tokens = (run.Chars + 3) / 4
	run.Citations = len(citeRE.FindAllString(run.Answer, -1))
	run.Cites = run.Citations > 0
}

// toolKind sorts a tool call into read, grep, glob, shell,
// knowledge_<tool> or other.
func toolKind(t *api.AgentToolCall) string {
	name, title := strings.ToLower(t.Name), strings.ToLower(t.Title)
	if k := knowledgeTool(name); k != "" {
		return k
	}
	if k := knowledgeTool(title); k != "" {
		return k
	}
	if t.Command != "" || isShell(acp.ToolCall{Name: t.Name, Kind: t.Kind}) {
		return "shell"
	}
	word := name
	if word == "" {
		word, _, _ = strings.Cut(title, " ")
	}
	switch word {
	case "read", "view", "read_file", "readfile", "cat":
		return "read"
	case "grep", "search", "rg", "ripgrep", "codesearch_local":
		return "grep"
	case "glob", "list", "ls", "find", "list_files":
		return "glob"
	}
	switch t.Kind {
	case "read":
		return "read"
	case "search":
		return "grep"
	}
	return "other"
}

// summarizeEval adds the per-mode averages.
func (m *Module) summarizeEval(res api.AgentEvalResult) api.AgentEvalResult {
	res.Summary = nil
	for _, mode := range res.Plan.Modes {
		s := api.AgentEvalSummary{Mode: mode}
		var dur int64
		var tools, shell, know, tokens, ctxTok, cites float64
		for _, r := range res.Runs {
			if r.Mode != mode {
				continue
			}
			s.Runs++
			if r.Error != "" {
				s.Errors++
			}
			dur += r.DurationMS
			tools += float64(r.ToolCalls)
			shell += float64(r.Shell)
			tokens += float64(r.Tokens)
			ctxTok += float64(r.ContextTokens)
			for k, n := range r.Tools {
				if strings.HasPrefix(k, "knowledge_") {
					know += float64(n)
				}
			}
			if r.Cites {
				cites++
			}
		}
		if s.Runs > 0 {
			n := float64(s.Runs)
			s.AvgDurationMS = dur / int64(s.Runs)
			s.AvgToolCalls, s.AvgShell, s.AvgKnowledge = tools/n, shell/n, know/n
			s.AvgTokens, s.AvgContext, s.CiteRate = tokens/n, ctxTok/n, cites/n
		}
		res.Summary = append(res.Summary, s)
	}
	return res
}

// evalMarkdown is the saved answer of a run.
func evalMarkdown(r api.AgentEvalRun, q string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s – question %d, run %d\n\n", r.Mode, r.Question, r.Run)
	fmt.Fprintf(&b, "**Question:** %s\n\n", q)
	var kinds []string
	for k, n := range r.Tools {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
	}
	slices.Sort(kinds)
	fmt.Fprintf(&b, "- Duration: %.1f s\n- Tool calls: %d (%s)\n- Streamed: %d chars (~%d tokens)\n", float64(r.DurationMS)/1000, r.ToolCalls, strings.Join(kinds, ", "), r.Chars, r.Tokens)
	if r.ContextTokens > 0 {
		fmt.Fprintf(&b, "- Injected context: ~%d tokens\n", r.ContextTokens)
	}
	fmt.Fprintf(&b, "- Citations (file:line): %d\n", r.Citations)
	if r.Blocked > 0 {
		fmt.Fprintf(&b, "- Blocked requests: %d\n", r.Blocked)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "- Error: %s\n", r.Error)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(r.Answer)
	b.WriteString("\n")
	return b.String()
}
