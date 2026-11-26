package controller

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/markdown"
)

// Markdown previews (D534): the parsed document of every previewed file
// (md.render, which reads the open document's unsaved text), refreshed
// on edits and saves. At most one render per file runs; changes during
// a render schedule one more.

// TabMarkdown is the virtual tab kind of a Markdown preview; the tab id
// is the absolute file.
const TabMarkdown = "markdown"

// MarkdownPreview is the state of one previewed file.
type MarkdownPreview struct {
	File string
	// Doc is the latest parsed document (nil until the first render).
	Doc *markdown.Doc
	// Err is the error of the latest render ("" = fine).
	Err string
	// Version changes with every new Doc or Err.
	Version uint64

	loading, stale bool
}

// Markdown holds the previews of the window.
type Markdown struct {
	c        *Controller
	previews map[string]*MarkdownPreview
}

func newMarkdown(c *Controller) *Markdown {
	return &Markdown{c: c, previews: map[string]*MarkdownPreview{}}
}

// IsMarkdown reports whether file is a Markdown file (by extension).
func IsMarkdown(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".md", ".markdown", ".mdown", ".mkd", ".mdx":
		return true
	}
	return false
}

// Preview returns the preview of file, rendering it on first use.
func (m *Markdown) Preview(file string) *MarkdownPreview {
	p := m.previews[file]
	if p == nil {
		p = &MarkdownPreview{File: file}
		m.previews[file] = p
		m.render(p)
	}
	return p
}

// Open opens the preview of file as a tab of the active group; side
// shows it in the group to the right (split first when there is only
// one group), the editor stays where it is.
func (m *Markdown) Open(file string, side bool) *Tab {
	m.Preview(file)
	title := filepath.Base(file) + " (Preview)"
	c := m.c
	if !side || c.ActiveTab() == nil {
		return c.OpenVirtual(TabMarkdown, file, title, file)
	}
	key := VirtualKey(TabMarkdown, file)
	if len(c.groups) > 1 {
		// An existing neighbour group takes the preview.
		next := (c.activeGroup + 1) % len(c.groups)
		c.FocusGroup(next)
		return c.OpenVirtual(TabMarkdown, file, title, file)
	}
	orig := c.activeGroup
	c.OpenVirtual(TabMarkdown, file, title, file)
	c.Split(false)
	side2 := c.activeGroup
	// The preview moved into the new group; the original group shows the
	// editor again.
	c.FocusGroup(orig)
	if i := c.tabIndex(key); i >= 0 {
		c.CloseTab(i)
	}
	c.FocusGroup(side2)
	return c.ActiveTab()
}

// opened shows the preview of a Markdown file the user just opened to
// the right of its editor (markdown.openMode "split"); the editor keeps
// the focus.
func (m *Markdown) opened(file string) {
	if !IsMarkdown(file) {
		return
	}
	c := m.c
	c.Go(func(ctx context.Context) func() {
		mode := getEffective(ctx, c.be, markdown.KeyOpenMode, markdown.OpenSplit)
		return func() {
			if mode != markdown.OpenSplit || m.shown(file) {
				return
			}
			t := c.ActiveTab()
			if t == nil || t.File != file || t.Virtual != "" {
				return // the user moved on
			}
			orig := c.activeGroup
			m.Open(file, true)
			c.FocusGroup(orig)
		}
	})
}

// shown reports whether a preview tab of file is open.
func (m *Markdown) shown(file string) bool {
	for _, t := range m.c.allTabs() {
		if t.Virtual == TabMarkdown && t.Data == file {
			return true
		}
	}
	return false
}

// Close forgets the preview of file (its tab closed).
func (m *Markdown) Close(file string) {
	for _, t := range m.c.allTabs() {
		if t.Virtual == TabMarkdown && t.Data == file {
			return // another group still shows it
		}
	}
	delete(m.previews, file)
}

// FileChanged renders a previewed file again (saved or edited).
func (m *Markdown) FileChanged(file string) {
	if p := m.previews[file]; p != nil {
		m.render(p)
	}
}

func (m *Markdown) docEdited(payload json.RawMessage) {
	if len(m.previews) == 0 {
		return
	}
	var ev struct {
		Path string `json:"path"`
		File string `json:"file"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return
	}
	f := ev.File
	if f == "" {
		f = m.c.Abs(ev.Path)
	}
	m.FileChanged(f)
}

func (m *Markdown) render(p *MarkdownPreview) {
	if p.loading {
		p.stale = true
		return
	}
	p.loading, p.stale = true, false
	file := p.File
	m.c.Go(func(ctx context.Context) func() {
		var r api.MarkdownResult
		err := m.c.call(ctx, "md.render", api.MarkdownParams{Path: file}, &r)
		return func() {
			p.loading = false
			if err != nil {
				p.Err = err.Error()
			} else {
				p.Doc, p.Err = r.Doc, ""
			}
			p.Version++
			if p.stale && m.previews[file] == p {
				m.render(p)
			}
		}
	})
}
