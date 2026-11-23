package api

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/markdown"
)

// Markdown reader (D534): md.render parses a Markdown file (the open
// document's text when it is open, so previews follow unsaved edits) or
// a given text into the renderer-neutral model of internal/markdown.
// Nothing is fetched: images and links are only referenced.

// MarkdownParams are the params of "md.render" and "md.outline". Path
// (absolute or project relative) or Text is required; Text wins.
type MarkdownParams struct {
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
	// Plain also renders plain text; Width > 0 wraps it.
	Plain bool `json:"plain,omitempty"`
	Width int  `json:"width,omitempty"`
}

// MarkdownResult is the result of "md.render".
type MarkdownResult struct {
	// Path is the rendered file (absolute; "" for Text).
	Path    string                  `json:"path,omitempty"`
	Doc     *markdown.Doc           `json:"doc"`
	Outline []markdown.OutlineEntry `json:"outline"`
	// Plain is the plain text rendering (with Plain).
	Plain string `json:"plain,omitempty"`
}

func (a *App) registerMarkdown() {
	mustHandle(a, "md.render", "Parse Markdown (a file, the open document's unsaved text, or text) into blocks and spans for a reader; plain: also as plain text", a.mdRender)
	mustHandle(a, "md.outline", "Headings of a Markdown file or text with anchors and lines (table of contents)", func(ctx context.Context, p MarkdownParams) ([]markdown.OutlineEntry, error) {
		r, err := a.mdRender(ctx, MarkdownParams{Path: p.Path, Text: p.Text})
		return r.Outline, err
	})
}

func (a *App) mdRender(_ context.Context, p MarkdownParams) (MarkdownResult, error) {
	var res MarkdownResult
	src := []byte(p.Text)
	if p.Text == "" {
		if p.Path == "" {
			return res, Errorf(CodeInvalidParams, "path or text is required")
		}
		abs, err := a.resolvePath(p.Path)
		if err != nil {
			return res, err
		}
		if src, _, err = a.ReadText(abs); err != nil {
			return res, err
		}
		res.Path = abs
	}
	res.Doc = markdown.Parse(src)
	res.Outline = res.Doc.Outline()
	if res.Outline == nil {
		res.Outline = []markdown.OutlineEntry{}
	}
	if p.Plain {
		res.Plain = res.Doc.PlainText(p.Width)
	}
	return res, nil
}
