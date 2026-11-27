package gui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// Analysis results in the UI (D556, D557): findings of the plugin owning
// analysis.* (the GUI names no tool, D190) are marked subtly – a thin dotted underline in Editor.Analysis (the
// diagnostics layer, Subtle) and a small dot at the left edge of the
// gutter – and listed in Problems in their own section. "Fix with AI" (only
// while the agent plugin runs and its program is installed) is offered
// in the editor and gutter context menus, the Problems context menu and
// Alt+Enter; it sends the finding to the agent and opens its window.

// Analysis action IDs.
const (
	ActAnalysisReload keymap.Action = "analysis.reload"
	ActAnalysisFix    keymap.Action = "analysis.fixWithAI"
)

type analysisDock struct {
	v       *View
	project string
}

func installAnalysis(v *View) *analysisDock {
	d := &analysisDock{v: v}
	r := v.Registry
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	r.AddAction(Action{ID: ActAnalysisReload, Title: "Reload Static Analysis Results", Menu: "Code", Section: "inspect", Icon: "refresh",
		Run: func(v *View) { v.ctl.Analysis.Reload() }})
	r.AddAction(Action{ID: ActAnalysisFix, Title: "Fix Static Analysis Finding with AI", Icon: "edit", Run: func(v *View) {
		s := v.activeSession()
		if s == nil {
			return
		}
		s.doc.Lock()
		line, _ := s.caretLineSel()
		s.doc.Unlock()
		if len(v.ctl.Analysis.At(s.file, line)) == 0 {
			v.ctl.Status.Notice = "No static analysis finding on this line"
			return
		}
		d.fix(s.file, line, "")
	}})
	r.AddContextMenu(ContextEditor, func(_ *View, t ContextTarget) []MenuItem { return d.menu(t.File, t.Line) })
	r.AddContextMenu(ContextEditorGutter, func(_ *View, t ContextTarget) []MenuItem { return d.menu(t.File, t.Line) })
	r.AddContextMenu(ContextProblems, func(_ *View, t ContextTarget) []MenuItem {
		if t.Problem == nil || t.Problem.Section != controller.SectionAnalysis {
			return nil
		}
		return d.menu(t.File, t.Line)
	})
	return d
}

// frame follows the project.
func (d *analysisDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		v.ctl.Analysis.ProjectChanged()
	}
}

// markers puts a small dot at the left edge of lines with findings.
func (d *analysisDock) markers(file string) editorview.MarkerProvider {
	p := d.v.ctl.Analysis
	if !p.Inline() {
		return nil
	}
	lines := p.Lines(file)
	if len(lines) == 0 {
		return nil
	}
	return editorview.MarkerFunc(func(line int) []editorview.Marker {
		if !lines[line+1] {
			return nil
		}
		return []editorview.Marker{{Kind: editorview.MarkerDot, Tone: editorview.ToneAnalysis}}
	})
}

// shorten cuts a message for a menu title.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// intentions are the Alt+Enter entries for the findings of a line.
func (d *analysisDock) intentions(file string, line int) []MenuItem {
	p := d.v.ctl.Analysis
	fs := p.At(file, line)
	if len(fs) == 0 {
		return nil
	}
	var out []MenuItem
	if p.FixAvailable() {
		for _, f := range fs {
			id := f.Identifier
			out = append(out, MenuItem{Title: "Fix with AI: " + shorten(f.Message, 70), Icon: "edit", Run: func() { d.fix(file, line, id) }})
		}
	}
	if p.CanRerun() {
		out = append(out, MenuItem{Title: "Re-run " + p.Tool() + " on This File", Icon: "refresh", Run: func() { p.Rerun(file) }})
	}
	return out
}

// menu are the context menu entries for the findings of a line.
func (d *analysisDock) menu(file string, line int) []MenuItem {
	p := d.v.ctl.Analysis
	fs := p.At(file, line)
	if len(fs) == 0 {
		return nil
	}
	var out []MenuItem
	if p.FixAvailable() {
		title := "Fix " + p.Tool() + " Finding with AI"
		if len(fs) > 1 {
			title = fmt.Sprintf("Fix %d %s Findings with AI", len(fs), p.Tool())
		}
		out = append(out, MenuItem{Title: title, Icon: "edit", Run: func() { d.fix(file, line, "") }})
	}
	if p.CanRerun() {
		out = append(out, MenuItem{Title: "Re-run " + p.Tool() + " on This File", Icon: "refresh", Run: func() { p.Rerun(file) }})
	}
	msg := make([]string, 0, len(fs))
	for _, f := range fs {
		m := f.Message
		if f.Identifier != "" {
			m += " [" + f.Identifier + "]"
		}
		msg = append(msg, m)
	}
	out = append(out, MenuItem{Title: "Copy " + p.Tool() + " Message", Icon: "copy", Run: func() {
		d.v.CopyText(strings.Join(msg, "\n"))
		d.v.ctl.Status.Notice = "Copied the " + p.Tool() + " message"
	}})
	return out
}

// fix sends the finding(s) to the agent and opens its window.
func (d *analysisDock) fix(file string, line int, identifier string) {
	v := d.v
	v.ctl.Analysis.Fix(file, line, identifier, func() { v.ShowTool(ToolAgent) })
}

// analysisHint is the hover line of analysis findings in the editor; nil
// when they are not shown inline.
func (v *View) analysisHint() *string {
	p := v.ctl.Analysis
	if !p.Inline() {
		return nil
	}
	h := ""
	if p.FixAvailable() {
		h = "Fix with AI: " + v.chordLabel(ActContextActions) + " or right click"
	}
	return &h
}
