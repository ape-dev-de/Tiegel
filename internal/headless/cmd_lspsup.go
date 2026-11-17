package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

// Language server supervisor (M11) and tool installation (D194).

func init() {
	flagSpec["global"] = false
	flagSpec["scope"] = true
	flagSpec["yes"] = false
	flagSpec["dry-run"] = false
	flagSpec["trace"] = false
	register(
		&command{name: "lsp-status", usage: "lsp-status [--start]", summary: "language server state (--start starts the selected server first; exit 1 unless running)", maxArgs: 0, cwdProject: true, run: runLSPStatus},
		&command{name: "lsp-restart", usage: "lsp-restart [--server ID] [--command PATH] [--trace]", summary: "(re)start the language server natively and wait for initialize (exit 1 unless running)", maxArgs: 0, cwdProject: true, run: runLSPRestart},
		&command{name: "lsp-doctor", usage: "lsp-doctor", summary: "why the language server runs or not: selection, searched locations, per-server findings", maxArgs: 0, cwdProject: true, run: runLSPDoctor},
		&command{name: "lsp-select", usage: "lsp-select auto|none|ID [--global]", summary: "choose the language server for the project (or globally); none switches it off", minArgs: 1, maxArgs: 1, cwdProject: true, run: runLSPSelect},
		&command{name: "install", usage: "install PLUGIN [--scope global|project] [--dry-run] [--yes]", summary: "install a plugin's tool via its package manager (shows the plan; runs only with --yes)", minArgs: 1, maxArgs: 1, cwdProject: true, run: runInstall},
	)
}

func printLSPStatus(w io.Writer, st langphp.Status) {
	name := st.Name
	if name == "" {
		name = st.Server
	}
	switch st.State {
	case supervisor.StateRunning:
		fmt.Fprintf(w, "running: %s (%s, pid %d)", name, st.Command, st.PID)
		if st.PHPVersion != "" {
			fmt.Fprintf(w, " PHP %s", st.PHPVersion)
		}
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "%s", st.State)
		if name != "" {
			fmt.Fprintf(w, ": %s", name)
		}
		fmt.Fprintln(w)
		if st.Reason != "" {
			fmt.Fprintf(w, "reason: %s\n", st.Reason)
		}
	}
	fmt.Fprintf(w, "selected: %s\n", st.Selected)
	if st.State == supervisor.StateNotFound && len(st.Searched) > 0 {
		fmt.Fprintln(w, "searched:")
		for _, d := range st.Searched {
			fmt.Fprintf(w, "  %s\n", d)
		}
		if st.Plugin != "" {
			fmt.Fprintf(w, "install: tiegel --headless install %s\n", st.Plugin)
		}
	}
}

// statusExit fails quietly unless the server runs.
func statusExit(st langphp.Status) error {
	if st.State != supervisor.StateRunning {
		return &quietError{code: ExitError}
	}
	return nil
}

func runLSPStatus(c *cli) error {
	var st langphp.Status
	if c.p.has("start") {
		_ = c.call("lsp.start", nil, nil) // the status explains failures
	}
	if err := c.call("lsp.status", nil, &st); err != nil {
		return err
	}
	if err := c.emit(st, func(w io.Writer) { printLSPStatus(w, st) }); err != nil {
		return err
	}
	return statusExit(st)
}

func runLSPRestart(c *cli) error {
	if c.p.has("trace") {
		on := true
		if err := c.call("lsp.trace", langphp.TraceParams{On: &on}, nil); err != nil {
			return err
		}
	}
	var st langphp.Status
	var err error
	if c.p.value("server") != "" || c.p.value("command") != "" {
		err = c.call("lsp.start", langphp.StartParams{Server: c.p.value("server"), Command: c.p.value("command")}, &st)
	} else {
		err = c.call("lsp.restart", nil, &st)
	}
	if err != nil && api.ErrorCode(err) != api.CodeUnavailable {
		return err
	}
	if err := c.call("lsp.status", nil, &st); err != nil {
		return err
	}
	if c.p.has("trace") && !c.json {
		var log []supervisor.Entry
		if c.call("lsp.log", langphp.LogParams{}, &log) == nil {
			for _, e := range log {
				fmt.Fprintf(c.env.Stdout, "%s %s\n", e.Kind, e.Text)
			}
		}
	}
	defer func() { _ = c.call("lsp.stop", nil, nil) }()
	if err := c.emit(st, func(w io.Writer) { printLSPStatus(w, st) }); err != nil {
		return err
	}
	return statusExit(st)
}

func runLSPDoctor(c *cli) error {
	var d langphp.Doctor
	if err := c.call("lsp.doctor", nil, &d); err != nil {
		return err
	}
	return c.emit(d, func(w io.Writer) {
		fmt.Fprintf(w, "selected: %s (%s)\n", d.Selected, d.Source)
		if d.Command != "" {
			fmt.Fprintf(w, "custom command: %s\n", d.Command)
		}
		if d.Resolved != "" {
			fmt.Fprintf(w, "would start: %s (%s)\n", d.Resolved, d.Path)
		} else {
			fmt.Fprintf(w, "cannot start: %s\n", d.Reason)
		}
		fmt.Fprintf(w, "state: %s\n", d.Status.State)
		fmt.Fprintln(w, "servers (enabled plugins):")
		if len(d.Servers) == 0 {
			fmt.Fprintln(w, "  none – enable a plugin: tiegel --headless modules enable lsp-phpactor")
		}
		for _, s := range d.Servers {
			mark := " "
			if s.Selected {
				mark = "*"
			}
			found := s.Path
			if !s.Found {
				found = "missing: " + s.Error
			}
			fmt.Fprintf(w, " %s %-13s %-12s %s\n", mark, s.ID, s.License, found)
		}
		fmt.Fprintln(w, "searched:")
		for _, dir := range d.Searched {
			fmt.Fprintf(w, "  %s\n", dir)
		}
		if d.InstallPlugin != "" {
			fmt.Fprintf(w, "install: tiegel --headless install %s\n", d.InstallPlugin)
		}
	})
}

func runLSPSelect(c *cli) error {
	scope := "project"
	if c.p.has("global") {
		scope = "global"
	}
	var st langphp.Status
	if err := c.call("lsp.select", langphp.SelectParams{Server: c.p.args[0], Scope: scope}, &st); err != nil {
		return err
	}
	defer func() { _ = c.call("lsp.stop", nil, nil) }()
	return c.emit(st, func(w io.Writer) { printLSPStatus(w, st) })
}

// installOutput is the JSON output of "install".
type installOutput struct {
	Plan   install.Plan    `json:"plan"`
	Result *install.Result `json:"result,omitempty"`
}

func runInstall(c *cli) error {
	plugin, scope := c.p.args[0], c.p.value("scope")
	var plan install.Plan
	if err := c.call("install.plan", api.InstallPlanParams{Plugin: plugin, Scope: scope}, &plan); err != nil {
		return err
	}
	printPlan := func(w io.Writer) {
		fmt.Fprintf(w, "%s (%s)\n", plan.Tool, plan.Plugin)
		if plan.Note != "" {
			fmt.Fprintf(w, "note: %s\n", plan.Note)
		}
		if plan.Display != "" {
			fmt.Fprintf(w, "command: %s\n", plan.Display)
			fmt.Fprintf(w, "target: %s (in %s)\n", plan.Scope, plan.Dir)
			fmt.Fprintf(w, "changes composer.json: %v\n", plan.ChangesComposerJSON)
		}
		for _, e := range plan.Effects {
			fmt.Fprintf(w, "effect: %s\n", e)
		}
		if plan.Hint != "" {
			fmt.Fprintf(w, "hint: %s\n", plan.Hint)
		}
	}
	if c.p.has("dry-run") || !plan.Available {
		if err := c.emit(installOutput{Plan: plan}, printPlan); err != nil {
			return err
		}
		if !plan.Available {
			return &quietError{code: ExitError}
		}
		return nil
	}
	if !c.p.has("yes") {
		if !c.json {
			printPlan(c.env.Stdout)
		}
		return fmt.Errorf("not installed: confirm with --yes (or preview with --dry-run)")
	}
	if !c.json {
		printPlan(c.env.Stdout)
	}
	lines := make(chan install.Line, 256)
	sess := c.app.NewSession(func(ev api.Event) {
		var l install.Line
		if ev.Topic == api.TopicInstallOutput && json.Unmarshal(ev.Payload, &l) == nil {
			select {
			case lines <- l:
			default:
			}
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicInstallOutput)
	var run api.InstallRunResult
	if err := c.call("install.run", api.InstallRunParams{Plugin: plugin, Scope: scope, Confirm: true}, &run); err != nil {
		return err
	}
	done := make(chan install.Result, 1)
	go func() {
		var res install.Result
		_ = c.call("install.wait", api.InstallWaitParams{RunID: run.RunID}, &res)
		done <- res
	}()
	var res install.Result
	for wait := true; wait; {
		select {
		case l := <-lines:
			if l.RunID == run.RunID && !c.json {
				fmt.Fprintln(c.env.Stdout, l.Text)
			}
		case res = <-done:
			wait = false
		case <-c.ctx.Done():
			_ = c.call("install.cancel", api.InstallRunIDParams{RunID: run.RunID}, nil)
			res = <-done
			wait = false
		}
	}
	// Output that arrived after the result.
	for drained := false; !drained; {
		select {
		case l := <-lines:
			if l.RunID == run.RunID && !c.json {
				fmt.Fprintln(c.env.Stdout, l.Text)
			}
		case <-time.After(20 * time.Millisecond):
			drained = true
		}
	}
	if err := c.emit(installOutput{Plan: run.Plan, Result: &res}, func(w io.Writer) {
		fmt.Fprintf(w, "%s: %s", strings.ToLower(res.Tool), res.State)
		if res.Error != "" {
			fmt.Fprintf(w, " (%s)", res.Error)
		}
		fmt.Fprintln(w)
	}); err != nil {
		return err
	}
	if res.State != install.StateSucceeded {
		return &quietError{code: ExitError}
	}
	return nil
}
