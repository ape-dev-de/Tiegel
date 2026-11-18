package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Quality tool plugins (AP17.1) as seen by the GUI: the tool list of
// qa.list, findings from qa.diagnostics events (Problems and editor
// squiggles, source = tool), Inspect File/Project and Reformat Code with
// a formatter plugin. The GUI knows tools only by plugin ID and name.

// QAState is the state of the quality tool plugins.
type QAState struct {
	// Tools are the running plugins (qa.list order).
	Tools []api.QAToolInfo
	// Running counts the tool runs in progress (qa.started/finished).
	Running int
}

// Formatter returns the formatter Reformat Code uses (first available
// one with "Use for Reformat Code").
func (s *QAState) Formatter() (api.QAToolInfo, bool) {
	for _, t := range s.Tools {
		if t.Formatter && t.Available && t.Reformat {
			return t, true
		}
	}
	return api.QAToolInfo{}, false
}

// Analyzers returns the available analyzers and the missing ones.
func (s *QAState) Analyzers() (available, missing []api.QAToolInfo) {
	for _, t := range s.Tools {
		if !t.Analyzer {
			continue
		}
		if t.Available {
			available = append(available, t)
		} else {
			missing = append(missing, t)
		}
	}
	return available, missing
}

// name returns the display name of plugin id.
func (s *QAState) name(id string) string {
	for _, t := range s.Tools {
		if t.Plugin == id {
			return t.Name
		}
	}
	return id
}

// RefreshQA reloads the tool list.
func (c *Controller) RefreshQA() {
	if !c.Project.Open {
		c.QA.Tools = nil
		return
	}
	c.Go(func(ctx context.Context) func() {
		var list []api.QAToolInfo
		err := c.call(ctx, "qa.list", nil, &list)
		return func() {
			if err == nil {
				c.QA.Tools = list
			}
		}
	})
}

// handleQAEvent processes qa.* events.
func (c *Controller) handleQAEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicQADiagnostics:
		var ev api.QADiagnosticsEvent
		if json.Unmarshal(payload, &ev) == nil {
			c.setQADiagnostics(ev)
		}
	case api.TopicQAStarted:
		c.QA.Running++
	case api.TopicQAFinished:
		if c.QA.Running > 0 {
			c.QA.Running--
		}
		var ev api.QARunEvent
		if json.Unmarshal(payload, &ev) == nil && ev.Error != "" {
			c.Status.Notice = c.QA.name(ev.Tool) + ": " + ev.Error
		}
	}
}

// setQADiagnostics replaces the findings of one tool for one file.
func (c *Controller) setQADiagnostics(ev api.QADiagnosticsEvent) {
	file := ev.File
	if file == "" {
		file = c.Abs(ev.Path)
	}
	fd := c.diags[file]
	if fd == nil {
		if len(ev.Diagnostics) == 0 {
			return
		}
		fd = &fileDiags{}
		c.diags[file] = fd
	}
	if fd.qa == nil {
		fd.qa = map[string][]Diagnostic{}
	}
	if len(ev.Diagnostics) == 0 {
		delete(fd.qa, ev.Source)
		c.DiagVersion++
		return
	}
	name := c.QA.name(ev.Source)
	out := make([]Diagnostic, 0, len(ev.Diagnostics))
	for _, d := range ev.Diagnostics {
		src := name
		if d.Code != "" {
			src += " · " + d.Code
		}
		out = append(out, Diagnostic{File: file, Path: c.Rel(file), Start: d.Start, End: d.End, Line: d.Line, Col: max(d.Col, 1),
			Severity: parseSeverity(d.Severity), Message: d.Message, Source: src})
	}
	fd.qa[ev.Source] = out
	c.DiagVersion++
}

// qaAll returns the findings of all quality tools of fd (by plugin ID).
func (fd *fileDiags) qaAll() []Diagnostic {
	ids := make([]string, 0, len(fd.qa))
	for id := range fd.qa {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Diagnostic
	for _, id := range ids {
		out = append(out, fd.qa[id]...)
	}
	return out
}

// qaReason returns the data.reason of a qa error ("missing",
// "noFormatter", "noAnalyzer") and the plugin it names.
func qaReason(err error) (reason, plugin string) {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Data == nil {
		return "", ""
	}
	var d struct {
		Reason string `json:"reason"`
		Plugin string `json:"plugin"`
	}
	if !decodeData(ae.Data, &d) {
		return "", ""
	}
	return d.Reason, d.Plugin
}

// Inspect runs the available analyzers on paths (project relative or
// absolute; none = the project). Without any available analyzer,
// missing(list) is called with the analyzers that could be installed.
func (c *Controller) Inspect(paths []string, missing func([]api.QAToolInfo)) {
	if !c.Project.Open {
		return
	}
	if avail, miss := c.QA.Analyzers(); len(avail) == 0 {
		if len(miss) > 0 && missing != nil {
			missing(miss)
			return
		}
		c.Status.Notice = "No inspection tool plugin is enabled (Settings › Plugins)"
		return
	}
	what := "project"
	if len(paths) == 1 {
		what = c.Rel(c.Abs(paths[0]))
	}
	c.Status.Notice = "Inspecting " + what + "…"
	c.Go(func(ctx context.Context) func() {
		var res api.QARunResult
		err := c.call(ctx, "qa.run", api.QARunParams{Paths: paths, Trigger: "manual"}, &res)
		return func() {
			if err != nil && len(res.Tools) == 0 {
				c.notice(err)
				return
			}
			failed := 0
			for _, t := range res.Tools {
				if t.Error != "" {
					failed++
				}
			}
			switch {
			case failed > 0:
				c.Status.Notice = fmt.Sprintf("Inspected %s: %d problem(s), %d tool(s) failed", what, res.Count, failed)
			case res.Count == 0:
				c.Status.Notice = "Inspected " + what + ": no problems"
			default:
				c.Status.Notice = fmt.Sprintf("Inspected %s: %d problem(s)", what, res.Count)
			}
		}
	})
}

// FormatWithQA formats file with the Reformat Code formatter plugin; the
// change is one undo step of the open document. missing is called when
// the configured tool vanished (offer the installation).
func (c *Controller) FormatWithQA(file string, missing func(plugin string)) bool {
	t, ok := c.QA.Formatter()
	if !ok {
		return false
	}
	c.Status.Notice = "Reformatting with " + t.Name + "…"
	c.Go(func(ctx context.Context) func() {
		var r api.QAFormatResult
		err := c.call(ctx, "qa.format", api.QAFormatParams{Tool: t.Plugin, Path: file}, &r)
		return func() {
			switch {
			case err != nil:
				if reason, plugin := qaReason(err); reason == "missing" && missing != nil {
					missing(plugin)
				}
				c.notice(err)
				c.RefreshQA()
			case !r.Changed:
				c.Status.Notice = "Already formatted (" + t.Name + ")"
			default:
				c.Status.Notice = fmt.Sprintf("Reformatted with %s (%d changes)", t.Name, r.Edits)
			}
		}
	})
	return true
}
