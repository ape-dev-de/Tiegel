package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Analysis results (D556): "analysis" shows what the plugin owning
// analysis.* read from a report or the analyzer's cache (the analyzer is
// not started), imports a report, lists the sources and sends a finding
// to the AI agent ("fix PATH:LINE", asks denied unless --yes, like
// "agent prompt"). The core names no tool (D190): labels come from the
// status.

func init() {
	flagSpec["reset"] = false
	flagSpec["stale"] = false
	register(&command{name: "analysis",
		usage: "analysis [findings [PATH] [--stale] | sources | import FILE | reload [--reset] | fix PATH:LINE [--dry-run] [--yes]]",
		summary: "static analysis results without running the analyzer: findings from a report or the cache of the last run, sources, import, " +
			"Fix with AI (exit 1 when findings are listed)",
		maxArgs: -1, cwdProject: true, run: runAnalysis})
}

func runAnalysis(c *cli) error {
	sub, args := "findings", c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "findings":
		if len(args) > 1 {
			return usagef("usage: tiegel --headless analysis findings [PATH]")
		}
		p := api.AnalysisFindingsParams{}
		if len(args) == 1 {
			p.Path = args[0]
		}
		var f api.AnalysisFindings
		if err := c.call("analysis.findings", p, &f); err != nil {
			return err
		}
		if c.p.has("stale") {
			f = onlyStale(f)
		}
		tool := "static analysis"
		if !c.json {
			var st api.AnalysisStatus
			if c.call("analysis.status", nil, &st) == nil && st.Tool != "" {
				tool = st.Tool
			}
		}
		if err := c.emit(f, func(w io.Writer) { printAnalysisFindings(w, tool, f) }); err != nil {
			return err
		}
		if f.Count > 0 {
			return &quietError{code: ExitError}
		}
		return nil
	case "sources", "status", "reload", "import":
		var st api.AnalysisStatus
		var err error
		switch sub {
		case "reload":
			err = c.call("analysis.reload", api.AnalysisReloadParams{Reset: c.p.has("reset")}, &st)
		case "import":
			if len(args) != 1 {
				return usagef("usage: tiegel --headless analysis import FILE")
			}
			err = c.call("analysis.import", api.AnalysisImportParams{File: args[0]}, &st)
		default:
			err = c.call("analysis.status", nil, &st)
		}
		if err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) { printAnalysisStatus(w, st) })
	case "fix":
		if len(args) != 1 {
			return usagef("usage: tiegel --headless analysis fix PATH:LINE [--dry-run] [--yes]")
		}
		i := strings.LastIndexByte(args[0], ':')
		line := 0
		if i > 0 {
			line, _ = strconv.Atoi(args[0][i+1:])
		}
		if line < 1 {
			return usagef("usage: tiegel --headless analysis fix PATH:LINE (line missing in %q)", args[0])
		}
		var r api.AnalysisFixResult
		if err := c.call("analysis.fix", api.AnalysisFixParams{Path: args[0][:i], Line: line, DryRun: true}, &r); err != nil {
			return err
		}
		if c.p.has("dry-run") {
			return c.emit(r, func(w io.Writer) { fmt.Fprintln(w, r.Prompt) })
		}
		var st api.AnalysisStatus
		if err := c.call("analysis.status", nil, &st); err != nil {
			return err
		}
		if !st.FixWithAI {
			return api.Errorf(api.CodeUnavailable, "Fix with AI: %s", strings.TrimSuffix(st.FixReason, "."))
		}
		// The agent's answer streams like "agent prompt" (permissions as
		// set; asks are denied unless --yes).
		return c.agentPrompt(r.Prompt)
	}
	return usagef("usage: tiegel --headless %s", commands["analysis"].usage)
}

func onlyStale(f api.AnalysisFindings) api.AnalysisFindings {
	out := api.AnalysisFindings{Files: []api.AnalysisFile{}, Version: f.Version}
	for _, file := range f.Files {
		var fs []api.AnalysisFinding
		for _, x := range file.Findings {
			if x.Stale {
				fs = append(fs, x)
			}
		}
		if len(fs) > 0 {
			out.Files = append(out.Files, api.AnalysisFile{Path: file.Path, File: file.File, Findings: fs})
			out.Count += len(fs)
		}
	}
	return out
}

func printAnalysisFindings(w io.Writer, tool string, f api.AnalysisFindings) {
	if f.Count == 0 {
		fmt.Fprintf(w, "no %s findings\n", tool)
		return
	}
	for _, file := range f.Files {
		for _, x := range file.Findings {
			mark := ""
			if x.Stale {
				mark = " (stale)"
			}
			id := ""
			if x.Identifier != "" {
				id = "  [" + x.Identifier + "]"
			}
			fmt.Fprintf(w, "%s:%d: %s%s%s\n", file.Path, x.Line, x.Message, id, mark)
			if x.Tip != "" {
				fmt.Fprintf(w, "    tip: %s\n", x.Tip)
			}
		}
	}
	fmt.Fprintf(w, "%d finding(s) in %d file(s)\n", f.Count, len(f.Files))
}

func printAnalysisStatus(w io.Writer, st api.AnalysisStatus) {
	for _, s := range st.Sources {
		mark := " "
		if s.Used {
			mark = "*"
		}
		what := s.Kind
		if s.Format != "" && s.Format != "resultCache" {
			what += " " + s.Format
		}
		if s.Version != "" {
			what += " (" + strings.TrimSpace(st.Tool+" "+s.Version) + ")"
		}
		state := "found"
		switch {
		case s.Reason != "":
			state = s.Reason
		case s.Exists:
			state = s.Modified.Local().Format("2006-01-02 15:04:05")
		}
		path := s.Path
		if path == "" {
			path = s.File
		}
		fmt.Fprintf(w, "%s %-7s %s  [%s] %s\n", mark, s.Origin, path, what, state)
	}
	if st.Config != "" {
		fmt.Fprintf(w, "config:   %s\n", st.Config)
	}
	if st.Used == nil {
		fmt.Fprintf(w, "no source in use (%s)\n", st.Hint)
	} else {
		fmt.Fprintf(w, "findings: %d in %d file(s)", st.Count, st.Files)
		if st.Stale > 0 {
			fmt.Fprintf(w, ", %d stale", st.Stale)
		}
		fmt.Fprintln(w)
	}
	if st.Error != "" {
		fmt.Fprintf(w, "error:    %s\n", st.Error)
	}
	for _, m := range st.Messages {
		fmt.Fprintf(w, "note:     %s\n", m)
	}
	if st.FixWithAI {
		fmt.Fprintln(w, "fix:      tiegel --headless analysis fix PATH:LINE (AI agent)")
	} else if st.FixReason != "" {
		fmt.Fprintf(w, "fix:      unavailable – %s\n", st.FixReason)
	}
}
