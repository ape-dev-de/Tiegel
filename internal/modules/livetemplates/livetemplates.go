// Package livetemplates is the plugin "live-templates" (AP17.3): code
// snippets with tab stops. A template has an abbreviation, a body in the
// snippet syntax ($NAME$, $END$, $SELECTION$, see internal/editor/snippet),
// variable expressions (className(), date(), clipboard(), …) and the
// contexts it applies in, which are determined with Tree-sitter (PHP
// statement, class member, expression, comment, HTML outside the PHP
// tags, other files).
//
// The plugin ships a neutral built-in set for PHP; user templates and
// switched-off built-ins are plugin settings (D190):
//
//	live-templates.user      one template per line (see ParseUser)
//	live-templates.disabled  abbreviations of built-ins that are off
//
// API: templates.list (optionally filtered by the context at a position)
// and templates.expand (Tab expansion of the abbreviation before the
// caret, insertion of a chosen template, Surround With). Expansion is one
// undoable edit; the result carries the tab stops in document offsets for
// the client's tab-stop session (snippet.Session).
package livetemplates

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/snippet"
)

// ID is the module ID.
const ID = "live-templates"

// Setting keys.
const (
	SettingUser     = ID + ".user"
	SettingDisabled = ID + ".disabled"
)

// Module is the live templates plugin.
type Module struct {
	mu  sync.Mutex
	app *api.App
	// now is the clock (tests).
	now func() time.Time
}

// New returns the plugin.
func New() *Module { return &Module{now: time.Now} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"templates.*"} }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingUser, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Label: "User templates",
			Help: `One template per line: "abbreviation | contexts | description | variables | body", e.g. "dd | php-statement | dump and die | | dd($SELECTION$$END$);". Contexts: ` +
				strings.Join(Contexts, ", ") + `. Variables: NAME=expression separated by ";" (expressions: ` + strings.Join(Expressions, ", ") +
				`; a trailing "!" skips the tab stop). In the body \n is a line break, \t one indentation unit, $$ a dollar sign. A user template replaces a built-in one with the same abbreviation.`},
		{Key: SettingDisabled, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Label: "Switched-off built-in templates",
			Help: "Abbreviations of built-in templates that are not offered (one per line)."},
	}
}

// Start implements module.Module.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("live-templates: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "templates.list", "Live templates (built-in and user) with contexts; with path and position only the ones that apply there", m.list)
	api.Add(p, "templates.expand", "Expand a live template: the abbreviation before the position (Tab), a named one, or around a selection (Surround With); one undoable edit with tab stops", m.expand)
	return p.Err()
}

// Stop implements module.Module.
func (m *Module) Stop() error { return nil }

func (m *Module) application() *api.App {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.app
}

// Problem is an invalid user template line.
type Problem struct {
	Line    int    `json:"line"` // 1-based entry of live-templates.user
	Message string `json:"message"`
}

// templates returns the effective templates (user ones replace built-ins
// with the same abbreviation; switched-off built-ins are marked) sorted by
// abbreviation, and the problems of invalid user lines.
func (m *Module) templates() ([]TemplateInfo, []Problem) {
	app := m.application()
	var user, disabled []string
	if app != nil {
		user = api.Setting(app, SettingUser, []string{})
		disabled = api.Setting(app, SettingDisabled, []string{})
	}
	byAbbr := map[string]TemplateInfo{}
	for _, t := range Builtin() {
		byAbbr[t.Abbreviation] = info(t, slices.Contains(disabled, t.Abbreviation))
	}
	var problems []Problem
	for i, line := range user {
		if strings.TrimSpace(line) == "" {
			continue
		}
		t, err := ParseUser(line)
		if err != nil {
			problems = append(problems, Problem{Line: i + 1, Message: err.Error()})
			continue
		}
		byAbbr[t.Abbreviation] = info(t, false)
	}
	out := make([]TemplateInfo, 0, len(byAbbr))
	for _, t := range byAbbr {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Abbreviation < out[j].Abbreviation })
	return out, problems
}

// TemplateInfo describes a template for templates.list.
type TemplateInfo struct {
	Template
	// Surround is true when the template wraps a selection.
	Surround bool `json:"surround"`
	// Disabled marks a switched-off built-in (live-templates.disabled).
	Disabled bool `json:"disabled,omitempty"`
}

func info(t Template, disabled bool) TemplateInfo {
	return TemplateInfo{Template: t, Surround: t.Surround(), Disabled: disabled}
}

// ListParams are the params of templates.list.
type ListParams struct {
	// Path and Pos filter by the context at that position (optional).
	Path string   `json:"path,omitempty"`
	Pos  *api.Pos `json:"pos,omitempty"`
	// Surround lists only templates that wrap a selection.
	Surround bool `json:"surround,omitempty"`
	// All includes switched-off built-ins.
	All bool `json:"all,omitempty"`
}

// ListResult is the result of templates.list.
type ListResult struct {
	// Context is the detected context at Pos ("" without position).
	Context   string         `json:"context,omitempty"`
	Templates []TemplateInfo `json:"templates"`
	// Problems are invalid lines of live-templates.user.
	Problems []Problem `json:"problems,omitempty"`
}

func (m *Module) list(ctx context.Context, p ListParams) (ListResult, error) {
	all, problems := m.templates()
	res := ListResult{Templates: []TemplateInfo{}, Problems: problems}
	if p.Path != "" {
		app := m.application()
		text, _, err := app.ReadText(p.Path)
		if err != nil {
			return res, err
		}
		pos := api.Pos{}
		if p.Pos != nil {
			pos = *p.Pos
		}
		off, err := pos.ByteOffset(text)
		if err != nil {
			return res, err
		}
		abs, _ := app.ResolvePath(p.Path)
		res.Context = analyze(ctx, abs, text, off, off).context
	}
	for _, t := range all {
		if (t.Disabled && !p.All) || (p.Surround && !t.Surround) || (res.Context != "" && !applies(t.Template, res.Context)) {
			continue
		}
		res.Templates = append(res.Templates, t)
	}
	return res, nil
}

// ExpandParams are the params of templates.expand.
type ExpandParams struct {
	Path string  `json:"path"`
	Pos  api.Pos `json:"pos"`
	// End ends a selection [Pos, End) that the template replaces; its text
	// becomes $SELECTION$ (Surround With).
	End *api.Pos `json:"end,omitempty"`
	// Abbreviation names the template. Empty: the word before Pos is the
	// abbreviation and is replaced (Tab expansion).
	Abbreviation string `json:"abbreviation,omitempty"`
	// Clipboard is the text clipboard() returns.
	Clipboard string `json:"clipboard,omitempty"`
	// Values override initial variable values.
	Values map[string]string `json:"values,omitempty"`
	// Apply writes the expansion (default true): an open document gets one
	// undoable edit, a closed file is rewritten with Local History.
	Apply *bool `json:"apply,omitempty"`
	// Version, if set, must match the open document's version.
	Version *uint64 `json:"version,omitempty"`
}

// ExpandResult is the result of templates.expand.
type ExpandResult struct {
	Abbreviation string `json:"abbreviation"`
	Context      string `json:"context"`
	// Start and End are the replaced range of the text before the
	// expansion; Text is the inserted text.
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
	// Fields are the tab stops and Caret the final caret ($END$), as
	// offsets in the text after the expansion.
	Fields []snippet.Field `json:"fields"`
	Caret  int             `json:"caret"`
	// Applied is true when the change was written; Version is the
	// document version afterwards (open documents).
	Applied bool   `json:"applied"`
	Version uint64 `json:"version,omitempty"`
}

func (m *Module) expand(ctx context.Context, p ExpandParams) (ExpandResult, error) {
	app := m.application()
	if p.Path == "" {
		return ExpandResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	abs, err := app.ResolvePath(p.Path)
	if err != nil {
		return ExpandResult{}, err
	}
	all, _ := m.templates()
	var res ExpandResult
	compute := func(text []byte) error {
		r, err := m.compute(ctx, abs, text, p, all)
		res = r
		return err
	}
	if p.Apply != nil && !*p.Apply {
		text, _, err := app.ReadText(abs)
		if err != nil {
			return ExpandResult{}, err
		}
		return res, compute(text)
	}
	steps, err := app.ApplyFileOps(ctx, "Live Template "+cmpOr(p.Abbreviation, "expansion"), []api.FileOp{{
		Kind: api.FileOpEdit, Path: abs, Version: p.Version,
		Changes: func(text []byte) ([]api.TextChange, error) {
			if err := compute(text); err != nil {
				return nil, err
			}
			return []api.TextChange{{Start: res.Start, End: res.End, Text: res.Text}}, nil
		},
	}})
	if err != nil {
		return ExpandResult{}, err
	}
	res.Applied = true
	if len(steps) > 0 {
		res.Version = steps[0].Version
	}
	return res, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// compute finds the template and expands it against text.
func (m *Module) compute(ctx context.Context, abs string, text []byte, p ExpandParams, all []TemplateInfo) (ExpandResult, error) {
	start, err := p.Pos.ByteOffset(text)
	if err != nil {
		return ExpandResult{}, err
	}
	end := start
	if p.End != nil {
		if end, err = p.End.ByteOffset(text); err != nil {
			return ExpandResult{}, err
		}
		if end < start {
			start, end = end, start
		}
	}
	selection := string(text[start:end])
	abbr := p.Abbreviation
	if abbr == "" {
		if end != start {
			return ExpandResult{}, api.Errorf(api.CodeInvalidParams, "a selection needs an abbreviation (Surround With)")
		}
		ws := start
		for ws > 0 && (isWordRune(rune(text[ws-1])) || text[ws-1] == '-') {
			ws--
		}
		if ws == start {
			return ExpandResult{}, api.Errorf(api.CodeNotFound, "no abbreviation before the caret")
		}
		abbr, start = string(text[ws:start]), ws
	}
	// A selection of whole lines is re-indented as a block: the template
	// starts at the line start with the line's indentation.
	ls := lineStart(text, start)
	indent := leadingSpace(text[ls:])
	if selection != "" && strings.TrimSpace(string(text[ls:start])) == "" {
		bs, be := ls+len(indent), end
		switch {
		case strings.HasSuffix(selection, "\r\n"):
			be -= 2
		case strings.HasSuffix(selection, "\n"):
			be--
		}
		if be < bs {
			// Only (part of) the indentation, maybe with the line
			// break, is selected: nothing to surround, the template
			// goes to the selection start and the whitespace stays.
			end, selection = start, ""
		} else {
			start, end = bs, be
			selection = dedent(string(text[start:end]), indent)
		}
	}
	var t *TemplateInfo
	for i := range all {
		if all[i].Abbreviation == abbr && !all[i].Disabled {
			t = &all[i]
			break
		}
	}
	if t == nil {
		return ExpandResult{}, api.Errorf(api.CodeNotFound, "no live template %q", abbr)
	}
	at := end // after the abbreviation
	if selection != "" {
		at = start // before the selection
	}
	an := analyze(ctx, abs, text, at, start)
	if !applies(t.Template, an.context) {
		return ExpandResult{}, api.Errorf(api.CodeNotFound, "live template %q does not apply here (context %s, template: %s)", abbr, an.context, strings.Join(t.Contexts, ", "))
	}
	tp, err := snippet.Parse(t.Body)
	if err != nil {
		return ExpandResult{}, api.Errorf(api.CodeFailed, "%s: %v", abbr, err)
	}
	an.pos.clipboard = p.Clipboard
	an.pos.now = m.now()
	values, skip := map[string]string{}, map[string]bool{}
	// Values from the client first, then plain expressions, then the
	// ones derived from other variables (camelCase(VAR), …).
	for pass := 0; pass < 2; pass++ {
		for _, name := range tp.Vars {
			if v, ok := p.Values[name]; ok {
				values[name] = v
				continue
			}
			def := t.Vars[name]
			e, err := parseExpr(def.Expr)
			if err != nil || (exprFuncs[e.fn] == 2) != (pass == 1) {
				continue
			}
			if v, ok := e.eval(an.pos, values); ok {
				values[name] = v
				skip[name] = def.Skip && v != ""
			}
		}
	}
	unit := "\t"
	if app := m.application(); app != nil && api.Setting(app, settings.KeyEditorInsertSpace, true) {
		unit = strings.Repeat(" ", max(api.Setting(app, settings.KeyEditorTabWidth, 4), 1))
	}
	ex := snippet.Expand(tp, snippet.ExpandOptions{Values: values, Skip: skip, Selection: selection, Indent: indent, Tab: unit}).Shift(start)
	return ExpandResult{
		Abbreviation: abbr, Context: an.context, Start: start, End: end, Text: ex.Text,
		Fields: ex.Fields, Caret: ex.End,
	}, nil
}

func lineStart(text []byte, off int) int {
	for off > 0 && text[off-1] != '\n' {
		off--
	}
	return off
}

func leadingSpace(b []byte) string {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return string(b[:i])
}

// dedent removes indent from the start of every line after the first
// (the first starts after it already).
func dedent(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], indent)
	}
	return strings.Join(lines, "\n")
}
