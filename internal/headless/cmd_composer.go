package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	runsvc "github.com/ape-dev-de/tiegel/internal/run"
)

// Composer scripts and external tools (AP17.2): "composer" lists and runs
// the scripts of composer.json (plugin composer), "external-tools" lists
// the external tools, their macros and runs one with an editor context
// given by flags.

func init() {
	flagSpec["line"] = true
	flagSpec["column"] = true
	flagSpec["selection"] = true
	register(
		&command{name: "composer", usage: "composer [scripts | run NAME [-- ARGS...]] [--interpreter project|host|ID]",
			summary: "list the scripts of composer.json or run one with Composer in the project interpreter; exit code passed on", maxArgs: -1, cwdProject: true, run: runComposer},
		&command{name: "external-tools", usage: "external-tools [list | macros | run NAME] [--file FILE] [--line N] [--column N] [--selection TEXT]",
			summary: "list external tools and macros, or run a tool with the macros filled from the given editor context", maxArgs: 2, cwdProject: true, run: runExternalTools},
	)
}

func runComposer(c *cli) error {
	sub := "scripts"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	switch sub {
	case "scripts":
		if len(c.p.args) > 1 {
			return usagef("usage: tiegel --headless %s", commands["composer"].usage)
		}
		var res api.ComposerScriptsResult
		if err := c.call("composer.scripts", nil, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if !res.Exists {
				fmt.Fprintf(w, "no composer.json in the project (%s)\n", res.File)
				return
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, s := range res.Scripts {
				detail := s.Description
				if detail == "" {
					detail = strings.Join(s.Commands, " && ")
				}
				fmt.Fprintf(tw, "%s\t%s\n", s.Name, detail)
			}
			_ = tw.Flush()
			if len(res.Scripts) == 0 {
				fmt.Fprintln(w, "composer.json has no scripts")
			}
		})
	case "run":
		if len(c.p.args) < 2 {
			return usagef("usage: tiegel --headless composer run NAME [-- ARGS...]")
		}
		return streamRun(c, "composer.run", api.ComposerRunParams{Script: c.p.args[1], Args: c.p.args[2:], Interpreter: c.p.value("interpreter")})
	}
	return usagef("usage: tiegel --headless %s", commands["composer"].usage)
}

// macroContext reads --file, --line, --column and --selection (nil when
// none is given).
func macroContext(c *cli) (*runsvc.MacroContext, error) {
	mc := &runsvc.MacroContext{File: c.p.value("file"), SelectedText: c.p.value("selection")}
	for _, f := range []struct {
		name string
		dst  *int
	}{{"line", &mc.Line}, {"column", &mc.Column}} {
		if v := c.p.value(f.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return nil, usagef("--%s must be a positive number", f.name)
			}
			*f.dst = n
		}
	}
	if *mc == (runsvc.MacroContext{}) {
		return nil, nil
	}
	return mc, nil
}

func runExternalTools(c *cli) error {
	sub := "list"
	if len(c.p.args) > 0 {
		sub = c.p.args[0]
	}
	mc, err := macroContext(c)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		var list []api.RunConfigInfo
		if err := c.call("run.configs.list", nil, &list); err != nil {
			return err
		}
		tools := []api.RunConfigInfo{}
		for _, cfg := range list {
			if cfg.Type == runsvc.TypeExternalTool {
				tools = append(tools, cfg)
			}
		}
		return c.emit(tools, func(w io.Writer) {
			if len(tools) == 0 {
				fmt.Fprintln(w, "no external tools (add one: run-configs save '{\"name\":…,\"type\":\"external-tool\",\"values\":{\"program\":…}}')")
				return
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, t := range tools {
				detail := t.Display
				if t.Error != "" {
					detail = "error: " + t.Error
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, string(api.ExternalToolAction(t.ID)), detail)
			}
			_ = tw.Flush()
		})
	case "macros":
		var macros []api.RunMacroInfo
		if err := c.call("run.macros", api.RunMacrosParams{Context: mc}, &macros); err != nil {
			return err
		}
		return c.emit(macros, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, m := range macros {
				v := m.Value
				if m.Error != "" {
					v = "(" + m.Error + ")"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Name, v, m.Description)
			}
			_ = tw.Flush()
		})
	case "run":
		if len(c.p.args) != 2 {
			return usagef("usage: tiegel --headless external-tools run NAME [--file FILE] [--line N] [--column N] [--selection TEXT]")
		}
		var list []api.RunConfigInfo
		if err := c.call("run.configs.list", nil, &list); err != nil {
			return err
		}
		id := ""
		for _, cfg := range list {
			if cfg.Type == runsvc.TypeExternalTool && (cfg.ID == c.p.args[1] || strings.EqualFold(cfg.Name, c.p.args[1])) {
				id = cfg.ID
				break
			}
		}
		if id == "" {
			return fmt.Errorf("no external tool %q (see external-tools list)", c.p.args[1])
		}
		return streamRun(c, "run.start", api.RunStartParams{Config: id, Context: mc})
	}
	return usagef("usage: tiegel --headless %s", commands["external-tools"].usage)
}
