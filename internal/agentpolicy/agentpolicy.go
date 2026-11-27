// Package agentpolicy classifies the tool calls of coding agents (ACP)
// into permission categories – file edits, shell commands, git commits
// and other history changes, git push, web access, other tools – and
// decides them by a policy (D549). It is shared by the agent plugin and
// the plan mode orchestrator (D567). Shell commands are parsed, so
// "cd x && git commit -m y" counts as a git commit, not as a plain shell
// command.
package agentpolicy

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// CommitVerbs are git subcommands that create or rewrite history or
// throw away work; they count as "git commit".
var CommitVerbs = map[string]bool{
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

// GitVerbs returns the git subcommands a shell command line runs, also
// inside quoted scripts ("sh -c 'git push'").
func GitVerbs(cmd string) []string { return gitVerbsDepth(cmd, 0) }

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

// ShellCategory classifies a shell command: git push, git commit (and
// other history changes) or plain shell.
func ShellCategory(cmd string) (cat, verb string) {
	cat = api.AgentCatShell
	for _, v := range GitVerbs(cmd) {
		switch {
		case v == "push":
			return api.AgentCatGitPush, "git push"
		case CommitVerbs[v] && cat == api.AgentCatShell:
			cat, verb = api.AgentCatGitCommit, "git "+v
		}
	}
	return cat, verb
}

// InputField returns a string field of a tool's raw input.
func InputField(raw json.RawMessage, names ...string) string {
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

// CommandOf returns the shell command of a tool call.
func CommandOf(tc acp.ToolCall) string {
	return InputField(tc.RawInput, "command", "cmd")
}

// SplitShell splits a shell command line at operators and subshells
// (the segments of "a && b | c").
var SplitShell = splitShell

// IsShell tells whether a tool call runs a command.
func IsShell(tc acp.ToolCall) bool {
	switch strings.ToLower(tc.Name) {
	case "bash", "shell", "execute_command", "run_command", "terminal":
		return true
	}
	return tc.Kind == acp.KindExecute || CommandOf(tc) != "" && tc.Kind != acp.KindEdit
}

// IsEdit tells whether a tool call changes files.
func IsEdit(tc acp.ToolCall) bool {
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

// IsWeb tells whether a tool call accesses the network.
func IsWeb(tc acp.ToolCall) bool {
	switch strings.ToLower(tc.Name) {
	case "webfetch", "websearch", "fetch", "web_search", "codesearch":
		return true
	}
	return tc.Kind == acp.KindFetch
}

// Category returns the permission category of a tool call and, for git
// commands, the blocked verb ("git commit").
func Category(tc acp.ToolCall) (cat, verb string) {
	switch {
	case IsShell(tc):
		return ShellCategory(CommandOf(tc))
	case IsEdit(tc):
		return api.AgentCatEdit, ""
	case IsWeb(tc):
		return api.AgentCatWeb, ""
	}
	return api.AgentCatOther, ""
}

// Decide returns the policy of category cat.
func Decide(p api.AgentPolicy, cat string) string {
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

// PickOption returns the first option of one of kinds (in order of
// preference), or "".
func PickOption(opts []acp.PermissionOption, kinds ...string) string {
	for _, k := range kinds {
		for _, o := range opts {
			if o.Kind == k {
				return o.OptionID
			}
		}
	}
	return ""
}

// AllowOption picks the one-time allow answer (Tiegel keeps "always"
// decisions itself, so every call keeps coming through it).
func AllowOption(opts []acp.PermissionOption) string {
	return PickOption(opts, acp.AllowOnce, acp.AllowAlways)
}

// RejectOption picks the one-time reject answer.
func RejectOption(opts []acp.PermissionOption) string {
	return PickOption(opts, acp.RejectOnce, acp.RejectAlways)
}

// OptionKind returns the kind of option id.
func OptionKind(opts []acp.PermissionOption, id string) string {
	for _, o := range opts {
		if o.OptionID == id {
			return o.Kind
		}
	}
	return ""
}

// IsReadOnly tells whether a tool call only reads or searches (read,
// grep, glob, list, or the ACP kinds read, search and think without a
// diff).
func IsReadOnly(tc acp.ToolCall) bool {
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

// EditPaths returns the files an edit tool call changes (diff contents,
// locations and the usual path fields of the raw input).
func EditPaths(tc acp.ToolCall) []string {
	var out []string
	add := func(p string) {
		for _, x := range out {
			if x == p {
				return
			}
		}
		out = append(out, p)
	}
	for _, c := range tc.Content {
		if c.Type == "diff" && c.Path != "" {
			add(c.Path)
		}
	}
	for _, l := range tc.Locations {
		if l.Path != "" {
			add(l.Path)
		}
	}
	for _, f := range []string{"path", "filePath", "file_path", "file"} {
		if p := InputField(tc.RawInput, f); p != "" {
			add(p)
		}
	}
	return out
}
