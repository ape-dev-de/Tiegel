package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/mcp"
	"github.com/ape-dev-de/tiegel/internal/modules/planmode"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// Plan mode (D567–D570): "plan" drives the planmode module (plan.*).
// Everything that runs agents needs --yes (start, changes, execute,
// resume, eval); without it the estimate is printed. "mcp plan" serves
// the plan document tools to a role session.

func init() {
	flagSpec["plan"] = true
	flagSpec["role"] = true
	flagSpec["comment"] = true
	flagSpec["section"] = true
	flagSpec["mediator"] = true
	flagSpec["modes"] = true
	flagSpec["no-subagents"] = false
	flagSpec["allow-shell"] = false
	register(&command{name: "plan",
		usage: "plan [start GOAL... [--no-subagents] [--mediator off|auto|always] [--yes] | status [ID] | show [ID] [--section S] | list | accept [ID] [--comment C] | " +
			"changes [ID] --comment C [--yes] | execute [ID] [--yes] [--allow-shell] | resume [ID] [--yes] | resolve ITEM dev|qa|dismiss|comment [--comment C] [--plan ID] | " +
			"cancel [ID] | transcript ROLE [--plan ID] | diff [PATH] [--plan ID] | revert PATH [--plan ID] | apply [ID] | keep [ID] | discard [ID] | estimate | eval GOAL... [--modes off,auto,always] --yes]",
		summary: "plan mode: role sessions plan in a Markdown document, you accept, DEV and QA execute in a git worktree with critic, debate and gates; agents run only with --yes",
		maxArgs: -1, cwdProject: true, run: runPlan})
}

func runPlan(c *cli) error {
	sub := "status"
	args := c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	id := c.p.value("plan")
	if id == "" && len(args) > 0 && sub != "start" && sub != "eval" && sub != "resolve" && sub != "transcript" && sub != "diff" && sub != "revert" {
		id = args[0]
	}
	switch sub {
	case "start":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless plan start GOAL... [--yes]")
		}
		if !c.p.has("yes") {
			return c.planEstimate()
		}
		p := api.PlanStartParams{Goal: strings.Join(args, " "), Mediator: c.p.value("mediator"), Wait: true}
		if c.p.has("no-subagents") {
			f := false
			p.Subagents = &f
		}
		return c.planRun("plan.start", p)
	case "status":
		var st api.PlanStatus
		if err := c.call("plan.status", api.PlanIDParams{ID: id}, &st); err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) { printPlanStatus(w, st) })
	case "show":
		var d api.PlanDocumentResult
		if err := c.call("plan.document", api.PlanDocumentParams{ID: id, Section: c.p.value("section")}, &d); err != nil {
			return err
		}
		return c.emit(d, func(w io.Writer) { fmt.Fprint(w, d.Markdown) })
	case "list":
		var l []api.PlanStatus
		if err := c.call("plan.list", nil, &l); err != nil {
			return err
		}
		return c.emit(l, func(w io.Writer) {
			if len(l) == 0 {
				fmt.Fprintln(w, "no plans")
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			for _, p := range l {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.State, p.Phase, p.Title)
			}
			tw.Flush()
		})
	case "accept":
		return c.planSimple("plan.accept", api.PlanCommentParams{ID: id, Comment: c.p.value("comment")})
	case "changes":
		if c.p.value("comment") == "" {
			return usagef("usage: tiegel --headless plan changes [ID] --comment \"what to change\" --yes")
		}
		if !c.p.has("yes") {
			return c.planEstimate()
		}
		return c.planRun("plan.requestChanges", api.PlanCommentParams{ID: id, Comment: c.p.value("comment"), Wait: true})
	case "execute", "resume":
		if !c.p.has("yes") {
			fmt.Fprintln(c.env.Stdout, "Execution runs DEV, QA, critic and mediator sessions (tokens billed by your agent's provider) in a git worktree of the project; add --yes.")
			return c.planEstimate()
		}
		return c.planRun("plan."+sub, api.PlanExecuteParams{ID: id, Wait: true, AllowShell: c.p.has("allow-shell")})
	case "resolve":
		if len(args) < 2 {
			return usagef("usage: tiegel --headless plan resolve ITEM dev|qa|dismiss|comment [--comment C] [--plan ID]")
		}
		return c.planSimple("plan.resolve", api.PlanResolveParams{ID: id, Item: args[0], Decision: args[1], Comment: c.p.value("comment")})
	case "cancel":
		return c.planSimple("plan.cancel", api.PlanIDParams{ID: id})
	case "apply":
		return c.planSimple("plan.apply", api.PlanIDParams{ID: id})
	case "keep":
		return c.planSimple("plan.keepBranch", api.PlanIDParams{ID: id})
	case "discard":
		return c.planSimple("plan.discard", api.PlanIDParams{ID: id})
	case "transcript":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless plan transcript ROLE [--plan ID]")
		}
		var es []api.AgentEntry
		if err := c.call("plan.transcript", api.PlanTranscriptParams{ID: id, Role: args[0]}, &es); err != nil {
			return err
		}
		return c.emit(es, func(w io.Writer) {
			if len(es) == 0 {
				fmt.Fprintln(w, "no transcript for "+args[0])
			}
			for _, e := range es {
				printAgentEntry(w, e, true)
			}
		})
	case "diff":
		paths := args
		if len(paths) == 0 {
			var cs []api.PlanChange
			if err := c.call("plan.changes", api.PlanIDParams{ID: id}, &cs); err != nil {
				return err
			}
			for _, ch := range cs {
				paths = append(paths, ch.Path)
			}
		}
		var ds []api.PlanDiff
		for _, p := range paths {
			var d api.PlanDiff
			if err := c.call("plan.diff", api.PlanDiffParams{ID: id, Path: p}, &d); err != nil {
				return err
			}
			ds = append(ds, d)
		}
		return c.emit(ds, func(w io.Writer) {
			if len(ds) == 0 {
				fmt.Fprintln(w, "no changes")
			}
			for _, d := range ds {
				fmt.Fprint(w, d.Unified)
			}
		})
	case "revert":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless plan revert PATH [--plan ID]")
		}
		var cs []api.PlanChange
		if err := c.call("plan.revert", api.PlanDiffParams{ID: id, Path: args[0]}, &cs); err != nil {
			return err
		}
		return c.emit(cs, func(w io.Writer) { fmt.Fprintf(w, "reverted %s; %d file(s) still changed\n", args[0], len(cs)) })
	case "estimate":
		return c.planEstimate()
	case "eval":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless plan eval GOAL... --yes")
		}
		if !c.p.has("yes") {
			fmt.Fprintln(c.env.Stdout, "plan eval runs the whole plan once per mediator mode (auto-accepted) – that costs the tokens of several plans; add --yes.")
			return c.planEstimate()
		}
		var modes []string
		if m := c.p.value("modes"); m != "" {
			modes = strings.Split(m, ",")
		}
		var rows []planmode.PlanEvalRow
		if err := c.call("plan.eval", planmode.PlanEvalParams{Goal: strings.Join(args, " "), Modes: modes, Yes: true}, &rows); err != nil {
			return err
		}
		return c.emit(rows, func(w io.Writer) { printPlanEval(w, rows) })
	}
	return usagef("unknown plan command %q", sub)
}

func (c *cli) planEstimate() error {
	var e planmode.PlanEstimate
	if err := c.call("plan.estimate", nil, &e); err != nil {
		return err
	}
	return c.emit(e, func(w io.Writer) {
		sub := "on"
		if !e.Subagents {
			sub = "off (one session per phase)"
		}
		fmt.Fprintf(w, "agent: %s\nsubagents: %s, mediator: %s, max iterations: %d, budget per call: %d tokens\n", e.Agent, sub, e.Mediator, e.MaxIterations, e.BudgetTokens)
		fmt.Fprintf(w, "agent calls: %d for planning, %d per iteration (+ repairs, mediator)\n", e.PlanningCalls, e.IterationCalls)
		fmt.Fprintf(w, "~%d tokens (%s)\n", e.Tokens, e.Note)
		fmt.Fprintln(w, "nothing was run; add --yes")
	})
}

func (c *cli) planSimple(method string, params any) error {
	var st api.PlanStatus
	if err := c.call(method, params, &st); err != nil {
		return err
	}
	return c.emit(st, func(w io.Writer) { printPlanStatus(w, st) })
}

// planRun runs a blocking plan call and prints progress (phase changes,
// role turns) while it runs.
func (c *cli) planRun(method string, params any) error {
	var mu sync.Mutex
	last := map[string]string{}
	phase := ""
	sess := c.app.NewSession(func(ev api.Event) {
		if c.json || ev.Topic != api.TopicPlanProgress {
			return
		}
		var st api.PlanStatus
		if json.Unmarshal(ev.Payload, &st) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w := c.env.Stdout
		if st.Phase != phase {
			phase = st.Phase
			fmt.Fprintf(w, "== %s (%s)\n", st.Phase, st.State)
		}
		for _, r := range st.Roles {
			key := r.State + "|" + fmt.Sprint(r.Turns)
			if last[r.Role] == key {
				continue
			}
			last[r.Role] = key
			switch r.State {
			case "running":
				fmt.Fprintf(w, "   %s: %s …\n", r.Role, r.Task)
			case "done":
				fmt.Fprintf(w, "   %s: done (%s)\n", r.Role, time.Duration(r.ElapsedMs)*time.Millisecond)
			case "failed":
				fmt.Fprintf(w, "   %s: failed: %s\n", r.Role, r.Error)
			}
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicPlanProgress)
	var st api.PlanStatus
	err := c.call(method, params, &st)
	sess.Flush(c.ctx)
	if err != nil {
		return err
	}
	if err := c.emit(st, func(w io.Writer) { printPlanStatus(w, st) }); err != nil {
		return err
	}
	switch {
	case st.State == api.PlanCancelled:
		return &quietError{code: 130}
	case st.State == api.PlanFailed:
		return &quietError{code: ExitError}
	}
	return nil
}

func printPlanStatus(w io.Writer, st api.PlanStatus) {
	fmt.Fprintf(w, "plan %s: %s\n", st.ID, st.Title)
	run := ""
	if st.Running {
		run = ", running"
	}
	fmt.Fprintf(w, "state: %s, phase: %s%s", st.State, st.Phase, run)
	if st.Iteration > 0 {
		fmt.Fprintf(w, ", iteration %d", st.Iteration)
	}
	fmt.Fprintln(w)
	if st.Workspace != nil {
		fmt.Fprintf(w, "workspace: %s %s", st.Workspace.Mode, st.Workspace.Dir)
		if st.Workspace.Branch != "" {
			fmt.Fprintf(w, " (branch %s)", st.Workspace.Branch)
		}
		fmt.Fprintln(w)
	}
	for _, g := range st.Gates {
		mark := "passed"
		switch {
		case g.Skipped != "":
			mark = "skipped: " + g.Skipped
		case !g.Passed:
			mark = "FAILED"
		}
		fmt.Fprintf(w, "gate %-10s %s  %s\n", g.Name, mark, g.Summary)
	}
	for _, d := range st.Disputes {
		fmt.Fprintf(w, "dispute %s (%s → %s): %s\n", d.ID, d.From, d.To, d.Claim)
		for _, r := range d.Responses {
			fmt.Fprintf(w, "   %s: %s %s\n", r.Role, r.Verdict, r.Text)
		}
		fmt.Fprintf(w, "   decide: tiegel --headless plan resolve %s dev|qa|dismiss --plan %s\n", d.ID, st.ID)
	}
	for _, n := range st.Notes {
		fmt.Fprintln(w, "note: "+n)
	}
	if st.Error != "" {
		fmt.Fprintln(w, "error: "+st.Error)
	}
	m := st.Metrics
	fmt.Fprintf(w, "metrics: %d turns, %d repairs, ~%d+%d tokens, %d findings (%d accepted, %d rejected), %d mediator decisions, %d by you\n",
		m.Turns, m.Repairs, m.PromptTokens, m.ReplyTokens, m.FindingsRaised, m.FindingsAccepted, m.FindingsRejected, m.MediatorDecisions, m.UserOverrides)
	fmt.Fprintln(w, "document: "+st.Document)
	switch st.State {
	case api.PlanReview:
		fmt.Fprintf(w, "next: tiegel --headless plan show | accept | changes --comment \"…\" --yes | discard\n")
	case api.PlanAccepted:
		fmt.Fprintf(w, "next: tiegel --headless plan execute --yes\n")
	case api.PlanDone, api.PlanIncomplete:
		fmt.Fprintf(w, "next: tiegel --headless plan diff | apply | keep | discard\n")
	}
}

func printPlanEval(w io.Writer, rows []planmode.PlanEvalRow) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "mediator\tstate\titerations\tto green\trounds\tfindings\taccepted\trejected\tmediator\tturns\ttokens\ttime")
	for _, r := range rows {
		m := r.Metrics
		state := r.State
		if r.Error != "" {
			state += " (" + r.Error + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", r.Mediator, state, m.Iterations, m.IterationsToGreen, m.Rounds, m.FindingsRaised,
			m.FindingsAccepted, m.FindingsRejected, m.MediatorDecisions, m.Turns, m.PromptTokens+m.ReplyTokens, time.Duration(m.DurationMs)*time.Millisecond)
	}
	tw.Flush()
	fmt.Fprintln(w, "The default mediator mode (auto) is provisional; compare several goals before changing it.")
}

// runPlanMCP serves the plan document tools of one role session over
// stdin/stdout (started by the agent for the session).
func runPlanMCP(c *cli) error {
	id, role := c.p.value("plan"), c.p.value("role")
	if id == "" || role == "" {
		return usagef("usage: tiegel --headless mcp plan --plan ID --role ROLE")
	}
	call := func(ctx context.Context, method string, params, out any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		res, err := c.sess.Call(ctx, method, raw)
		if err != nil {
			return err
		}
		if out == nil {
			return nil
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	propose := func(ctx context.Context, p api.PlanProposeParams) (string, error) {
		p.ID, p.Role = id, role
		if err := call(ctx, "plan.propose", p, nil); err != nil {
			return "", err
		}
		return "Proposed; Tiegel merges it after your turn.", nil
	}
	tools := []mcp.Tool{
		{Name: "plan_read", Description: "Read a section of the plan document: goal, context, findings, plan, tests, criteria, risks, review, decisions, gates, summary (\"\" = all).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"}}}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Section string `json:"section"`
				}
				_ = json.Unmarshal(args, &a)
				var d api.PlanDocumentResult
				if err := call(ctx, "plan.document", api.PlanDocumentParams{ID: id, Section: a.Section}, &d); err != nil {
					return "", err
				}
				return d.Markdown, nil
			}},
		{Name: "plan_update", Description: "Propose new content for a section you own (planners: context; summarizer: summary).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"},"content":{"type":"string"}},"required":["section","content"]}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ Section, Content string }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				return propose(ctx, api.PlanProposeParams{Op: "update", Section: a.Section, Content: a.Content})
			}},
		{Name: "plan_add_finding", Description: "Add a finding: a claim about the code with its evidence path:line (it is validated).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"claim":{"type":"string"},"evidence":{"type":"string"}},"required":["claim","evidence"]}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ Claim, Evidence string }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				return propose(ctx, api.PlanProposeParams{Op: "finding", Claim: a.Claim, Evidence: a.Evidence})
			}},
		{Name: "plan_comment", Description: "Comment on a review item (Q…) of the review log.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"item":{"type":"string"},"text":{"type":"string"}},"required":["item","text"]}`),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ Item, Text string }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
				return propose(ctx, api.PlanProposeParams{Op: "comment", Item: a.Item, Content: a.Text})
			}},
	}
	srv := &mcp.Server{Name: "tiegel-plan", Version: version.Version, Tools: tools,
		Instructions: "The working document of the Tiegel plan you work on (role " + role + "). Read sections instead of guessing; proposals are merged by Tiegel after your turn."}
	return srv.Serve(c.ctx, c.env.Stdin, c.env.Stdout)
}
