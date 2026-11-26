package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// AI coding agent (D540): "agent" drives the agent module (agent.*):
// status, start, prompts with streamed output, changed files, diffs,
// revert, model choice. Permissions set to "ask" are denied unless --yes;
// blocked categories (e.g. git commits) stay blocked either way.

func init() {
	flagSpec["attach"] = true
	flagSpec["thoughts"] = false
	register(&command{name: "agent",
		usage: "agent [status | start | stop | new | prompt TEXT... [--yes] [--attach FILE] [--thoughts] | transcript | changes | diff [PATH] | " +
			"revert PATH | revert --all [--force] | accept | model [MODEL] | option ID VALUE]",
		summary: "AI coding agent (opencode over ACP): prompt with streamed output, changed files with diff and revert, model; asks are denied unless --yes",
		maxArgs: -1, cwdProject: true, run: runAgent})
}

func runAgent(c *cli) error {
	sub := "status"
	args := c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch sub {
	case "status", "start", "stop", "new":
		method := map[string]string{"status": "agent.status", "start": "agent.start", "stop": "agent.stop", "new": "agent.newSession"}[sub]
		var st api.AgentStatus
		if err := c.call(method, nil, &st); err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) { printAgentStatus(w, st) })
	case "prompt":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless agent prompt TEXT... [--yes]")
		}
		return c.agentPrompt(strings.Join(args, " "))
	case "transcript":
		var es []api.AgentEntry
		if err := c.call("agent.transcript", nil, &es); err != nil {
			return err
		}
		return c.emit(es, func(w io.Writer) {
			for _, e := range es {
				printAgentEntry(w, e, true)
			}
		})
	case "changes":
		var cs []api.AgentChange
		if err := c.call("agent.changes", nil, &cs); err != nil {
			return err
		}
		return c.emit(cs, func(w io.Writer) {
			if len(cs) == 0 {
				fmt.Fprintln(w, "no changes")
			}
			for _, ch := range cs {
				mark := ""
				if ch.Conflict {
					mark = "  (changed since)"
				}
				fmt.Fprintf(w, "%-8s %s  +%d -%d%s\n", ch.Kind, ch.Path, ch.Added, ch.Removed, mark)
			}
		})
	case "diff":
		paths := args
		if len(paths) == 0 {
			var cs []api.AgentChange
			if err := c.call("agent.changes", nil, &cs); err != nil {
				return err
			}
			for _, ch := range cs {
				paths = append(paths, ch.Path)
			}
		}
		var ds []api.AgentDiff
		for _, p := range paths {
			var d api.AgentDiff
			if err := c.call("agent.diff", api.AgentPathParams{Path: p}, &d); err != nil {
				return err
			}
			ds = append(ds, d)
		}
		return c.emit(ds, func(w io.Writer) {
			for _, d := range ds {
				fmt.Fprint(w, d.Unified)
			}
		})
	case "revert":
		if arg == "" && !c.p.has("all") {
			return usagef("usage: tiegel --headless agent revert PATH | --all [--force]")
		}
		var res api.AgentRevertResult
		if err := c.call("agent.revert", api.AgentRevertParams{Path: arg, All: c.p.has("all"), Force: c.p.has("force")}, &res); err != nil {
			return err
		}
		if err := c.emit(res, func(w io.Writer) {
			for _, p := range res.Reverted {
				fmt.Fprintln(w, "reverted "+p)
			}
			for _, cf := range res.Conflicts {
				fmt.Fprintf(w, "conflict %s: %s\n", cf.Path, cf.Reason)
			}
		}); err != nil {
			return err
		}
		if len(res.Conflicts) > 0 {
			return &quietError{code: ExitError}
		}
		return nil
	case "accept":
		if err := c.call("agent.acceptChanges", nil, nil); err != nil {
			return err
		}
		return c.emit(map[string]bool{"accepted": true}, func(w io.Writer) { fmt.Fprintln(w, "changes accepted") })
	case "model", "option":
		var st api.AgentStatus
		var err error
		switch {
		case sub == "option" && len(args) == 2:
			err = c.call("agent.setOption", api.AgentOptionParams{ID: args[0], Value: args[1]}, &st)
		case sub == "option":
			return usagef("usage: tiegel --headless agent option ID VALUE")
		case arg != "":
			err = c.call("agent.setModel", api.AgentModelParams{Model: arg}, &st)
		default:
			err = c.call("agent.start", nil, &st)
		}
		if err != nil {
			return err
		}
		return c.emit(st.Options, func(w io.Writer) {
			for _, o := range st.Options {
				if sub == "model" && o.ID != "model" && o.Category != "model" {
					continue
				}
				fmt.Fprintf(w, "%s (%s): %s\n", o.Name, o.ID, o.Value)
				if arg == "" {
					for _, ch := range o.Choices {
						mark := " "
						if ch.Value == o.Value {
							mark = "*"
						}
						fmt.Fprintf(w, "  %s %s\n", mark, ch.Value)
					}
				}
			}
		})
	}
	return usagef("usage: tiegel --headless %s", commands["agent"].usage)
}

func printAgentStatus(w io.Writer, st api.AgentStatus) {
	state := st.State
	if !st.Available {
		state = "not installed"
	}
	fmt.Fprintf(w, "agent:    %s (%s)\n", st.Command, state)
	for _, kv := range [][2]string{{"path", st.Path}, {"running", strings.TrimSpace(st.Agent + " " + st.Version)}, {"session", st.SessionID},
		{"model", st.Model}, {"policy", st.Policy.Summary}, {"login", st.AuthHint}, {"error", st.Error}, {"hint", st.InstallHint}} {
		if kv[1] != "" {
			fmt.Fprintf(w, "%-9s %s\n", kv[0]+":", kv[1])
		}
	}
	if st.Changes > 0 {
		fmt.Fprintf(w, "changes:  %d file(s) (agent changes)\n", st.Changes)
	}
	if st.Install != "" {
		fmt.Fprintf(w, "install:  tiegel --headless install %s --dry-run\n", st.Install)
	}
}

// printAgentEntry prints a transcript entry (full: with message texts).
func printAgentEntry(w io.Writer, e api.AgentEntry, full bool) {
	switch e.Kind {
	case api.EntryUser:
		fmt.Fprintf(w, "\n> %s\n\n", strings.ReplaceAll(e.Text, "\n", "\n> "))
	case api.EntryAgent:
		if full {
			fmt.Fprintln(w, e.Text)
		}
	case api.EntryThought:
		if full {
			fmt.Fprintf(w, "(thinking) %s\n", e.Text)
		}
	case api.EntryTool:
		fmt.Fprintln(w, toolLine(e.Tool))
	case api.EntryPlan:
		for _, p := range e.Plan {
			fmt.Fprintf(w, "  [%s] %s\n", p.Status, p.Content)
		}
	case api.EntryPermission:
		p := e.Permission
		ans := "waiting"
		switch {
		case p.Answer == "":
		case p.Allowed:
			ans = "allowed"
		default:
			ans = "rejected"
		}
		fmt.Fprintf(w, "? %s (%s)\n", p.Summary, ans)
	case api.EntryNotice:
		fmt.Fprintf(w, "! %s\n", e.Text)
	}
}

func toolLine(t *api.AgentToolCall) string {
	s := "• " + t.Title
	if t.Command != "" && t.Command != t.Title {
		s += ": " + t.Command
	}
	for _, d := range t.Diffs {
		s += fmt.Sprintf("  %s +%d -%d", d.Path, d.Added, d.Removed)
	}
	if len(t.Diffs) == 0 && len(t.Paths) > 0 && t.Command == "" {
		s += "  " + strings.Join(t.Paths, ", ")
	}
	if t.Status != "" {
		s += "  [" + t.Status + "]"
	}
	return s
}

// agentPrompt sends a prompt and streams the answer: message text as it
// arrives, finished tool calls, notices; the exit code is 1 when the turn
// failed, 130 after Ctrl+C.
func (c *cli) agentPrompt(text string) error {
	var mu sync.Mutex
	lastAgent := -1
	printed := map[int]string{}
	ask := api.PolicyDeny
	if c.p.has("yes") {
		ask = api.PolicyAllow
	}
	sess := c.app.NewSession(func(ev api.Event) {
		if c.json || ev.Topic != api.TopicAgentUpdate {
			return
		}
		var u api.AgentUpdateEvent
		if json.Unmarshal(ev.Payload, &u) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w := c.env.Stdout
		e := u.Entry
		switch e.Kind {
		case api.EntryAgent, api.EntryThought:
			if e.Kind == api.EntryThought && !c.p.has("thoughts") {
				return
			}
			if lastAgent != e.Index {
				if lastAgent >= 0 {
					fmt.Fprintln(w)
				}
				lastAgent = e.Index
				if e.Kind == api.EntryThought {
					fmt.Fprint(w, "(thinking) ")
				}
			}
			fmt.Fprint(w, u.Delta)
		case api.EntryUser:
		default:
			// Tool calls are printed once they ended (or were blocked);
			// notices and permissions once.
			key := ""
			if e.Tool != nil {
				if e.Tool.Status != "completed" && e.Tool.Status != "failed" {
					return
				}
				key = e.Tool.Status
			}
			if e.Permission != nil {
				key = e.Permission.Answer
			}
			if v, ok := printed[e.Index]; ok && v == key {
				return
			}
			printed[e.Index] = key
			if lastAgent >= 0 {
				fmt.Fprintln(w)
				lastAgent = -1
			}
			printAgentEntry(w, e, false)
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicAgentUpdate)
	var res api.AgentTurnResult
	err := c.call("agent.prompt", api.AgentPromptParams{Text: text, Wait: true, Ask: ask, Files: c.p.flags["attach"]}, &res)
	if err != nil {
		return err
	}
	// Events are delivered asynchronously: wait for the session queue.
	sess.Flush(c.ctx)
	if c.json {
		var es []api.AgentEntry
		_ = c.call("agent.transcript", nil, &es)
		var cs []api.AgentChange
		_ = c.call("agent.changes", nil, &cs)
		return writeJSON(c.env.Stdout, map[string]any{"result": res, "transcript": es, "changes": cs})
	}
	mu.Lock()
	if lastAgent >= 0 {
		fmt.Fprintln(c.env.Stdout)
	}
	mu.Unlock()
	var cs []api.AgentChange
	if c.call("agent.changes", nil, &cs) == nil && len(cs) > 0 {
		fmt.Fprintf(c.env.Stdout, "\n%d changed file(s) – tiegel --headless agent changes | diff | revert\n", len(cs))
	}
	switch {
	case res.StopReason == "cancelled" && (res.ByUser || c.ctx.Err() != nil):
		return &quietError{code: 130}
	case res.Error != "":
		fmt.Fprintln(c.env.Stderr, "agent: "+res.Error)
		return &quietError{code: ExitError}
	}
	return nil
}
