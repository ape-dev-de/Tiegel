package api

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Kernel service "qa" (AP17.1, decisions D350ff.): quality tools
// (formatters and analyzers) are plugins. Each contributes a QATool
// through HookQATools while it runs; the kernel only dispatches by plugin
// ID and never names a tool. qa.format applies a formatter's result to
// the open document as one undoable edit (or rewrites a closed file with
// Local History); qa.run collects the analyzers' findings, keeps them per
// tool and file and publishes them as qa.diagnostics for Problems and the
// editor.

// Topics of the qa service.
const (
	// TopicQADiagnostics: the findings of one tool for one file changed
	// (QADiagnosticsEvent; an empty list clears them).
	TopicQADiagnostics = "qa.diagnostics"
	// TopicQAStarted / TopicQAFinished: a tool run began or ended
	// (QARunEvent).
	TopicQAStarted  = "qa.started"
	TopicQAFinished = "qa.finished"
)

// QA run kinds of QARunEvent.
const (
	QAKindFormat  = "format"
	QAKindInspect = "inspect"
)

// QATool is a quality tool contributed by a plugin (value of
// HookQATools). The callbacks run in the caller's goroutine and must
// honour ctx (timeouts are the plugin's business).
type QATool struct {
	// Plugin is the ID of the contributing module; Name the display name.
	Plugin string
	Name   string
	// Order sorts the tools (lower first); the first available formatter
	// used for Reformat Code wins.
	Order int
	// Formatter and Analyzer tell which callbacks are set.
	Formatter bool
	Analyzer  bool
	// Status reports whether the tool can run and its settings.
	Status func(ctx context.Context) QAToolStatus
	// Format returns content formatted as the tool would format the file
	// File (absolute host path); it must not write File.
	Format func(ctx context.Context, req QAFormatRequest) ([]byte, error)
	// Analyze returns the findings for Paths (absolute host files or
	// directories; empty: the tool's project configuration).
	Analyze func(ctx context.Context, req QAAnalyzeRequest) (QAReport, error)
}

// QAFormatRequest is the input of QATool.Format.
type QAFormatRequest struct {
	File    string
	Content []byte
}

// QAAnalyzeRequest is the input of QATool.Analyze.
type QAAnalyzeRequest struct {
	Paths []string
}

// QAReport is the result of QATool.Analyze: findings with absolute host
// files; Messages are tool level remarks (configuration errors, …).
type QAReport struct {
	Files    []QAFileDiagnostics
	Messages []string
}

// QAToolStatus describes whether a tool can run.
type QAToolStatus struct {
	// Available is false when the program was not found (Reason says
	// where it was looked for).
	Available bool   `json:"available"`
	Command   string `json:"command,omitempty"`
	Where     string `json:"where,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Reformat: the formatter serves Reformat Code.
	Reformat bool `json:"reformat,omitempty"`
	// FormatOnSave / InspectOnSave mirror the plugin's settings.
	FormatOnSave  bool `json:"formatOnSave,omitempty"`
	InspectOnSave bool `json:"inspectOnSave,omitempty"`
}

// QAToolInfo is an entry of "qa.list".
type QAToolInfo struct {
	Plugin    string `json:"plugin"`
	Name      string `json:"name"`
	Formatter bool   `json:"formatter"`
	Analyzer  bool   `json:"analyzer"`
	QAToolStatus
	// Install tells whether the plugin declares an installation recipe
	// (install.plan with this plugin, D194).
	Install bool `json:"install"`
}

// QADiagnostic is one finding. Line and Col are 1-based (Col in
// characters, 0 = unknown); Start and End are byte offsets filled by the
// kernel from the current text (open document or file).
type QADiagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
	Code     string `json:"code,omitempty"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endCol,omitempty"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

// QAFileDiagnostics are the findings of one file.
type QAFileDiagnostics struct {
	Path        string         `json:"path"`
	File        string         `json:"file"`
	Diagnostics []QADiagnostic `json:"diagnostics"`
}

// QADiagnosticsEvent is the payload of qa.diagnostics: the current
// findings of tool Source (plugin ID) for one file.
type QADiagnosticsEvent struct {
	Path        string         `json:"path"`
	File        string         `json:"file"`
	Source      string         `json:"source"`
	Diagnostics []QADiagnostic `json:"diagnostics"`
}

// QARunEvent is the payload of qa.started and qa.finished.
type QARunEvent struct {
	Tool    string   `json:"tool"`
	Kind    string   `json:"kind"`
	Paths   []string `json:"paths,omitempty"`
	Trigger string   `json:"trigger,omitempty"`
	// Count is the number of findings (inspect) or edits (format).
	Count    int      `json:"count,omitempty"`
	Error    string   `json:"error,omitempty"`
	Messages []string `json:"messages,omitempty"`
	Millis   int64    `json:"millis,omitempty"`
}

// QARunParams are the params of "qa.run".
type QARunParams struct {
	// Tool is a plugin ID ("" = every available analyzer).
	Tool string `json:"tool,omitempty"`
	// Paths are files or directories (relative to the project or
	// absolute); empty = the whole project as the tools configure it.
	Paths []string `json:"paths,omitempty"`
	// Trigger is informational ("manual", "save").
	Trigger string `json:"trigger,omitempty"`
}

// QAToolRun reports one tool of a qa.run.
type QAToolRun struct {
	Tool     string   `json:"tool"`
	Count    int      `json:"count"`
	Error    string   `json:"error,omitempty"`
	Messages []string `json:"messages,omitempty"`
	Millis   int64    `json:"millis"`
}

// QARunResult is the result of "qa.run".
type QARunResult struct {
	Tools []QAToolRun         `json:"tools"`
	Files []QAFileDiagnostics `json:"files"`
	Count int                 `json:"count"`
}

// QAFormatParams are the params of "qa.format".
type QAFormatParams struct {
	// Tool is a plugin ID ("" = the first available formatter used for
	// Reformat Code).
	Tool string `json:"tool,omitempty"`
	Path string `json:"path"`
	// Apply false only computes the result (Text); default true.
	Apply *bool `json:"apply,omitempty"`
	// Version, if non-zero, must equal the open document's version.
	Version uint64 `json:"version,omitempty"`
}

// QAFormatResult is the result of "qa.format".
type QAFormatResult struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
	File string `json:"file"`
	// Changed tells whether the formatter changed anything; Edits is the
	// number of applied changes.
	Changed bool `json:"changed"`
	Edits   int  `json:"edits"`
	// Open: the change went to the open document (undoable, not saved);
	// otherwise the file was rewritten (with Local History).
	Open    bool   `json:"open,omitempty"`
	Applied bool   `json:"applied"`
	Version uint64 `json:"version,omitempty"`
	// Text is the formatted content when apply is false.
	Text string `json:"text,omitempty"`
}

// QADiagnosticsParams are the params of "qa.diagnostics" and "qa.clear".
type QADiagnosticsParams struct {
	Tool string `json:"tool,omitempty"`
	Path string `json:"path,omitempty"`
}

// HookQATools collects the quality tools of the running plugins.
var HookQATools = hooks.NewFilter[[]QATool]("qa.tools")

// qaNoFormatter is the error data reason when no formatter is available
// (the GUI then falls back to the language server).
const qaNoFormatter = "noFormatter"

// qaStore keeps the findings per tool and file.
type qaStore struct {
	mu sync.Mutex
	// diags: tool → absolute file → findings.
	diags map[string]map[string][]QADiagnostic
	// gen is the project generation: it changes when the project closes,
	// so that runs started before discard their findings (storeQA).
	gen uint64
}

// qaGen returns the current project generation of the findings.
func (a *App) qaGen() uint64 {
	a.qa.mu.Lock()
	defer a.qa.mu.Unlock()
	return a.qa.gen
}

// bumpQAGen starts a new project generation: findings of runs started
// before are discarded.
func (a *App) bumpQAGen() {
	a.qa.mu.Lock()
	a.qa.gen++
	a.qa.mu.Unlock()
}

func (a *App) registerQA() {
	mustHandle(a, "qa.list", "Quality tool plugins (formatters, analyzers) with availability, resolved command and installability", a.qaList)
	mustHandle(a, "qa.run", "Run analyzer plugins on paths (or the project); findings go to qa.diagnostics", a.qaRun)
	mustHandle(a, "qa.format", "Format a file with a formatter plugin; open documents get one undoable edit", a.qaFormat)
	mustHandle(a, "qa.diagnostics", "Stored findings of the analyzer plugins (optionally of one tool or path)", a.qaDiagnostics)
	mustHandle(a, "qa.clear", "Clear stored findings (of one tool or path, or all)", a.qaClear)
}

// QATools returns the contributed tools sorted by order and plugin.
func (a *App) QATools(ctx context.Context) []QATool {
	list, err := HookQATools.Apply(ctx, a.hooks, nil)
	if err != nil {
		a.log.Warn("qa.tools", "err", err)
	}
	out := slices.Clone(list)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Plugin < out[j].Plugin
	})
	return out
}

func (a *App) qaTool(ctx context.Context, id string) (QATool, error) {
	for _, t := range a.QATools(ctx) {
		if t.Plugin == id {
			return t, nil
		}
	}
	return QATool{}, Errorf(CodeNotFound, "no quality tool plugin %q is running (see qa.list, modules.enable)", id)
}

func toolStatus(ctx context.Context, t QATool) QAToolStatus {
	if t.Status == nil {
		return QAToolStatus{Available: true}
	}
	return t.Status(ctx)
}

func (a *App) qaList(ctx context.Context, _ struct{}) ([]QAToolInfo, error) {
	recipes := map[string]bool{}
	for _, r := range a.recipes(ctx) {
		recipes[r.Plugin] = true
	}
	out := []QAToolInfo{}
	for _, t := range a.QATools(ctx) {
		out = append(out, QAToolInfo{Plugin: t.Plugin, Name: t.Name, Formatter: t.Formatter, Analyzer: t.Analyzer,
			QAToolStatus: toolStatus(ctx, t), Install: recipes[t.Plugin]})
	}
	return out, nil
}

// QARun runs analyzers (see "qa.run"); plugins call it for inspections
// on save.
func (a *App) QARun(ctx context.Context, p QARunParams) (QARunResult, error) { return a.qaRun(ctx, p) }

func (a *App) qaRun(ctx context.Context, p QARunParams) (QARunResult, error) {
	// The generation is taken before the project check: a project closed
	// in between has bumped it, so the findings of this run are dropped.
	gen := a.qaGen()
	root, err := a.RequireProject()
	if err != nil {
		return QARunResult{}, err
	}
	var tools []QATool
	if p.Tool != "" {
		t, err := a.qaTool(ctx, p.Tool)
		if err != nil {
			return QARunResult{}, err
		}
		if !t.Analyzer || t.Analyze == nil {
			return QARunResult{}, Errorf(CodeInvalidParams, "%s is no analyzer", t.Name)
		}
		if st := toolStatus(ctx, t); !st.Available {
			return QARunResult{}, Errorf(CodeUnavailable, "%s: %s", t.Name, orText(st.Reason, "not available")).
				WithData(map[string]string{"plugin": t.Plugin, "reason": "missing"})
		}
		tools = []QATool{t}
	} else {
		for _, t := range a.QATools(ctx) {
			if t.Analyzer && t.Analyze != nil && toolStatus(ctx, t).Available {
				tools = append(tools, t)
			}
		}
		if len(tools) == 0 {
			return QARunResult{}, Errorf(CodeUnavailable, "no analyzer plugin is available (see qa.list)").
				WithData(map[string]string{"reason": "noAnalyzer"})
		}
	}
	paths := make([]string, 0, len(p.Paths))
	for _, s := range p.Paths {
		abs, err := a.resolvePath(s)
		if err != nil {
			return QARunResult{}, err
		}
		if !within(root, abs) {
			return QARunResult{}, Errorf(CodeInvalidParams, "%s is outside the project", s)
		}
		paths = append(paths, abs)
	}
	disp := make([]string, len(paths))
	for i, s := range paths {
		disp[i] = a.displayPath(s)
	}
	res := QARunResult{Tools: []QAToolRun{}, Files: []QAFileDiagnostics{}}
	for _, t := range tools {
		a.bus.Publish(TopicQAStarted, QARunEvent{Tool: t.Plugin, Kind: QAKindInspect, Paths: disp, Trigger: p.Trigger})
		start := time.Now()
		rep, err := t.Analyze(ctx, QAAnalyzeRequest{Paths: slices.Clone(paths)})
		run := QAToolRun{Tool: t.Plugin, Millis: time.Since(start).Milliseconds(), Messages: rep.Messages}
		if err != nil {
			run.Error = err.Error()
		} else {
			files, ok := a.storeQA(gen, t.Plugin, paths, rep.Files)
			if !ok {
				run.Error = "the project was closed during the run; findings discarded"
			}
			for _, f := range files {
				run.Count += len(f.Diagnostics)
			}
			res.Files = append(res.Files, files...)
			res.Count += run.Count
		}
		res.Tools = append(res.Tools, run)
		a.bus.Publish(TopicQAFinished, QARunEvent{Tool: t.Plugin, Kind: QAKindInspect, Paths: disp, Trigger: p.Trigger,
			Count: run.Count, Error: run.Error, Messages: run.Messages, Millis: run.Millis})
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
	}
	sort.SliceStable(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	if len(res.Tools) == 1 && res.Tools[0].Error != "" {
		return res, Errorf(CodeFailed, "%s", res.Tools[0].Error).WithData(res)
	}
	return res, nil
}

// storeQA replaces the findings of tool within scope (all files when
// scope is empty) by files, fills offsets and publishes the changes. It
// returns the stored non-empty entries; ok is false (nothing stored)
// when the project generation is no longer gen.
func (a *App) storeQA(gen uint64, tool string, scope []string, files []QAFileDiagnostics) (_ []QAFileDiagnostics, ok bool) {
	merged := map[string][]QADiagnostic{}
	for _, f := range files {
		if f.File == "" {
			continue
		}
		merged[f.File] = append(merged[f.File], f.Diagnostics...)
	}
	out := make([]QAFileDiagnostics, 0, len(merged))
	for file, ds := range merged {
		text, _, err := a.ReadText(file)
		if err != nil {
			text = nil
		}
		ds = slices.Clone(ds)
		for i := range ds {
			ds[i].Source = tool
			fillQAOffsets(&ds[i], text)
		}
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].Start < ds[j].Start })
		merged[file] = ds
		out = append(out, QAFileDiagnostics{Path: a.displayPath(file), File: file, Diagnostics: ds})
	}
	a.qa.mu.Lock()
	if a.qa.gen != gen {
		a.qa.mu.Unlock()
		return nil, false
	}
	if a.qa.diags == nil {
		a.qa.diags = map[string]map[string][]QADiagnostic{}
	}
	cur := a.qa.diags[tool]
	if cur == nil {
		cur = map[string][]QADiagnostic{}
		a.qa.diags[tool] = cur
	}
	var cleared []string
	for file := range cur {
		if _, ok := merged[file]; ok || !inScope(file, scope) {
			continue
		}
		delete(cur, file)
		cleared = append(cleared, file)
	}
	for file, ds := range merged {
		if len(ds) == 0 {
			delete(cur, file)
			continue
		}
		cur[file] = ds
	}
	a.qa.mu.Unlock()
	sort.Strings(cleared)
	for _, file := range cleared {
		a.bus.Publish(TopicQADiagnostics, QADiagnosticsEvent{Path: a.displayPath(file), File: file, Source: tool, Diagnostics: []QADiagnostic{}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	kept := out[:0]
	for _, f := range out {
		a.bus.Publish(TopicQADiagnostics, QADiagnosticsEvent{Path: f.Path, File: f.File, Source: tool, Diagnostics: f.Diagnostics})
		if len(f.Diagnostics) > 0 {
			kept = append(kept, f)
		}
	}
	return kept, true
}

// inScope reports whether file is one of the scope paths or below one
// (an empty scope covers everything).
func inScope(file string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if file == s || within(s, file) {
			return true
		}
	}
	return false
}

// ClearQA forgets the findings of tool (all tools if "") below path (all
// files if "") and publishes the changes. Plugins call it when they stop.
func (a *App) ClearQA(tool, path string) { a.clearQA(tool, path) }

func (a *App) clearQA(tool, path string) []string {
	type key struct{ tool, file string }
	var cleared []key
	a.qa.mu.Lock()
	for t, files := range a.qa.diags {
		if tool != "" && t != tool {
			continue
		}
		for f := range files {
			if path != "" && f != path && !within(path, f) {
				continue
			}
			delete(files, f)
			cleared = append(cleared, key{t, f})
		}
	}
	a.qa.mu.Unlock()
	sort.Slice(cleared, func(i, j int) bool {
		if cleared[i].tool != cleared[j].tool {
			return cleared[i].tool < cleared[j].tool
		}
		return cleared[i].file < cleared[j].file
	})
	out := make([]string, 0, len(cleared))
	for _, c := range cleared {
		a.bus.Publish(TopicQADiagnostics, QADiagnosticsEvent{Path: a.displayPath(c.file), File: c.file, Source: c.tool, Diagnostics: []QADiagnostic{}})
		out = append(out, a.displayPath(c.file))
	}
	return out
}

// QADiagnosticsResult is an entry of "qa.diagnostics".
type QADiagnosticsResult struct {
	Tool string `json:"tool"`
	QAFileDiagnostics
}

func (a *App) qaDiagnostics(_ context.Context, p QADiagnosticsParams) ([]QADiagnosticsResult, error) {
	path := ""
	if p.Path != "" {
		abs, err := a.resolvePath(p.Path)
		if err != nil {
			return nil, err
		}
		path = abs
	}
	out := []QADiagnosticsResult{}
	a.qa.mu.Lock()
	for t, files := range a.qa.diags {
		if p.Tool != "" && t != p.Tool {
			continue
		}
		for f, ds := range files {
			if path != "" && f != path && !within(path, f) {
				continue
			}
			out = append(out, QADiagnosticsResult{Tool: t, QAFileDiagnostics: QAFileDiagnostics{File: f, Diagnostics: slices.Clone(ds)}})
		}
	}
	a.qa.mu.Unlock()
	for i := range out {
		out[i].Path = a.displayPath(out[i].File)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

func (a *App) qaClear(_ context.Context, p QADiagnosticsParams) ([]string, error) {
	path := ""
	if p.Path != "" {
		abs, err := a.resolvePath(p.Path)
		if err != nil {
			return nil, err
		}
		path = abs
	}
	return a.clearQA(p.Tool, path), nil
}

// formatter picks the tool of a qa.format.
func (a *App) formatter(ctx context.Context, id string) (QATool, error) {
	if id != "" {
		t, err := a.qaTool(ctx, id)
		if err != nil {
			return t, err
		}
		if !t.Formatter || t.Format == nil {
			return t, Errorf(CodeInvalidParams, "%s is no formatter", t.Name)
		}
		if st := toolStatus(ctx, t); !st.Available {
			return t, Errorf(CodeUnavailable, "%s: %s", t.Name, orText(st.Reason, "not available")).
				WithData(map[string]string{"plugin": t.Plugin, "reason": "missing"})
		}
		return t, nil
	}
	for _, t := range a.QATools(ctx) {
		if !t.Formatter || t.Format == nil {
			continue
		}
		if st := toolStatus(ctx, t); st.Available && st.Reformat {
			return t, nil
		}
	}
	return QATool{}, Errorf(CodeUnavailable, "no formatter plugin is available for Reformat Code (see qa.list)").
		WithData(map[string]string{"reason": qaNoFormatter})
}

func (a *App) qaFormat(ctx context.Context, p QAFormatParams) (QAFormatResult, error) {
	if p.Path == "" {
		return QAFormatResult{}, Errorf(CodeInvalidParams, "path is required")
	}
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return QAFormatResult{}, err
	}
	t, err := a.formatter(ctx, p.Tool)
	if err != nil {
		return QAFormatResult{}, err
	}
	res := QAFormatResult{Tool: t.Plugin, Path: a.displayPath(abs), File: abs}
	var version uint64
	var text []byte
	if d := a.openDoc(abs); d != nil {
		snap := d.Snapshot()
		if p.Version != 0 && p.Version != snap.Version {
			return res, Errorf(CodeConflict, "version mismatch: %s is at %d, format expects %d", res.Path, snap.Version, p.Version)
		}
		version, text = snap.Version, snap.Text
	} else if text, _, err = a.ReadText(abs); err != nil {
		return res, err
	}
	disp := []string{res.Path}
	a.bus.Publish(TopicQAStarted, QARunEvent{Tool: t.Plugin, Kind: QAKindFormat, Paths: disp, Trigger: "manual"})
	start := time.Now()
	out, err := t.Format(ctx, QAFormatRequest{File: abs, Content: slices.Clone(text)})
	finished := QARunEvent{Tool: t.Plugin, Kind: QAKindFormat, Paths: disp, Trigger: "manual", Millis: time.Since(start).Milliseconds()}
	defer func() { a.bus.Publish(TopicQAFinished, finished) }()
	if err != nil {
		finished.Error = err.Error()
		return res, Errorf(CodeFailed, "%s: %w", t.Name, err)
	}
	changes := MinimalChanges(text, out)
	res.Changed = len(changes) > 0
	if p.Apply != nil && !*p.Apply {
		res.Text, res.Edits = string(out), len(changes)
		return res, nil
	}
	if !res.Changed {
		res.Applied = true
		return res, nil
	}
	op := FileOp{Kind: FileOpEdit, Path: abs, Changes: func(cur []byte) ([]TextChange, error) {
		if !bytes.Equal(cur, text) {
			return nil, Errorf(CodeConflict, "%s changed while formatting; try again", res.Path)
		}
		return changes, nil
	}}
	if version != 0 {
		op.Version = &version
	}
	steps, err := a.ApplyFileOps(ctx, "reformat ("+t.Name+")", []FileOp{op})
	if err != nil {
		finished.Error = err.Error()
		return res, err
	}
	if len(steps) == 1 {
		res.Open, res.Edits, res.Version = steps[0].Open, steps[0].Edits, steps[0].Version
	}
	finished.Count = res.Edits
	res.Applied = true
	return res, nil
}

// MinimalChanges returns the changes that turn old into updated: one per
// differing line region (line diff), narrowed to the differing bytes, so
// carets and marks outside them stay put (as D245 for save filters).
func MinimalChanges(old, updated []byte) []TextChange {
	if bytes.Equal(old, updated) {
		return nil
	}
	oldLines, newLines := gitcli.SplitLines(old), gitcli.SplitLines(updated)
	oldOff, newOff := lineOffsets(oldLines), lineOffsets(newLines)
	var out []TextChange
	for _, c := range gitcli.DiffLines(oldLines, newLines) {
		start := oldOff[c.OldStart]
		o := old[start:oldOff[c.OldEnd]]
		n := updated[newOff[c.NewStart]:newOff[c.NewEnd]]
		p, s := commonAffixes(o, n)
		out = append(out, TextChange{Start: start + p, End: start + len(o) - s, Text: string(n[p : len(n)-s])})
	}
	return out
}

// fillQAOffsets computes Start/End of d from its line and column in text:
// from the column (or the first non-blank character) to EndLine/EndCol,
// otherwise to the end of the line without trailing blanks.
func fillQAOffsets(d *QADiagnostic, text []byte) {
	if d.Line < 1 {
		d.Line = 1
	}
	if text == nil {
		return
	}
	start, ok := lineStart(text, d.Line-1)
	if !ok {
		d.Start, d.End = len(text), len(text)
		return
	}
	line := lineAt(text, start)
	if d.Col > 0 {
		d.Start = start + buffer.ByteColFromRunes(line, d.Col-1)
	} else {
		trimmed := bytes.TrimLeftFunc(line, unicode.IsSpace)
		d.Start = start + len(line) - len(trimmed)
	}
	d.End = start + len(bytes.TrimRightFunc(line, unicode.IsSpace))
	if d.EndLine >= d.Line && d.EndCol > 0 {
		if es, ok := lineStart(text, d.EndLine-1); ok {
			d.End = es + buffer.ByteColFromRunes(lineAt(text, es), d.EndCol-1)
		}
	} else if d.Col > 0 && d.Start < d.End {
		// A column without an end: the word (or character) there.
		end := d.Start
		for end < d.End {
			r, n := utf8.DecodeRune(text[end:])
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '\\') {
				break
			}
			end += n
		}
		if end == d.Start {
			_, n := utf8.DecodeRune(text[end:])
			end += n
		}
		d.End = end
	}
	if d.End < d.Start {
		d.End = d.Start
	}
}

// lineAt returns the line starting at offset start (without newline).
func lineAt(text []byte, start int) []byte {
	line := text[start:]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return bytes.TrimSuffix(line, []byte{'\r'})
}

// QAErrorReason returns the "reason" of a qa error's data ("missing",
// "noFormatter", "noAnalyzer") or "".
func QAErrorReason(err error) string {
	var ae *Error
	if !errors.As(err, &ae) {
		return ""
	}
	if m, ok := ae.Data.(map[string]string); ok {
		return m["reason"]
	}
	return ""
}
