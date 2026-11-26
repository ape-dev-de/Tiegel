package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Analysis results as seen by the GUI (D556): the status and findings of
// the plugin owning analysis.*, kept up to date by analysis.changed.
// Findings appear as subtle diagnostics (Section SectionAnalysis) in the
// editor and the Problems window; while the plugin runs, the diagnostics
// of the QA tool it replaces (Status.QATool) come through it (folded in
// by the plugin), not twice. The GUI names no tool (D190): labels use
// Status.Tool.

// SectionAnalysis is the Problems section of analysis findings.
const SectionAnalysis = "analysis"

// Analysis is the state of the analysis results plugin.
type Analysis struct {
	c *Controller
	// Status is the plugin's status; Loaded tells whether it was fetched
	// and Disabled is set while the plugin does not run.
	Status   api.AnalysisStatus
	Loaded   bool
	Disabled bool
	// files are the findings by absolute file.
	files map[string][]api.AnalysisFinding
	// fetched is the store version of files; fetching is set while a
	// request runs.
	fetched  uint64
	fetching bool
	project  string
}

func newAnalysis(c *Controller) *Analysis { return &Analysis{c: c} }

// Active reports whether the plugin runs for the project.
func (p *Analysis) Active() bool { return p.Loaded && !p.Disabled }

// Tool is the analyzer's display name.
func (p *Analysis) Tool() string {
	if p.Status.Tool != "" {
		return p.Status.Tool
	}
	return "Analysis"
}

// Replaces tells whether the QA tool plugin's diagnostics are part of
// the findings (and dropped as QA diagnostics).
func (p *Analysis) Replaces(qaTool string) bool {
	return p.Active() && qaTool != "" && qaTool == p.Status.QATool
}

// Inline tells whether findings are marked in the editor.
func (p *Analysis) Inline() bool { return p.Active() && p.Status.Inline }

// FixAvailable tells whether "Fix with AI" is offered.
func (p *Analysis) FixAvailable() bool { return p.Active() && p.Status.FixWithAI }

// ProjectChanged forgets the previous project's findings and loads.
func (p *Analysis) ProjectChanged() {
	if p.c.Project.Root == p.project {
		return
	}
	p.project = p.c.Project.Root
	p.Status, p.Loaded, p.Disabled, p.fetched = api.AnalysisStatus{}, false, false, 0
	p.apply(nil)
	if p.project != "" {
		p.Load()
	}
}

// Load fetches status and findings.
func (p *Analysis) Load() {
	c := p.c
	if !c.Project.Open {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var st api.AnalysisStatus
		err := c.call(ctx, "analysis.status", nil, &st)
		var f api.AnalysisFindings
		if err == nil {
			err = c.call(ctx, "analysis.findings", nil, &f)
		}
		return func() {
			p.Loaded = true
			p.Disabled = disabledErr(err)
			if p.Disabled {
				p.Status, p.fetched = api.AnalysisStatus{}, 0
				p.apply(nil)
				return
			}
			if err != nil && api.ErrorCode(err) != api.CodeFailed {
				return
			}
			p.Status = st
			p.fetched = f.Version
			p.apply(f.Files)
		}
	})
}

// fetch reloads the findings when the store moved on.
func (p *Analysis) fetch() {
	c := p.c
	if p.fetching || p.Status.Version == p.fetched {
		return
	}
	p.fetching = true
	c.Go(func(ctx context.Context) func() {
		var f api.AnalysisFindings
		err := c.call(ctx, "analysis.findings", nil, &f)
		return func() {
			p.fetching = false
			if err != nil {
				return
			}
			p.fetched = f.Version
			p.apply(f.Files)
			if p.Status.Version != p.fetched {
				p.fetch()
			}
		}
	})
}

// handleEvent applies phpstan.changed.
func (p *Analysis) handleEvent(topic string, payload json.RawMessage) {
	if topic != api.TopicAnalysisChanged {
		return
	}
	var st api.AnalysisStatus
	if json.Unmarshal(payload, &st) != nil {
		return
	}
	inline := p.Status.Inline
	p.Status, p.Loaded, p.Disabled = st, true, false
	if inline != st.Inline {
		p.c.DiagVersion++
	}
	p.fetch()
}

// apply replaces the findings (files nil: none) and their diagnostics;
// while the plugin runs, the replaced QA tool's diagnostics are dropped
// (they are part of the plugin's findings).
func (p *Analysis) apply(files []api.AnalysisFile) {
	c := p.c
	next := map[string][]api.AnalysisFinding{}
	for _, f := range files {
		next[f.File] = f.Findings
	}
	for file, fd := range c.diags {
		if _, ok := next[file]; !ok && len(fd.analysis) > 0 {
			fd.analysis = nil
		}
		if p.Replaces(p.Status.QATool) {
			delete(fd.qa, p.Status.QATool)
		}
	}
	for file, fs := range next {
		fd := c.diags[file]
		if fd == nil {
			fd = &fileDiags{}
			c.diags[file] = fd
		}
		fd.analysis = analysisDiags(file, c.Rel(file), p.Tool(), fs)
	}
	p.files = next
	c.DiagVersion++
}

// analysisDiags converts findings to subtle diagnostics.
func analysisDiags(file, rel, tool string, fs []api.AnalysisFinding) []Diagnostic {
	out := make([]Diagnostic, 0, len(fs))
	for _, f := range fs {
		src := tool
		if f.Identifier != "" {
			src += " · " + f.Identifier
		}
		d := Diagnostic{File: file, Path: rel, Start: f.Start, End: f.End, Line: f.Line, Col: 1, Severity: SevWarning,
			Message: f.Message, Source: src, Section: SectionAnalysis, Subtle: true, Code: f.Identifier, Tip: f.Tip, Stale: f.Stale}
		out = append(out, d)
	}
	return out
}

// At returns the findings of file on the 1-based line.
func (p *Analysis) At(file string, line int) []api.AnalysisFinding {
	var out []api.AnalysisFinding
	for _, f := range p.files[file] {
		if f.Line == line {
			out = append(out, f)
		}
	}
	return out
}

// Lines returns the 1-based lines of file with findings.
func (p *Analysis) Lines(file string) map[int]bool {
	fs := p.files[file]
	if len(fs) == 0 {
		return nil
	}
	out := make(map[int]bool, len(fs))
	for _, f := range fs {
		out[f.Line] = true
	}
	return out
}

// Count returns the number of findings.
func (p *Analysis) Count() int {
	n := 0
	for _, fs := range p.files {
		n += len(fs)
	}
	return n
}

// Summary describes the source for the Problems section header.
func (p *Analysis) Summary() string {
	st := p.Status
	s := fmt.Sprintf("%s · %d", p.Tool(), st.Count)
	if u := st.Used; u != nil {
		switch u.Kind {
		case api.AnalysisSourceCache:
			s += " · result cache"
			if u.Version != "" {
				s += " " + u.Version
			}
		default:
			s += " · " + u.Path
		}
		if !u.Modified.IsZero() {
			s += " · " + u.Modified.Local().Format("Jan 2 15:04")
		}
	}
	if st.Stale > 0 {
		s += fmt.Sprintf(" · %d stale", st.Stale)
	}
	return s
}

// Reload reads the source again.
func (p *Analysis) Reload() {
	p.call("analysis.reload", api.AnalysisReloadParams{})
}

// Import reads a report or cache file and keeps it as the source.
func (p *Analysis) Import(file string) {
	p.call("analysis.import", api.AnalysisImportParams{File: file})
}

func (p *Analysis) call(method string, params any) {
	c := p.c
	if !c.Project.Open {
		return
	}
	c.Status.Notice = "Reading " + p.Tool() + " results…"
	c.Go(func(ctx context.Context) func() {
		var st api.AnalysisStatus
		err := c.call(ctx, method, params, &st)
		return func() {
			if err != nil {
				c.notice(err)
				if disabledErr(err) {
					p.Disabled = true
				}
				return
			}
			p.Status, p.Loaded, p.Disabled = st, true, false
			switch {
			case st.Used == nil:
				c.Status.Notice = p.Tool() + ": no results found – " + st.Hint
			default:
				c.Status.Notice = fmt.Sprintf("%s: %d finding(s) from %s", p.Tool(), st.Count, st.Used.Path)
			}
			p.fetch()
		}
	})
}

// Fix asks the AI agent to fix the findings of file on line (identifier
// "" = all of the line); then opens the agent window.
func (p *Analysis) Fix(file string, line int, identifier string, opened func()) {
	c := p.c
	if !p.FixAvailable() {
		c.Status.Notice = "Fix with AI: " + p.Status.FixReason
		return
	}
	c.Status.Notice = fmt.Sprintf("Asking the agent to fix %s:%d…", c.Rel(file), line)
	c.Go(func(ctx context.Context) func() {
		var r api.AnalysisFixResult
		err := c.call(ctx, "analysis.fix", api.AnalysisFixParams{Path: file, Line: line, Identifier: identifier}, &r)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Agent.Load()
			if opened != nil {
				opened()
			}
		}
	})
}

// Rerun analyzes file with Tiegel's own run of the replaced QA tool;
// the plugin folds the result into the findings.
func (p *Analysis) Rerun(file string) bool {
	if !p.CanRerun() {
		return false
	}
	c, tool := p.c, p.Status.QATool
	c.Status.Notice = "Running " + p.Tool() + " on " + c.Rel(file) + "…"
	c.Go(func(ctx context.Context) func() {
		var res api.QARunResult
		err := c.call(ctx, "qa.run", api.QARunParams{Tool: tool, Paths: []string{file}, Trigger: "manual"}, &res)
		return func() {
			if err != nil && len(res.Tools) == 0 {
				c.notice(err)
				return
			}
			c.Status.Notice = fmt.Sprintf("%s on %s: %d problem(s)", p.Tool(), c.Rel(file), res.Count)
		}
	})
	return true
}

// CanRerun tells whether Tiegel can run the analyzer itself (its QA tool
// plugin is available).
func (p *Analysis) CanRerun() bool {
	if p.Status.QATool == "" {
		return false
	}
	for _, t := range p.c.QA.Tools {
		if t.Plugin == p.Status.QATool && t.Available {
			return true
		}
	}
	return false
}
