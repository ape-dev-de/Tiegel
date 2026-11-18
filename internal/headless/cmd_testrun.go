package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	runsvc "github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// Tests (AP15.2–15.4): "test" runs the test plugin's methods (test.*):
// status, discovery, runs with streamed results, rerun of the failed
// tests, Go to Test and Create Test.

func init() {
	flagSpec["suite"] = true
	flagSpec["class"] = true
	flagSpec["method"] = true
	flagSpec["data-set"] = true
	flagSpec["config"] = true
	flagSpec["debug"] = false
	register(&command{name: "test",
		usage: "test [status | discover [PATH] | run [PATH] [--suite S] [--class C] [--method M] [--data-set D] [--filter F] [--config NAME] [--debug] [--interpreter I] | " +
			"rerun-failed [RUN] [--debug] | results [RUN] | node ID [RUN] | config [PATH] [--suite S] [--class C] [--method M] [--data-set D] | goto FILE | create FILE [--file TARGET] [--dry-run]]",
		summary: "test framework status, discovery, runs with results (exit 1 on failures), rerun failed, go to/create test", maxArgs: 3, cwdProject: true, run: runTest})
}

func runTest(c *cli) error {
	sub := "status"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	arg := ""
	if len(c.p.args) > 1 {
		arg = c.p.args[1]
	}
	switch sub {
	case "status":
		var st api.TestStatus
		if err := c.call("test.status", nil, &st); err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) {
			state := "available"
			if !st.Available {
				state = "missing"
			}
			fmt.Fprintf(w, "%s: %s", st.Framework, state)
			if st.Version != "" {
				fmt.Fprintf(w, " (%s)", st.Version)
			}
			fmt.Fprintln(w)
			for _, kv := range [][2]string{{"script", st.Executable}, {"parallel", st.Parallel}, {"config", st.Config},
				{"suites", strings.Join(st.Suites, ", ")}, {"test dirs", strings.Join(st.TestDirs, ", ")}, {"hint", st.Hint}} {
				if kv[1] != "" {
					fmt.Fprintf(w, "  %-9s %s\n", kv[0]+":", kv[1])
				}
			}
			if st.Install != "" {
				fmt.Fprintf(w, "  install:  tiegel --headless install %s --dry-run\n", st.Install)
			}
		})
	case "discover":
		var d api.TestDiscovery
		if err := c.call("test.discover", api.TestDiscoverParams{Path: arg}, &d); err != nil {
			return err
		}
		return c.emit(d, func(w io.Writer) {
			n := 0
			for _, cl := range d.Classes {
				fmt.Fprintf(w, "%s  %s:%d%s\n", cl.ID, c.app.DisplayPath(cl.File), cl.Line, statusMark(cl.Status))
				for _, m := range cl.Children {
					n++
					extra := ""
					if len(m.DataProviders) > 0 {
						extra = "  [data: " + strings.Join(m.DataProviders, ", ") + "]"
					}
					fmt.Fprintf(w, "  %s  :%d%s%s\n", m.Method, m.Line, extra, statusMark(m.Status))
				}
			}
			fmt.Fprintf(w, "%d classes, %d tests in %d files\n", len(d.Classes), n, d.Files)
			for _, e := range d.Errors {
				fmt.Fprintln(w, "error: "+e)
			}
		})
	case "run":
		p := api.TestRunParams{
			TestScope: api.TestScope{Path: arg, Suite: c.p.value("suite"), Class: c.p.value("class"), Method: c.p.value("method"),
				DataSet: c.p.value("data-set"), Filter: c.p.value("filter")},
			Debug: c.p.has("debug"), Interpreter: c.p.value("interpreter"), Config: c.p.value("config"),
		}
		return c.testRun("test.run", p)
	case "config":
		sc := api.TestScope{Path: arg, Suite: c.p.value("suite"), Class: c.p.value("class"), Method: c.p.value("method"),
			DataSet: c.p.value("data-set"), Filter: c.p.value("filter")}
		var cfg runsvc.Config
		if err := c.call("test.configFor", sc, &cfg); err != nil {
			return err
		}
		// Always JSON: ready for "run-configs save -".
		return writeJSON(c.env.Stdout, cfg)
	case "rerun-failed":
		return c.testRun("test.rerunFailed", api.TestRunIDParams{RunID: arg, Debug: c.p.has("debug")})
	case "results":
		var r api.TestRun
		if err := c.call("test.results", api.TestRunIDParams{RunID: arg}, &r); err != nil {
			return err
		}
		return c.emit(r, func(w io.Writer) { printTestRun(w, c, r) })
	case "node":
		if arg == "" {
			return usagef("usage: tiegel --headless test node ID [RUN]")
		}
		runID := ""
		if len(c.p.args) > 2 {
			runID = c.p.args[2]
		}
		var n teamcity.Node
		if err := c.call("test.node", api.TestNodeParams{RunID: runID, ID: arg}, &n); err != nil {
			return err
		}
		return c.emit(n, func(w io.Writer) { printTestNode(w, c, n) })
	case "goto":
		if arg == "" {
			return usagef("usage: tiegel --headless test goto FILE")
		}
		var g api.TestGotoResult
		if err := c.call("test.goto", api.TestGotoParams{Path: arg}, &g); err != nil {
			return err
		}
		return c.emit(g, func(w io.Writer) {
			fmt.Fprintf(w, "%s (%s)\n", g.Class, map[string]string{"test": "tests", "subject": "class under test"}[g.Direction])
			for _, t := range g.Targets {
				mark := "missing"
				if t.Exists {
					mark = "exists"
				}
				fmt.Fprintf(w, "  %-7s %s  %s\n", mark, t.Class, c.app.DisplayPath(t.File))
			}
			if g.Create != nil && !g.Create.Exists {
				fmt.Fprintf(w, "create: tiegel --headless test create %s   (→ %s)\n", arg, c.app.DisplayPath(g.Create.File))
			}
		})
	case "create":
		if arg == "" {
			return usagef("usage: tiegel --headless test create FILE [--file TARGET] [--dry-run]")
		}
		var plan api.TestCreatePlan
		if err := c.call("test.create", api.TestCreateParams{Path: arg, File: c.p.value("file"), DryRun: c.p.has("dry-run")}, &plan); err != nil {
			return err
		}
		return c.emit(plan, func(w io.Writer) {
			switch {
			case plan.Exists:
				fmt.Fprintf(w, "exists: %s (%s)\n", c.app.DisplayPath(plan.File), plan.Class)
			case c.p.has("dry-run"):
				fmt.Fprintf(w, "would create %s (%s):\n\n%s", c.app.DisplayPath(plan.File), plan.Class, plan.Content)
			default:
				fmt.Fprintf(w, "created %s (%s)\n", c.app.DisplayPath(plan.File), plan.Class)
			}
		})
	}
	return usagef("usage: tiegel --headless %s", commands["test"].usage)
}

func statusMark(s teamcity.Status) string {
	if s == "" {
		return ""
	}
	return "  (" + string(s) + ")"
}

// testRun starts a test run, prints each finished test as it arrives and
// the summary; the exit code is 1 when tests failed (130 after Ctrl+C).
func (c *cli) testRun(method string, params any) error {
	var (
		mu      sync.Mutex
		id      string
		printed = map[string]bool{}
		pending []api.TestRun
	)
	show := func(r api.TestRun) {
		if c.json {
			return
		}
		teamcity.Walk(r.Tree, func(n *teamcity.Node) {
			if !n.Kind.IsTest() || n.Status == teamcity.StatusRunning || printed[n.ID] {
				return
			}
			printed[n.ID] = true
			fmt.Fprintf(c.env.Stdout, "%-10s %s (%d ms)\n", strings.ToUpper(string(n.Status)), n.ID, n.DurationMs)
		})
	}
	sess := c.app.NewSession(func(ev api.Event) {
		var r api.TestRun
		if (ev.Topic != api.TopicTestUpdated && ev.Topic != api.TopicTestFinished) || json.Unmarshal(ev.Payload, &r) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case id == "":
			pending = append(pending, r)
		case r.RunID == id:
			show(r)
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicTestUpdated, api.TopicTestFinished)

	var started api.TestRun
	if err := c.call(method, params, &started); err != nil {
		return err
	}
	mu.Lock()
	id = started.RunID
	for _, r := range pending {
		if r.RunID == id {
			show(r)
		}
	}
	pending = nil
	mu.Unlock()

	bg := &cli{ctx: context.WithoutCancel(c.ctx), app: c.app, sess: c.sess, env: c.env, p: c.p, json: c.json}
	done := make(chan runsvc.Info, 1)
	go func() {
		var res runsvc.Info
		_ = bg.call("run.wait", api.RunIDParams{RunID: id}, &res)
		done <- res
	}()
	var info runsvc.Info
	select {
	case info = <-done:
	case <-c.ctx.Done():
		_ = bg.call("run.stop", api.RunIDParams{RunID: id}, nil)
		info = <-done
	}
	// The plugin finishes the results after the run ended.
	var r api.TestRun
	for i := 0; i < 200; i++ {
		if err := bg.call("test.results", api.TestRunIDParams{RunID: id}, &r); err != nil {
			return err
		}
		if r.State != runsvc.StateRunning {
			break
		}
		waitShort()
	}
	if c.json {
		if err := writeJSON(c.env.Stdout, r); err != nil {
			return err
		}
	} else {
		mu.Lock()
		show(r)
		mu.Unlock()
		printSummary(c.env.Stdout, r)
	}
	switch {
	case info.State == runsvc.StateStopped:
		return &quietError{code: 130}
	case r.Counts.Failed > 0 || r.Counts.Terminated > 0 || r.Error != "" || (r.Counts.Total == 0 && info.ExitCode != 0):
		return &quietError{code: ExitError}
	}
	return nil
}

func printSummary(w io.Writer, r api.TestRun) {
	c := r.Counts
	fmt.Fprintf(w, "%s: %d tests, %d passed, %d failed, %d ignored", r.Name, c.Total, c.Passed, c.Failed, c.Ignored)
	if c.Terminated > 0 {
		fmt.Fprintf(w, ", %d terminated", c.Terminated)
	}
	fmt.Fprintf(w, " (%d ms, exit %d)\n", r.DurationMs, r.ExitCode)
	if r.Error != "" {
		fmt.Fprintln(w, r.Error)
	}
}

// printTestRun prints the result tree with the failures.
func printTestRun(w io.Writer, c *cli, r api.TestRun) {
	var walk func(ns []*teamcity.Node, depth int)
	walk = func(ns []*teamcity.Node, depth int) {
		for _, n := range ns {
			fmt.Fprintf(w, "%s%-10s %s (%d ms)\n", strings.Repeat("  ", depth), strings.ToUpper(string(n.Status)), n.Name, n.DurationMs)
			if n.Status == teamcity.StatusFailed || n.Status == teamcity.StatusIgnored && n.Message != "" {
				ind := strings.Repeat("  ", depth+1)
				for _, l := range strings.Split(n.Message, "\n") {
					fmt.Fprintln(w, ind+l)
				}
				if n.Comparison {
					fmt.Fprintf(w, "%sexpected: %s\n%sactual:   %s\n", ind, n.Expected, ind, n.Actual)
				}
				if n.FailFile != "" {
					fmt.Fprintf(w, "%sat %s:%d\n", ind, c.app.DisplayPath(n.FailFile), n.FailLine)
				}
			}
			walk(n.Children, depth+1)
		}
	}
	roots := make([]*teamcity.Node, len(r.Tree))
	for i := range r.Tree {
		roots[i] = &r.Tree[i]
	}
	walk(roots, 0)
	printSummary(w, r)
}

// printTestNode prints one result in full: status, message,
// expected/actual, location, details and output.
func printTestNode(w io.Writer, c *cli, n teamcity.Node) {
	fmt.Fprintf(w, "%s  %s (%d ms)\n", strings.ToUpper(string(n.Status)), n.ID, n.DurationMs)
	if n.Message != "" {
		fmt.Fprintln(w, n.Message)
	}
	if n.Comparison {
		fmt.Fprintf(w, "expected: %s\nactual:   %s\n", n.Expected, n.Actual)
	}
	if n.FailFile != "" {
		fmt.Fprintf(w, "at %s:%d\n", c.app.DisplayPath(n.FailFile), n.FailLine)
	}
	if n.Details != "" {
		fmt.Fprintln(w, n.Details)
	}
	if n.Output != "" {
		fmt.Fprintln(w, "output:")
		fmt.Fprintln(w, n.Output)
	}
}

// waitShort pauses between polls of a finishing test run.
func waitShort() { time.Sleep(25 * time.Millisecond) }
