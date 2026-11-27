package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// "agent eval" (D571) compares the context modes of the project
// knowledge (off, mcp, rag, both) on questions: agent.eval runs every
// question in every mode in a fresh read-only session and measures it.
// It costs tokens: without --yes only the plan is printed.

func init() {
	flagSpec["modes"] = true
	flagSpec["runs"] = true
}

// agentEval is "agent eval [--modes off,mcp,rag,both] [--runs N]
// [--file questions.txt] [--timeout SEC] [--yes] [QUESTION...]".
func (c *cli) agentEval(args []string) error {
	var qs []string
	if q := strings.TrimSpace(strings.Join(args, " ")); q != "" {
		qs = append(qs, q)
	}
	if f := c.p.value("file"); f != "" {
		var b []byte
		var err error
		if f == "-" {
			b, err = io.ReadAll(c.env.Stdin)
		} else {
			b, err = os.ReadFile(f)
		}
		if err != nil {
			return usagef("questions file: %v", err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				qs = append(qs, l)
			}
		}
	}
	if len(qs) == 0 {
		return usagef("usage: tiegel --headless agent eval [--modes off,mcp,rag,both] [--runs N] [--yes] QUESTION | --file questions.txt")
	}
	var modes []string
	for _, m := range strings.Split(c.p.value("modes"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			modes = append(modes, m)
		}
	}
	runs, err := c.p.intValue("runs")
	if err != nil {
		return err
	}
	timeout, err := c.p.intValue("timeout")
	if err != nil {
		return err
	}
	params := api.AgentEvalParams{Questions: qs, Modes: modes, Runs: runs, TimeoutSec: timeout, Confirm: c.p.has("yes")}
	if params.Confirm && !c.json {
		sess := c.app.NewSession(func(ev api.Event) {
			if ev.Topic != api.TopicAgentEval {
				return
			}
			var p api.AgentEvalProgress
			if json.Unmarshal(ev.Payload, &p) != nil {
				return
			}
			if !p.Done {
				fmt.Fprintf(c.env.Stderr, "[%d/%d] %s …\n", p.Index, p.Total, p.Mode)
				return
			}
			if r := p.Run; r != nil {
				line := fmt.Sprintf("[%d/%d] %s: %.1f s, %d tool calls, ~%d tokens", p.Index, p.Total, p.Mode, float64(r.DurationMS)/1000, r.ToolCalls, r.Tokens)
				if r.Error != "" {
					line += " – " + r.Error
				}
				fmt.Fprintln(c.env.Stderr, line)
			}
		})
		defer sess.Close()
		sess.Subscribe(api.TopicAgentEval)
	}
	var res api.AgentEvalResult
	if err := c.call("agent.eval", params, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if !res.Ran {
			printEvalPlan(w, res.Plan)
			return
		}
		printEvalTable(w, res)
	})
}

// printEvalPlan prints what --yes would run.
func printEvalPlan(w io.Writer, p api.AgentEvalPlan) {
	fmt.Fprintln(w, "Evaluation plan (nothing ran – the runs cost tokens):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	model := p.Model
	if model == "" {
		model = "the agent's default"
	}
	fmt.Fprintf(tw, "  modes:\t%s\n", strings.Join(p.Modes, ", "))
	fmt.Fprintf(tw, "  questions:\t%d\n", len(p.Questions))
	fmt.Fprintf(tw, "  runs:\t%d per question and mode\n", p.Runs)
	fmt.Fprintf(tw, "  total:\t%d agent turns\n", p.Total)
	fmt.Fprintf(tw, "  agent:\t%s (model: %s)\n", p.Agent, model)
	_ = tw.Flush()
	for i, q := range p.Questions {
		fmt.Fprintf(w, "  %d. %s\n", i+1, q)
	}
	fmt.Fprintln(w, p.Note)
	fmt.Fprintln(w, "Run it with --yes.")
}

// printEvalTable prints the comparison per mode and the runs.
func printEvalTable(w io.Writer, res api.AgentEvalResult) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODE\tRUNS\tAVG TIME\tTOOL CALLS\tSHELL\tKNOWLEDGE\t~TOKENS OUT\t~CONTEXT IN\tCITES\tERRORS")
	for _, s := range res.Summary {
		fmt.Fprintf(tw, "%s\t%d\t%.1f s\t%.1f\t%.1f\t%.1f\t%.0f\t%.0f\t%.0f%%\t%d\n", s.Mode, s.Runs, float64(s.AvgDurationMS)/1000,
			s.AvgToolCalls, s.AvgShell, s.AvgKnowledge, s.AvgTokens, s.AvgContext, s.CiteRate*100, s.Errors)
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tQ\tTIME\tTOOLS (by kind)\tCITATIONS\tFILE")
	for _, r := range res.Runs {
		var kinds []string
		for k, n := range r.Tools {
			kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
		}
		slices.Sort(kinds)
		tools := fmt.Sprintf("%d", r.ToolCalls)
		if len(kinds) > 0 {
			tools += " (" + strings.Join(kinds, ", ") + ")"
		}
		file := filepath.Base(r.File)
		if r.File == "" {
			file = "–"
		}
		if r.Error != "" {
			file = "error: " + r.Error
		}
		fmt.Fprintf(tw, "%s-%d\t%d\t%.1f s\t%s\t%d\t%s\n", r.Mode, r.N, r.Question, float64(r.DurationMS)/1000, tools, r.Citations, file)
	}
	_ = tw.Flush()
	if res.Dir != "" {
		fmt.Fprintf(w, "\nAnswers: %s (<mode>-<n>.md)\n", res.Dir)
	}
}
