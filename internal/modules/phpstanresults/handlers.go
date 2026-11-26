package phpstanresults

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// statusNow builds the status from the store.
func (m *Module) statusNow(ctx context.Context) api.AnalysisStatus {
	a := m.agentNow(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	st := api.AnalysisStatus{
		Sources: slices.Clone(m.st.sources), Config: m.st.config, Messages: slices.Clone(m.st.messages), Error: m.st.err,
		Loaded: m.st.loaded, Version: m.st.version, Inline: setting(m, SettingInline, true),
		FixWithAI: a.available, FixReason: a.reason,
		Tool: "PHPStan", QATool: qaTool,
		Hint: "set " + SettingReportFile + " (Settings › Quality Tools › PHPStan Results), or run PHPStan once so that its result cache exists",
	}
	if st.Sources == nil {
		st.Sources = []api.AnalysisSource{}
	}
	if m.st.used != nil {
		u := *m.st.used
		st.Used = &u
		for i := range st.Sources {
			if st.Sources[i].Used {
				st.Sources[i].Format, st.Sources[i].Version = u.Format, u.Version
			}
		}
	}
	for _, fs := range m.st.files {
		st.Files++
		st.Count += len(fs)
		for _, f := range fs {
			if f.Stale {
				st.Stale++
			}
		}
	}
	return st
}

func (m *Module) publishStatus() {
	m.publish(api.TopicAnalysisChanged, m.statusNow(context.Background()))
}

func (m *Module) status(ctx context.Context, _ struct{}) (api.AnalysisStatus, error) {
	if _, err := m.requireProject(); err != nil {
		return api.AnalysisStatus{}, err
	}
	m.mu.Lock()
	loaded := !m.st.loaded.IsZero() && m.st.root == m.application().ProjectRoot()
	m.mu.Unlock()
	if !loaded {
		return m.load(ctx, loadOpts{})
	}
	return m.statusNow(ctx), nil
}

func (m *Module) requireProject() (*api.App, error) {
	app := m.application()
	if app == nil {
		return nil, api.Errorf(api.CodeUnavailable, "phpstan: not started")
	}
	if _, err := app.RequireProject(); err != nil {
		return nil, err
	}
	return app, nil
}

// ensureLoaded loads the store once per project (headless calls arrive
// before the asynchronous first load).
func (m *Module) ensureLoaded(ctx context.Context) error {
	app, err := m.requireProject()
	if err != nil {
		return err
	}
	m.mu.Lock()
	loaded := !m.st.loaded.IsZero() && m.st.root == app.ProjectRoot()
	m.mu.Unlock()
	if !loaded {
		if _, err := m.load(ctx, loadOpts{}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) findings(ctx context.Context, p api.AnalysisFindingsParams) (api.AnalysisFindings, error) {
	if err := m.ensureLoaded(ctx); err != nil {
		return api.AnalysisFindings{}, err
	}
	app := m.application()
	scope := ""
	if p.Path != "" {
		abs, err := app.ResolvePath(p.Path)
		if err != nil {
			return api.AnalysisFindings{}, err
		}
		scope = abs
	}
	out := api.AnalysisFindings{Files: []api.AnalysisFile{}}
	m.mu.Lock()
	for f, fs := range m.st.files {
		if scope != "" && f != scope && !within(scope, f) {
			continue
		}
		out.Files = append(out.Files, api.AnalysisFile{File: f, Findings: slices.Clone(fs)})
		out.Count += len(fs)
	}
	out.Version = m.st.version
	m.mu.Unlock()
	for i := range out.Files {
		out.Files[i].Path = app.DisplayPath(out.Files[i].File)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

func (m *Module) importFile(ctx context.Context, p api.AnalysisImportParams) (api.AnalysisStatus, error) {
	app, err := m.requireProject()
	if err != nil {
		return api.AnalysisStatus{}, err
	}
	if p.File != "" {
		abs, err := app.ResolvePath(p.File)
		if err != nil {
			return api.AnalysisStatus{}, err
		}
		if !fileExists(abs) {
			return api.AnalysisStatus{}, api.Errorf(api.CodeNotFound, "%s does not exist", p.File)
		}
		m.mu.Lock()
		if m.st.root != app.ProjectRoot() {
			v := m.st.version
			m.st = state{root: app.ProjectRoot(), version: v}
		}
		m.st.imported = abs
		m.mu.Unlock()
	}
	return m.load(ctx, loadOpts{})
}

func (m *Module) reload(ctx context.Context, p api.AnalysisReloadParams) (api.AnalysisStatus, error) {
	if _, err := m.requireProject(); err != nil {
		return api.AnalysisStatus{}, err
	}
	if p.Reset {
		m.mu.Lock()
		m.st.imported = ""
		m.mu.Unlock()
	}
	return m.load(ctx, loadOpts{ifChanged: p.IfChanged && !p.Reset})
}

// contextLines are shown around the finding in the prompt.
const contextLines = 3

func (m *Module) fix(ctx context.Context, p api.AnalysisFixParams) (api.AnalysisFixResult, error) {
	if err := m.ensureLoaded(ctx); err != nil {
		return api.AnalysisFixResult{}, err
	}
	app := m.application()
	if p.Path == "" || p.Line < 1 {
		return api.AnalysisFixResult{}, api.Errorf(api.CodeInvalidParams, "path and line are required")
	}
	file, err := app.ResolvePath(p.Path)
	if err != nil {
		return api.AnalysisFixResult{}, err
	}
	m.mu.Lock()
	var fs []api.AnalysisFinding
	for _, f := range m.st.files[file] {
		if f.Line == p.Line && (p.Identifier == "" || f.Identifier == p.Identifier) {
			fs = append(fs, f)
		}
	}
	m.mu.Unlock()
	res := api.AnalysisFixResult{Path: app.DisplayPath(file), Findings: fs}
	if len(fs) == 0 {
		return res, api.Errorf(api.CodeNotFound, "no PHPStan finding at %s:%d", res.Path, p.Line)
	}
	text, _, err := app.ReadText(file)
	if err != nil {
		return res, err
	}
	res.Prompt = Prompt(res.Path, p.Line, fs, text)
	if p.DryRun {
		return res, nil
	}
	if a := m.agentNow(ctx); !a.available {
		return res, api.Errorf(api.CodeUnavailable, "Fix with AI: %s", strings.TrimSuffix(a.reason, ".")).WithData(map[string]string{"reason": "noAgent"})
	}
	var turn api.AgentTurnResult
	if err := app.CallLocal(ctx, "agent.prompt", api.AgentPromptParams{Text: res.Prompt, Files: []string{file}, Wait: p.Wait, Ask: p.Ask}, &turn); err != nil {
		return res, err
	}
	res.Sent, res.Turn = true, turn
	return res, nil
}

// Prompt is the message for the agent: the finding(s) with identifier and
// tip, the code around the line, and the instruction to fix only that.
func Prompt(path string, line int, fs []api.AnalysisFinding, text []byte) string {
	var b strings.Builder
	issue := "this PHPStan issue"
	if len(fs) > 1 {
		issue = "these PHPStan issues"
	}
	fmt.Fprintf(&b, "Fix %s in %s at line %d – only %s, nothing else.\n\n", issue, path, line, map[bool]string{true: "them", false: "it"}[len(fs) > 1])
	for _, f := range fs {
		fmt.Fprintf(&b, "PHPStan: %s\n", f.Message)
		if f.Identifier != "" {
			fmt.Fprintf(&b, "Identifier: %s\n", f.Identifier)
		}
		if f.Tip != "" {
			fmt.Fprintf(&b, "Tip: %s\n", f.Tip)
		}
		if f.Stale {
			b.WriteString("(The file changed after PHPStan reported this; check whether it still applies.)\n")
		}
		b.WriteString("\n")
	}
	lines := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	from, to := max(1, line-contextLines), min(len(lines), line+contextLines)
	if from <= to && line <= len(lines) {
		lang := strings.TrimPrefix(filepath.Ext(path), ".")
		fmt.Fprintf(&b, "Code around line %d:\n```%s\n", line, lang)
		w := len(fmt.Sprint(to))
		for i := from; i <= to; i++ {
			mark := "  "
			if i == line {
				mark = "> "
			}
			fmt.Fprintf(&b, "%s%*d | %s\n", mark, w, i, lines[i-1])
		}
		b.WriteString("```\n\n")
	}
	b.WriteString("Make the smallest change that fixes the cause (no @phpstan-ignore, no baseline entry, no casts just to silence it). Do not change unrelated code, do not commit.")
	return b.String()
}
