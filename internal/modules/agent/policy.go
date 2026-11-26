package agent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Permission enforcement (D549): every tool call the agent asks about is
// put into a category and decided by the settings – allow, ask (the user
// decides in the Agent window) or deny. Shell commands are parsed, so
// "cd x && git commit -m y" counts as a git commit, not as a plain shell
// command.

// commitVerbs are git subcommands that create or rewrite history or
// throw away work; they count as "git commit".
var commitVerbs = map[string]bool{
	"commit": true, "merge": true, "rebase": true, "reset": true, "cherry-pick": true,
	"revert": true, "am": true, "tag": true, "stash": true, "pull": true,
	"filter-branch": true, "filter-repo": true, "update-ref": true, "notes": true,
}

// gitValueOptions are global git options followed by a separate value.
var gitValueOptions = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "--exec-path": true}

// shellWrappers are prefixes that run the rest of the line as a command.
var shellWrappers = map[string]bool{"sudo": true, "env": true, "command": true, "exec": true, "nohup": true, "time": true, "xargs": true, "nice": true}

var (
	splitShell = regexp.MustCompile(`&&|\|\||[;|&\n(){}` + "`" + `]|\$\(`)
	quoted     = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)
)

// gitVerbs returns the git subcommands a shell command line runs, also
// inside quoted scripts ("sh -c 'git push'").
func gitVerbs(cmd string) []string { return gitVerbsDepth(cmd, 0) }

func gitVerbsDepth(cmd string, depth int) []string {
	var out []string
	if depth < 3 {
		for _, m := range quoted.FindAllStringSubmatch(cmd, -1) {
			out = append(out, gitVerbsDepth(m[1]+m[2], depth+1)...)
		}
	}
	for _, seg := range splitShell.Split(cmd, -1) {
		words := strings.Fields(strings.NewReplacer(`"`, " ", `'`, " ").Replace(seg))
		for len(words) > 0 {
			w := words[0]
			if shellWrappers[w] || strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
				words = words[1:]
				continue
			}
			break
		}
		if len(words) == 0 || filepath.Base(words[0]) != "git" {
			continue
		}
		rest := words[1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			if gitValueOptions[rest[0]] && len(rest) > 1 {
				rest = rest[2:]
				continue
			}
			rest = rest[1:]
		}
		if len(rest) > 0 {
			out = append(out, rest[0])
		}
	}
	return out
}

// shellCategory classifies a shell command: git push, git commit (and
// other history changes) or plain shell.
func shellCategory(cmd string) (cat, verb string) {
	cat = api.AgentCatShell
	for _, v := range gitVerbs(cmd) {
		switch {
		case v == "push":
			return api.AgentCatGitPush, "git push"
		case commitVerbs[v] && cat == api.AgentCatShell:
			cat, verb = api.AgentCatGitCommit, "git "+v
		}
	}
	return cat, verb
}

// inputField returns a string field of a tool's raw input.
func inputField(raw json.RawMessage, names ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, n := range names {
		if s, ok := m[n].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// commandOf returns the shell command of a tool call.
func commandOf(tc acp.ToolCall) string {
	return inputField(tc.RawInput, "command", "cmd")
}

// isShell tells whether a tool call runs a command.
func isShell(tc acp.ToolCall) bool {
	switch strings.ToLower(tc.Name) {
	case "bash", "shell", "execute_command", "run_command", "terminal":
		return true
	}
	return tc.Kind == acp.KindExecute || commandOf(tc) != "" && tc.Kind != acp.KindEdit
}

// isEdit tells whether a tool call changes files.
func isEdit(tc acp.ToolCall) bool {
	switch tc.Kind {
	case acp.KindEdit, acp.KindDelete, acp.KindMove:
		return true
	}
	switch strings.ToLower(tc.Name) {
	case "edit", "write", "patch", "apply_patch", "multiedit", "str_replace", "create", "delete":
		return true
	}
	for _, c := range tc.Content {
		if c.Type == "diff" {
			return true
		}
	}
	return false
}

// isWeb tells whether a tool call accesses the network.
func isWeb(tc acp.ToolCall) bool {
	switch strings.ToLower(tc.Name) {
	case "webfetch", "websearch", "fetch", "web_search", "codesearch":
		return true
	}
	return tc.Kind == acp.KindFetch
}

// category returns the permission category of a tool call and, for git
// commands, the blocked verb ("git commit").
func category(tc acp.ToolCall) (cat, verb string) {
	switch {
	case isShell(tc):
		return shellCategory(commandOf(tc))
	case isEdit(tc):
		return api.AgentCatEdit, ""
	case isWeb(tc):
		return api.AgentCatWeb, ""
	}
	return api.AgentCatOther, ""
}

// decide returns the policy of category cat.
func decide(p api.AgentPolicy, cat string) string {
	switch cat {
	case api.AgentCatEdit:
		return p.Edit
	case api.AgentCatShell:
		return p.Shell
	case api.AgentCatGitCommit:
		// A commit is a shell command too: a denied shell blocks it.
		if p.Shell == api.PolicyDeny {
			return api.PolicyDeny
		}
		return p.GitCommit
	case api.AgentCatGitPush:
		if p.Shell == api.PolicyDeny {
			return api.PolicyDeny
		}
		return p.GitPush
	case api.AgentCatWeb:
		return p.Web
	}
	return p.Other
}

// pickOption returns the first option of one of kinds (in order of
// preference), or "".
func pickOption(opts []acp.PermissionOption, kinds ...string) string {
	for _, k := range kinds {
		for _, o := range opts {
			if o.Kind == k {
				return o.OptionID
			}
		}
	}
	return ""
}

// allowOption and rejectOption pick the one-time answers (Tiegel keeps
// "always" decisions itself, so every call keeps coming through it).
func allowOption(opts []acp.PermissionOption) string {
	return pickOption(opts, acp.AllowOnce, acp.AllowAlways)
}

func rejectOption(opts []acp.PermissionOption) string {
	return pickOption(opts, acp.RejectOnce, acp.RejectAlways)
}

// optionKind returns the kind of option id.
func optionKind(opts []acp.PermissionOption, id string) string {
	for _, o := range opts {
		if o.OptionID == id {
			return o.Kind
		}
	}
	return ""
}

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
