package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

func init() {
	for name, takes := range map[string]bool{"apply": true, "no-declaration": false, "tab-size": true} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "usages", usage: "usages FILE LINE:COL [--no-declaration] [--server ID] [--command PATH]", summary: "find usages of the symbol at a position (language server)", minArgs: 2, maxArgs: 2, cwdProject: true, run: runUsages},
		&command{name: "format", usage: "format FILE... [--preview] [--tab-size N] [--server ID] [--command PATH]", summary: "reformat files with the language server and save them", minArgs: 1, maxArgs: -1, cwdProject: true, run: runFormat},
		&command{name: "code-actions", usage: "code-actions FILE LINE:COL [--apply N|ID] [--timeout DUR] [--server ID] [--command PATH]", summary: "list (or apply) code actions at a position incl. quick fixes", minArgs: 2, maxArgs: 2, cwdProject: true, run: runCodeActions},
		&command{name: "rename", usage: "rename FILE LINE:COL NEW-NAME [--preview] [--server ID] [--command PATH]", summary: "rename a symbol in all files (language server) and save", minArgs: 3, maxArgs: 3, cwdProject: true, run: runRename},
	)
}

// parseLineCol parses "LINE[:COL]" (1-based).
func parseLineCol(s string) (api.Pos, error) {
	l, c, hasCol := strings.Cut(s, ":")
	line, err := strconv.Atoi(l)
	if err != nil || line < 1 {
		return api.Pos{}, usagef("position must be LINE:COL (1-based), got %q", s)
	}
	col := 1
	if hasCol {
		if col, err = strconv.Atoi(c); err != nil || col < 1 {
			return api.Pos{}, usagef("position must be LINE:COL (1-based), got %q", s)
		}
	}
	return api.Pos{Line: line, Col: col}, nil
}

// withLSP starts the language server, opens files and runs fn; the server
// is stopped afterwards. Modified documents are saved by fn's callers.
func withLSP(c *cli, files []string, fn func(opened []api.DocOpenResult) error) error {
	if err := c.call("lsp.start", langphp.StartParams{Server: c.p.value("server"), Command: c.p.value("command")}, nil); err != nil {
		return err
	}
	defer func() { _ = c.call("lsp.stop", nil, nil) }()
	opened := make([]api.DocOpenResult, 0, len(files))
	for _, f := range files {
		var st api.DocOpenResult
		if err := c.call("doc.open", api.DocOpenParams{Path: f}, &st); err != nil {
			return err
		}
		if st.Language != "php" {
			return fmt.Errorf("%s is not a PHP file (language %s)", f, st.Language)
		}
		opened = append(opened, st)
	}
	return fn(opened)
}

// saveAll saves the documents changed by an applied edit.
func (c *cli) saveAll() error {
	return c.call("doc.saveAll", nil, nil)
}

func runUsages(c *cli) error {
	at, err := parseLineCol(c.p.args[1])
	if err != nil {
		return err
	}
	return withLSP(c, c.p.args[:1], func([]api.DocOpenResult) error {
		params := langphp.ReferencesParams{Path: c.p.args[0], At: at}
		if c.p.has("no-declaration") {
			no := false
			params.IncludeDeclaration = &no
		}
		var us []langphp.Usage
		if err := c.call("lsp.references", params, &us); err != nil {
			return err
		}
		if err := c.emit(us, func(w io.Writer) {
			for _, u := range us {
				fmt.Fprintf(w, "%s:%d:%d: %s\n", u.Path, u.Range.Start.Line, u.Range.Start.Col, strings.TrimSpace(u.LineText))
			}
		}); err != nil {
			return err
		}
		if len(us) == 0 {
			return &quietError{code: ExitError}
		}
		return nil
	})
}

func runFormat(c *cli) error {
	tab, err := c.p.intValue("tab-size")
	if err != nil {
		return err
	}
	preview := c.p.has("preview")
	return withLSP(c, c.p.args, func(opened []api.DocOpenResult) error {
		out := make([]langphp.FormatResult, 0, len(opened))
		for _, d := range opened {
			apply := !preview
			var fr langphp.FormatResult
			if err := c.call("lsp.formatting", langphp.FormatParams{Path: d.Path, TabSize: tab, Apply: &apply}, &fr); err != nil {
				return err
			}
			out = append(out, fr)
		}
		if !preview {
			if err := c.saveAll(); err != nil {
				return err
			}
		}
		return c.emit(out, func(w io.Writer) {
			for _, fr := range out {
				switch {
				case preview:
					for _, e := range fr.Edits {
						fmt.Fprintf(w, "%s:%d:%d-%d:%d: %q\n", fr.Path, e.Range.Start.Line, e.Range.Start.Col, e.Range.End.Line, e.Range.End.Col, e.NewText)
					}
				case fr.Applied:
					fmt.Fprintf(w, "formatted %s (%d edits)\n", fr.Path, len(fr.Edits))
				default:
					fmt.Fprintf(w, "%s already formatted\n", fr.Path)
				}
			}
		})
	})
}

func runCodeActions(c *cli) error {
	at, err := parseLineCol(c.p.args[1])
	if err != nil {
		return err
	}
	timeout := 3 * time.Second
	if v := c.p.value("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return usagef("--timeout needs a duration like 3s, got %q", v)
		}
		timeout = d
	}
	// Quick fixes need the diagnostics: wait for them (best effort).
	diags := make(chan langphp.Diagnostics, 16)
	events := c.app.NewSession(func(ev api.Event) {
		var d langphp.Diagnostics
		if ev.Topic == api.TopicLSPDiagnostics && json.Unmarshal(ev.Payload, &d) == nil {
			select {
			case diags <- d:
			default:
			}
		}
	})
	defer events.Close()
	events.Subscribe(api.TopicLSPDiagnostics)
	return withLSP(c, c.p.args[:1], func(opened []api.DocOpenResult) error {
		deadline := time.After(timeout)
	wait:
		for {
			select {
			case d := <-diags:
				if d.Path == opened[0].Path && d.Version >= opened[0].Version {
					break wait
				}
			case <-deadline:
				break wait
			case <-c.ctx.Done():
				return c.ctx.Err()
			}
		}
		var acts []langphp.CodeActionInfo
		if err := c.call("lsp.codeActions", langphp.CodeActionsParams{Path: opened[0].Path, Start: at}, &acts); err != nil {
			return err
		}
		sel := c.p.value("apply")
		if sel == "" {
			for i := range acts {
				acts[i].Action = nil
			}
			return c.emit(acts, func(w io.Writer) {
				for i, a := range acts {
					fmt.Fprintf(w, "%d. %s", i+1, a.Title)
					if a.Kind != "" {
						fmt.Fprintf(w, " [%s]", a.Kind)
					}
					if a.Preferred {
						fmt.Fprint(w, " (preferred)")
					}
					if a.Disabled != "" {
						fmt.Fprintf(w, " (disabled: %s)", a.Disabled)
					}
					fmt.Fprintln(w)
				}
			})
		}
		id := ""
		for i, a := range acts {
			if sel == a.ID || sel == strconv.Itoa(i+1) || sel == a.Title {
				id = a.ID
			}
		}
		if id == "" {
			return fmt.Errorf("no code action %q here (%d available)", sel, len(acts))
		}
		var res langphp.EditResult
		if err := c.call("lsp.applyCodeAction", langphp.ApplyCodeActionParams{ID: id}, &res); err != nil {
			return err
		}
		if err := c.saveAll(); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) { printEdit(w, res) })
	})
}

func printEdit(w io.Writer, res langphp.EditResult) {
	fmt.Fprintf(w, "applied %s\n", res.Label)
	for _, f := range res.Files {
		switch f.Kind {
		case api.FileOpRename:
			fmt.Fprintf(w, "  renamed %s -> %s\n", f.Path, f.NewPath)
		case api.FileOpEdit:
			fmt.Fprintf(w, "  %s (%d edits)\n", f.Path, f.Edits)
		default:
			fmt.Fprintf(w, "  %s %s\n", f.Kind, f.Path)
		}
	}
}

func runRename(c *cli) error {
	at, err := parseLineCol(c.p.args[1])
	if err != nil {
		return err
	}
	return withLSP(c, c.p.args[:1], func(opened []api.DocOpenResult) error {
		var res langphp.RenameResult
		params := langphp.RenameParams{Path: opened[0].Path, At: at, NewName: c.p.args[2], Preview: c.p.has("preview")}
		if err := c.call("lsp.rename", params, &res); err != nil {
			return err
		}
		if !params.Preview {
			if err := c.saveAll(); err != nil {
				return err
			}
		}
		return c.emit(res, func(w io.Writer) {
			if params.Preview {
				for _, fc := range res.Preview {
					for _, e := range fc.Edits {
						fmt.Fprintf(w, "%s:%d:%d: -> %s\n", fc.Path, e.Range.Start.Line, e.Range.Start.Col, e.NewText)
					}
					if fc.Kind != api.FileOpEdit {
						fmt.Fprintf(w, "%s %s %s\n", fc.Kind, fc.Path, fc.NewPath)
					}
				}
				return
			}
			printEdit(w, res.EditResult)
		})
	})
}
