package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/agentpolicy"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Permission enforcement (D549): every tool call the agent asks about is
// put into a category and decided by the settings – allow, ask (the user
// decides in the Agent window) or deny. Shell commands are parsed, so
// "cd x && git commit -m y" counts as a git commit, not as a plain shell
// command.

// Aliases of the shared classification (internal/agentpolicy).
var (
	commitVerbs   = agentpolicy.CommitVerbs
	gitVerbs      = agentpolicy.GitVerbs
	shellCategory = agentpolicy.ShellCategory
	inputField    = agentpolicy.InputField
	commandOf     = agentpolicy.CommandOf
	category      = agentpolicy.Category
	decide        = agentpolicy.Decide
	allowOption   = agentpolicy.AllowOption
	rejectOption  = agentpolicy.RejectOption
	optionKind    = agentpolicy.OptionKind
)

// alwaysKey is what an "Always allow" answer covers for the rest of the
// session: all edits, the program of a shell command ("npm"), the web,
// or one tool.
func alwaysKey(cat string, tc acp.ToolCall) string {
	switch cat {
	case api.AgentCatEdit, api.AgentCatWeb:
		return cat
	case api.AgentCatShell:
		if f := strings.Fields(commandOf(tc)); len(f) > 0 {
			return cat + ":" + filepath.Base(f[0])
		}
		return cat
	case api.AgentCatGitCommit, api.AgentCatGitPush:
		return "" // never remembered: each commit is confirmed
	}
	return cat + ":" + tc.Name
}

// label names a category for blocked notices and summaries.
func label(cat string) string {
	switch cat {
	case api.AgentCatEdit:
		return "file edits"
	case api.AgentCatShell:
		return "shell commands"
	case api.AgentCatGitCommit:
		return "git commits"
	case api.AgentCatGitPush:
		return "git push"
	case api.AgentCatWeb:
		return "web access"
	}
	return "this tool"
}

// policyWord describes a policy value for the summary.
func policyWord(v string, blockedWord string) string {
	switch v {
	case api.PolicyAllow:
		return "allowed"
	case api.PolicyDeny:
		return blockedWord
	}
	return "ask"
}

// summarize fills p.Summary ("Edits: allowed · Shell: ask · Commits:
// blocked · Push: blocked · Web: ask").
func summarize(p api.AgentPolicy) api.AgentPolicy {
	parts := []string{
		"Edits: " + policyWord(p.Edit, "blocked"),
		"Shell: " + policyWord(p.Shell, "blocked"),
		"Commits: " + policyWord(decide(p, api.AgentCatGitCommit), "blocked"),
		"Push: " + policyWord(decide(p, api.AgentCatGitPush), "blocked"),
		"Web: " + policyWord(p.Web, "blocked"),
	}
	p.Summary = strings.Join(parts, " · ")
	return p
}

// rulesPreamble is sent with the first prompt of a session: the user's
// written rules and, in words, what Tiegel enforces.
func rulesPreamble(p api.AgentPolicy) string {
	var b strings.Builder
	b.WriteString("Context from the IDE (Tiegel) – please follow it:\n")
	var blocked []string
	if decide(p, api.AgentCatGitCommit) == api.PolicyDeny {
		blocked = append(blocked, "git commits and other history changes (commit, merge, rebase, reset, tag, stash, pull)")
	}
	if decide(p, api.AgentCatGitPush) == api.PolicyDeny {
		blocked = append(blocked, "git push")
	}
	if p.Shell == api.PolicyDeny {
		blocked = append(blocked, "shell commands")
	}
	if p.Web == api.PolicyDeny {
		blocked = append(blocked, "web access")
	}
	if len(blocked) > 0 {
		b.WriteString("- The user disabled " + strings.Join(blocked, ", ") + "; such tool calls are rejected. Do not try them, leave them to the user.\n")
	}
	b.WriteString("- Your file changes are shown to the user, who can review and revert them.\n")
	if r := strings.TrimSpace(p.Rules); r != "" {
		b.WriteString("\nRules from the user:\n" + r + "\n")
	}
	return b.String()
}

// opencodeConfig is the inline configuration (OPENCODE_CONFIG_CONTENT)
// that makes opencode ask Tiegel before edits, commands and web access,
// and deny the blocked categories itself as a second line of defence.
func opencodeConfig(p api.AgentPolicy) string {
	bash := map[string]string{"*": "ask"}
	if p.Shell == api.PolicyDeny {
		bash["*"] = "deny"
	}
	if decide(p, api.AgentCatGitCommit) == api.PolicyDeny {
		for v := range commitVerbs {
			bash["git "+v+"*"] = "deny"
			bash["git * "+v+"*"] = "deny"
		}
	}
	if decide(p, api.AgentCatGitPush) == api.PolicyDeny {
		bash["git push*"] = "deny"
		bash["git * push*"] = "deny"
	}
	web := "ask"
	if p.Web == api.PolicyDeny {
		web = "deny"
	}
	cfg := map[string]any{"permission": map[string]any{
		// Always ask: Tiegel answers at once when edits are allowed, but
		// takes the snapshot for revert first.
		"edit":      "ask",
		"bash":      bash,
		"webfetch":  web,
		"websearch": web,
	}}
	b, _ := json.Marshal(cfg)
	return string(b)
}
