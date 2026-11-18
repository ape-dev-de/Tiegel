package headless

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Quality tool plugins (AP17.1): "qa" lists the tools, inspects files or
// the project, formats files and shows or clears stored findings. Tools
// are addressed by plugin ID only (--tool).

func init() {
	flagSpec["tool"] = true
	register(&command{name: "qa",
		usage:   "qa [list | run [PATH...] | format FILE... [--preview] | diagnostics [PATH] | clear [PATH]] [--tool PLUGIN]",
		summary: "quality tool plugins: list, inspect (exit 1 on findings), format (saves closed files), stored findings",
		maxArgs: -1, cwdProject: true, run: runQA})
}

func runQA(c *cli) error {
	sub, args := "list", c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	tool := c.p.value("tool")
	switch sub {
	case "list":
		if len(args) > 0 {
			return usagef("usage: tiegel --headless qa list")
		}
		var list []api.QAToolInfo
		if err := c.call("qa.list", nil, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, t := range list {
				kind := []string{}
				if t.Formatter {
					kind = append(kind, "format")
				}
				if t.Analyzer {
					kind = append(kind, "inspect")
				}
				state := "available"
				if !t.Available {
					state = "missing: " + t.Reason
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Plugin, t.Name, strings.Join(kind, ","), t.Command, state)
			}
			_ = tw.Flush()
			for _, t := range list {
				if !t.Available && t.Install {
					fmt.Fprintf(w, "install %s: tiegel --headless install %s\n", t.Name, t.Plugin)
				}
			}
		})
	case "run":
		var res api.QARunResult
		err := c.call("qa.run", api.QARunParams{Tool: tool, Paths: args, Trigger: "manual"}, &res)
		if err != nil && len(res.Tools) == 0 {
			return err
		}
		if eerr := c.emit(res, func(w io.Writer) {
			for _, f := range res.Files {
				for _, d := range f.Diagnostics {
					fmt.Fprintf(w, "%s:%d:%d: %s: %s", f.Path, d.Line, max(d.Col, 1), d.Severity, d.Message)
					if d.Code != "" {
						fmt.Fprintf(w, " [%s]", d.Code)
					}
					fmt.Fprintf(w, " (%s)\n", d.Source)
				}
			}
			for _, t := range res.Tools {
				for _, m := range t.Messages {
					fmt.Fprintf(c.env.Stderr, "%s: %s\n", t.Tool, m)
				}
				if t.Error != "" {
					fmt.Fprintf(c.env.Stderr, "%s failed: %s\n", t.Tool, t.Error)
				}
			}
		}); eerr != nil {
			return eerr
		}
		if err != nil {
			return &quietError{code: ExitError}
		}
		for _, t := range res.Tools {
			if t.Error != "" {
				return &quietError{code: ExitError}
			}
		}
		if res.Count > 0 {
			return &quietError{code: ExitError}
		}
		return nil
	case "format":
		if len(args) == 0 {
			return usagef("usage: tiegel --headless qa format FILE... [--tool PLUGIN] [--preview]")
		}
		preview := c.p.has("preview")
		var all []api.QAFormatResult
		for _, f := range args {
			p := api.QAFormatParams{Tool: tool, Path: f}
			if preview {
				p.Apply = new(bool)
			}
			var r api.QAFormatResult
			if err := c.call("qa.format", p, &r); err != nil {
				return err
			}
			all = append(all, r)
		}
		return c.emit(all, func(w io.Writer) {
			for _, r := range all {
				switch {
				case preview && r.Changed:
					fmt.Fprintf(w, "--- %s (%s, %d changes)\n%s", r.Path, r.Tool, r.Edits, r.Text)
				case r.Changed:
					fmt.Fprintf(w, "%s: formatted with %s (%d changes)\n", r.Path, r.Tool, r.Edits)
				default:
					fmt.Fprintf(w, "%s: already formatted (%s)\n", r.Path, r.Tool)
				}
			}
		})
	case "diagnostics", "clear":
		if len(args) > 1 {
			return usagef("usage: tiegel --headless qa %s [PATH] [--tool PLUGIN]", sub)
		}
		p := api.QADiagnosticsParams{Tool: tool}
		if len(args) == 1 {
			p.Path = args[0]
		}
		if sub == "clear" {
			var gone []string
			if err := c.call("qa.clear", p, &gone); err != nil {
				return err
			}
			return c.emit(gone, func(w io.Writer) { fmt.Fprintf(w, "cleared %d file(s)\n", len(gone)) })
		}
		var list []api.QADiagnosticsResult
		if err := c.call("qa.diagnostics", p, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) {
			for _, f := range list {
				for _, d := range f.Diagnostics {
					fmt.Fprintf(w, "%s:%d:%d: %s: %s (%s)\n", f.Path, d.Line, max(d.Col, 1), d.Severity, d.Message, f.Tool)
				}
			}
		})
	}
	return usagef("usage: tiegel --headless %s", commands["qa"].usage)
}
