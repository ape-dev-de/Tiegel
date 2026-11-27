package agent

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Read-only MCP servers and the permissions of agent.eval (D571).

// sanitizeTool is how agents turn an MCP server name into a tool name
// prefix (opencode: characters outside [A-Za-z0-9_-] become "_").
var sanitizeTool = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// serverTool tells whether a tool name belongs to MCP server srv:
// "<srv>_<tool>" (opencode), "mcp__<srv>__<tool>" (Claude) or
// "<srv>.<tool>" / "<srv>:<tool>" / "<srv>/<tool>".
func serverTool(name, srv string) bool {
	name = strings.TrimSpace(name)
	if name == "" || srv == "" {
		return false
	}
	for _, s := range []string{srv, sanitizeTool.ReplaceAllString(srv, "_")} {
		for _, prefix := range []string{s + "_", "mcp__" + s + "__", s + ".", s + ":", s + "/"} {
			if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
				return true
			}
		}
	}
	return false
}

// readOnlyTool tells whether tc is a tool of a read-only MCP server of
// the session (the project knowledge): such calls are allowed without
// asking, whatever "Other tools" says.
func (m *Module) readOnlyTool(tc acp.ToolCall) bool {
	m.mu.Lock()
	servers := m.readOnly
	m.mu.Unlock()
	for _, srv := range servers {
		if serverTool(tc.Name, srv) || serverTool(tc.Title, srv) {
			return true
		}
	}
	return false
}

// evalPolicy is the policy of agent.eval: nothing that changes the
// project or leaves the machine.
func evalPolicy(rules string) api.AgentPolicy {
	r := "This is a read-only evaluation run: answer the question from the code. Do not change files, do not run commands that write, do not use git or the web."
	if rules = strings.TrimSpace(rules); rules != "" {
		r = rules + "\n" + r
	}
	return summarize(api.AgentPolicy{Edit: api.PolicyDeny, Shell: api.PolicyAsk, GitCommit: api.PolicyDeny, GitPush: api.PolicyDeny,
		Web: api.PolicyDeny, Other: api.PolicyDeny, Rules: r})
}

// evalDecision decides a permission request during agent.eval: shell
// commands that only read are allowed, everything else is rejected.
func evalDecision(cat string, tc acp.ToolCall) string {
	if cat == api.AgentCatShell && readOnlyCommand(commandOf(tc)) {
		return api.PolicyAllow
	}
	return api.PolicyDeny
}

// readOnlyPrograms are the programs a read-only command line may run.
var readOnlyPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true, "fgrep": true, "rg": true,
	"ag": true, "find": true, "fd": true, "tree": true, "file": true, "stat": true, "pwd": true, "echo": true, "sort": true,
	"uniq": true, "cut": true, "tr": true, "sed": true, "awk": true, "nl": true, "diff": true, "basename": true,
	"dirname": true, "realpath": true, "which": true, "true": true, "git": true, "jq": true, "column": true,
}

// readOnlyGit are the git subcommands that only read.
var readOnlyGit = map[string]bool{
	"log": true, "show": true, "diff": true, "status": true, "grep": true, "blame": true, "ls-files": true,
	"rev-parse": true, "describe": true, "shortlog": true, "cat-file": true, "ls-tree": true,
}

var harmlessRedirect = regexp.MustCompile(`[12]?>\s*/dev/null|2>&1|1>&2|>&2`)

// readOnlyCommand tells whether a shell command line only reads: every
// part runs a known reading program, nothing is redirected into a file,
// sed and find do not write, awk runs nothing, git only reads. Unknown
// commands are not read-only.
func readOnlyCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return false
	}
	cmd = harmlessRedirect.ReplaceAllString(cmd, " ")
	if strings.Contains(cmd, ">") {
		return false
	}
	for _, seg := range splitShell.Split(cmd, -1) {
		words := strings.Fields(seg)
		if len(words) == 0 {
			continue
		}
		prog := filepath.Base(words[0])
		if prog == "cd" {
			continue
		}
		if !readOnlyPrograms[prog] {
			return false
		}
		for _, w := range words[1:] {
			switch {
			case prog == "sed" && (strings.HasPrefix(w, "-i") || strings.HasPrefix(w, "--in-place")):
				return false
			case prog == "find" && (w == "-delete" || w == "-exec" || w == "-execdir" || w == "-ok" || w == "-okdir" || strings.HasPrefix(w, "-fprint")):
				return false
			case prog == "awk" && (strings.Contains(seg, "system") || strings.Contains(seg, "print >")):
				return false
			}
		}
		if prog == "git" {
			verbs := gitVerbs(seg)
			if len(verbs) == 0 {
				return false
			}
			for _, v := range verbs {
				if !readOnlyGit[v] {
					return false
				}
			}
		}
	}
	return true
}
