package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	runsvc "github.com/ape-dev-de/tiegel/internal/run"
)

// Run infrastructure (AP15.1): "run" starts a configuration, a PHP file
// or an ad-hoc command, streams its output and passes the exit code on;
// "run-configs" manages the project's run configurations.

func init() {
	flagSpec["file"] = true
	flagSpec["cmd"] = false
	flagSpec["interpreter"] = true
	flagSpec["env"] = true
	flagSpec["dir"] = true
	register(
		&command{name: "run", usage: "run CONFIG | --file FILE [ARGS...] | --cmd -- COMMAND [ARGS...] [--interpreter project|host|ID] [--env K=V]... [--dir DIR]",
			summary: "run a configuration, a PHP file or a command in the interpreter; output streamed, exit code passed on", maxArgs: -1, cwdProject: true, run: runRun},
		&command{name: "run-configs", usage: "run-configs [list | types | save JSON|- | delete ID | select ID]",
			summary: "list, add/change, delete or select run configurations; types lists the configuration types", maxArgs: 2, cwdProject: true, run: runRunConfigs},
	)
}

// runOutput is the JSON output of "run".
type runOutput struct {
	Run    runsvc.Info   `json:"run"`
	Output []runsvc.Line `json:"output"`
}

// runParams builds the run.start params from the command line.
func runParams(c *cli) (api.RunStartParams, error) {
	p := api.RunStartParams{Interpreter: c.p.value("interpreter")}
	file, cmd := c.p.value("file"), c.p.has("cmd")
	switch {
	case file != "" && cmd:
		return p, usagef("give either --file or --cmd")
	case file != "":
		p.File, p.Args = file, c.p.args
	case cmd:
		if len(c.p.args) == 0 {
			return p, usagef("usage: tiegel --headless run --cmd -- COMMAND [ARGS...]")
		}
		p.Spec = &runsvc.Spec{Program: c.p.args[0], Args: c.p.args[1:], Interpreter: c.p.value("interpreter")}
	default:
		if len(c.p.args) != 1 {
			return p, usagef("usage: tiegel --headless %s", commands["run"].usage)
		}
		p.Config = c.p.args[0]
		if p.Interpreter != "" {
			return p, usagef("--interpreter applies to --file and --cmd; configurations have their own")
		}
	}
	env, dir := c.p.flags["env"], c.p.value("dir")
	if len(env) > 0 || dir != "" {
		if p.Spec == nil {
			return p, usagef("--env and --dir apply to --cmd")
		}
		p.Spec.Env, p.Spec.Dir = env, dir
	}
	return p, nil
}

func runRun(c *cli) error {
	params, err := runParams(c)
	if err != nil {
		return err
	}
	// Output arrives as run.output events, possibly before run.start
	// returns the ID; lines are printed once, in sequence order.
	var (
		mu      sync.Mutex
		id      string
		last    int
		pending []runsvc.Output
	)
	write := func(lines []runsvc.Line) {
		if c.json {
			return
		}
		for _, l := range lines {
			if l.Seq <= last {
				continue
			}
			last = l.Seq
			w := c.env.Stdout
			if l.Stream != runsvc.StreamStdout {
				w = c.env.Stderr
			}
			fmt.Fprintln(w, l.Text)
		}
	}
	sess := c.app.NewSession(func(ev api.Event) {
		var o runsvc.Output
		if ev.Topic != api.TopicRunOutput || json.Unmarshal(ev.Payload, &o) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case id == "":
			pending = append(pending, o)
		case o.RunID == id:
			write(o.Lines)
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicRunOutput)

	var info runsvc.Info
	if err := c.call("run.start", params, &info); err != nil {
		return err
	}
	mu.Lock()
	id = info.ID
	for _, o := range pending {
		if o.RunID == id {
			write(o.Lines)
		}
	}
	pending = nil
	mu.Unlock()

	// Interrupting (ctx) stops the run; waiting for its end and reading
	// the rest of the output must outlive the interrupt.
	bg := &cli{ctx: context.WithoutCancel(c.ctx), app: c.app, sess: c.sess, env: c.env, p: c.p, json: c.json}
	done := make(chan runsvc.Info, 1)
	go func() {
		var res runsvc.Info
		_ = bg.call("run.wait", api.RunIDParams{RunID: id}, &res)
		done <- res
	}()
	var res runsvc.Info
	select {
	case res = <-done:
	case <-c.ctx.Done():
		_ = bg.call("run.stop", api.RunIDParams{RunID: id}, nil)
		res = <-done
	}
	// Lines whose events were still on their way.
	var pg runsvc.Page
	if err := bg.call("run.output", api.RunOutputParams{RunID: id}, &pg); err != nil {
		return err
	}
	mu.Lock()
	write(pg.Lines)
	mu.Unlock()
	if c.json {
		if res.ID == "" {
			res = info
		}
		if err := writeJSON(c.env.Stdout, runOutput{Run: res, Output: pg.Lines}); err != nil {
			return err
		}
	}
	switch {
	case res.State == runsvc.StateStopped:
		return &quietError{code: 130}
	case res.ExitCode == 0:
		return nil
	case res.ExitCode > 0 && res.ExitCode < 256:
		return &quietError{code: res.ExitCode}
	}
	return &quietError{code: ExitError}
}

func runRunConfigs(c *cli) error {
	sub := "list"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	arg := ""
	if len(c.p.args) > 1 {
		arg = c.p.args[1]
	}
	need := func() error {
		if arg == "" {
			return usagef("usage: tiegel --headless run-configs %s ID", sub)
		}
		return nil
	}
	switch sub {
	case "list":
	case "types":
		var types []api.RunConfigTypeInfo
		if err := c.call("run.configTypes", nil, &types); err != nil {
			return err
		}
		return c.emit(types, func(w io.Writer) {
			for _, t := range types {
				owner := "core"
				if t.Plugin != "" {
					owner = t.Plugin
				}
				fmt.Fprintf(w, "%s\t%s (%s)\n", t.ID, t.Label, owner)
				for _, f := range t.Fields {
					req := ""
					if f.Required {
						req = " (required)"
					}
					fmt.Fprintf(w, "  %s\t%s %s%s", f.Key, f.Kind, f.Label, req)
					if s := t.Suggestions[f.Key]; len(s) > 0 {
						fmt.Fprintf(w, " [%s]", strings.Join(s, ", "))
					}
					fmt.Fprintln(w)
				}
			}
		})
	case "save":
		if err := need(); err != nil {
			return err
		}
		raw := arg
		if arg == "-" {
			b, err := io.ReadAll(c.env.Stdin)
			if err != nil {
				return err
			}
			raw = string(b)
		}
		var cfg runsvc.Config
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return usagef("configuration JSON: %v", err)
		}
		var saved runsvc.Config
		if err := c.call("run.configs.save", cfg, &saved); err != nil {
			return err
		}
		return c.emit(saved, func(w io.Writer) { fmt.Fprintf(w, "saved %s (%s)\n", saved.Name, saved.ID) })
	case "delete", "select":
		if err := need(); err != nil {
			return err
		}
		if err := c.call("run.configs."+sub, api.RunConfigIDParams{ID: arg}, nil); err != nil {
			return err
		}
	default:
		return usagef("usage: tiegel --headless %s", commands["run-configs"].usage)
	}
	var list []api.RunConfigInfo
	if err := c.call("run.configs.list", nil, &list); err != nil {
		return err
	}
	return c.emit(list, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, cfg := range list {
			mark := " "
			if cfg.Selected {
				mark = "*"
			}
			detail := cfg.Display
			if cfg.Error != "" {
				detail = "error: " + cfg.Error
			}
			fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", mark, cfg.Name, cfg.Type, cfg.ID, detail)
		}
		_ = tw.Flush()
	})
}
