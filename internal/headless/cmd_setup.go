package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

func init() {
	flagSpec["discover"] = false
	register(
		&command{name: "setup", usage: "setup [--dry-run] | setup apply [KEY...] | setup dismiss", summary: "proposed project setup (PHP root, interpreter, PHPUnit, QA tools) from local discovery; apply writes the proposals (all or the given keys)",
			maxArgs: -1, cwdProject: true, run: runSetup},
	)
}

func runSetup(c *cli) error {
	var prop api.SetupProposal
	if err := c.call("project.setupProposal", nil, &prop); err != nil {
		return err
	}
	verb := ""
	if len(c.p.args) > 0 {
		verb = c.p.args[0]
	}
	if verb != "" && verb != "apply" && verb != "dismiss" {
		return usagef("usage: tiegel --headless %s", commands["setup"].usage)
	}
	if verb == "" {
		return c.emit(prop, func(w io.Writer) {
			if len(prop.Items) == 0 {
				fmt.Fprintln(w, "nothing to set up: no unconfigured setting has a recommended value")
				return
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, it := range prop.Items {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Key, it.Proposed.Label, it.Proposed.Detail)
			}
			_ = tw.Flush()
			fmt.Fprintln(w, "apply with: tiegel --headless setup apply [KEY...]")
		})
	}
	params := api.SetupApplyParams{Dismiss: verb == "dismiss"}
	if verb == "apply" {
		only := map[string]bool{}
		for _, k := range c.p.args[1:] {
			only[k] = true
		}
		for _, it := range prop.Items {
			if len(only) > 0 && !only[it.Key] {
				continue
			}
			raw, err := json.Marshal(it.Proposed.Value)
			if err != nil {
				return err
			}
			params.Items = append(params.Items, api.SetupValue{Key: it.Key, Value: raw})
		}
	}
	var res api.SetupApplyResult
	if err := c.call("project.setupApply", params, &res); err != nil {
		return err
	}
	if err := c.emit(res, func(w io.Writer) {
		for _, k := range res.Applied {
			fmt.Fprintf(w, "applied %s\n", k)
		}
		for _, e := range res.Errors {
			fmt.Fprintf(w, "error %s\n", e)
		}
		if params.Dismiss {
			fmt.Fprintln(w, "setup proposal dismissed for this project")
		}
	}); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		return &quietError{code: ExitError}
	}
	return nil
}

func runSettingsCandidates(c *cli, key string) error {
	var res api.CandidatesResult
	if err := c.call("settings.candidates", api.CandidatesParams{Key: key}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if len(res.Candidates) == 0 {
			fmt.Fprintf(w, "%s: no candidates found", key)
			if res.Source != "" {
				fmt.Fprintf(w, " (%s)", res.Source)
			}
			fmt.Fprintln(w)
			return
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, cand := range res.Candidates {
			mark := " "
			switch {
			case cand.Current:
				mark = "*"
			case cand.Recommended:
				mark = "+"
			}
			v, _ := json.Marshal(cand.Value)
			fmt.Fprintf(tw, "%s %s\t%s\t%s\n", mark, cand.Label, v, cand.Detail)
		}
		_ = tw.Flush()
		fmt.Fprintln(w, "(* current, + recommended)")
	})
}

func rel(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil && base != "" {
		return filepath.ToSlash(r)
	}
	return p
}

func runComposeDiscover(c *cli) error {
	var res phpruntime.DiscoverResult
	if err := c.call("compose.discover", phpruntime.DiscoverParams{Profiles: c.p.flags["profile"]}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if len(res.Sets) == 0 {
			fmt.Fprintf(w, "no compose file in or around %s\n", res.Root)
			return
		}
		fmt.Fprintf(w, "PHP root %s\n", rel(res.Root, res.PHPRoot))
		for _, s := range res.Sets {
			mark := ""
			if s.Configured {
				mark = " [in use]"
			}
			fmt.Fprintf(w, "\n%s (%s, %d level(s))%s\n", rel(res.Root, s.Dir), s.Relation, s.Levels, mark)
			if s.Error != "" {
				fmt.Fprintf(w, "  error: %s\n", s.Error)
				continue
			}
			names := make([]string, len(s.Files))
			for i, f := range s.Files {
				names[i] = rel(res.Root, f)
			}
			fmt.Fprintf(w, "  files: %s\n", strings.Join(names, ", "))
			for _, o := range s.Overlays {
				fmt.Fprintf(w, "  optional: %s\n", rel(res.Root, o))
			}
			for _, cand := range s.Candidates {
				printCandidate(w, res.Root, cand)
			}
		}
	})
}

func printCandidate(w io.Writer, root string, cand phpruntime.CandidateInfo) {
	fmt.Fprintf(w, "  %s (score %d)", cand.Service, cand.Score)
	if cand.Image != "" {
		fmt.Fprintf(w, " image %s", cand.Image)
	}
	fmt.Fprintln(w)
	for _, r := range cand.Reasons {
		fmt.Fprintf(w, "    + %s\n", r)
	}
	for _, m := range cand.Mappings {
		fmt.Fprintf(w, "    map %s -> %s\n", rel(root, m.Host), m.Remote)
	}
}

func runComposeUse(c *cli, service string) error {
	var info phpruntime.InterpreterInfo
	if err := c.call("interp.useCompose", phpruntime.UseComposeParams{Service: service, Files: c.p.flags["file"]}, &info); err != nil {
		return err
	}
	return c.emit(info, func(w io.Writer) { fmt.Fprintf(w, "selected %s\n", info.ID) })
}
