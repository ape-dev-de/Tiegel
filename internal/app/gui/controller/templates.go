package controller

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/snippet"
)

// Live templates (plugin live-templates, AP17.3): the GUI expands the
// abbreviation before the caret on Tab, offers the templates of the
// caret's context (Insert Live Template) and the surround templates
// (Surround With). The backend applies the expansion as one undoable
// edit; the view walks its tab stops (snippet.Session).

// TemplateItem is a live template as listed by templates.list.
type TemplateItem struct {
	Abbreviation string   `json:"abbreviation"`
	Description  string   `json:"description"`
	Contexts     []string `json:"contexts"`
	Body         string   `json:"body"`
	Surround     bool     `json:"surround"`
}

// TemplateExpansion is an applied expansion (templates.expand).
type TemplateExpansion struct {
	File         string          `json:"-"`
	Abbreviation string          `json:"abbreviation"`
	Fields       []snippet.Field `json:"fields"`
	Caret        int             `json:"caret"`
	Version      uint64          `json:"version"`
	Applied      bool            `json:"applied"`
}

// LiveTemplates is the state of the live templates integration.
type LiveTemplates struct {
	// Available is false while the plugin is off (no Tab expansion).
	Available bool
	abbrs     map[string]bool
	// Expanded is the last applied expansion; ExpandedVersion counts
	// them so the view starts each tab-stop session once.
	Expanded        *TemplateExpansion
	ExpandedVersion uint64
	loadSeq         uint64
}

// IsAbbreviation reports whether word names an enabled template (cheap
// check before Tab is taken from the editor).
func (t *LiveTemplates) IsAbbreviation(word string) bool { return t.Available && t.abbrs[word] }

// LoadTemplates refreshes the abbreviations used for Tab expansion.
func (c *Controller) LoadTemplates() {
	lt := &c.Templates
	lt.loadSeq++
	seq := lt.loadSeq
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Templates []TemplateItem `json:"templates"`
		}
		err := c.call(ctx, "templates.list", map[string]any{}, &res)
		return func() {
			if seq != lt.loadSeq {
				return
			}
			lt.Available = err == nil
			lt.abbrs = map[string]bool{}
			for _, t := range res.Templates {
				lt.abbrs[t.Abbreviation] = true
			}
		}
	})
}

// TemplatesAt lists the templates that apply at offset off of file
// (surround: only those wrapping a selection) and hands them to done on
// the UI goroutine.
func (c *Controller) TemplatesAt(file string, off int, surround bool, done func([]TemplateItem)) {
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Templates []TemplateItem `json:"templates"`
		}
		err := c.call(ctx, "templates.list", map[string]any{"path": file, "pos": pos{Offset: off}, "surround": surround}, &res)
		return func() {
			switch {
			case err != nil:
				c.notice(err)
			case len(res.Templates) == 0:
				c.Status.Notice = "No live templates apply here"
			default:
				done(res.Templates)
			}
		}
	})
}

// ExpandTemplate expands a template in file: abbr at [start, end) (a
// selection is wrapped), or with abbr "" the abbreviation before start
// (Tab). clipboard feeds clipboard(). If nothing can be expanded and
// fallback is set, fallback runs instead (Tab indents then).
func (c *Controller) ExpandTemplate(file string, start, end int, abbr, clipboard string, fallback func()) {
	lt := &c.Templates
	c.Go(func(ctx context.Context) func() {
		p := map[string]any{"path": file, "pos": pos{Offset: start}}
		if end > start {
			p["end"] = pos{Offset: end}
		}
		if abbr != "" {
			p["abbreviation"] = abbr
		}
		if clipboard != "" {
			p["clipboard"] = clipboard
		}
		var res TemplateExpansion
		err := c.call(ctx, "templates.expand", p, &res)
		return func() {
			if err != nil {
				if fallback != nil && api.ErrorCode(err) == api.CodeNotFound {
					fallback()
					return
				}
				c.notice(err)
				return
			}
			res.File = file
			lt.Expanded = &res
			lt.ExpandedVersion++
		}
	})
}
