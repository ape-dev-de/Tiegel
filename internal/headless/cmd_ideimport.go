package headless

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// Settings import from the reference IDE (M16, D191): "import-ide"
// previews (--dry-run) or imports the application configuration (newest
// found or --from) and, with --project, the project's .idea directory.

func init() {
	for name, takes := range map[string]bool{"from": true, "dry-run": false, "areas": true, "no-app": false, "no-project": false, "sources": false, "allow-project-executables": false} {
		flagSpec[name] = takes
	}
	register(&command{name: "import-ide",
		usage:   "import-ide [--project DIR] [--from CONFIGDIR] [--areas LIST] [--no-app] [--no-project] [--allow-project-executables] [--dry-run] [--sources]",
		summary: "import settings from the reference IDE (application configuration and the project's .idea; --dry-run only reports)",
		maxArgs: 0, run: runImportIDE})
}

func runImportIDE(c *cli) error {
	if c.p.has("sources") {
		var list []ideimport.Source
		if err := c.call("import.ide.sources", api.IDEImportSourcesParams{Dir: c.p.value("from")}, &list); err != nil {
			return err
		}
		return c.emit(list, func(w io.Writer) {
			if len(list) == 0 {
				fmt.Fprintln(w, "no configuration of the reference IDE found")
			}
			for _, s := range list {
				php := ""
				if s.PHP {
					php = "  (PHP)"
				}
				fmt.Fprintf(w, "%s\t%s%s\n", s.Name, s.Dir, php)
			}
		})
	}
	p := api.IDEImportParams{From: c.p.value("from"), NoApp: c.p.has("no-app"), NoProject: c.p.has("no-project"), DryRun: c.p.has("dry-run"),
		AllowProjectExecutables: c.p.has("allow-project-executables")}
	if v := c.p.value("areas"); v != "" {
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				p.Areas = append(p.Areas, a)
			}
		}
	}
	var rep api.IDEImportReport
	if err := c.call("import.ide.apply", p, &rep); err != nil {
		return err
	}
	if err := c.emit(rep, func(w io.Writer) { printImportReport(w, rep) }); err != nil {
		return err
	}
	if rep.Counts.Failed > 0 {
		return &quietError{code: ExitError}
	}
	return nil
}

// printImportReport writes the report grouped by area.
func printImportReport(w io.Writer, rep api.IDEImportReport) {
	if rep.Source != nil {
		fmt.Fprintf(w, "configuration: %s (%s)\n", rep.Source.Name, rep.Source.Dir)
	}
	if rep.Project != "" {
		fmt.Fprintf(w, "project: %s\n", rep.Project)
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	for _, ar := range rep.Areas {
		if ar.Total == 0 {
			continue
		}
		sel := " "
		if ar.Selected {
			sel = "x"
		}
		fmt.Fprintf(w, "[%s] %s (%s)\n", sel, ar.Label, ar.ID)
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, it := range rep.Items {
			if it.Area != ar.ID {
				continue
			}
			detail := it.Detail
			if it.Plugin != "" {
				detail = ideimport.JoinDetail(detail, "plugin "+it.Plugin)
			}
			fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\n", statusWord(it.Status, rep.DryRun), it.Name, detail, it.Source)
		}
		_ = tw.Flush()
	}
	held := 0
	for _, it := range rep.Items {
		if it.Executable != "" && it.Status == ideimport.StatusSkipped {
			held++
		}
	}
	if held > 0 {
		fmt.Fprintf(w, "%d item(s) would run a program from the repository and were left out; review them and pass --allow-project-executables to import them\n", held)
	}
	k := rep.Counts
	if rep.DryRun {
		fmt.Fprintf(w, "dry run: %d would be imported, %d skipped, %d unsupported, %d failed\n", k.Ready, k.Skipped, k.Unsupported, k.Failed)
		return
	}
	fmt.Fprintf(w, "%d imported, %d skipped, %d unsupported, %d failed\n", k.Imported, k.Skipped, k.Unsupported, k.Failed)
}

func statusWord(s ideimport.Status, dry bool) string {
	if s == ideimport.StatusReady && dry {
		return "would import"
	}
	return string(s)
}
